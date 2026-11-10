-- +goose Up
-- +goose StatementBegin
-- The login session that last registered this browser. Web push stops once that
-- session has ended. There is no foreign key: backups carry
-- web_push_subscriptions but leave auth_sessions out, so a restored row can name
-- a session that no longer exists, which counts as ended. NULL is a subscription
-- with no known session (an API key, or one made before this column), which
-- keeps delivering.
-- This server has no browser web push, so its table may not exist.
ALTER TABLE IF EXISTS public.web_push_subscriptions ADD COLUMN login_session_id text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE IF EXISTS public.web_push_subscriptions DROP COLUMN IF EXISTS login_session_id;
-- +goose StatementEnd
