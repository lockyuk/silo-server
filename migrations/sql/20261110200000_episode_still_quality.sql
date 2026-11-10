-- +goose Up
-- +goose StatementBegin
-- The image size a library wants for its episode thumbnails: hd (1280x720),
-- full_hd (1920x1080) or uhd (3840x2160). A provider image below it is
-- replaced by a frame of the video when the library takes frames, and an
-- image that nothing can bring up to the target stays at the best size found.
ALTER TABLE public.media_folders
    ADD COLUMN episode_still_quality text NOT NULL DEFAULT 'full_hd'
        CHECK (episode_still_quality IN ('hd', 'full_hd', 'uhd'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_folders DROP COLUMN episode_still_quality;
-- +goose StatementEnd
