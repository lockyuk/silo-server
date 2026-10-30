-- +goose Up
-- +goose StatementBegin
-- Automatic movie collections: a movie library can have Silo build one
-- collection per TMDB collection (franchise) it holds at least two films of,
-- with TMDB's poster and backdrop or, with collection artwork off, the
-- generated collage.
ALTER TABLE public.media_folders
    ADD COLUMN auto_collections_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN collection_artwork_enabled boolean NOT NULL DEFAULT true;

-- Collections the automatic sync owns; it creates, refreshes and removes them.
ALTER TABLE public.library_collections
    DROP CONSTRAINT library_collections_management_mode_check,
    ADD CONSTRAINT library_collections_management_mode_check
        CHECK (management_mode = ANY (ARRAY['manual'::text, 'section'::text, 'template_bundle'::text, 'auto'::text]));

-- Which TMDB collection each movie belongs to (collection_id NULL = none),
-- so a sync looks a movie up again only after the recheck interval.
CREATE TABLE public.movie_tmdb_collection_refs (
    tmdb_movie_id integer PRIMARY KEY,
    collection_id integer,
    checked_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.movie_tmdb_collection_refs;
DELETE FROM public.library_collections WHERE management_mode = 'auto';
ALTER TABLE public.library_collections
    DROP CONSTRAINT library_collections_management_mode_check,
    ADD CONSTRAINT library_collections_management_mode_check
        CHECK (management_mode = ANY (ARRAY['manual'::text, 'section'::text, 'template_bundle'::text]));
ALTER TABLE public.media_folders
    DROP COLUMN auto_collections_enabled,
    DROP COLUMN collection_artwork_enabled;
-- +goose StatementEnd
