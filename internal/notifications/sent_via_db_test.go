package notifications

import (
	"strings"
	"testing"
	"time"
)

// The inbox names the alert emails that carried a delivery's item and a
// push that went out alone or inside a grouped push; a pending or failed
// push, and another profile's email, count for nothing.
func TestSentViaReportsEmailLegsAndPushDB(t *testing.T) {
	p := inboxPageDB(t)
	ctx := t.Context()
	if _, err := p.Exec(ctx, `
		CREATE TABLE notification_email_sent_items (LIKE public.notification_email_sent_items INCLUDING ALL);
		CREATE TABLE push_delivery_attempts (LIKE public.push_delivery_attempts INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i, d := range []struct{ id, typ, series, episode string }{
		{"d-ep", "episode.available", "s-1", "e-1"},
		{"d-movie", "movie.available", "m-1", ""},
		{"d-batched", "episode.available", "s-1", "e-2"},
		{"d-pending", "episode.available", "s-1", "e-3"},
	} {
		var episode any
		if d.episode != "" {
			episode = d.episode
		}
		if _, err := p.Exec(ctx, `
			INSERT INTO notification_deliveries (id, release_event_id, user_id, profile_id, library_id, series_id, episode_id, type, reason_flags, created_at)
			VALUES ($1, $1, 1, 'p', 1, $2, $3, $4, '{}', $5)`, d.id, d.series, episode, d.typ, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("insert %s: %v", d.id, err)
		}
	}
	alerts := &EmailAlertsRepository{pool: p}
	if err := alerts.RecordSentItems(ctx, 1, "p", "daily", []string{"e-1", "m-1"}, at); err != nil {
		t.Fatal(err)
	}
	if err := alerts.RecordSentItems(ctx, 1, "p", "instant", []string{"e-1"}, at); err != nil {
		t.Fatal(err)
	}
	if err := alerts.RecordSentItems(ctx, 1, "other", "weekly", []string{"e-1"}, at); err != nil {
		t.Fatal(err)
	}
	for i, a := range []struct{ id, delivery, outcome string }{
		{"a-1", "d-ep", "failed"}, {"a-2", "d-ep", "delivered"},
		{"a-3", "d-batched", "batched"}, {"a-4", "d-pending", "pending"},
	} {
		if _, err := p.Exec(ctx, `
			INSERT INTO push_delivery_attempts (id, notification_delivery_id, push_device_id, provider, platform, outcome, attempt_number)
			VALUES ($1, $2, 'dev', 'fcm_direct', 'android', $3, $4)`, a.id, a.delivery, a.outcome, i); err != nil {
			t.Fatalf("insert attempt: %v", err)
		}
	}

	got, err := NewDeliveryRepository(p).SentVia(ctx, 1, "p", []string{"d-ep", "d-movie", "d-batched", "d-pending"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"d-ep":      "email_instant,email_daily,push",
		"d-movie":   "email_daily",
		"d-batched": "push",
	}
	if len(got) != len(want) {
		t.Fatalf("SentVia = %v, want %v", got, want)
	}
	for id, w := range want {
		if s := strings.Join(got[id], ","); s != w {
			t.Errorf("%s = %q, want %q", id, s, w)
		}
	}
	n, err := NewDeliveryRepository(p).InboxCount(ctx, 1, "p", InboxFilter{}, Cursor{CreatedAt: at.Add(time.Hour), ID: "zz"})
	if err != nil || n != 4 {
		t.Fatalf("InboxCount = %d, %v; want 4", n, err)
	}
}
