package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/episodeframes"
)

// LibraryEpisodeStillSizesService counts a library's episode stills by source
// and size; episodeframes.Sizes.
type LibraryEpisodeStillSizesService interface {
	Summary(ctx context.Context, libraryID int) ([]episodeframes.SizeCount, error)
}

type LibraryEpisodeStillSizeCount struct {
	Source string `json:"source" enum:"tmdb,tvdb,frame,local,other,none" doc:"Where the still came from: TMDB, TheTVDB, a frame of the video, an image beside the file, another provider, or no still"`
	Size   string `json:"size" enum:"4k,1080p,720p,below_720p,none,unmeasured,unreadable" doc:"Size band of the stored still; unmeasured covers stills not measured yet or still downloading"`
	Count  int    `json:"count" example:"120"`
}

type LibraryEpisodeStillSizes struct {
	Total  int                            `json:"total" doc:"Episodes in the library" example:"25634"`
	Counts []LibraryEpisodeStillSizeCount `json:"counts"`
}

type LibraryEpisodeStillSizesOutput struct{ Body LibraryEpisodeStillSizes }

func registerLibraryEpisodeStillSizes(reg *Registry) {
	op := humaOp(http.MethodGet, Prefix+"/libraries/{id}/episode-still-sizes", "getLibraryEpisodeStillSizes", "libraries",
		"Count the library's episode thumbnails by source and size. Sizes are measured by the generate_episode_frame_stills task.")
	Register(reg, Operation{Operation: op, Class: ClassActingAdmin, ServiceBacked: true},
		func(ctx context.Context, in *LibraryIDInput) (*LibraryEpisodeStillSizesOutput, error) {
			if reg.deps.LibraryEpisodeStillSizes == nil {
				return nil, unavailable("episode thumbnail sizes")
			}
			id, p := libraryID(in.ID)
			if p != nil {
				return nil, p
			}
			counts, err := reg.deps.LibraryEpisodeStillSizes.Summary(ctx, id)
			if err != nil {
				return nil, serviceProblem(err)
			}
			out := LibraryEpisodeStillSizes{Counts: make([]LibraryEpisodeStillSizeCount, 0, len(counts))}
			for _, c := range counts {
				out.Total += c.Count
				out.Counts = append(out.Counts, LibraryEpisodeStillSizeCount(c))
			}
			return &LibraryEpisodeStillSizesOutput{Body: out}, nil
		})
}
