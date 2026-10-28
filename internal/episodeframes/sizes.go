package episodeframes

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SizeCount is how many of a library's episodes have a still from one source
// in one size band.
type SizeCount struct {
	// Source is tmdb, tvdb, frame (a frame of the video), local (an image
	// beside the file), other, or none.
	Source string
	// Size is 4k, 1080p, 720p, below_720p, none, unmeasured (not measured
	// yet, or still downloading) or unreadable.
	Size  string
	Count int
}

// Sizes summarizes the size of libraries' episode stills.
type Sizes struct {
	pool *pgxpool.Pool
}

// NewSizes builds a summary reader over pool.
func NewSizes(pool *pgxpool.Pool) *Sizes { return &Sizes{pool: pool} }

// Summary counts a library's episodes by still source and size band.
func (s *Sizes) Summary(ctx context.Context, libraryID int) ([]SizeCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			CASE
				WHEN e.still_source_path LIKE 'tmdb://%' THEN 'tmdb'
				WHEN e.still_source_path LIKE 'tvdb://%' THEN 'tvdb'
				WHEN e.still_source_path LIKE 'frame:%' THEN 'frame'
				WHEN e.still_source_path LIKE 'file://%' OR e.still_source_path LIKE 'nfo+%' THEN 'local'
				WHEN COALESCE(e.still_path, '') = '' THEN 'none'
				ELSE 'other'
			END AS source,
			CASE
				WHEN COALESCE(e.still_path, '') = '' THEN 'none'
				WHEN s.content_id IS NULL THEN 'unmeasured'
				WHEN s.width = 0 THEN 'unreadable'
				WHEN s.width >= 3840 OR s.height >= 2160 THEN '4k'
				WHEN s.width >= 1920 OR s.height >= 1080 THEN '1080p'
				WHEN s.width >= 1280 OR s.height >= 720 THEN '720p'
				ELSE 'below_720p'
			END AS size,
			count(*)::int
		FROM episode_libraries el
		JOIN episodes e ON e.content_id = el.episode_id
		LEFT JOIN episode_still_sizes s ON s.content_id = e.content_id AND s.still_path = e.still_path
		WHERE el.media_folder_id = $1
		GROUP BY 1, 2
		ORDER BY 1, 2`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("episode frames: summarizing still sizes: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (SizeCount, error) {
		var c SizeCount
		return c, row.Scan(&c.Source, &c.Size, &c.Count)
	})
}
