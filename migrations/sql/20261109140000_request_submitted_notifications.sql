-- +goose Up
-- +goose StatementBegin
-- request.submitted: tells each administrator profile that a request awaits
-- approval. At-most-once per (profile, request), like the other request
-- lifecycle notices.
CREATE UNIQUE INDEX notification_deliveries_profile_request_submitted_key
    ON public.notification_deliveries (profile_id, (reason_flags->>'request_id'))
    WHERE type = 'request.submitted';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.notification_deliveries_profile_request_submitted_key;
-- +goose StatementEnd
