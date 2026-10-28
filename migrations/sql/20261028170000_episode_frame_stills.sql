-- +goose Up
-- +goose StatementBegin
-- When a library takes an episode's thumbnail from a frame of its video:
-- never (off), only when no provider has an image (missing), or also when the
-- provider image is smaller than full HD (below_full_hd). The position is how
-- far into the episode, in percent, the frame is taken.
ALTER TABLE public.media_folders
    ADD COLUMN episode_frame_stills text NOT NULL DEFAULT 'off'
        CHECK (episode_frame_stills IN ('off', 'missing', 'below_full_hd')),
    ADD COLUMN episode_frame_position integer NOT NULL DEFAULT 20
        CHECK (episode_frame_position BETWEEN 5 AND 90);

-- What the frame-still task decided for an episode. source_path is the
-- provider still source it judged (empty when there was none): the task
-- looks at the episode again once that changes. For outcome 'frame' it is the
-- provider source the frame displaced, which metadata refreshes leave alone.
CREATE TABLE public.episode_frame_still_checks (
    content_id text COLLATE "C" PRIMARY KEY
        REFERENCES public.episodes(content_id) ON UPDATE CASCADE ON DELETE CASCADE,
    source_path text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('provider_ok', 'frame', 'failed')),
    provider_width integer NOT NULL DEFAULT 0,
    frame_width integer NOT NULL DEFAULT 0,
    retry_after timestamptz,
    checked_at timestamptz NOT NULL DEFAULT now()
);

-- The pixel size of each episode's cached still, measured by the same task,
-- for the library image-size summary. A row describes still_path only; once
-- the episode's still changes it is measured again.
CREATE TABLE public.episode_still_sizes (
    content_id text COLLATE "C" PRIMARY KEY
        REFERENCES public.episodes(content_id) ON UPDATE CASCADE ON DELETE CASCADE,
    still_path text NOT NULL,
    width integer NOT NULL,
    height integer NOT NULL,
    measured_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.episode_still_sizes;
DROP TABLE public.episode_frame_still_checks;
ALTER TABLE public.media_folders
    DROP COLUMN episode_frame_position,
    DROP COLUMN episode_frame_stills;
-- +goose StatementEnd
