package apiv2

import (
	"context"
	"errors"
	"net/http"
	"time"

	mediacatalog "github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/filmography"
)

// FilmographyService lists a person's full credits and holds the request
// server titles outside the library link to.
type FilmographyService interface {
	Get(ctx context.Context, personID int64, filter mediacatalog.AccessFilter) (filmography.Filmography, error)
	RequestURL(ctx context.Context) (string, error)
	SetRequestURL(ctx context.Context, value string) (string, error)
}

type FilmographyRole struct {
	Department   string `json:"department" doc:"Acting for a cast credit; the crew department otherwise, such as Directing."`
	Character    string `json:"character,omitempty"`
	Job          string `json:"job,omitempty"`
	EpisodeCount int    `json:"episode_count,omitempty"`
	Self         bool   `json:"self,omitempty" doc:"An appearance as themselves, such as a talk-show guest spot."`
}

type FilmographyCredit struct {
	Key           string            `json:"key" doc:"Stable key for the title within this filmography."`
	ItemType      string            `json:"item_type" enum:"movie,series"`
	Title         string            `json:"title"`
	OriginalTitle string            `json:"original_title,omitempty"`
	ReleaseDate   string            `json:"release_date,omitempty" format:"date" doc:"First release or air date."`
	Year          int               `json:"year,omitempty"`
	Overview      string            `json:"overview,omitempty"`
	PosterURL     string            `json:"poster_url,omitempty" doc:"Provider poster; not validated as a URI."`
	BackdropURL   string            `json:"backdrop_url,omitempty" doc:"Provider backdrop; not validated as a URI."`
	Roles         []FilmographyRole `json:"roles"`
	ProviderIDs   map[string]string `json:"provider_ids"`
	VoteAverage   float64           `json:"vote_average,omitempty"`
	ItemID        string            `json:"item_id,omitempty" doc:"The library item, when a library the viewer can see has the title."`
	RequestURL    string            `json:"request_url,omitempty" doc:"Opens the title on the configured request server, for titles the viewer's libraries lack; not validated as a URI."`
}

type PersonFilmography struct {
	PersonID  ID                  `json:"person_id"`
	Available bool                `json:"available" doc:"False when no metadata provider could list the person's credits; credits is then empty."`
	FetchedAt *time.Time          `json:"fetched_at,omitempty" doc:"When the provider credits were fetched; they are reused for a week."`
	Credits   []FilmographyCredit `json:"credits" doc:"One entry per title, newest first; titles without a date lead."`
}

type PersonFilmographyOutput struct{ Body PersonFilmography }

type FilmographySettings struct {
	RequestURL string `json:"request_url" doc:"Base URL of a Jellyseerr or Overseerr server; empty hides request links, so not validated as a URI."`
}

type FilmographySettingsOutput struct{ Body FilmographySettings }

type FilmographySettingsInput struct{ Body FilmographySettings }

func (reg *Registry) filmography() (FilmographyService, *Problem) {
	if reg.deps.Filmography == nil {
		return nil, unavailable("filmography")
	}
	return reg.deps.Filmography, nil
}

func registerFilmography(reg *Registry) {
	Register(reg, viewerOperation(humaOp(http.MethodGet, Prefix+"/catalog/people/{id}/filmography", "getPersonFilmography", "catalog",
		"Every movie and series the person worked on, as their metadata provider lists them, marked with the library items the viewer can open.")),
		func(ctx context.Context, in *PersonInput) (*PersonFilmographyOutput, error) {
			svc, p := reg.filmography()
			if p != nil {
				return nil, p
			}
			people, p := reg.people()
			if p != nil {
				return nil, p
			}
			if _, _, p := viewerIdentity(ctx); p != nil {
				return nil, p
			}
			id, p := in.ID.positive(locationPathID)
			if p != nil {
				return nil, p
			}
			filter, p := reg.peopleAccessFilter(ctx)
			if p != nil {
				return nil, p
			}
			// The person must be one the viewer may open.
			if _, err := people.Person(ctx, int64(id), false, filter); err != nil {
				return nil, serviceProblem(err)
			}
			f, err := svc.Get(ctx, int64(id), filter)
			if errors.Is(err, filmography.ErrPersonNotFound) {
				return nil, NewProblem(TypeNotFound, "No person has this id.")
			}
			if err != nil {
				return nil, serviceProblem(err)
			}
			out := PersonFilmography{PersonID: in.ID, Available: f.Available, Credits: make([]FilmographyCredit, 0, len(f.Credits))}
			if !f.FetchedAt.IsZero() {
				out.FetchedAt = &f.FetchedAt
			}
			for _, c := range f.Credits {
				roles := make([]FilmographyRole, 0, len(c.Roles))
				for _, r := range c.Roles {
					roles = append(roles, FilmographyRole(r))
				}
				out.Credits = append(out.Credits, FilmographyCredit{
					Key: c.Key, ItemType: c.ItemType, Title: c.Title, OriginalTitle: c.OriginalTitle,
					ReleaseDate: c.ReleaseDate, Year: c.Year, Overview: c.Overview, PosterURL: c.PosterURL,
					BackdropURL: c.BackdropURL, Roles: roles, ProviderIDs: c.ProviderIDs, VoteAverage: c.VoteAverage,
					ItemID: c.ItemID, RequestURL: c.RequestURL,
				})
			}
			return &PersonFilmographyOutput{Body: out}, nil
		})

	Register(reg, Operation{Operation: humaOp(http.MethodGet, Prefix+"/admin/filmography/settings", "getAdminFilmographySettings", "admin",
		"Read the filmography settings."), Class: ClassActingAdmin, ServiceBacked: true},
		func(ctx context.Context, _ *struct{}) (*FilmographySettingsOutput, error) {
			svc, p := reg.filmography()
			if p != nil {
				return nil, p
			}
			value, err := svc.RequestURL(ctx)
			if err != nil {
				return nil, serviceProblem(err)
			}
			return &FilmographySettingsOutput{Body: FilmographySettings{RequestURL: value}}, nil
		})

	Register(reg, Operation{Operation: humaOp(http.MethodPut, Prefix+"/admin/filmography/settings", "updateAdminFilmographySettings", "admin",
		"Save the filmography settings."), Class: ClassActingAdmin, ServiceBacked: true, DemoRestricted: true, RetrySafety: RetrySafetyNaturalIdempotent},
		func(ctx context.Context, in *FilmographySettingsInput) (*FilmographySettingsOutput, error) {
			svc, p := reg.filmography()
			if p != nil {
				return nil, p
			}
			value, err := svc.SetRequestURL(ctx, in.Body.RequestURL)
			if errors.Is(err, filmography.ErrInvalidRequestURL) {
				return nil, NewProblem(TypeValidationFailed, "The request server must be an http or https URL.")
			}
			if err != nil {
				return nil, serviceProblem(err)
			}
			return &FilmographySettingsOutput{Body: FilmographySettings{RequestURL: value}}, nil
		})
}
