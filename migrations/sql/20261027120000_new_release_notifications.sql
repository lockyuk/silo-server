-- +goose Up
-- +goose StatementBegin
-- "Everything new" notifications. A profile can ask to hear about every new
-- episode, whether or not it follows the series, and about every new movie.
-- Both stay off until the profile turns them on.
ALTER TABLE public.notification_preferences
    ADD COLUMN notify_new_episodes boolean NOT NULL DEFAULT false,
    ADD COLUMN notify_new_movies boolean NOT NULL DEFAULT false;

-- Webhook reason filter for the new_release reason. On by default, so a
-- webhook follows the profile's own choice.
ALTER TABLE public.notification_webhooks
    ADD COLUMN notify_new_releases boolean NOT NULL DEFAULT true;

-- A movie.available delivery names its movie in series_id, as
-- request.fulfilled does. One per profile and movie, across libraries.
ALTER TABLE public.notification_deliveries
    ADD CONSTRAINT notification_deliveries_movie_fields_check CHECK (
        type <> 'movie.available'
        OR (library_id IS NOT NULL AND series_id IS NOT NULL)
    );
CREATE UNIQUE INDEX notification_deliveries_account_profile_movie_key
    ON public.notification_deliveries (user_id, profile_id, series_id)
    WHERE type = 'movie.available';

-- Weekly digest mode for the account channels.
ALTER TABLE public.notification_email_prefs
    DROP CONSTRAINT notification_email_prefs_mode_check;
ALTER TABLE public.notification_email_prefs
    ADD CONSTRAINT notification_email_prefs_mode_check
    CHECK (mode IN ('off', 'per_episode', 'daily_digest', 'per_episode_and_digest', 'weekly_digest'));

ALTER TABLE public.notification_discord_prefs
    DROP CONSTRAINT notification_discord_prefs_mode_check;
ALTER TABLE public.notification_discord_prefs
    ADD CONSTRAINT notification_discord_prefs_mode_check
    CHECK (mode IN ('off', 'per_episode', 'daily_digest', 'per_episode_and_digest', 'weekly_digest'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE public.notification_email_prefs
    SET mode = 'daily_digest' WHERE mode = 'weekly_digest';
UPDATE public.notification_discord_prefs
    SET mode = 'daily_digest' WHERE mode = 'weekly_digest';

ALTER TABLE public.notification_email_prefs
    DROP CONSTRAINT notification_email_prefs_mode_check;
ALTER TABLE public.notification_email_prefs
    ADD CONSTRAINT notification_email_prefs_mode_check
    CHECK (mode IN ('off', 'per_episode', 'daily_digest', 'per_episode_and_digest'));

ALTER TABLE public.notification_discord_prefs
    DROP CONSTRAINT notification_discord_prefs_mode_check;
ALTER TABLE public.notification_discord_prefs
    ADD CONSTRAINT notification_discord_prefs_mode_check
    CHECK (mode IN ('off', 'per_episode', 'daily_digest', 'per_episode_and_digest'));

DELETE FROM public.notification_deliveries WHERE type = 'movie.available';
DROP INDEX IF EXISTS public.notification_deliveries_account_profile_movie_key;
ALTER TABLE public.notification_deliveries
    DROP CONSTRAINT IF EXISTS notification_deliveries_movie_fields_check;

ALTER TABLE public.notification_webhooks DROP COLUMN notify_new_releases;
ALTER TABLE public.notification_preferences
    DROP COLUMN notify_new_episodes,
    DROP COLUMN notify_new_movies;
-- +goose StatementEnd
