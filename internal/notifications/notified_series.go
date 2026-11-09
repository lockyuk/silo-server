package notifications

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// NotifiedSeries is one series a profile follows or muted.
type NotifiedSeries struct {
	SeriesID string
	SeriesSortFields
	// Followed is set when the profile follows the series.
	Followed bool
	// Muted is set when the profile muted the series: it hears nothing about it,
	// followed or not.
	Muted bool
}

// SeriesSortFields are the catalog fields the notified-series list is
// searched and sorted by.
type SeriesSortFields struct {
	// SortKey is the default order: the case-folded sort title, else the title.
	SortKey string
	Title   string
	// Year is 0 when the catalog has none.
	Year    int
	AddedAt time.Time
	// LatestEpisodeAddedAt is when the series last gained an episode; nil
	// before its first.
	LatestEpisodeAddedAt *time.Time
}

// ListNotifiedSeries answers every series the profile on userID's account
// follows, and every series it muted, ordered by SortKey then SeriesID. It
// does not judge catalog access; callers filter the series the profile may
// not open. Series the catalog no longer has are skipped.
func (s *System) ListNotifiedSeries(ctx context.Context, userID int, profileID string) ([]NotifiedSeries, error) {
	rows, err := s.Follows.pool.Query(ctx, `
		WITH followed AS (
			SELECT series_id FROM profile_series_follows WHERE user_id = $2 AND profile_id = $1
		), muted AS (
			SELECT series_id FROM profile_series_notification_mutes WHERE user_id = $2 AND profile_id = $1
		)
		SELECT mi.content_id, f.series_id IS NOT NULL, m.series_id IS NOT NULL,
		       lower(COALESCE(NULLIF(mi.sort_title, ''), mi.title)), mi.title,
		       COALESCE(mi.year, 0), mi.created_at, mi.latest_episode_added_at
		FROM followed f
		FULL JOIN muted m ON m.series_id = f.series_id
		JOIN media_items mi ON mi.content_id = COALESCE(f.series_id, m.series_id)`,
		profileID, userID)
	if err != nil {
		return nil, fmt.Errorf("list notified series: %w", err)
	}
	defer rows.Close()
	out := make([]NotifiedSeries, 0, 32)
	for rows.Next() {
		var row NotifiedSeries
		if err := rows.Scan(&row.SeriesID, &row.Followed, &row.Muted,
			&row.SortKey, &row.Title, &row.Year, &row.AddedAt, &row.LatestEpisodeAddedAt); err != nil {
			return nil, fmt.Errorf("scan notified series: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b NotifiedSeries) int {
		return cmp.Or(strings.Compare(a.SortKey, b.SortKey), strings.Compare(a.SeriesID, b.SeriesID))
	})
	return out, nil
}
