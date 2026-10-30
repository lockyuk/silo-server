package apiv2

import (
	"context"
	"net/http"
)

// AdminRatingSource is one external rating source an administrator can show on
// title pages: one of Silo's own, or one a metadata plugin declared.
type AdminRatingSource struct {
	Source      string `json:"source" doc:"Source name, the value catalog.extra_rating_sources lists" example:"rt_critic"`
	Label       string `json:"label" doc:"The source's name in full" example:"Rotten Tomatoes critics"`
	Name        string `json:"name" doc:"The plain-text mark clients show next to its score" example:"RT"`
	AlwaysShown bool   `json:"always_shown" doc:"True for IMDb and TMDB, which always show; every other source shows only once catalog.extra_rating_sources lists it"`
	Provider    string `json:"provider,omitempty" doc:"The metadata providers that declare the source, such as \"NFO Files and MDBList\"; absent for Silo's own sources" example:"Kinopoisk"`
}

// AdminRatingSourceCapabilities describes the rating source support in this
// API build, not which sources a plugin declares or an administrator shows.
type AdminRatingSourceCapabilities struct {
	Capability
	PluginDeclaredSources bool `json:"plugin_declared_sources" doc:"listAdminRatingSources lists IMDb, TMDB and the sources enabled metadata plugins declare, and title pages show a declared source once catalog.extra_rating_sources names it"`
}

// AdminRatingSourceCapabilitiesOutput is the getAdminRatingSourceCapabilities
// response.
type AdminRatingSourceCapabilitiesOutput struct {
	Status       int
	ETag         string `header:"ETag"`
	CacheControl string `header:"Cache-Control"`
	Body         AdminRatingSourceCapabilities
}

func (c AdminRatingSourceCapabilities) capabilityState() string { return StateAvailable }

// AdminRatingSourceCollectionOutput is the listAdminRatingSources response.
type AdminRatingSourceCollectionOutput struct {
	Body Collection[AdminRatingSource]
}

func registerAdminRatingSources(reg *Registry) {
	capabilities := Operation{Operation: humaOp(http.MethodGet, Prefix+"/admin/rating-sources/capabilities", "getAdminRatingSourceCapabilities", "admin-settings",
		"Discover the rating source support in this build, such as sources metadata plugins declare. Support does not promise that any plugin declares a source."), Class: ClassActingAdmin}
	Register(reg, capabilities, func(context.Context, *CapabilityInput) (*AdminRatingSourceCapabilitiesOutput, error) {
		return &AdminRatingSourceCapabilitiesOutput{Body: AdminRatingSourceCapabilities{PluginDeclaredSources: true}}, nil
	})

	o := Operation{
		Operation: humaOp(http.MethodGet, Prefix+"/admin/rating-sources", "listAdminRatingSources", "admin-settings",
			"List the external rating sources an administrator can show on title pages, including sources metadata plugins declare."),
		Class: ClassActingAdmin, ServiceBacked: true,
	}
	Register(reg, o, func(ctx context.Context, _ *struct{}) (*AdminRatingSourceCollectionOutput, error) {
		sources, err := reg.deps.RatingSources.Sources(ctx)
		if err != nil {
			return nil, serviceProblem(err)
		}
		items := make([]AdminRatingSource, 0, len(sources))
		for _, s := range sources {
			items = append(items, AdminRatingSource{Source: s.Source, Label: s.Label, Name: s.Name, AlwaysShown: s.AlwaysShown, Provider: s.Provider})
		}
		return &AdminRatingSourceCollectionOutput{Body: NewCollection(items)}, nil
	})
}

// AdminLibraryRatingSources is one library's choice of the rating sources its
// titles show.
type AdminLibraryRatingSources struct {
	UseServerDefault bool     `json:"use_server_default" doc:"True when the library shows what the server-wide setting shows (IMDb, TMDB and catalog.extra_rating_sources)"`
	Sources          []string `json:"sources" doc:"The sources the library shows, in no particular order; empty with use_server_default. May leave out IMDb or TMDB, and may name any source listAdminRatingSources lists"`
}

type AdminLibraryRatingSourcesInput struct {
	ID int `path:"id" minimum:"1" doc:"Library id."`
}

type AdminLibraryRatingSourcesUpdateInput struct {
	ID   int `path:"id" minimum:"1" doc:"Library id."`
	Body AdminLibraryRatingSources
}

type AdminLibraryRatingSourcesOutput struct{ Body AdminLibraryRatingSources }

func registerAdminLibraryRatingSources(reg *Registry) {
	get := Operation{Operation: humaOp(http.MethodGet, Prefix+"/admin/libraries/{id}/rating-sources", "getAdminLibraryRatingSources", "admin-settings",
		"Read which external rating sources one library's titles show."), Class: ClassActingAdmin, ServiceBacked: true}
	Register(reg, get, func(ctx context.Context, in *AdminLibraryRatingSourcesInput) (*AdminLibraryRatingSourcesOutput, error) {
		sources, ok, err := reg.deps.RatingSources.LibraryChoice(ctx, in.ID)
		if err != nil {
			return nil, serviceProblem(err)
		}
		return &AdminLibraryRatingSourcesOutput{Body: AdminLibraryRatingSources{UseServerDefault: !ok, Sources: NonNil(sources)}}, nil
	})

	put := Operation{Operation: humaOp(http.MethodPut, Prefix+"/admin/libraries/{id}/rating-sources", "updateAdminLibraryRatingSources", "admin-settings",
		"Choose which external rating sources one library's titles show, or make it follow the server-wide setting again."),
		Class: ClassActingAdmin, ServiceBacked: true, DemoRestricted: true, RetrySafety: RetrySafetyNaturalIdempotent}
	Register(reg, put, func(ctx context.Context, in *AdminLibraryRatingSourcesUpdateInput) (*AdminLibraryRatingSourcesOutput, error) {
		var sources []string
		if !in.Body.UseServerDefault {
			known, err := reg.deps.RatingSources.Sources(ctx)
			if err != nil {
				return nil, serviceProblem(err)
			}
			valid := make(map[string]bool, len(known))
			for _, s := range known {
				valid[s.Source] = true
			}
			seen := map[string]bool{}
			sources = []string{}
			for _, s := range in.Body.Sources {
				if !valid[s] {
					return nil, validationProblem(locationBody+".sources", codeInvalid, "Unknown rating source "+s+".")
				}
				if !seen[s] {
					seen[s] = true
					sources = append(sources, s)
				}
			}
		}
		if err := reg.deps.RatingSources.SetLibraryChoice(ctx, in.ID, sources); err != nil {
			return nil, serviceProblem(err)
		}
		return &AdminLibraryRatingSourcesOutput{Body: AdminLibraryRatingSources{UseServerDefault: sources == nil, Sources: NonNil(sources)}}, nil
	})
}
