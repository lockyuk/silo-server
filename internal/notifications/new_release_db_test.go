package notifications

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
)

// TestFanoutNewReleasePreferences covers the "everything new" preferences:
// a profile that asked for every new episode hears about a series it does not
// follow unless it muted the series, and only profiles that asked for every
// new movie, and can open it, hear about a movie. A second copy of the movie
// in another library is not news.
func TestFanoutNewReleasePreferences(t *testing.T) {
	pool := inboxPageDB(t)
	ctx := t.Context()
	c := seedAccessCatalog(t, pool)
	seedFanoutEvent(t, pool, c, []string{"follower"})
	movie := c.series + "-movie"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = $1`, movie) })
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO notification_preferences (user_id, profile_id, notify_new_episodes, notify_new_movies) VALUES
			(1, 'everything', true, true),
			(1, 'movies', false, true),
			(1, 'muted', true, false),
			(1, 'locked-out', false, true),
			(1, 'switched-off', true, true)`, nil},
		{`UPDATE notification_preferences SET enabled = false WHERE profile_id = 'switched-off'`, nil},
		{`INSERT INTO profile_series_notification_mutes (user_id, profile_id, series_id) VALUES (1, 'muted', $1)`, []any{c.series}},
		{`INSERT INTO media_items (content_id, type, title, genres) VALUES ($1, 'movie', 'Movie', '{}')`, []any{movie}},
		{`INSERT INTO media_item_libraries (content_id, media_folder_id) VALUES ($1, $2), ($1, $3)`,
			[]any{movie, c.library, c.otherLibrary}},
		{`INSERT INTO release_events (id, library_id, kind, item_id, available_at, dedupe_key)
			VALUES ('movie-1', $1, 'movie', $2, now(), $3)`, []any{c.library, movie, fmt.Sprintf("movie:%d:%s", c.library, movie)}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	open := access.Scope{}
	worker := newAccessFanoutWorker(pool, scopeByProfile{
		"follower": open, "everything": open, "movies": open, "muted": open, "switched-off": open,
		"locked-out": {AllowedLibraryIDs: []int{}},
	})
	if _, err := worker.processBatch(ctx); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if got := accountRecipients(t, pool, "event-1"); !slices.Equal(got, []string{"1/everything", "1/follower"}) {
		t.Fatalf("episode recipients = %v, want the follower and the everything profile", got)
	}
	if got := accountRecipients(t, pool, "movie-1"); !slices.Equal(got, []string{"1/everything", "1/movies"}) {
		t.Fatalf("movie recipients = %v, want the two profiles that asked and can open it", got)
	}
	var kind, item string
	var flags []byte
	if err := pool.QueryRow(ctx, `
		SELECT type, series_id, reason_flags FROM notification_deliveries
		WHERE release_event_id = 'movie-1' AND profile_id = 'movies'`).Scan(&kind, &item, &flags); err != nil {
		t.Fatalf("read movie delivery: %v", err)
	}
	var reasons ReasonFlags
	if err := json.Unmarshal(flags, &reasons); err != nil {
		t.Fatalf("reason flags: %v", err)
	}
	if kind != DeliveryTypeMovieAvailable || item != movie || !reasons.NewRelease {
		t.Fatalf("movie delivery = %s %s %+v, want movie.available for the movie with new_release", kind, item, reasons)
	}
	var everythingReasons []byte
	if err := pool.QueryRow(ctx, `
		SELECT reason_flags FROM notification_deliveries
		WHERE release_event_id = 'event-1' AND profile_id = 'everything'`).Scan(&everythingReasons); err != nil {
		t.Fatalf("read episode delivery: %v", err)
	}
	if err := json.Unmarshal(everythingReasons, &reasons); err != nil || !reasons.NewRelease || reasons.Favorite {
		t.Fatalf("episode reasons = %+v (%v), want new_release only", reasons, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO release_events (id, library_id, kind, item_id, available_at, dedupe_key)
		VALUES ('movie-2', $1, 'movie', $2, now(), $3)`, c.otherLibrary, movie, fmt.Sprintf("movie:%d:%s", c.otherLibrary, movie)); err != nil {
		t.Fatalf("seed second copy: %v", err)
	}
	if _, err := worker.processBatch(ctx); err != nil {
		t.Fatalf("processBatch second copy: %v", err)
	}
	if got := accountRecipients(t, pool, "movie-2"); len(got) != 0 {
		t.Fatalf("second copy recipients = %v, want none", got)
	}
}
