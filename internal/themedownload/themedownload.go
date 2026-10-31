// Package themedownload fetches theme songs for TV series and movies into
// Silo's blob storage: from Plex's public TV theme library, then ThemerrDB's
// community YouTube links, then (when switched on) a YouTube search whose
// result an admin approves. It never writes to a media folder: a theme placed
// in a series or movie folder is used instead, and the title is skipped.
package themedownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

const (
	// SettingEnabled switches downloading on; it is off by default because it
	// contacts a third-party server.
	SettingEnabled = "theme_songs.download_enabled"
	// SettingLastReport holds the most recent run's report, as JSON.
	SettingLastReport = "theme_songs.last_report"
	// SettingRecheckDays is how long a theme the source lacks waits before it
	// is asked for again.
	SettingRecheckDays = "theme_songs.recheck_days"

	// DefaultRecheckDays applies when SettingRecheckDays is unset.
	DefaultRecheckDays = 14

	// SettingThemerrDB switches the ThemerrDB source; on unless set "false".
	SettingThemerrDB = "theme_songs.themerrdb_enabled"
	// SettingYouTubeSearch switches the YouTube search source; off by default.
	SettingYouTubeSearch = "theme_songs.youtube_search_enabled"

	// SourcePlex is Plex's public TV theme library, keyed by TheTVDB id.
	SourcePlex = "plex"

	// TypeSeries and TypeMovie are the media types that get themes.
	TypeSeries = "series"
	TypeMovie  = "movie"

	// KeyRoot is the blob namespace downloaded themes are stored under.
	KeyRoot = "theme-songs/"

	defaultSourceURL = "https://tvthemes.plexapp.com/%s.mp3"
	minThemeBytes    = 16 << 10
	maxThemeBytes    = 20 << 20
	// requestGap paces requests to the source.
	requestGap = 500 * time.Millisecond
)

var errNotAvailable = errors.New("no theme available")

// Settings reads and writes server settings.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// Blobs stores theme bytes.
type Blobs interface {
	Put(ctx context.Context, key string, data []byte) error
	Delete(ctx context.Context, keys []string) (int, error)
}

// Service downloads themes and reports on them.
type Service struct {
	pool       *pgxpool.Pool
	blobs      Blobs
	settings   Settings
	ffprobe    string
	themes     *themesongs.Repository
	client     *http.Client
	sourceURL  string
	themerrURL string
	gap        time.Duration
	// ytdlp and ffmpeg fetch YouTube audio; empty ytdlp disables those sources.
	ytdlp, ffmpeg string
	now           func() time.Time

	mu       sync.Mutex
	running  bool
	progress string
	// probeAudio reads a download's audio format; tests replace ffprobe.
	probeAudio func(ctx context.Context, data []byte) (themesongs.File, error)
}

// New creates a Service. ffprobe is used to read each download's audio format.
func New(pool *pgxpool.Pool, blobs Blobs, settings Settings, ffprobe string) *Service {
	s := &Service{
		pool: pool, blobs: blobs, settings: settings, ffprobe: ffprobe,
		themes:     themesongs.NewRepository(pool),
		client:     &http.Client{Timeout: 60 * time.Second},
		sourceURL:  defaultSourceURL,
		themerrURL: defaultThemerrURL,
		gap:        requestGap,
		now:        time.Now,
	}
	s.probeAudio = s.probe
	return s
}

// Config is the admin-editable configuration.
type Config struct {
	Enabled       bool `json:"enabled"`
	RecheckDays   int  `json:"recheck_days"`
	ThemerrDB     bool `json:"themerrdb"`
	YouTubeSearch bool `json:"youtube_search"`
}

