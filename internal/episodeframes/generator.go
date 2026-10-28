// Package episodeframes gives episodes a thumbnail taken from a frame of
// their own video when the providers have no image, or, if the library asks
// for it, none of at least full HD. Libraries choose this with
// media_folders.episode_frame_stills; a frame only replaces a provider image
// it is wider than.
//
// A frame still is stored like any cached still, with still_source_path
// "frame:<file id>". episode_frame_still_checks remembers each decision and,
// for a frame, the provider source it displaced: metadata refreshes keep the
// frame while the provider still offers that same source
// (internal/metadata episodeFrameStills).
package episodeframes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // provider stills and extracted frames
	_ "image/png"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "golang.org/x/image/webp" // cached originals are WebP

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/chapterthumbs"
	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/Silo-Server/silo-server/internal/models"
)

// lockKey keeps one replica generating at a time ("SILOEPF").
const lockKey int64 = 0x53494c4f455046

// SourcePrefix starts the still_source_path of a frame still, followed by the
// media file id; the same prefix local extras use.
const SourcePrefix = catalog.StillSourceFramePrefix

// Full HD is the size a provider still needs to be kept when the library
// takes frames for anything smaller.
const (
	fullHDWidth  = 1920
	fullHDHeight = 1080
)

// retryDelay is how long an episode waits after a failure that may pass, such
// as a timeout on a busy server or a share that is offline.
const retryDelay = 6 * time.Hour

// Outcomes recorded in episode_frame_still_checks.
const (
	outcomeProviderOK = "provider_ok"
	outcomeFrame      = "frame"
	outcomeFailed     = "failed"
)

// FileLoader loads the media file a frame is taken from.
type FileLoader interface {
	GetByID(ctx context.Context, id int) (*models.MediaFile, error)
}

// FrameExtractor takes one JPEG frame of a file at full size;
// chapterthumbs.Service.
type FrameExtractor interface {
	ExtractStill(ctx context.Context, file *models.MediaFile, seekSeconds float64) ([]byte, error)
}

// StillCacher stores an episode's frame still; imagecache.Cacher.
type StillCacher interface {
	CacheEpisodeFrameStill(ctx context.Context, data []byte, seriesID string, seasonNumber, episodeNumber int) (storedPath string, thumbhash string, err error)
}

// StillReader reads cached artwork, to measure provider stills.
type StillReader interface {
	Get(ctx context.Context, key string) (io.ReadCloser, blobstore.ObjectInfo, error)
}

// Report counts what a run did.
type Report struct {
	Measured   int // stills whose size was read
	Checked    int // episodes looked at for a frame
	Framed     int // frames saved
	ProviderOK int // provider images kept: full HD, or no smaller than the frame
	Failed     int
}

// Generator decides, episode by episode, between the provider still and a
// frame of the video.
type Generator struct {
	pool   *pgxpool.Pool
	files  FileLoader
	frames FrameExtractor
	cacher StillCacher
	stills StillReader
}

// NewGenerator builds a generator over the episodes in pool.
func NewGenerator(pool *pgxpool.Pool, files FileLoader, frames FrameExtractor, cacher StillCacher, stills StillReader) *Generator {
	return &Generator{pool: pool, files: files, frames: frames, cacher: cacher, stills: stills}
}

// candidate is an episode the generator has to look at.
type candidate struct {
	ContentID     string
	SeriesID      string
	SeasonNumber  int
	EpisodeNumber int
	StillPath     string
	StillSource   string
	Mode          string
	Position      int
	FileID        int
	Duration      int
	// Displaced is the provider source a frame still replaced, for a frame
	// still whose stored image went missing.
	Displaced string
	// Width and Height are the measured size of StillPath; zero when it
	// could not be read.
	Width, Height int
}

