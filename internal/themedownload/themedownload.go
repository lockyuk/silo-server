// Package themedownload fetches TV theme songs from Plex's public theme
// library into Silo's blob storage. It never writes to a media folder: a
// theme placed in a series folder is used instead, and the series is skipped.
package themedownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

const (
	// SettingEnabled switches downloading on; it is off by default because it
	// contacts a third-party server.
	SettingEnabled = "theme_songs.download_enabled"
	// SettingRecheckDays is how long a theme the source lacks waits before it
	// is asked for again.
	SettingRecheckDays = "theme_songs.recheck_days"

	// DefaultRecheckDays applies when SettingRecheckDays is unset.
	DefaultRecheckDays = 14

	// SourcePlex is Plex's public TV theme library, keyed by TheTVDB id.
	SourcePlex = "plex"

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
	pool      *pgxpool.Pool
	blobs     Blobs
	settings  Settings
	ffprobe   string
	themes    *themesongs.Repository
	client    *http.Client
	sourceURL string
	gap       time.Duration
	now       func() time.Time
	// probeAudio reads a download's audio format; tests replace ffprobe.
	probeAudio func(ctx context.Context, data []byte) (themesongs.File, error)
}

// New creates a Service. ffprobe is used to read each download's audio format.
func New(pool *pgxpool.Pool, blobs Blobs, settings Settings, ffprobe string) *Service {
	s := &Service{
		pool: pool, blobs: blobs, settings: settings, ffprobe: ffprobe,
		themes:    themesongs.NewRepository(pool),
		client:    &http.Client{Timeout: 60 * time.Second},
		sourceURL: defaultSourceURL,
		gap:       requestGap,
		now:       time.Now,
	}
	s.probeAudio = s.probe
	return s
}

// Config is the admin-editable configuration.
type Config struct {
	Enabled     bool `json:"enabled"`
	RecheckDays int  `json:"recheck_days"`
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
	cfg := Config{Enabled: enabled == "true", RecheckDays: DefaultRecheckDays}
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
	return cfg, nil
}

// Enabled reports whether downloading is switched on.
func (s *Service) Enabled(ctx context.Context) (bool, error) {
	cfg, err := s.Config(ctx)
	return cfg.Enabled, err
}

// Series is one TV series and where its theme comes from.
type Series struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	TVDBID       string     `json:"tvdb_id"`
	State        string     `json:"state" enum:"folder,downloaded,not_available,failed,pending"`
	Detail       string     `json:"detail,omitempty"`
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
)

// List returns every TV series with files, and its theme state.
func (s *Service) List(ctx context.Context) ([]Series, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT mi.content_id, mi.title, mi.tvdb_id, dt.downloaded_at, c.outcome, c.detail, c.checked_at
		FROM media_items mi
		LEFT JOIN downloaded_theme_songs dt ON dt.series_id = mi.content_id
		LEFT JOIN theme_song_checks c ON c.series_id = mi.content_id
		WHERE mi.type = 'series' AND EXISTS (
			SELECT 1 FROM episodes e
			JOIN media_files mf ON mf.episode_id = e.content_id
			JOIN media_folders f ON f.id = mf.media_folder_id
			WHERE e.series_id = mi.content_id AND mf.missing_since IS NULL AND f.enabled)
		ORDER BY lower(mi.title), mi.content_id`)
	if err != nil {
		return nil, err
	}
	var out []Series
	for rows.Next() {
		var sr Series
		var outcome, detail *string
		if err := rows.Scan(&sr.ID, &sr.Title, &sr.TVDBID, &sr.DownloadedAt, &outcome, &detail, &sr.CheckedAt); err != nil {
			rows.Close()
			return nil, err
		}
		switch {
		case sr.DownloadedAt != nil:
			sr.State = StateDownloaded
		case outcome != nil && *outcome == "not_available":
			sr.State = StateNotAvailable
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
		local, err := s.themes.HasFolderTheme(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		if local {
			out[i].State, out[i].Detail = StateFolder, ""
		}
	}
	return out, nil
}

// Report summarizes one run.
type Report struct {
	Checked      int      `json:"checked"`
	Downloaded   int      `json:"downloaded"`
	NotAvailable int      `json:"not_available"`
	Failed       int      `json:"failed"`
	Skipped      int      `json:"skipped" doc:"Series with a folder theme, a download, or a recent miss."`
	Failures     []string `json:"failures,omitempty"`
}

// Run downloads the themes still missing. force asks again for themes the
// source lacked, without waiting for the recheck interval. progress may be nil.
func (s *Service) Run(ctx context.Context, force bool, progress func(percent float64, message string)) (Report, error) {
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
	var due []Series
	for _, sr := range all {
		switch {
		case sr.State == StateFolder || sr.State == StateDownloaded:
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
			progress(float64(i)*100/float64(len(due)), "Checking "+sr.Title)
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
		if err := s.fetch(ctx, sr); err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			if errors.Is(err, errNotAvailable) {
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
		progress(100, fmt.Sprintf("Downloaded %d, not available %d, failed %d", report.Downloaded, report.NotAvailable, report.Failed))
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
		outcome, detail := "downloaded", ""
		if err := s.fetch(ctx, sr); errors.Is(err, errNotAvailable) {
			outcome = "not_available"
		} else if err != nil {
			outcome, detail = "failed", err.Error()
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO theme_song_checks (series_id, checked_at, outcome, detail) VALUES ($1, $2, $3, $4)
			ON CONFLICT (series_id) DO UPDATE SET checked_at = EXCLUDED.checked_at, outcome = EXCLUDED.outcome, detail = EXCLUDED.detail`,
			sr.ID, s.now(), outcome, detail); err != nil {
			return Series{}, err
		}
		return s.one(ctx, seriesID)
	}
	return Series{}, themesongs.ErrNotFound
}

// Remove deletes a series' downloaded theme. The next run fetches it again
// unless a folder theme has appeared.
func (s *Service) Remove(ctx context.Context, seriesID string) (Series, error) {
	var key string
	err := s.pool.QueryRow(ctx, `DELETE FROM downloaded_theme_songs WHERE series_id = $1 RETURNING blob_key`, seriesID).Scan(&key)
	if err == nil {
		if _, err := s.blobs.Delete(ctx, []string{key}); err != nil {
			slog.WarnContext(ctx, "theme songs: stored theme not deleted; the orphan sweep will remove it", "component", "themedownload", "key", key, "error", err)
		}
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

// fetch downloads, checks and stores one series' theme.
func (s *Service) fetch(ctx context.Context, sr Series) error {
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
		sr.ID, SourcePlex, tvdb, key, hash, file.DurationSeconds, file.AudioCodec, file.AudioChannels, file.BitrateKbps,
		file.SampleRate, int64(len(data)), s.now().Truncate(time.Microsecond)).Scan(&old)
	if err != nil {
		return err
	}
	if old != "" && old != key {
		_, _ = s.blobs.Delete(ctx, []string{old})
	}
	return nil
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
