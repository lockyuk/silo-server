package notifications

import (
	"errors"
	"slices"
	"testing"
)

// TestFanoutFollowsAndEverythingNew covers who a new episode reaches: a
// follower always (whatever its "everything new" switch), a profile that asked
// for everything new, and nobody else; a muted series reaches no one, and
// following lifts the mute.
func TestFanoutFollowsAndEverythingNew(t *testing.T) {
	pool := inboxPageDB(t)
	ctx := t.Context()
	c := seedAccessCatalog(t, pool)
	// seedFanoutEvent makes every listed profile follow the series.
	seedFanoutEvent(t, pool, c, []string{"follower", "muted-follower"})
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, profile_id, enabled, notify_new_episodes, notify_new_movies) VALUES
			(1, 'follower', true, false, false),
			(1, 'bystander', true, false, false),
			(1, 'everything', true, true, true),
			(1, 'everything-muted', true, true, true)`); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	s := &System{Follows: NewFollowRepository(pool)}
	if err := s.MuteSeries(ctx, 1, "muted-follower", c.series); err != nil {
		t.Fatalf("MuteSeries: %v", err)
	}
	if err := s.MuteSeries(ctx, 1, "everything-muted", c.series); err != nil {
		t.Fatalf("MuteSeries: %v", err)
	}
	scopes := scopeByProfile{"follower": {}, "muted-follower": {}, "bystander": {}, "everything": {}, "everything-muted": {}}
	worker := newAccessFanoutWorker(pool, scopes)
	if _, err := worker.processBatch(ctx); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if got := accountRecipients(t, pool, "event-1"); !slices.Equal(got, []string{"1/everything", "1/follower"}) {
		t.Fatalf("recipients = %v, want the follower and the everything-new profile", got)
	}
}

// TestFollowUnfollow covers the follow list: following is idempotent, only
// series can be followed, a follow lifts the mute, and unfollowing removes the
// series from the list.
func TestFollowUnfollow(t *testing.T) {
	_, s := seedSeriesMutes(t)
	ctx := t.Context()
	for _, id := range []string{"movie:heat", "series:gone"} {
		if err := s.FollowSeries(ctx, 1, "p1", id); !errors.Is(err, ErrSeriesNotFound) {
			t.Fatalf("FollowSeries(%s) = %v, want ErrSeriesNotFound", id, err)
		}
	}
	for range 2 {
		if err := s.FollowSeries(ctx, 1, "p1", "series:andor"); err != nil {
			t.Fatalf("FollowSeries: %v", err)
		}
	}
	got, err := s.ListNotifiedSeries(ctx, 1, "p1")
	if err != nil {
		t.Fatalf("ListNotifiedSeries: %v", err)
	}
	if len(got) != 2 || got[0].SeriesID != "series:andor" || !got[0].Followed || got[0].Muted {
		t.Fatalf("after follow = %+v, want andor followed and no longer muted", got)
	}
	for range 2 {
		if err := s.UnfollowSeries(ctx, 1, "p1", "series:andor"); err != nil {
			t.Fatalf("UnfollowSeries: %v", err)
		}
	}
	got, err = s.ListNotifiedSeries(ctx, 1, "p1")
	if err != nil {
		t.Fatalf("ListNotifiedSeries: %v", err)
	}
	if len(got) != 1 || got[0].SeriesID != "series:bear" {
		t.Fatalf("after unfollow = %+v, want only bear", got)
	}
}
