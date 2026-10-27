-- +goose Up
-- +goose StatementBegin
-- Push notification style: admin templates, image and synopsis choice,
-- episode batching and quiet hours.

-- One row: the push settings as JSON.
CREATE TABLE public.notification_push_style_settings (
    id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    settings jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A push that stands for several episodes of one show: the delivery whose
-- push was sent, and every delivery it covers (itself included).
CREATE TABLE public.notification_push_groups (
    delivery_id text PRIMARY KEY REFERENCES public.notification_deliveries(id) ON DELETE CASCADE,
    member_ids text[] NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- batched: the attempt's delivery went out inside another delivery's
-- grouped push, so it is never sent on its own.
ALTER TABLE public.push_delivery_attempts DROP CONSTRAINT push_delivery_attempts_outcome_check;
ALTER TABLE public.push_delivery_attempts ADD CONSTRAINT push_delivery_attempts_outcome_check
    CHECK (outcome IN ('pending', 'delivered', 'retrying', 'failed', 'batched'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE public.push_delivery_attempts SET outcome = 'delivered' WHERE outcome = 'batched';
ALTER TABLE public.push_delivery_attempts DROP CONSTRAINT push_delivery_attempts_outcome_check;
ALTER TABLE public.push_delivery_attempts ADD CONSTRAINT push_delivery_attempts_outcome_check
    CHECK (outcome IN ('pending', 'delivered', 'retrying', 'failed'));
DROP TABLE public.notification_push_groups;
DROP TABLE public.notification_push_style_settings;
-- +goose StatementEnd
