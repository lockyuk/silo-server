package notifications

import (
	"testing"
	"time"
)

// The inbox list and its count both honour the library and age filters,
// alone and together.
func TestInboxFilterByLibraryAndAgeDB(t *testing.T) {
	p := inboxPageDB(t)
	ctx := t.Context()
	now := time.Now().UTC()
	for _, d := range []struct {
		id      string
		library int
		age     time.Duration
	}{
		{"d-new-1", 1, time.Hour},
		{"d-new-2", 2, 2 * time.Hour},
		{"d-old-1", 1, 10 * 24 * time.Hour},
		{"d-old-2", 2, 40 * 24 * time.Hour},
	} {
		if _, err := p.Exec(ctx, `
			INSERT INTO notification_deliveries (id, release_event_id, user_id, profile_id, library_id, series_id, type, reason_flags, created_at)
			VALUES ($1, $1, 1, 'p', $2, 's-' || $1, 'movie.available', '{}', now())`, d.id, d.library); err != nil {
			t.Fatalf("insert %s: %v", d.id, err)
		}
		// Inserts are stamped with the commit time; backdate for the age filter.
		if _, err := p.Exec(ctx, `UPDATE notification_deliveries SET created_at = $2 WHERE id = $1`, d.id, now.Add(-d.age)); err != nil {
			t.Fatalf("backdate %s: %v", d.id, err)
		}
	}
	r := NewDeliveryRepository(p)
	through := Cursor{CreatedAt: now.Add(time.Hour), ID: "zz"}
	week := now.AddDate(0, 0, -7)
	for _, c := range []struct {
		name   string
		filter InboxFilter
		want   []string
	}{
		{"none", InboxFilter{}, []string{"d-new-1", "d-new-2", "d-old-1", "d-old-2"}},
		{"library", InboxFilter{LibraryID: 1}, []string{"d-new-1", "d-old-1"}},
		{"age", InboxFilter{Since: &week}, []string{"d-new-1", "d-new-2"}},
		{"both", InboxFilter{LibraryID: 2, Since: &week}, []string{"d-new-2"}},
	} {
		rows, more, err := r.ListInboxWindow(ctx, 1, "p", c.filter, 10, nil, through)
		if err != nil || more {
			t.Fatalf("%s: ListInboxWindow more=%v err=%v", c.name, more, err)
		}
		var got []string
		for _, row := range rows {
			got = append(got, row.ID)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: rows = %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: rows = %v, want %v", c.name, got, c.want)
			}
		}
		n, err := r.InboxCount(ctx, 1, "p", c.filter, through)
		if err != nil || n != len(c.want) {
			t.Fatalf("%s: InboxCount = %d, %v; want %d", c.name, n, err, len(c.want))
		}
	}
}
