package notifications

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFanoutSkipsProfilesThatMutedTheSeries covers the series mute at fanout:
// a profile that muted the series gets no delivery, so no channel sees the
// episode, and its notification cursor stays put. A refused mute changes
// nothing, and neither does the mute for a profile with the same ID on another
// account. The mute leaves the follow row as it was, and once the profile
// unmutes, the next episode notifies it again.
func TestFanoutSkipsProfilesThatMutedTheSeries(t *testing.T) {
	pool := inboxPageDB(t)
	ctx := t.Context()
	c := seedAccessCatalog(t, pool)
	seedFanoutEvent(t, pool, c, []string{"open", "muted", "other-muted"})
	s := &System{Follows: NewFollowRepository(pool)}
	if err := s.MuteSeries(ctx, 1, "muted", c.series); err != nil {
		t.Fatalf("MuteSeries: %v", err)
	}
	// A profile on account 2 with the same ID as account 1's "open" mutes the
	// series; account 1's "open" is still notified.
	if err := s.MuteSeries(ctx, 2, "open", c.series); err != nil {
		t.Fatalf("MuteSeries account 2: %v", err)
	}
	// A mute of an ID that is no series: refused, and fanout is unaffected.
	if err := s.MuteSeries(ctx, 1, "other-muted", c.episode); !errors.Is(err, ErrSeriesNotFound) {
		t.Fatalf("mute an episode = %v, want ErrSeriesNotFound", err)
	}

	worker := newAccessFanoutWorker(pool, scopeByProfile{"open": {}, "muted": {}, "other-muted": {}})
	if _, err := worker.processBatch(ctx); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if got := accountRecipients(t, pool, "event-1"); !slices.Equal(got, []string{"1/open", "1/other-muted"}) {
		t.Fatalf("event-1 recipients = %v, want account 1's open and other-muted", got)
	}
	var cursorMoved bool
	if err := pool.QueryRow(ctx, `
		SELECT last_notified_episode_key IS NOT NULL FROM profile_series_follows WHERE user_id = 1 AND profile_id = 'muted'`,
	).Scan(&cursorMoved); err != nil {
		t.Fatalf("read muted follow: %v", err)
	}
	if cursorMoved {
		t.Fatal("the muted profile's notification cursor moved; want the row unchanged")
	}

	if err := s.UnmuteSeries(ctx, 1, "muted", c.series); err != nil {
		t.Fatalf("UnmuteSeries: %v", err)
	}
	nextKey := EpisodeKey(1, 3)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO episodes (content_id, series_id, season_id, season_number, episode_number, title)
			VALUES ($1 || '-e3', $1, $1 || '-s1', 1, 3, 'E3')`, []any{c.series}},
		{`INSERT INTO release_events (id, library_id, series_id, episode_id, season_number, episode_number,
			episode_key, available_at, dedupe_key)
			VALUES ('event-2', $1, $2, $2 || '-e3', 1, 3, $3, now(), $4)`,
			[]any{c.library, c.series, nextKey, EpisodeDedupeKey(c.library, c.series, nextKey)}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed next episode: %v", err)
		}
	}
	if _, err := worker.processBatch(ctx); err != nil {
		t.Fatalf("processBatch after unmute: %v", err)
	}
	if got := accountRecipients(t, pool, "event-2"); !slices.Equal(got, []string{"1/muted", "1/open", "1/other-muted"}) {
		t.Fatalf("event-2 recipients = %v, want every profile once unmuted", got)
	}
}

// accountRecipients lists a release event's deliveries as account/profile.
func accountRecipients(t *testing.T, pool *pgxpool.Pool, eventID string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT user_id::text || '/' || profile_id FROM notification_deliveries
		WHERE release_event_id = $1 ORDER BY 1`, eventID)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	return got
}