// Config returns the saved configuration.
func (s *Service) Config(ctx context.Context) (Config, error) {
	enabled, err := s.settings.Get(ctx, SettingEnabled)
	if err != nil {
		return Config{}, err
	}
	days, err := s.settings.Get(ctx, SettingRecheckDays)
	if err != nil {
		return Config{}, err
	}
	themerr, err := s.settings.Get(ctx, SettingThemerrDB)
	if err != nil {
		return Config{}, err
	}
	search, err := s.settings.Get(ctx, SettingYouTubeSearch)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Enabled: enabled == "true", RecheckDays: DefaultRecheckDays, ThemerrDB: themerr != "false", YouTubeSearch: search == "true"}
	if n, err := strconv.Atoi(days); err == nil && n > 0 {
		cfg.RecheckDays = n
	}
	return cfg, nil
}

// SaveConfig stores cfg.
func (s *Service) SaveConfig(ctx context.Context, cfg Config) (Config, error) {
	if cfg.RecheckDays < 1 || cfg.RecheckDays > 365 {
		cfg.RecheckDays = DefaultRecheckDays
	}
	if err := s.settings.Set(ctx, SettingEnabled, strconv.FormatBool(cfg.Enabled)); err != nil {
		return Config{}, err
	}
	if err := s.settings.Set(ctx, SettingRecheckDays, strconv.Itoa(cfg.RecheckDays)); err != nil {
		return Config{}, err
	}
	if err := s.settings.Set(ctx, SettingThemerrDB, strconv.FormatBool(cfg.ThemerrDB)); err != nil {
		return Config{}, err
	}
	if err := s.settings.Set(ctx, SettingYouTubeSearch, strconv.FormatBool(cfg.YouTubeSearch)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Enabled reports whether downloading is switched on.
func (s *Service) Enabled(ctx context.Context) (bool, error) {
	cfg, err := s.Config(ctx)
	return cfg.Enabled, err
}

// Series is one TV series or movie and where its theme comes from.
type Series struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Title        string     `json:"title"`
	Year         int        `json:"year"`
	TVDBID       string     `json:"tvdb_id"`
	TMDBID       string     `json:"tmdb_id"`
	State        string     `json:"state" enum:"folder,downloaded,not_available,failed,pending,skipped,review"`
	Detail       string     `json:"detail,omitempty"`
	Source       string     `json:"source,omitempty"`
	Candidate    *Candidate `json:"candidate,omitempty"`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	DownloadedAt *time.Time `json:"downloaded_at,omitempty"`
}

// Series states.
const (
	StateFolder       = "folder"
	StateDownloaded   = "downloaded"
	StateNotAvailable = "not_available"
	StateFailed       = "failed"
	StatePending      = "pending"
	// StateSkipped is a series an admin chose never to download a theme for.
	StateSkipped = "skipped"
	// StateReview is a YouTube search result waiting for an admin.
	StateReview = "review"
)

// List returns every TV series and movie with files, and its theme state.
func (s *Service) List(ctx context.Context) ([]Series, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT mi.content_id, mi.type, mi.title, COALESCE(mi.year, 0), COALESCE(mi.tvdb_id, ''), COALESCE(mi.tmdb_id, ''),
			dt.downloaded_at, COALESCE(dt.source, ''), c.outcome, c.detail, c.checked_at,
			tc.video_id, tc.video_title, tc.channel, tc.duration_seconds, tc.query, tc.found_at
		FROM media_items mi
		LEFT JOIN downloaded_theme_songs dt ON dt.series_id = mi.content_id
		LEFT JOIN theme_song_checks c ON c.series_id = mi.content_id
		LEFT JOIN theme_song_candidates tc ON tc.item_id = mi.content_id
		WHERE (mi.type = 'series' AND EXISTS (
			SELECT 1 FROM episodes e
			JOIN media_files mf ON mf.episode_id = e.content_id
			JOIN media_folders f ON f.id = mf.media_folder_id
			WHERE e.series_id = mi.content_id AND mf.missing_since IS NULL AND f.enabled))
		OR (mi.type = 'movie' AND EXISTS (
			SELECT 1 FROM media_files mf
			JOIN media_folders f ON f.id = mf.media_folder_id
			WHERE mf.content_id = mi.content_id AND mf.extra_id IS NULL AND mf.missing_since IS NULL AND f.enabled))
		ORDER BY lower(mi.title), mi.content_id`)
	if err != nil {
		return nil, err
	}
	var out []Series
	for rows.Next() {
		var sr Series
		var outcome, detail *string
		var videoID, videoTitle, channel, query *string
		var duration *int
		var foundAt *time.Time
		if err := rows.Scan(&sr.ID, &sr.Type, &sr.Title, &sr.Year, &sr.TVDBID, &sr.TMDBID, &sr.DownloadedAt, &sr.Source,
			&outcome, &detail, &sr.CheckedAt, &videoID, &videoTitle, &channel, &duration, &query, &foundAt); err != nil {
			rows.Close()
			return nil, err
		}
		if videoID != nil {
			sr.Candidate = &Candidate{VideoID: *videoID, Title: deref(videoTitle), Channel: deref(channel), Query: deref(query)}
			if duration != nil {
				sr.Candidate.DurationSeconds = *duration
			}
			if foundAt != nil {
				sr.Candidate.FoundAt = *foundAt
			}
		}
		switch {
		case sr.DownloadedAt != nil:
			sr.State = StateDownloaded
		case sr.Candidate != nil:
			sr.State = StateReview
		case outcome != nil && *outcome == "not_available":
			sr.State = StateNotAvailable
		case outcome != nil && *outcome == "skipped":
			sr.State = StateSkipped
		case outcome != nil && *outcome == "failed":
			sr.State, sr.Detail = StateFailed, *detail
		default:
			sr.State = StatePending
		}
		out = append(out, sr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A folder theme overrides whatever was downloaded.
	for i := range out {
		local, err := s.themes.HasFolderTheme(ctx, out[i].ID, out[i].Type)
		if err != nil {
			return nil, err
		}
		if local {
			out[i].State, out[i].Detail, out[i].Source, out[i].Candidate = StateFolder, "", "", nil
		}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Report summarizes one run.
type Report struct {
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished"`
	Reason       string    `json:"reason"`
	Error        string    `json:"error,omitempty"`
	Series       int       `json:"series"`
	Checked      int       `json:"checked"`
	Downloaded   int       `json:"downloaded"`
	NotAvailable int       `json:"not_available"`
	Failed       int       `json:"failed"`
	Review       int       `json:"review" doc:"Search results now waiting for an admin."`
	Skipped      int       `json:"skipped" doc:"Titles with a folder theme, a download, a search result to review, or a recent miss."`
	Failures     []string  `json:"failures,omitempty"`
}

// ErrRunning means a check is already running.
var ErrRunning = errors.New("a theme song check is already running")

func (s *Service) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running, s.progress = true, "Starting"
	return true
}