// Generate measures up to measureLimit episode stills, then looks at up to
// frameLimit episodes for a frame, and reports what it did. It does nothing
// while another replica is generating, and stops early when no transcode node
// can be reserved.
func (g *Generator) Generate(ctx context.Context, measureLimit, frameLimit int) (Report, error) {
	var report Report
	lock, acquired, err := pglock.TryAcquire(ctx, g.pool, lockKey)
	if err != nil || !acquired {
		return report, err
	}
	defer func() {
		if err := lock.Release(context.WithoutCancel(ctx)); err != nil {
			slog.WarnContext(ctx, "episode frames: releasing lock failed", "component", "episodeframes", "error", err)
		}
	}()

	measured, err := g.measure(ctx, measureLimit)
	report.Measured = measured
	if err != nil {
		return report, err
	}
	pending, err := g.listCandidates(ctx, frameLimit)
	if err != nil {
		return report, err
	}
	for _, c := range pending {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		report.Checked++
		outcome, err := g.process(ctx, c)
		if errors.Is(err, chapterthumbs.ErrExtractionUnavailable) {
			slog.InfoContext(ctx, "episode frames: no transcode node could be reserved; trying again next run",
				"component", "episodeframes", "episode_id", c.ContentID, "error", err)
			return report, nil
		}
		switch outcome {
		case outcomeFrame:
			report.Framed++
		case outcomeProviderOK:
			report.ProviderOK++
		case outcomeFailed:
			report.Failed++
		}
	}
	return report, nil
}

