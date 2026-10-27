-- +goose Up
-- +goose StatementBegin
-- Per-profile switch for instant phone and browser push. Off keeps every
-- notification in the inbox and leaves email and webhooks alone; it only
-- stops push attempts from being queued.
ALTER TABLE public.notification_preferences
    ADD COLUMN push_enabled boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.notification_preferences DROP COLUMN push_enabled;
-- +goose StatementEnd
