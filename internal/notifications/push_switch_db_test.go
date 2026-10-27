package notifications

import (
	"context"
	"testing"
)

// A profile that switched push off gets no devices back from the fanout
// lookup, so no push attempt is queued; a missing preferences row means on.
func TestListEnabledPushByProfilesSkipsProfilesWithPushOffDB(t *testing.T) {
	ctx := context.Background()
	repo, pool := newPushDeviceTestRepo(t)
	if _, err := pool.Exec(ctx, `
		CREATE TEMP TABLE notification_preferences (
			user_id integer NOT NULL,
			profile_id text NOT NULL,
			push_enabled boolean NOT NULL DEFAULT true
		) ON COMMIT PRESERVE ROWS`); err != nil {
		t.Fatalf("create temp notification_preferences: %v", err)
	}
	for _, d := range []struct{ id, profile string }{{"dev-on", "p-on"}, {"dev-off", "p-off"}, {"dev-norow", "p-norow"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO push_devices (id, user_id, profile_id, device_id, platform, provider, server_device_id)
			VALUES ($1, 1, $2, $4, 'android', 'fcm_direct', $3)`, d.id, d.profile, "srv-"+d.id, d.id); err != nil {
			t.Fatalf("insert device: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, profile_id, push_enabled)
		VALUES (1, 'p-on', true), (1, 'p-off', false)`); err != nil {
		t.Fatalf("insert prefs: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profiles := []profileKey{{1, "p-on"}, {1, "p-off"}, {1, "p-norow"}}
	got, err := repo.ListEnabledPushByProfiles(ctx, tx, profiles, []string{PushPlatformAndroid})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got[profileKey{1, "p-on"}]) != 1 || len(got[profileKey{1, "p-norow"}]) != 1 {
		t.Fatalf("profiles with push on lost their devices: %+v", got)
	}
	if n := len(got[profileKey{1, "p-off"}]); n != 0 {
		t.Fatalf("profile with push off returned %d devices, want 0", n)
	}
}
