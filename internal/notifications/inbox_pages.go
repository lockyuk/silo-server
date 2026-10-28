package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InboxCutoff captures the newest committed delivery currently visible to this
// profile. An empty cursor describes an empty inbox and remains a no-op cutoff.
func (r *DeliveryRepository) InboxCutoff(ctx context.Context, userID int, profileID string) (Cursor, error) {
	var cursor Cursor
	err := r.pool.QueryRow(ctx, `SELECT created_at,id FROM notification_deliveries WHERE user_id = $1 AND profile_id = $2 ORDER BY created_at DESC,id DESC LIMIT 1`, userID, profileID).Scan(&cursor.CreatedAt, &cursor.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, nil
	}
	return cursor, err
}

func (r *DeliveryRepository) ListInboxWindow(ctx context.Context, userID int, profileID string, unread bool, limit int, before *Cursor, through Cursor) ([]DeliveryRow, bool, error) {
	limit = max(1, min(limit, 200))
	if through.ID == "" {
		return []DeliveryRow{}, false, nil
	}
	var beforeTime any
	beforeID := ""
	if before != nil {
		beforeTime = before.CreatedAt
		beforeID = before.ID
	}
	rows, err := r.pool.Query(ctx, deliveryRowSelect+` WHERE d.user_id = $1 AND d.profile_id = $2 AND (NOT $3 OR d.read_at IS NULL) AND (d.created_at,d.id)<=($4,$5) AND ($6::timestamptz IS NULL OR (d.created_at,d.id)<($6,$7)) ORDER BY d.created_at DESC,d.id DESC LIMIT $8`, userID, profileID, unread, through.CreatedAt, through.ID, beforeTime, beforeID, limit+1)
	if err != nil {
		return nil, false, err
	}
	result, err := scanDeliveryRows(rows)
	if err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

// InboxCount counts the deliveries a window covers, for page numbers.
func (r *DeliveryRepository) InboxCount(ctx context.Context, userID int, profileID string, unread bool, through Cursor) (int, error) {
	if through.ID == "" {
		return 0, nil
	}
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries d WHERE d.user_id = $1 AND d.profile_id = $2 AND (NOT $3 OR d.read_at IS NULL) AND (d.created_at,d.id)<=($4,$5)`, userID, profileID, unread, through.CreatedAt, through.ID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count notification inbox: %w", err)
	}
	return n, nil
}

// SentVia reports how each delivery reached the profile beyond the inbox:
// email_instant, email_daily and email_weekly for the alert emails that
// carried its item, and push once a phone push went out (on its own or
// inside a grouped push).
func (r *DeliveryRepository) SentVia(ctx context.Context, userID int, profileID string, ids []string) (map[string][]string, error) {
	out := make(map[string][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT d.id,
		       ARRAY(SELECT 'email_' || s.leg FROM notification_email_sent_items s
		             WHERE s.user_id = d.user_id AND s.profile_id = d.profile_id
		               AND s.content_id = COALESCE(d.episode_id, d.series_id)
		             ORDER BY array_position(ARRAY['instant','daily','weekly'], s.leg))
		       || CASE WHEN EXISTS (SELECT 1 FROM push_delivery_attempts a
		                            WHERE a.notification_delivery_id = d.id AND a.outcome IN ('delivered', 'batched'))
		               THEN ARRAY['push'] ELSE '{}'::text[] END
		FROM notification_deliveries d
		WHERE d.user_id = $1 AND d.profile_id = $2 AND d.id = ANY($3)`, userID, profileID, ids)
	if err != nil {
		return nil, fmt.Errorf("read notification channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var via []string
		if err := rows.Scan(&id, &via); err != nil {
			return nil, fmt.Errorf("scan notification channels: %w", err)
		}
		if len(via) > 0 {
			out[id] = via
		}
	}
	return out, rows.Err()
}

// MarkReadThrough never expands the target when the same command is retried.
func (r *DeliveryRepository) MarkReadThrough(ctx context.Context, userID int, profileID string, through Cursor) (int64, error) {
	if through.ID == "" {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `UPDATE notification_deliveries SET read_at=now() WHERE user_id = $1 AND profile_id = $2 AND read_at IS NULL AND (created_at,id)<=($3,$4)`, userID, profileID, through.CreatedAt, through.ID)
	if err != nil {
		return 0, fmt.Errorf("mark notification cutoff read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PreferencePatch preserves omitted fields without a read/whole-row-write race.
type PreferencePatch struct {
	Enabled                *bool
	NotifyFavorites        *bool
	NotifyWatchlist        *bool
	NotifyContinueWatching *bool
	NotifyNextUp           *bool
	NotifyNewEpisodes      *bool
	NotifyNewMovies        *bool
	PushEnabled            *bool
	PushLibraryIDs         *[]int
}

func (r *PreferencesRepository) Patch(ctx context.Context, userID int, profileID string, p PreferencePatch) (Preferences, error) {
	prefs := DefaultPreferences(userID, profileID)
	err := r.pool.QueryRow(ctx, `INSERT INTO notification_preferences(user_id,profile_id,enabled,notify_favorites,notify_watchlist,notify_continue_watching,notify_next_up,notify_new_episodes,notify_new_movies,push_enabled,push_library_ids,updated_at) VALUES($1,$2,COALESCE($3,true),COALESCE($4,true),COALESCE($5,true),COALESCE($6,true),COALESCE($7,true),COALESCE($8,true),COALESCE($9,true),COALESCE($10,true),COALESCE($11,'{}'::integer[]),now()) ON CONFLICT(user_id,profile_id) DO UPDATE SET enabled=COALESCE($3,notification_preferences.enabled),notify_favorites=COALESCE($4,notification_preferences.notify_favorites),notify_watchlist=COALESCE($5,notification_preferences.notify_watchlist),notify_continue_watching=COALESCE($6,notification_preferences.notify_continue_watching),notify_next_up=COALESCE($7,notification_preferences.notify_next_up),notify_new_episodes=COALESCE($8,notification_preferences.notify_new_episodes),notify_new_movies=COALESCE($9,notification_preferences.notify_new_movies),push_enabled=COALESCE($10,notification_preferences.push_enabled),push_library_ids=COALESCE($11,notification_preferences.push_library_ids),updated_at=now() RETURNING enabled,notify_favorites,notify_watchlist,notify_continue_watching,notify_next_up,notify_new_episodes,notify_new_movies,push_enabled,push_library_ids,updated_at`, userID, profileID, p.Enabled, p.NotifyFavorites, p.NotifyWatchlist, p.NotifyContinueWatching, p.NotifyNextUp, p.NotifyNewEpisodes, p.NotifyNewMovies, p.PushEnabled, p.PushLibraryIDs).Scan(&prefs.Enabled, &prefs.NotifyFavorites, &prefs.NotifyWatchlist, &prefs.NotifyContinueWatching, &prefs.NotifyNextUp, &prefs.NotifyNewEpisodes, &prefs.NotifyNewMovies, &prefs.PushEnabled, &prefs.PushLibraryIDs, &prefs.UpdatedAt)
	return prefs, err
}
