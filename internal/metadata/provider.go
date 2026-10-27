package metadata

import "context"

// Provider is the base interface all providers implement.
type Provider interface {
	Slug() string       // Stable provider identifier.
	Name() string       // Human-readable provider name.
	ForTypes() []string // Content types handled, e.g. ["movie", "series"].
}

// SearchProvider finds items by name/year or external IDs.
type SearchProvider interface {
	Provider
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
}

// MetadataProvider fetches full metadata for an identified item.
type MetadataProvider interface {
	Provider
	GetMetadata(ctx context.Context, req MetadataRequest) (*MetadataResult, error)
}

// PersonProvider fetches biographical details for a person entity.
type PersonProvider interface {
	Provider
	GetPersonDetail(ctx context.Context, req PersonDetailRequest) (*PersonDetailResult, error)
}

// PersonCreditsProvider lists every title a person worked on.
type PersonCreditsProvider interface {
	Provider
	GetPersonCredits(ctx context.Context, req PersonDetailRequest) ([]PersonCredit, error)
}

// ImageProvider fetches available images (posters, backdrops, logos).
type ImageProvider interface {
	Provider
	GetImages(ctx context.Context, req ImageRequest) ([]RemoteImage, error)
}

// EpisodeProvider fetches season/episode data for series.
type EpisodeProvider interface {
	Provider
	GetSeasons(ctx context.Context, req SeasonsRequest) ([]SeasonResult, error)
	GetEpisodes(ctx context.Context, req EpisodesRequest) ([]EpisodeResult, error)
}