// seedSeriesMutes creates the list's tables in a test schema with two series
// and a movie, the Bear followed by p1 and p2 on account 1 and by p1 on account 2, and account 1 p1's mutes of both
// series, each written twice.
func seedSeriesMutes(t *testing.T) (*pgxpool.Pool, *System) {
	t.Helper()
	pool := schemaDB(t, "series_mutes_")
	ctx := t.Context()
	for _, stmt := range []string{
		`CREATE TABLE profile_series_follows (LIKE public.profile_series_follows INCLUDING ALL)`,
		`CREATE TABLE media_items (LIKE public.media_items INCLUDING ALL)`,
		`CREATE TABLE notification_preferences (LIKE public.notification_preferences INCLUDING ALL)`,
		`CREATE TABLE profile_series_notification_mutes (LIKE public.profile_series_notification_mutes INCLUDING ALL)`,
		`INSERT INTO media_items (content_id, type, title, sort_title, genres, year) VALUES
			('series:bear', 'series', 'The Bear', '', '{}', 2022),
			('series:andor', 'series', 'Andor', '', '{}', 2022),
			('movie:heat', 'movie', 'Heat', '', '{}', 1995)`,
		`INSERT INTO profile_series_follows (user_id, profile_id, series_id) VALUES
			(1, 'p1', 'series:bear'),
			(1, 'p2', 'series:bear'),
			(2, 'p1', 'series:bear')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	s := &System{Follows: NewFollowRepository(pool), Preferences: NewPreferencesRepository(pool)}
	for range 2 {
		for _, id := range []string{"series:bear", "series:andor"} {
			if err := s.MuteSeries(ctx, 1, "p1", id); err != nil {
				t.Fatalf("MuteSeries(%s): %v", id, err)
			}
		}
	}
	return pool, s
}

// TestSeriesMutesList covers the mute store and the notified-series list:
// muting is per profile and account and idempotent, only series can be muted, a muted
// series is listed muted and a muted series that is not followed is still
// listed so it can be unmuted.
func TestSeriesMutesList(t *testing.T) {
	_, s := seedSeriesMutes(t)
	ctx := t.Context()
	for _, id := range []string{"movie:heat", "series:gone"} {
		if err := s.MuteSeries(ctx, 1, "p1", id); !errors.Is(err, ErrSeriesNotFound) {
			t.Fatalf("MuteSeries(%s) = %v, want ErrSeriesNotFound", id, err)
		}
	}
	got, err := s.ListNotifiedSeries(ctx, 1, "p1")
	if err != nil {
		t.Fatalf("ListNotifiedSeries: %v", err)
	}
	if len(got) != 2 || got[0].SeriesID != "series:andor" || got[1].SeriesID != "series:bear" {
		t.Fatalf("p1 series = %+v, want andor then bear", got)
	}
	andor, bear := got[0], got[1]
	if !andor.Muted || andor.Followed || andor.Title != "Andor" {
		t.Errorf("andor = %+v, want a muted entry that is not followed", andor)
	}
	if !bear.Muted || !bear.Followed {
		t.Errorf("bear = %+v, want muted and still followed", bear)
	}
	for _, profile := range []struct {
		userID int
		id     string
	}{{1, "p2"}, {2, "p1"}} {
		other, err := s.ListNotifiedSeries(ctx, profile.userID, profile.id)
		if err != nil {
			t.Fatalf("ListNotifiedSeries %d/%s: %v", profile.userID, profile.id, err)
		}
		if len(other) != 1 || other[0].Muted || !other[0].Followed {
			t.Fatalf("%d/%s series = %+v, want bear followed and unmuted: a mute is per profile and account", profile.userID, profile.id, other)
		}
	}
}

// TestSeriesMutesUnmuteAndPurge covers lifting a mute, which is idempotent and
// drops a series that is not followed from the list, and the removal of a deleted
// profile's mutes, which leaves a same-ID profile on another account alone.
func TestSeriesMutesUnmuteAndPurge(t *testing.T) {
	pool, s := seedSeriesMutes(t)
	ctx := t.Context()
	for range 2 {
		if err := s.UnmuteSeries(ctx, 1, "p1", "series:andor"); err != nil {
			t.Fatalf("UnmuteSeries: %v", err)
		}
	}
	got, err := s.ListNotifiedSeries(ctx, 1, "p1")
	if err != nil {
		t.Fatalf("ListNotifiedSeries: %v", err)
	}
	if len(got) != 1 || got[0].SeriesID != "series:bear" || !got[0].Muted {
		t.Fatalf("after unmute = %+v, want only bear, still muted", got)
	}
	// Account 2's p1 mutes too; deleting account 1's p1 leaves it.
	if err := s.MuteSeries(ctx, 2, "p1", "series:bear"); err != nil {
		t.Fatalf("MuteSeries account 2: %v", err)
	}
	if err := s.Follows.DeleteAllForProfile(ctx, 1, "p1"); err != nil {
		t.Fatalf("deleteMutesForProfile: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT user_id::text || '/' || profile_id || '/' || series_id FROM profile_series_notification_mutes`)
	if err != nil {
		t.Fatal(err)
	}
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !slices.Equal(left, []string{"2/p1/series:bear"}) {
		t.Fatalf("mutes left = %v (%v), want only account 2's", left, err)
	}
}
