package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/librarystats"
)

// LibraryMediaStatsService summarizes a library's video files by resolution;
// librarystats.Stats.
type LibraryMediaStatsService interface {
	Summary(ctx context.Context, libraryID int) ([]librarystats.Band, error)
}

type LibraryMediaStatsBand struct {
	Resolution string `json:"resolution" enum:"4k,1080p,720p,sd,unknown" doc:"Resolution band of the video files"`
	Files      int    `json:"files" example:"1200"`
	SizeBytes  int64  `json:"size_bytes" example:"3200000000000"`
}

type LibraryMediaStats struct {
	TotalFiles     int                     `json:"total_files" doc:"Video files in the library" example:"19268"`
	TotalSizeBytes int64                   `json:"total_size_bytes" doc:"Space the library's video files take on disk" example:"70000000000000"`
	Bands          []LibraryMediaStatsBand `json:"bands"`
}

type LibraryMediaStatsOutput struct{ Body LibraryMediaStats }

func registerLibraryMediaStats(reg *Registry) {
	op := humaOp(http.MethodGet, Prefix+"/libraries/{id}/media-stats", "getLibraryMediaStats", "libraries",
		"Size on disk and file counts by resolution for the library's video files.")
	Register(reg, Operation{Operation: op, Class: ClassActingAdmin, ServiceBacked: true},
		func(ctx context.Context, in *LibraryIDInput) (*LibraryMediaStatsOutput, error) {
			if reg.deps.LibraryMediaStats == nil {
				return nil, unavailable("library media stats")
			}
			id, p := libraryID(in.ID)
			if p != nil {
				return nil, p
			}
			bands, err := reg.deps.LibraryMediaStats.Summary(ctx, id)
			if err != nil {
				return nil, serviceProblem(err)
			}
			out := LibraryMediaStats{Bands: make([]LibraryMediaStatsBand, 0, len(bands))}
			for _, b := range bands {
				out.TotalFiles += b.Files
				out.TotalSizeBytes += b.SizeBytes
				out.Bands = append(out.Bands, LibraryMediaStatsBand(b))
			}
			return &LibraryMediaStatsOutput{Body: out}, nil
		})
}
