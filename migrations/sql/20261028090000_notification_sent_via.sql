-- +goose Up
-- +goose StatementBegin
-- Which of a profile's alert emails carried each item, so the inbox can say
-- how a notification reached the profile. content_id is the episode id, or
-- the movie id for a movie.
CREATE TABLE public.notification_email_sent_items (
    user_id integer NOT NULL,
    profile_id text NOT NULL,
    content_id text NOT NULL,
    leg text NOT NULL CHECK (leg IN ('instant', 'daily', 'weekly')),
    sent_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, profile_id, content_id, leg)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.notification_email_sent_items;
-- +goose StatementEnd
