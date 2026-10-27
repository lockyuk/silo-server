-- +goose Up
-- +goose StatementBegin
-- Theme songs Silo downloads for a series, kept in blob storage rather than
-- written into media folders. Ids come from the folder themes' sequence so a
-- theme id names one theme whichever table holds it.
CREATE TABLE public.downloaded_theme_songs (
    id BIGINT PRIMARY KEY DEFAULT nextval('public.item_theme_songs_id_seq'),
    series_id TEXT NOT NULL UNIQUE REFERENCES public.media_items(content_id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    source_ref TEXT NOT NULL,
    blob_key TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    title TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL CHECK (duration_seconds >= 0),
    container TEXT NOT NULL,
    audio_codec TEXT NOT NULL,
    audio_channels INTEGER NOT NULL,
    bitrate_kbps INTEGER NOT NULL,
    sample_rate INTEGER NOT NULL,
    file_size BIGINT NOT NULL CHECK (file_size > 0),
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX downloaded_theme_songs_blob_key ON public.downloaded_theme_songs(blob_key);

-- The last download attempt per series, so a theme the source does not have
-- is only asked for again after the recheck interval.
CREATE TABLE public.theme_song_checks (
    series_id TEXT PRIMARY KEY REFERENCES public.media_items(content_id) ON DELETE CASCADE,
    checked_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('downloaded', 'not_available', 'failed')),
    detail TEXT NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.theme_song_checks;
DROP TABLE public.downloaded_theme_songs;
-- +goose StatementEnd
