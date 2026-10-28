-- +goose Up
-- +goose StatementBegin
-- Where a library's episode stills come from: provider_order lets the
-- provider chain decide; high_quality prefers TMDB's full-HD stills over
-- TVDB's low-resolution screencaps.
ALTER TABLE public.media_folders
    ADD COLUMN episode_still_source text NOT NULL DEFAULT 'provider_order'
    CHECK (episode_still_source IN ('provider_order', 'high_quality'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_folders DROP COLUMN episode_still_source;
-- +goose StatementEnd
