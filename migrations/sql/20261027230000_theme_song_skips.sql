-- +goose Up
-- +goose StatementBegin
-- An admin can skip a series: its downloaded theme is deleted and no run
-- looks for one again until it is retried.
ALTER TABLE public.theme_song_checks DROP CONSTRAINT theme_song_checks_outcome_check;
ALTER TABLE public.theme_song_checks ADD CONSTRAINT theme_song_checks_outcome_check
    CHECK (outcome IN ('downloaded', 'not_available', 'failed', 'skipped'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.theme_song_checks WHERE outcome = 'skipped';
ALTER TABLE public.theme_song_checks DROP CONSTRAINT theme_song_checks_outcome_check;
ALTER TABLE public.theme_song_checks ADD CONSTRAINT theme_song_checks_outcome_check
    CHECK (outcome IN ('downloaded', 'not_available', 'failed'));
-- +goose StatementEnd
