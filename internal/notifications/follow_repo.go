package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSeriesNotFound reports a follow or mute of an ID the catalog has no
// series for.
var ErrSeriesNotFound = errors.New("series not found")

// FollowRepository owns profile_series_follows (the series a profile asked to
// hear about) and profile_series_notification_mutes (series a profile that
// asked for everything new opted out of).
type FollowRepository struct {
	pool *pgxpool.Pool
}

// NewFollowRepository creates a FollowRepository.
func NewFollowRepository(pool *pgxpool.Pool) *FollowRepository {
	return &FollowRepository{pool: pool}
}

// FanoutCandidate is one profile fanout may notify of a new episode.
type FanoutCandidate struct {
	UserID    int
	ProfileID string
	// Followed is set when the profile follows the series.
	Followed bool
	// LastNotifiedEpisodeKey is the newest episode the profile was told about
	// for a followed series; nil for a series it does not follow.
	LastNotifiedEpisodeKey *int
}

func (c FanoutCandidate) key() profileKey { return profileKey{c.UserID, c.ProfileID} }

// ListCandidates loads the profiles that may be notified of a new episode of
// the series: those following it, and those that asked for every new episode,
// except the profiles that muted it.
func (r *FollowRepository) ListCandidates(ctx context.Context, tx pgx.Tx, seriesID string) ([]FanoutCandidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT f.user_id, f.profile_id, true, f.last_notified_episode_key
		FROM profile_series_follows f
		WHERE f.series_id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM profile_series_notification_mutes m
			WHERE m.series_id = $1 AND m.user_id = f.user_id AND m.profile_id = f.profile_id)
		UNION ALL
		SELECT np.user_id, np.profile_id, false, NULL::integer
		FROM notification_preferences np
		WHERE np.enabled AND np.notify_new_episodes
		  AND NOT EXISTS (
			SELECT 1 FROM profile_series_follows f
			WHERE f.series_id = $1 AND f.user_id = np.user_id AND f.profile_id = np.profile_id)
		  AND NOT EXISTS (
			SELECT 1 FROM profile_series_notification_mutes m
			WHERE m.series_id = $1 AND m.user_id = np.user_id AND m.profile_id = np.profile_id)`,
		seriesID)
	if err != nil {
		return nil, fmt.Errorf("list fanout candidates: %w", err)
	}
	defer rows.Close()
	out := make([]FanoutCandidate, 0, 16)
	for rows.Next() {
		var c FanoutCandidate
		if err := rows.Scan(&c.UserID, &c.ProfileID, &c.Followed, &c.LastNotifiedEpisodeKey); err != nil {
			return nil, fmt.Errorf("scan fanout candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GuardedSetLastNotified raises last_notified_episode_key for the followers
// among the given profiles. The < guard makes concurrent workers handling
// adjacent release events safe: the higher key wins regardless of commit order.
func (r *FollowRepository) GuardedSetLastNotified(ctx context.Context, tx pgx.Tx, seriesID string, episodeKey int, profiles []profileKey) error {
	if len(profiles) == 0 {
		return nil
	}
	userIDs, profileIDs := profileKeyArrays(profiles)
	_, err := tx.Exec(ctx, `
		UPDATE profile_series_follows f
		SET last_notified_episode_key = $1
		FROM unnest($3::integer[], $4::text[]) AS notified(user_id, profile_id)
		WHERE f.series_id = $2
		  AND f.user_id = notified.user_id AND f.profile_id = notified.profile_id
		  AND (f.last_notified_episode_key IS NULL OR f.last_notified_episode_key < $1)`,
		episodeKey, seriesID, userIDs, profileIDs)
	if err != nil {
		return fmt.Errorf("update last notified episode key: %w", err)
	}
	return nil
}

// follow records that the profile follows the series. Following a series
// already followed changes nothing. Following lifts a mute: the two are
// contradictory.
func (r *FollowRepository) follow(ctx context.Context, userID int, profileID, seriesID string) error {
	var found bool
	err := r.pool.QueryRow(ctx, `
		WITH series AS (
			SELECT content_id FROM media_items WHERE content_id = $3 AND type = 'series'
		), followed AS (
			INSERT INTO profile_series_follows (user_id, profile_id, series_id)
			SELECT $1, $2, content_id FROM series
			ON CONFLICT (user_id, profile_id, series_id) DO NOTHING
		), unmuted AS (
			DELETE FROM profile_series_notification_mutes
			WHERE user_id = $1 AND profile_id = $2 AND series_id IN (SELECT content_id FROM series)
		)
		SELECT EXISTS (SELECT 1 FROM series)`,
		userID, profileID, seriesID).Scan(&found)
	if err != nil {
		return fmt.Errorf("follow series: %w", err)
	}
	if !found {
		return ErrSeriesNotFound
	}
	return nil
}

func (r *FollowRepository) unfollow(ctx context.Context, userID int, profileID, seriesID string) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM profile_series_follows WHERE user_id = $1 AND profile_id = $2 AND series_id = $3`,
		userID, profileID, seriesID); err != nil {
		return fmt.Errorf("unfollow series: %w", err)
	}
	return nil
}

func (r *FollowRepository) muteSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	var found bool
	err := r.pool.QueryRow(ctx, `
		WITH series AS (
			SELECT content_id FROM media_items WHERE content_id = $3 AND type = 'series'
		), muted AS (
			INSERT INTO profile_series_notification_mutes (user_id, profile_id, series_id)
			SELECT $1, $2, content_id FROM series
			ON CONFLICT (user_id, profile_id, series_id) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM series)`,
		userID, profileID, seriesID).Scan(&found)
	if err != nil {
		return fmt.Errorf("mute series: %w", err)
	}
	if !found {
		return ErrSeriesNotFound
	}
	return nil
}

func (r *FollowRepository) unmuteSeries(ctx context.Context, userID int, profileID, seriesID string) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM profile_series_notification_mutes WHERE user_id = $1 AND profile_id = $2 AND series_id = $3`,
		userID, profileID, seriesID); err != nil {
		return fmt.Errorf("unmute series: %w", err)
	}
	return nil
}

// DeleteAllForProfile removes a deleted profile's follows and mutes. Profiles
// may live in per-user SQLite stores, so this cleanup cannot rely on a
// Postgres cascade.
func (r *FollowRepository) DeleteAllForProfile(ctx context.Context, userID int, profileID string) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM profile_series_follows WHERE user_id = $1 AND profile_id = $2`, userID, profileID); err != nil {
		return fmt.Errorf("delete series follows: %w", err)
	}
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM profile_series_notification_mutes WHERE user_id = $1 AND profile_id = $2`, userID, profileID); err != nil {
		return fmt.Errorf("delete series mutes: %w", err)
	}
	return nil
}
