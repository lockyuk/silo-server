package metadata

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestEpisodeFrameStillStateKeeps(t *testing.T) {
	framed := &models.Episode{ContentID: "e1", StillSourcePath: "frame:7"}
	noProvider := &models.Episode{ContentID: "e2", StillSourcePath: "frame:8"}
	provider := &models.Episode{ContentID: "e3", StillSourcePath: "tvdb://banners/e3.jpg"}
	displaced := map[string]string{"e1": "tvdb://banners/e1.jpg", "e2": "", "e3": "tvdb://banners/e3.jpg"}
	below := episodeFrameStillState{mode: models.EpisodeFrameStillsBelowFullHD, displaced: displaced}
	missing := episodeFrameStillState{mode: models.EpisodeFrameStillsMissing, displaced: displaced}

	for _, tt := range []struct {
		name     string
		st       episodeFrameStillState
		ep       *models.Episode
		incoming string
		want     bool
	}{
		{"same provider source", below, framed, "tvdb://banners/e1.jpg", true},
		{"new provider source", below, framed, "tmdb://still/e1.jpg", false},
		{"library stopped taking frames", episodeFrameStillState{}, framed, "tvdb://banners/e1.jpg", false},
		{"missing mode with a provider image", missing, framed, "tvdb://banners/e1.jpg", false},
		{"missing mode, still no provider image", missing, noProvider, "", true},
		{"not a frame", below, provider, "tvdb://banners/e3.jpg", false},
	} {
		if got := tt.st.keeps(tt.ep, tt.incoming); got != tt.want {
			t.Errorf("%s: keeps = %v, want %v", tt.name, got, tt.want)
		}
	}
}
