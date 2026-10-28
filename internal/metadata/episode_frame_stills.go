package metadata

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// episodeFrameStillState is what a refresh needs to keep the frame stills
// internal/episodeframes gave a series' episodes: the series' frame mode and,
// per episode with a frame, the provider source the frame displaced.
type episodeFrameStillState struct {
	mode      string
	displaced map[string]string
}

// episodeFrameStills loads the frame-still state of a series. The mode is the
// most inclusive of its libraries'.
func (s *MetadataService) episodeFrameStills(ctx context.Context, seriesID string) episodeFrameStillState {
	var st episodeFrameStillState
	if s.dbPool == nil || seriesID == "" {
		return st
	}
	err := s.dbPool.QueryRow(ctx, `
		SELECT f.episode_frame_stills
		FROM media_item_libraries mil
		JOIN media_folders f ON f.id = mil.media_folder_id
		WHERE mil.content_id = $1 AND f.episode_frame_stills <> 'off'
		ORDER BY (f.episode_frame_stills = 'below_full_hd') DESC
		LIMIT 1`, seriesID).Scan(&st.mode)
	if err != nil {
		return episodeFrameStillState{}
	}
	rows, err := s.dbPool.Query(ctx, `
		SELECT c.content_id, c.source_path
		FROM episode_frame_still_checks c
		JOIN episodes e ON e.content_id = c.content_id
		WHERE e.series_id = $1 AND c.outcome = 'frame'`, seriesID)
	if err != nil {
		slog.WarnContext(ctx, "metadata: loading episode frame stills failed", "component", "metadata",
			"series_id", seriesID, "error", err)
		return episodeFrameStillState{}
	}
	defer rows.Close()
	st.displaced = make(map[string]string)
	for rows.Next() {
		var id, source string
		if err := rows.Scan(&id, &source); err != nil {
			return episodeFrameStillState{}
		}
		st.displaced[id] = source
	}
	if rows.Err() != nil {
		return episodeFrameStillState{}
	}
	return st
}

// keeps reports whether a refresh should leave an episode's frame still in
// place: the library still takes frames for this case and the provider offers
// the same source the frame displaced. A new provider image replaces the frame,
// and the frame task then judges the new image.
func (st episodeFrameStillState) keeps(existing *models.Episode, incomingSource string) bool {
	if st.mode == "" || existing == nil || !strings.HasPrefix(existing.StillSourcePath, catalog.StillSourceFramePrefix) {
		return false
	}
	displaced, ok := st.displaced[existing.ContentID]
	if !ok || displaced != incomingSource {
		return false
	}
	return st.mode == models.EpisodeFrameStillsBelowFullHD || displaced == ""
}
