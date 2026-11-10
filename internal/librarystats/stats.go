// Package librarystats summarizes what a library holds on disk: how many video
// files, how much space they take, and how that splits by resolution.
package librarystats

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Band is one resolution band of a library: how many files and how many bytes.
type Band struct {
	// Resolution is 4k, 1080p, 720p, sd or unknown.
	Resolution string
	Files      int
	SizeBytes  int64
}

// Stats reads library summaries.
type Stats struct {
	pool *pgxpool.Pool
}

// New builds a summary reader over pool.
func New(pool *pgxpool.Pool) *Stats { return &Stats{pool: pool} }

// Summary groups a library's present video files (extras and files marked
// missing are left out) by resolution band.
func (s *Stats) Summary(ctx context.Context, libraryID int) ([]Band, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			CASE
				WHEN COALESCE(resolution, '') = '' THEN 'unknown'
				WHEN lower(resolution) IN ('2160p', '4k', 'uhd') THEN '4k'
				WHEN lower(resolution) IN ('1080p', '1440p') THEN '1080p'
				WHEN lower(resolution) = '720p' THEN '720p'
				ELSE 'sd'
			END AS band,
			count(*)::int,
			COALESCE(sum(file_size), 0)::bigint
		FROM media_files
		WHERE media_folder_id = $1 AND missing_since IS NULL AND extra_id IS NULL
		GROUP BY 1
		ORDER BY 1`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("library stats: summarizing media files: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Band, error) {
		var b Band
		return b, row.Scan(&b.Resolution, &b.Files, &b.SizeBytes)
	})
}
