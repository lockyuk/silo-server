-- +goose Up
-- +goose StatementBegin
-- A library's own choice of the external rating sources its titles show,
-- overriding the server-wide catalog.extra_rating_sources (and able to hide
-- IMDb or TMDB). A library without a row uses the server-wide choice.
CREATE TABLE public.library_rating_sources (
    media_folder_id INTEGER PRIMARY KEY REFERENCES public.media_folders(id) ON DELETE CASCADE,
    sources TEXT[] NOT NULL DEFAULT '{}'
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.library_rating_sources;
-- +goose StatementEnd