func (s *Service) end() {
	s.mu.Lock()
	s.running, s.progress = false, ""
	s.mu.Unlock()
}

func (s *Service) setProgress(message string) {
	s.mu.Lock()
	s.progress = message
	s.mu.Unlock()
}

// Status reports whether a check is running and what it is doing.
func (s *Service) Status() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.progress
}

// Run downloads the themes still missing. force asks again for themes the
// source lacked, without waiting for the recheck interval. reason is kept
// with the run's report. progress may be nil. Only one check runs at a time.
func (s *Service) Run(ctx context.Context, force bool, reason string, progress func(percent float64, message string)) (Report, error) {
	if !s.begin() {
		return Report{}, ErrRunning
	}
	defer s.end()
	return s.runAndRecord(ctx, force, reason, progress)
}

// Start runs a check in the background, for the admin page.
func (s *Service) Start(force bool, reason string) error {
	if !s.begin() {
		return ErrRunning
	}
	go func() {
		defer s.end()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
		defer cancel()
		_, _ = s.runAndRecord(ctx, force, reason, nil)
	}()
	return nil
}

func (s *Service) runAndRecord(ctx context.Context, force bool, reason string, progress func(percent float64, message string)) (Report, error) {
	started := s.now()
	report, err := s.run(ctx, force, func(percent float64, message string) {
		s.setProgress(message)
		if progress != nil {
			progress(percent, message)
		}
	})
	report.Started, report.Finished, report.Reason = started, s.now(), reason
	if err != nil {
		report.Error = err.Error()
	}
	if raw, marshalErr := json.Marshal(report); marshalErr == nil {
		// A fresh context: a cancelled run still records how far it got.
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if saveErr := s.settings.Set(saveCtx, SettingLastReport, string(raw)); saveErr != nil {
			slog.WarnContext(ctx, "theme songs: run report not saved", "component", "themedownload", "error", saveErr)
		}
	}
	return report, err
}

