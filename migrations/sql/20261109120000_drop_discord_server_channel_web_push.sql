-- +goose Up
-- +goose StatementBegin
-- Discord DMs, server-wide channels and browser web push are gone. Their
-- tables, and the settings that configured them, are removed with the code.
DROP TABLE IF EXISTS public.notification_discord_link_state;
DROP TABLE IF EXISTS public.notification_discord_prefs;
DROP TABLE IF EXISTS public.notification_server_channels;
DROP TABLE IF EXISTS public.web_push_delivery_attempts;
DROP TABLE IF EXISTS public.web_push_subscriptions;
DELETE FROM public.server_settings
 WHERE key LIKE 'discord.%'
    OR key LIKE 'notifications.server_channels.%'
    OR key LIKE 'notifications.web_push%';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Dropped tables hold only credentials and delivery logs and are not restored.
SELECT 1;
-- +goose StatementEnd
