package apiv2

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/themedownload"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

// AdminThemeSongsService manages the TV theme songs Silo downloads into its
// own storage. The run itself is the download_theme_songs task.
type AdminThemeSongsService interface {
	Config(context.Context) (themedownload.Config, error)
	SaveConfig(context.Context, themedownload.Config) (themedownload.Config, error)
	List(context.Context) ([]themedownload.Series, error)
	Retry(context.Context, string) (themedownload.Series, error)
	Remove(context.Context, string) (themedownload.Series, error)
	Skip(context.Context, string) (themedownload.Series, error)
	Start(force bool, reason string) error
	Status() (bool, string)
	LastReport(context.Context) (*themedownload.Report, error)
}

type ThemeSongDownloadConfig struct {
	Enabled     bool `json:"enabled" doc:"Download theme songs for series without one in their folder."`
	RecheckDays int  `json:"recheck_days" minimum:"1" maximum:"365" doc:"Days before a theme the source lacked is asked for again."`
}

type ThemeSongSeries struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	TVDBID       string     `json:"tvdb_id" doc:"TheTVDB id the theme is looked up by; empty when the series has none."`
	State        string     `json:"state" enum:"folder,downloaded,not_available,failed,pending,skipped" doc:"folder: a theme file in the series folder is used; downloaded: Silo's stored copy is used; skipped: an admin stopped looking for one."`
	Detail       string     `json:"detail,omitempty" doc:"Why the last download failed."`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	DownloadedAt *time.Time `json:"downloaded_at,omitempty"`
}

type ThemeSongCounts struct {
	Folder       int `json:"folder"`
	Downloaded   int `json:"downloaded"`
	NotAvailable int `json:"not_available"`
	Failed       int `json:"failed"`
	Pending      int `json:"pending"`
	Skipped      int `json:"skipped"`
}

type ThemeSongRunReport struct {
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished"`
	Reason       string    `json:"reason"`
	Error        string    `json:"error,omitempty"`
	Series       int       `json:"series"`
	Checked      int       `json:"checked"`
	Downloaded   int       `json:"downloaded"`
	NotAvailable int       `json:"not_available"`
	Failed       int       `json:"failed"`
	Skipped      int       `json:"skipped"`
	Failures     []string  `json:"failures,omitempty"`
}

type AdminThemeSongs struct {
	Config ThemeSongDownloadConfig `json:"config"`
	Counts ThemeSongCounts         `json:"counts"`
	Series []ThemeSongSeries       `json:"series"`
	// Running and Progress describe a check in progress on this process.
	Running  bool                `json:"running"`
	Progress string              `json:"progress,omitempty"`
	Report   *ThemeSongRunReport `json:"report,omitempty" doc:"The most recent check."`
}

type AdminThemeSongsRunInput struct {
	Body struct {
		Force bool `json:"force,omitempty" doc:"Ask again for themes the source lacked, without waiting for the recheck interval."`
	}
}

type AdminThemeSongsOutput struct{ Body AdminThemeSongs }

type AdminThemeSongsConfigInput struct {
	Body ThemeSongDownloadConfig
}

type AdminThemeSongSeriesInput struct {
	ID string `path:"id" doc:"Series id."`
}

type AdminThemeSongSeriesOutput struct{ Body ThemeSongSeries }

func themeSongSeries(s themedownload.Series) ThemeSongSeries {
	return ThemeSongSeries{ID: s.ID, Title: s.Title, TVDBID: s.TVDBID, State: s.State, Detail: s.Detail, CheckedAt: s.CheckedAt, DownloadedAt: s.DownloadedAt}
}

func (reg *Registry) adminThemeSongs() (AdminThemeSongsService, *Problem) {
	if reg.deps.AdminThemeSongs == nil {
		return nil, unavailable("theme song downloads")
	}
	return reg.deps.AdminThemeSongs, nil
}

func themeSongsOperation(method, path, id, summary string) Operation {
	op := Operation{Operation: humaOp(method, Prefix+"/admin/theme-songs"+path, id, "admin", summary), Class: ClassActingAdmin, ServiceBacked: true}
	if method != http.MethodGet {
		op.DemoRestricted = true
		op.RetrySafety = RetrySafetyNonRetryable
	}
	return op
}