// LastReport returns the most recent run's report, or nil before the first.
func (s *Service) LastReport(ctx context.Context) (*Report, error) {
	raw, err := s.settings.Get(ctx, SettingLastReport)
	if err != nil || raw == "" {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, nil
	}
	return &report, nil
}

func (s *Service) run(ctx context.Context, force bool, progress func(percent float64, message string)) (Report, error) {
	var report Report
	cfg, err := s.Config(ctx)
	if err != nil {
		return report, err
	}
	all, err := s.List(ctx)
	if err != nil {
		return report, err
	}
	recheck := time.Duration(cfg.RecheckDays) * 24 * time.Hour
	report.Series = len(all)
	var due []Series
	for _, sr := range all {
		switch {
		case sr.State == StateFolder || sr.State == StateDownloaded || sr.State == StateSkipped || sr.State == StateReview:
			report.Skipped++
		case sr.State == StateNotAvailable && !force && sr.CheckedAt != nil && s.now().Sub(*sr.CheckedAt) < recheck:
			report.Skipped++
		default:
			due = append(due, sr)
		}
	}
	for i, sr := range due {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if progress != nil {
			progress(float64(i)*100/float64(len(due)), fmt.Sprintf("Checking %s (%d of %d)", sr.Title, i+1, len(due)))
		}
		if i > 0 && s.gap > 0 {
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(s.gap):
			}
		}
		report.Checked++
		outcome, detail := "downloaded", ""
		if err := s.fetch(ctx, sr, cfg); err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			if errors.Is(err, errReview) {
				outcome = "review"
				report.Review++
			} else if errors.Is(err, errNotAvailable) {
				outcome = "not_available"
				report.NotAvailable++
			} else {
				outcome, detail = "failed", err.Error()
				report.Failed++
				report.Failures = append(report.Failures, sr.Title+": "+detail)
				slog.WarnContext(ctx, "theme songs: download failed", "component", "themedownload", "series", sr.ID, "error", err)
			}
		} else {
			report.Downloaded++
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO theme_song_checks (series_id, checked_at, outcome, detail) VALUES ($1, $2, $3, $4)
			ON CONFLICT (series_id) DO UPDATE SET checked_at = EXCLUDED.checked_at, outcome = EXCLUDED.outcome, detail = EXCLUDED.detail`,
			sr.ID, s.now(), outcome, detail); err != nil {
			return report, err
		}
	}
	if progress != nil {
		progress(100, fmt.Sprintf("Downloaded %d, to review %d, not available %d, failed %d", report.Downloaded, report.Review, report.NotAvailable, report.Failed))
	}
	return report, nil
}

// Retry downloads one series' theme now, whatever its last outcome.
func (s *Service) Retry(ctx context.Context, seriesID string) (Series, error) {
	all, err := s.List(ctx)
	if err != nil {
		return Series{}, err
	}
	for _, sr := range all {
		if sr.ID != seriesID {
			continue
		}
		if sr.State == StateFolder {
			return sr, nil
		}
		cfg, err := s.Config(ctx)
		if err != nil {
			return Series{}, err
		}
		if err := s.dropCandidate(ctx, sr.ID); err != nil {
			return Series{}, err
		}
		if err := s.record(ctx, sr.ID, s.fetch(ctx, sr, cfg)); err != nil {
			return Series{}, err
		}
		return s.one(ctx, seriesID)
	}
	return Series{}, themesongs.ErrNotFound
}

// Skip deletes a series' downloaded theme and stops looking for one, until
// an admin retries it.
func (s *Service) Skip(ctx context.Context, seriesID string) (Series, error) {
	if _, err := s.one(ctx, seriesID); err != nil {
		return Series{}, err
	}
	if err := s.deleteDownload(ctx, seriesID); err != nil {
		return Series{}, err
	}
	if err := s.dropCandidate(ctx, seriesID); err != nil {
		return Series{}, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO theme_song_checks (series_id, checked_at, outcome, detail) VALUES ($1, $2, 'skipped', '')
		ON CONFLICT (series_id) DO UPDATE SET checked_at = EXCLUDED.checked_at, outcome = EXCLUDED.outcome, detail = ''`,
		seriesID, s.now()); err != nil {
		return Series{}, err
	}
	return s.one(ctx, seriesID)
}

