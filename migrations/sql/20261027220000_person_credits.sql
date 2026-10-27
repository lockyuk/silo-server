-- +goose Up
-- +goose StatementBegin
-- A person's movie and TV credits as their metadata provider lists them,
-- cached for the filmography page.
CREATE TABLE public.person_credits (
    person_id BIGINT PRIMARY KEY REFERENCES public.people(id) ON DELETE CASCADE,
    credits JSONB NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.person_credits;
-- +goose StatementEnd