func (reg *Registry) themeSongsOverview(ctx context.Context, svc AdminThemeSongsService) (*AdminThemeSongsOutput, error) {
	cfg, err := svc.Config(ctx)
	if err != nil {
		return nil, serviceProblem(err)
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, serviceProblem(err)
	}
	out := AdminThemeSongs{Config: ThemeSongDownloadConfig(cfg), Series: make([]ThemeSongSeries, 0, len(list))}
	for _, s := range list {
		out.Series = append(out.Series, themeSongSeries(s))
		switch s.State {
		case themedownload.StateFolder:
			out.Counts.Folder++
		case themedownload.StateDownloaded:
			out.Counts.Downloaded++
		case themedownload.StateNotAvailable:
			out.Counts.NotAvailable++
		case themedownload.StateFailed:
			out.Counts.Failed++
		case themedownload.StateSkipped:
			out.Counts.Skipped++
		default:
			out.Counts.Pending++
		}
	}
	out.Running, out.Progress = svc.Status()
	report, err := svc.LastReport(ctx)
	if err != nil {
		return nil, serviceProblem(err)
	}
	if report != nil {
		r := ThemeSongRunReport(*report)
		out.Report = &r
	}
	return &AdminThemeSongsOutput{Body: out}, nil
}

func themeSongSeriesProblem(err error) error {
	if errors.Is(err, themesongs.ErrNotFound) {
		return NewProblem(TypeNotFound, "No TV series with files has this id.")
	}
	return serviceProblem(err)
}

func registerAdminThemeSongs(reg *Registry) {
	Register(reg, themeSongsOperation(http.MethodGet, "", "getAdminThemeSongs", "Read the theme song download settings and every TV series' theme state."),
		func(ctx context.Context, _ *struct{}) (*AdminThemeSongsOutput, error) {
			svc, p := reg.adminThemeSongs()
			if p != nil {
				return nil, p
			}
			return reg.themeSongsOverview(ctx, svc)
		})

	settings := themeSongsOperation(http.MethodPut, "/settings", "updateAdminThemeSongSettings", "Save the theme song download settings.")
	settings.RetrySafety = RetrySafetyNaturalIdempotent
	Register(reg, settings, func(ctx context.Context, in *AdminThemeSongsConfigInput) (*AdminThemeSongsOutput, error) {
		svc, p := reg.adminThemeSongs()
		if p != nil {
			return nil, p
		}
		if _, err := svc.SaveConfig(ctx, themedownload.Config(in.Body)); err != nil {
			return nil, serviceProblem(err)
		}
		return reg.themeSongsOverview(ctx, svc)
	})

	Register(reg, themeSongsOperation(http.MethodPost, "/series/{id}/retry", "retryAdminThemeSong", "Download one series' theme now, whatever its last outcome. A series with a folder theme is left alone."),
		func(ctx context.Context, in *AdminThemeSongSeriesInput) (*AdminThemeSongSeriesOutput, error) {
			svc, p := reg.adminThemeSongs()
			if p != nil {
				return nil, p
			}
			s, err := svc.Retry(ctx, in.ID)
			if err != nil {
				return nil, themeSongSeriesProblem(err)
			}
			return &AdminThemeSongSeriesOutput{Body: themeSongSeries(s)}, nil
		})

	run := themeSongsOperation(http.MethodPost, "/run", "runAdminThemeSongs", "Start a check for missing themes in the background; 409 while one runs.")
	run.Errors = []int{http.StatusConflict}
	Register(reg, run, func(ctx context.Context, in *AdminThemeSongsRunInput) (*AdminThemeSongsOutput, error) {
		svc, p := reg.adminThemeSongs()
		if p != nil {
			return nil, p
		}
		reason := "run from the admin page"
		if in.Body.Force {
			reason = "recheck from the admin page"
		}
		if err := svc.Start(in.Body.Force, reason); errors.Is(err, themedownload.ErrRunning) {
			return nil, NewProblem(TypeConflict, "A check is already running.")
		} else if err != nil {
			return nil, serviceProblem(err)
		}
		return reg.themeSongsOverview(ctx, svc)
	})

	Register(reg, themeSongsOperation(http.MethodPost, "/series/{id}/skip", "skipAdminThemeSong", "Delete one series' downloaded theme and stop looking for one until it is retried."),
		func(ctx context.Context, in *AdminThemeSongSeriesInput) (*AdminThemeSongSeriesOutput, error) {
			svc, p := reg.adminThemeSongs()
			if p != nil {
				return nil, p
			}
			s, err := svc.Skip(ctx, in.ID)
			if err != nil {
				return nil, themeSongSeriesProblem(err)
			}
			return &AdminThemeSongSeriesOutput{Body: themeSongSeries(s)}, nil
		})

	Register(reg, themeSongsOperation(http.MethodDelete, "/series/{id}", "deleteAdminThemeSong", "Delete one series' downloaded theme. The next run downloads it again."),
		func(ctx context.Context, in *AdminThemeSongSeriesInput) (*AdminThemeSongSeriesOutput, error) {
			svc, p := reg.adminThemeSongs()
			if p != nil {
				return nil, p
			}
			s, err := svc.Remove(ctx, in.ID)
			if err != nil {
				return nil, themeSongSeriesProblem(err)
			}
			return &AdminThemeSongSeriesOutput{Body: themeSongSeries(s)}, nil
		})
}