func (s *Service) deleteDownload(ctx context.Context, seriesID string) error {
	var key string
	err := s.pool.QueryRow(ctx, `DELETE FROM downloaded_theme_songs WHERE series_id = $1 RETURNING blob_key`, seriesID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := s.blobs.Delete(ctx, []string{key}); err != nil {
		slog.WarnContext(ctx, "theme songs: stored theme not deleted; the orphan sweep will remove it", "component", "themedownload", "key", key, "error", err)
	}
	return nil
}

// Remove deletes a series' downloaded theme. The next run fetches it again
// unless a folder theme has appeared.
func (s *Service) Remove(ctx context.Context, seriesID string) (Series, error) {
	if err := s.deleteDownload(ctx, seriesID); err != nil {
		return Series{}, err
	}
	if err := s.dropCandidate(ctx, seriesID); err != nil {
		return Series{}, err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM theme_song_checks WHERE series_id = $1`, seriesID); err != nil {
		return Series{}, err
	}
	return s.one(ctx, seriesID)
}

func (s *Service) one(ctx context.Context, seriesID string) (Series, error) {
	all, err := s.List(ctx)
	if err != nil {
		return Series{}, err
	}
	for _, sr := range all {
		if sr.ID == seriesID {
			return sr, nil
		}
	}
	return Series{}, themesongs.ErrNotFound
}

// fetch finds, checks and stores one title's theme: Plex for a series with a
// TVDB id, then ThemerrDB, then a YouTube search, which stores a candidate
// for review (errReview) rather than a theme. It returns errNotAvailable when
// no source has one.
func (s *Service) fetch(ctx context.Context, sr Series, cfg Config) error {
	var failures []string
	if sr.Type != TypeMovie {
		err := s.fetchPlex(ctx, sr)
		if err == nil || !errors.Is(err, errNotAvailable) {
			return err
		}
	}
	if cfg.ThemerrDB {
		url, err := s.themerrLookup(ctx, sr)
		if err == nil {
			data, dlErr := s.youtubeAudio(ctx, url)
			if dlErr == nil {
				return s.store(ctx, sr, SourceThemerrDB, url, data)
			}
			failures = append(failures, "ThemerrDB: "+dlErr.Error())
		} else if !errors.Is(err, errNotAvailable) {
			failures = append(failures, err.Error())
		}
	}
	if cfg.YouTubeSearch {
		rejected, err := s.rejections(ctx, sr.ID)
		if err != nil {
			return err
		}
		c, err := s.search(ctx, sr, rejected)
		if err == nil {
			if err := s.saveCandidate(ctx, sr.ID, c); err != nil {
				return err
			}
			return errReview
		}
		if !errors.Is(err, errNotAvailable) {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return errNotAvailable
}

// fetchPlex downloads a series' theme from Plex's TV theme library.
func (s *Service) fetchPlex(ctx context.Context, sr Series) error {
	tvdb := strings.TrimSpace(sr.TVDBID)
	if tvdb == "" {
		return errNotAvailable
	}
	if _, err := strconv.ParseUint(tvdb, 10, 64); err != nil {
		return errNotAvailable
	}
	data, err := s.download(ctx, tvdb)
	if err != nil {
		return err
	}
	return s.store(ctx, sr, SourcePlex, tvdb, data)
}

// store checks a theme's audio and keeps it as the title's downloaded theme.
func (s *Service) store(ctx context.Context, sr Series, source, ref string, data []byte) error {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	file, err := s.probeAudio(ctx, data)
	if err != nil {
		return err
	}
	key := KeyRoot + sr.ID + "/" + hash + ".mp3"
	if err := s.blobs.Put(ctx, key, data); err != nil {
		return fmt.Errorf("storing theme: %w", err)
	}
	var old string
	err = s.pool.QueryRow(ctx, `
		WITH previous AS (SELECT blob_key FROM downloaded_theme_songs WHERE series_id = $1)
		INSERT INTO downloaded_theme_songs (series_id, source, source_ref, blob_key, sha256, title, duration_seconds, container,
			audio_codec, audio_channels, bitrate_kbps, sample_rate, file_size, downloaded_at)
		VALUES ($1, $2, $3, $4, $5, 'Theme', $6, 'mp3', $7, $8, $9, $10, $11, $12)
		ON CONFLICT (series_id) DO UPDATE SET source = EXCLUDED.source, source_ref = EXCLUDED.source_ref,
			blob_key = EXCLUDED.blob_key, sha256 = EXCLUDED.sha256, duration_seconds = EXCLUDED.duration_seconds,
			audio_codec = EXCLUDED.audio_codec, audio_channels = EXCLUDED.audio_channels, bitrate_kbps = EXCLUDED.bitrate_kbps,
			sample_rate = EXCLUDED.sample_rate, file_size = EXCLUDED.file_size, downloaded_at = EXCLUDED.downloaded_at
		RETURNING COALESCE((SELECT blob_key FROM previous), '')`,
		sr.ID, source, ref, key, hash, file.DurationSeconds, file.AudioCodec, file.AudioChannels, file.BitrateKbps,
		file.SampleRate, int64(len(data)), s.now().Truncate(time.Microsecond)).Scan(&old)
	if err != nil {
		return err
	}
	if old != "" && old != key {
		_, _ = s.blobs.Delete(ctx, []string{old})
	}
	return nil
}

// record stores the outcome of a fetch for one title.
func (s *Service) record(ctx context.Context, id string, fetchErr error) error {
	outcome, detail := "downloaded", ""
	switch {
	case fetchErr == nil:
	case errors.Is(fetchErr, errReview):
		outcome = "review"
	case errors.Is(fetchErr, errNotAvailable):
		outcome = "not_available"
	default:
		outcome, detail = "failed", fetchErr.Error()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO theme_song_checks (series_id, checked_at, outcome, detail) VALUES ($1, $2, $3, $4)
		ON CONFLICT (series_id) DO UPDATE SET checked_at = EXCLUDED.checked_at, outcome = EXCLUDED.outcome, detail = EXCLUDED.detail`,
		id, s.now(), outcome, detail)
	return err
}

func (s *Service) saveCandidate(ctx context.Context, id string, c Candidate) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO theme_song_candidates (item_id, video_id, video_title, channel, duration_seconds, query, found_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (item_id) DO UPDATE SET video_id = EXCLUDED.video_id, video_title = EXCLUDED.video_title,
			channel = EXCLUDED.channel, duration_seconds = EXCLUDED.duration_seconds, query = EXCLUDED.query, found_at = EXCLUDED.found_at`,
		id, c.VideoID, c.Title, c.Channel, c.DurationSeconds, c.Query, c.FoundAt)
	return err
}

func (s *Service) dropCandidate(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM theme_song_candidates WHERE item_id = $1`, id)
	return err
}

