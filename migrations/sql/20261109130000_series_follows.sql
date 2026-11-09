-- +goose Up
-- +goose StatementBegin
-- Notifications for new episodes now come from an explicit follow list (plus
-- the "everything new" switch) instead of reasons derived from favorites, the
-- watchlist and watch progress. The derived interest index, the per-reason
-- switches on preferences, and the per-reason flags on webhooks are removed.
CREATE TABLE public.profile_series_follows (
    user_id integer NOT NULL,
    profile_id text NOT NULL,
    series_id text NOT NULL,
    -- Newest episode key the profile was told about; owned by the fanout worker.
    last_notified_episode_key integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, profile_id, series_id)
);
CREATE INDEX profile_series_follows_series_idx
    ON public.profile_series_follows (series_id);

DROP TABLE IF EXISTS public.profile_series_interest;

ALTER TABLE public.notification_preferences
    DROP COLUMN IF EXISTS notify_favorites,
    DROP COLUMN IF EXISTS notify_watchlist,
    DROP COLUMN IF EXISTS notify_continue_watching,
    DROP COLUMN IF EXISTS notify_next_up;

ALTER TABLE public.notification_webhooks
    DROP COLUMN IF EXISTS notify_favorites,
    DROP COLUMN IF EXISTS notify_watchlist,
    DROP COLUMN IF EXISTS notify_continue_watching,
    DROP COLUMN IF EXISTS notify_next_up;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
