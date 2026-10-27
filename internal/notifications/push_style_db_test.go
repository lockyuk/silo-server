package notifications

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/pushstyle"
)

// Three episodes of one show arriving a minute apart go out as one push once
// the batch window after the newest has passed; the older attempts close as
// batched and the push renders as a group. During quiet hours a movie's push
// is held until they end.
func TestPushBatchingAndQuietHoursDB(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	prefix := fmt.Sprintf("pushstyle-%d", time.Now().UnixNano())
	var account int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, '', 'user') RETURNING id`,
		prefix, prefix+"@example.test").Scan(&account); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM push_devices WHERE user_id = $1`, account)
		_, _ = pool.Exec(cleanup, `DELETE FROM notification_deliveries WHERE user_id = $1`, account)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id = $1`, account)
		_, _ = pool.Exec(cleanup, `DELETE FROM notification_push_style_settings`)
	})
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	cipher := testPushCipher(t)
	repo := NewPushDeviceRepository(pool)
	phone, err := repo.UpsertApple(ctx, ApplePushDeviceRegistration{
		UserID: account, ProfileID: "default", DeviceID: prefix + "-phone", APNsToken: strings.Repeat("c", 64),
		APNsEnvironment: APNsEnvironmentSandbox, APNsTopic: ApplePushTopicSilo, PushMode: PushModePrivatePush,
	}, cipher)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	style := NewPushStyleRepository(pool)
	cfg := pushstyle.DefaultSettings()
	cfg.BatchMinutes = 5
	if _, err := style.SaveSettings(ctx, cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	sends := 0
	settings := NewSettings(mapSettingReader{SettingPushRelayURL: DefaultPushRelayURL, SettingPushRelayAPIKey: "relay-key", SettingApplePushDeliveryEnabled: "true"})
	deliveries := NewDeliveryRepository(pool)
	sender := newPushSender(repo, deliveries, cipher, settings)
	sender.style = style
	sender.client = &http.Client{Transport: relayRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		sends++
		return relayResponse(http.StatusOK, `{"request_id":"relay-request","status":"accepted"}`), nil
	})}
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	now := t0
	sender.now = func() time.Time { return now }

	series := prefix + "-series"
	var attempts []string
	for i := 0; i < 3; i++ {
		delivery := fmt.Sprintf("%s-ep%d", prefix, i+1)
		exec(`INSERT INTO notification_deliveries (id, user_id, profile_id, library_id, series_id, episode_id, type, reason_flags, created_at)
			VALUES ($1, $2, 'default', 1, $3, $4, 'episode.available', '{}', $5)`,
			delivery, account, series, delivery+"-item", t0.Add(time.Duration(i)*time.Minute))
		attempt := delivery + "-attempt"
		exec(`INSERT INTO push_delivery_attempts (id, notification_delivery_id, push_device_id, trigger_type, provider, platform, attempt_number, outcome, created_at)
			VALUES ($1, $2, $3, 'delivery', 'silo_relay', 'apple', 0, 'pending', $4)`,
			attempt, delivery, phone.ID, t0.Add(time.Duration(i)*time.Minute))
		attempts = append(attempts, attempt)
	}
	process := func(id string) {
		t.Helper()
		claimed, err := repo.ClaimPushAttemptByID(ctx, id)
		if err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if len(claimed) == 0 {
			exec(`UPDATE push_delivery_attempts SET outcome = 'pending' WHERE id = $1 AND outcome = 'retrying'`, id)
			if claimed, err = repo.ClaimPushAttemptByID(ctx, id); err != nil || len(claimed) != 1 {
				t.Fatalf("reclaim %s: %v %v", id, claimed, err)
			}
		}
		sender.processAttempt(ctx, claimed[0])
	}
	state := func(id string) (outcome string, next *time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT outcome, next_retry_at FROM push_delivery_attempts WHERE id = $1`, id).Scan(&outcome, &next); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return outcome, next
	}

	// Inside the window: the oldest waits for the newest, and the newest
	// waits out its window.
	now = t0.Add(3 * time.Minute)
	process(attempts[0])
	process(attempts[2])
	if sends != 0 {
		t.Fatalf("sent %d pushes inside the batch window", sends)
	}
	if _, next := state(attempts[2]); next == nil || !next.Equal(t0.Add(7*time.Minute)) {
		t.Fatalf("newest held until %v, want %v", next, t0.Add(7*time.Minute))
	}

	// After it: the oldest still defers to the newest, which sends for all.
	now = t0.Add(8 * time.Minute)
	process(attempts[0])
	process(attempts[2])
	if sends != 1 {
		t.Fatalf("sent %d pushes, want one grouped push", sends)
	}
	for i, want := range []string{PushOutcomeBatched, PushOutcomeBatched, PushOutcomeDelivered} {
		if outcome, _ := state(attempts[i]); outcome != want {
			t.Errorf("attempt %d outcome = %s, want %s", i+1, outcome, want)
		}
	}

	system := &System{PushStyle: style, Deliveries: deliveries}
	row, err := deliveries.GetRowByID(ctx, prefix+"-ep3")
	if err != nil || row == nil {
		t.Fatalf("load delivery: %v", err)
	}
	content := system.PushContentForRow(ctx, *row)
	if content == nil || content.Count != 3 || !strings.HasPrefix(content.Body, "3 new episodes") {
		t.Fatalf("grouped content = %+v", content)
	}

	// Quiet hours hold a movie until they end.
	cfg.QuietEnabled, cfg.QuietStart, cfg.QuietEnd, cfg.Timezone = true, "00:00", "23:59", "UTC"
	if _, err := style.SaveSettings(ctx, cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	movie := prefix + "-movie"
	exec(`INSERT INTO notification_deliveries (id, user_id, profile_id, library_id, series_id, type, reason_flags, created_at)
		VALUES ($1, $2, 'default', 1, $3, 'movie.available', '{}', $4)`, movie, account, movie+"-item", now)
	exec(`INSERT INTO push_delivery_attempts (id, notification_delivery_id, push_device_id, trigger_type, provider, platform, attempt_number, outcome)
		VALUES ($1, $2, $3, 'delivery', 'silo_relay', 'apple', 0, 'pending')`, movie+"-attempt", movie, phone.ID)
	now = time.Date(t0.Year(), t0.Month(), t0.Day(), 12, 0, 0, 0, time.UTC)
	process(movie + "-attempt")
	if sends != 1 {
		t.Fatal("a push went out during quiet hours")
	}
	if outcome, next := state(movie + "-attempt"); outcome != PushOutcomeRetrying || next == nil || !next.Equal(now.Truncate(24*time.Hour).Add(23*time.Hour+59*time.Minute)) {
		t.Fatalf("quiet hold = %s until %v", outcome, next)
	}
}