func (s *Service) rejections(ctx context.Context, id string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT video_id FROM theme_song_rejections WHERE item_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// ErrNoCandidate means a title has no search result waiting for review.
var ErrNoCandidate = errors.New("no search result is waiting for review")

// Approve downloads the search result waiting for a title and keeps it as the
// title's theme.
func (s *Service) Approve(ctx context.Context, id string) (Series, error) {
	sr, err := s.one(ctx, id)
	if err != nil {
		return Series{}, err
	}
	if sr.Candidate == nil {
		return Series{}, ErrNoCandidate
	}
	data, err := s.youtubeAudio(ctx, sr.Candidate.URL())
	if err == nil {
		err = s.store(ctx, sr, SourceYouTube, sr.Candidate.VideoID, data)
	}
	if errors.Is(err, ErrVideoUnavailable) {
		// Nothing to try again: turn it down and look for the next result,
		// which waits for review like the first.
		return s.Reject(ctx, id)
	}
	if err != nil {
		// The candidate stays, so the admin can try again or reject it.
		return Series{}, err
	}
	if err := s.dropCandidate(ctx, id); err != nil {
		return Series{}, err
	}
	if err := s.record(ctx, id, nil); err != nil {
		return Series{}, err
	}
	return s.one(ctx, id)
}

// Reject turns down the search result waiting for a title and searches again,
// skipping every result rejected for it so far.
func (s *Service) Reject(ctx context.Context, id string) (Series, error) {
	sr, err := s.one(ctx, id)
	if err != nil {
		return Series{}, err
	}
	if sr.Candidate == nil {
		return Series{}, ErrNoCandidate
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO theme_song_rejections (item_id, video_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		id, sr.Candidate.VideoID); err != nil {
		return Series{}, err
	}
	if err := s.dropCandidate(ctx, id); err != nil {
		return Series{}, err
	}
	rejected, err := s.rejections(ctx, id)
	if err != nil {
		return Series{}, err
	}
	next := errNotAvailable
	if c, searchErr := s.search(ctx, sr, rejected); searchErr == nil {
		if err := s.saveCandidate(ctx, id, c); err != nil {
			return Series{}, err
		}
		next = errReview
	} else if !errors.Is(searchErr, errNotAvailable) {
		next = searchErr
	}
	if err := s.record(ctx, id, next); err != nil {
		return Series{}, err
	}
	return s.one(ctx, id)
}

func (s *Service) download(ctx context.Context, tvdb string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(s.sourceURL, tvdb), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Silo theme songs")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading theme: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotAvailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("theme server returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxThemeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading theme: %w", err)
	}
	if len(data) > maxThemeBytes {
		return nil, errors.New("theme file is unexpectedly large")
	}
	if !looksLikeMP3(data) {
		// Error pages and empty files sometimes arrive with a 200.
		return nil, errNotAvailable
	}
	return data, nil
}

func looksLikeMP3(b []byte) bool {
	if len(b) < minThemeBytes {
		return false
	}
	return bytes.HasPrefix(b, []byte("ID3")) || (b[0] == 0xFF && b[1]&0xE0 == 0xE0)
}

// probe reads the audio format the way the scanner reads a folder theme.
func (s *Service) probe(ctx context.Context, data []byte) (themesongs.File, error) {
	dir, err := os.MkdirTemp("", "silo-theme-probe-")
	if err != nil {
		return themesongs.File{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "theme.mp3")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return themesongs.File{}, err
	}
	probe, err := scanner.ProbeFile(ctx, s.ffprobe, path)
	if err != nil {
		return themesongs.File{}, fmt.Errorf("reading theme audio: %w", err)
	}
	if len(probe.AudioTracks) == 0 || len(probe.VideoTracks) > 0 {
		return themesongs.File{}, errors.New("theme file is not plain audio")
	}
	return themesongs.File{
		Song:       themesongs.Song{DurationSeconds: probe.Duration},
		AudioCodec: probe.CodecAudio, AudioChannels: probe.AudioChannels,
		BitrateKbps: probe.Bitrate, SampleRate: probe.AudioTracks[0].SampleRate,
	}, nil
}
