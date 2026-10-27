-- +goose Up
-- +goose StatementBegin
-- Notifications are "everything new": every profile hears about every new
-- episode and movie it can open, and its email sends everything new. The
-- per-reason choices are no longer offered.
ALTER TABLE public.notification_preferences
    ALTER COLUMN notify_new_episodes SET DEFAULT true,
    ALTER COLUMN notify_new_movies SET DEFAULT true;
UPDATE public.notification_preferences
    SET notify_new_episodes = true, notify_new_movies = true, updated_at = now();
INSERT INTO public.notification_preferences (user_id, profile_id)
SELECT up.user_id, up.id FROM public.user_profiles up
ON CONFLICT (user_id, profile_id) DO NOTHING;

-- A new profile gets its preferences row (with the defaults above) at once,
-- so the everything-new fanout, which reads these rows, includes it.
CREATE FUNCTION public.notification_preferences_for_new_profile() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO public.notification_preferences (user_id, profile_id)
    VALUES (NEW.user_id, NEW.id)
    ON CONFLICT (user_id, profile_id) DO NOTHING;
    RETURN NEW;
END $$;
CREATE TRIGGER trg_notification_preferences_for_new_profile
    AFTER INSERT ON public.user_profiles
    FOR EACH ROW EXECUTE FUNCTION public.notification_preferences_for_new_profile();

ALTER TABLE public.notification_email_prefs ALTER COLUMN content SET DEFAULT 'all';
UPDATE public.notification_email_prefs SET content = 'all';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER trg_notification_preferences_for_new_profile ON public.user_profiles;
DROP FUNCTION public.notification_preferences_for_new_profile();
ALTER TABLE public.notification_email_prefs ALTER COLUMN content SET DEFAULT 'followed';
ALTER TABLE public.notification_preferences
    ALTER COLUMN notify_new_episodes SET DEFAULT false,
    ALTER COLUMN notify_new_movies SET DEFAULT false;
-- +goose StatementEnd