// listCandidates finds episodes in a library that takes frames, with a video
// file, whose current still the generator has not judged yet: no still, a
// measured provider still too small for a below_full_hd library, a frame still
// whose image went missing, or a failure due for another try. A provider
// still waits until it is cached and measured.
func (g *Generator) listCandidates(ctx context.Context, limit int) ([]candidate, error) {
	rows, err := g.pool.Query(ctx, `
		SELECT e.content_id, e.series_id, e.season_number, e.episode_number,
		       COALESCE(e.still_path, ''), e.still_source_path,
		       f.episode_frame_stills, f.episode_frame_position,
		       mf.id, COALESCE(mf.duration, 0)::int,
		       COALESCE(c.source_path, ''),
		       COALESCE(s.width, 0), COALESCE(s.height, 0)
		FROM episodes e
		JOIN LATERAL (
			SELECT f.episode_frame_stills, f.episode_frame_position
			FROM episode_libraries el
			JOIN media_folders f ON f.id = el.media_folder_id
			WHERE el.episode_id = e.content_id
			  AND f.enabled IS NOT FALSE
			  AND f.episode_frame_stills <> 'off'
			ORDER BY (f.episode_frame_stills = 'below_full_hd') DESC, f.id
			LIMIT 1
		) f ON true
		JOIN LATERAL (
			SELECT m.id, m.duration
			FROM media_files m
			WHERE m.episode_id = e.content_id AND m.missing_since IS NULL AND m.extra_id IS NULL
			ORDER BY m.id
			LIMIT 1
		) mf ON true
		LEFT JOIN episode_frame_still_checks c ON c.content_id = e.content_id
		LEFT JOIN episode_still_sizes s ON s.content_id = e.content_id AND s.still_path = e.still_path
		WHERE (
			(e.still_source_path LIKE 'frame:%' AND COALESCE(e.still_path, '') = '')
			OR (
				e.still_source_path NOT LIKE 'frame:%'
				AND (
					(e.still_source_path = '' AND COALESCE(e.still_path, '') = '')
					OR (f.episode_frame_stills = 'below_full_hd'
						AND e.still_source_path <> ''
						AND s.content_id IS NOT NULL)
				)
				AND (
					c.content_id IS NULL
					OR c.source_path <> e.still_source_path
					OR c.outcome = 'frame'
					OR (c.outcome = 'failed' AND c.retry_after IS NOT NULL AND c.retry_after <= now())
				)
			)
		)
		ORDER BY e.series_id, e.season_number, e.episode_number
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("episode frames: listing candidates: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (candidate, error) {
		var c candidate
		err := row.Scan(&c.ContentID, &c.SeriesID, &c.SeasonNumber, &c.EpisodeNumber,
			&c.StillPath, &c.StillSource, &c.Mode, &c.Position, &c.FileID, &c.Duration, &c.Displaced,
			&c.Width, &c.Height)
		return c, err
	})
}

// process decides one episode and returns the outcome it recorded, or "" when
// it recorded nothing and the episode comes up again next run.
func (g *Generator) process(ctx context.Context, c candidate) (string, error) {
	attrs := []any{"component", "episodeframes", "episode_id", c.ContentID, "file_id", c.FileID}
	isFrame := len(c.StillSource) >= len(SourcePrefix) && c.StillSource[:len(SourcePrefix)] == SourcePrefix
	displaced := c.StillSource
	if isFrame {
		displaced = c.Displaced
	}

	providerWidth := 0
	if !isFrame && c.StillSource != "" {
		if isFullHD(c.Width, c.Height) {
			return outcomeProviderOK, g.record(ctx, c.ContentID, displaced, outcomeProviderOK, c.Width, 0, nil)
		}
		providerWidth = c.Width
	}

	file, err := g.files.GetByID(ctx, c.FileID)
	if err != nil {
		slog.WarnContext(ctx, "episode frames: loading file failed", append(attrs, "error", err)...)
		return "", nil
	}
	frame, frameWidth, frameHeight, err := g.takeFrame(ctx, file, c.Duration, c.Position)
	if err != nil {
		if errors.Is(err, chapterthumbs.ErrExtractionUnavailable) || ctx.Err() != nil {
			return "", err
		}
		return g.recordFailure(ctx, c, displaced, err, attrs)
	}
	if frameWidth <= providerWidth {
		// A low-resolution video gives nothing sharper than the provider.
		return outcomeProviderOK, g.record(ctx, c.ContentID, displaced, outcomeProviderOK, providerWidth, frameWidth, nil)
	}

	path, thumbhash, err := g.cacher.CacheEpisodeFrameStill(ctx, frame, c.SeriesID, c.SeasonNumber, c.EpisodeNumber)
	if err != nil {
		slog.WarnContext(ctx, "episode frames: storing frame failed", append(attrs, "error", err)...)
		return "", nil
	}
	saved, err := g.saveFrame(ctx, c, path, thumbhash, displaced, providerWidth, frameWidth, frameHeight)
	if err != nil {
		slog.WarnContext(ctx, "episode frames: saving failed", append(attrs, "error", err)...)
		return "", nil
	}
	if !saved {
		// A refresh changed the still meanwhile; the stored frame is left
		// unreferenced for the artwork revision GC.
		return "", nil
	}
	return outcomeFrame, nil
}

// saveFrame points the episode at the frame unless a refresh changed its still
// meanwhile, and records the decision in the same transaction.
func (g *Generator) saveFrame(ctx context.Context, c candidate, path, thumbhash, displaced string, providerWidth, frameWidth, frameHeight int) (bool, error) {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE episodes
		SET still_path = $2, still_thumbhash = $3, still_source_path = $4, updated_at = now()
		WHERE content_id = $1 AND still_source_path = $5`,
		c.ContentID, path, thumbhash, SourcePrefix+strconv.Itoa(c.FileID), c.StillSource)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	if err := recordIn(ctx, tx, c.ContentID, displaced, outcomeFrame, providerWidth, frameWidth, nil); err != nil {
		return false, err
	}
	if err := recordSize(ctx, tx, c.ContentID, path, frameWidth, frameHeight); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// recordFailure records a failed measurement or extraction. An undecodable
// file is not tried again until the episode's still changes.
func (g *Generator) recordFailure(ctx context.Context, c candidate, displaced string, err error, attrs []any) (string, error) {
	var retryAfter *time.Time
	if errors.Is(err, chapterthumbs.ErrUndecodableFile) {
		slog.WarnContext(ctx, "episode frames: file cannot be decoded; skipped until the still changes", append(attrs, "error", err)...)
	} else {
		at := time.Now().Add(retryDelay)
		retryAfter = &at
		slog.WarnContext(ctx, "episode frames: failed; trying again later", append(attrs, "retry_after", at, "error", err)...)
	}
	return outcomeFailed, g.record(ctx, c.ContentID, displaced, outcomeFailed, 0, 0, retryAfter)
}

func (g *Generator) record(ctx context.Context, contentID, source, outcome string, providerWidth, frameWidth int, retryAfter *time.Time) error {
	return recordIn(ctx, g.pool, contentID, source, outcome, providerWidth, frameWidth, retryAfter)
}

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func recordIn(ctx context.Context, db execer, contentID, source, outcome string, providerWidth, frameWidth int, retryAfter *time.Time) error {
	_, err := db.Exec(ctx, `
		INSERT INTO episode_frame_still_checks (content_id, source_path, outcome, provider_width, frame_width, retry_after, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (content_id) DO UPDATE
		SET source_path = EXCLUDED.source_path, outcome = EXCLUDED.outcome,
		    provider_width = EXCLUDED.provider_width, frame_width = EXCLUDED.frame_width,
		    retry_after = EXCLUDED.retry_after, checked_at = now()`,
		contentID, source, outcome, providerWidth, frameWidth, retryAfter)
	if err != nil {
		return fmt.Errorf("episode frames: recording %s: %w", contentID, err)
	}
	return nil
}

// measure reads the size of up to limit cached episode stills not measured
// yet, and reports how many it read. A still that cannot be read is recorded
// as 0x0 so it is not read again until it changes.
func (g *Generator) measure(ctx context.Context, limit int) (int, error) {
	rows, err := g.pool.Query(ctx, `
		SELECT e.content_id, e.still_path
		FROM episodes e
		LEFT JOIN episode_still_sizes s ON s.content_id = e.content_id
		WHERE COALESCE(e.still_path, '') <> ''
		  AND e.still_path NOT LIKE '%://%'
		  AND (s.content_id IS NULL OR s.still_path <> e.still_path)
		  AND EXISTS (SELECT 1 FROM episode_libraries el WHERE el.episode_id = e.content_id)
		LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("episode frames: listing unmeasured stills: %w", err)
	}
	type unmeasured struct{ contentID, path string }
	pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (unmeasured, error) {
		var u unmeasured
		return u, row.Scan(&u.contentID, &u.path)
	})
	if err != nil {
		return 0, fmt.Errorf("episode frames: listing unmeasured stills: %w", err)
	}
	measured := 0
	for _, u := range pending {
		if ctx.Err() != nil {
			return measured, ctx.Err()
		}
		width, height, err := g.stillSize(ctx, u.path)
		if err != nil {
			slog.DebugContext(ctx, "episode frames: reading still size failed", "component", "episodeframes",
				"episode_id", u.contentID, "still_path", u.path, "error", err)
		}
		if err := recordSize(ctx, g.pool, u.contentID, u.path, width, height); err != nil {
			return measured, err
		}
		measured++
	}
	return measured, nil
}

func recordSize(ctx context.Context, db execer, contentID, path string, width, height int) error {
	_, err := db.Exec(ctx, `
		INSERT INTO episode_still_sizes (content_id, still_path, width, height, measured_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (content_id) DO UPDATE
		SET still_path = EXCLUDED.still_path, width = EXCLUDED.width, height = EXCLUDED.height, measured_at = now()`,
		contentID, path, width, height)
	if err != nil {
		return fmt.Errorf("episode frames: recording size of %s: %w", contentID, err)
	}
	return nil
}

// stillSize reads a cached still's dimensions from its header.
func (g *Generator) stillSize(ctx context.Context, key string) (int, int, error) {
	r, _, err := g.stills.Get(ctx, key)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// takeFrame takes the frame at position percent of the way in, or, when that
// frame is dark or blank (a fade, a title card), the first good one of a few
// nearby positions, falling back to the most detailed one taken. It returns
// the JPEG and its size.
func (g *Generator) takeFrame(ctx context.Context, file *models.MediaFile, durationSeconds, position int) ([]byte, int, int, error) {
	var best []byte
	var bestSize image.Point
	bestScore := -1.0
	var lastErr error
	for _, at := range framePositions(durationSeconds, position) {
		data, err := g.frames.ExtractStill(ctx, file, at)
		if err != nil {
			if errors.Is(err, chapterthumbs.ErrExtractionUnavailable) || errors.Is(err, chapterthumbs.ErrUndecodableFile) || ctx.Err() != nil {
				return nil, 0, 0, err
			}
			lastErr = err
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			lastErr = err
			continue
		}
		mean, spread := lumaStats(img)
		size := img.Bounds().Size()
		if mean >= minMeanLuma && spread >= minLumaSpread {
			return data, size.X, size.Y, nil
		}
		if score := mean + spread; score > bestScore {
			best, bestSize, bestScore = data, size, score
		}
	}
	if best == nil {
		if lastErr == nil {
			lastErr = errors.New("no frame taken")
		}
		return nil, 0, 0, lastErr
	}
	return best, bestSize.X, bestSize.Y, nil
}

func isFullHD(width, height int) bool {
	return width >= fullHDWidth || height >= fullHDHeight
}
