-- +goose Up
-- +goose StatementBegin
-- Downloaded themes now cover movies too (series_id names any media item) and
-- come from ThemerrDB and YouTube search as well as Plex. A search result is
-- only a suggestion: it waits in theme_song_candidates, with the check marked
-- 'review', until an admin approves or rejects it.
ALTER TABLE public.theme_song_checks DROP CONSTRAINT theme_song_checks_outcome_check;
ALTER TABLE public.theme_song_checks ADD CONSTRAINT theme_song_checks_outcome_check
    CHECK (outcome IN ('downloaded', 'not_available', 'failed', 'skipped', 'review'));

CREATE TABLE public.theme_song_candidates (
    item_id TEXT PRIMARY KEY REFERENCES public.media_items(content_id) ON DELETE CASCADE,
    video_id TEXT NOT NULL,
    video_title TEXT NOT NULL,
    channel TEXT NOT NULL DEFAULT '',
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    query TEXT NOT NULL,
    found_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Search results an admin turned down, so the next search skips them.
CREATE TABLE public.theme_song_rejections (
    item_id TEXT NOT NULL REFERENCES public.media_items(content_id) ON DELETE CASCADE,
    video_id TEXT NOT NULL,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, video_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.theme_song_rejections;
DROP TABLE public.theme_song_candidates;
DELETE FROM public.theme_song_checks WHERE outcome = 'review';
ALTER TABLE public.theme_song_checks DROP CONSTRAINT theme_song_checks_outcome_check;
ALTER TABLE public.theme_song_checks ADD CONSTRAINT theme_song_checks_outcome_check
    CHECK (outcome IN ('downloaded', 'not_available', 'failed', 'skipped'));
-- +goose StatementEnd
