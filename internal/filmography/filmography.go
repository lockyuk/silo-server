// Package filmography lists everything a person worked on: every movie and
// series their metadata provider knows, marked with whether the viewer's
// libraries have it. Provider credits are cached per person.
package filmography

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata"
)

const (
	// SettingRequestURL is the base URL of a Jellyseerr or Overseerr server.
	// Titles a library lacks link to it so viewers can request them.
	SettingRequestURL = "filmography.request_url"

	// cacheTTL is how long a person's provider credits are reused.
	cacheTTL = 7 * 24 * time.Hour
	// failureTTL keeps a failed lookup from being retried on every view.
	failureTTL = 15 * time.Minute

	ItemTypeMovie  = "movie"
	ItemTypeSeries = "series"
)

// ErrPersonNotFound means no person has the id.
var ErrPersonNotFound = errors.New("person not found")

// Providers returns the enabled metadata providers.
type Providers func(ctx context.Context) ([]metadata.Provider, error)

// Images resolves provider artwork paths to URLs.
type Images interface {
	ResolveImageURLs(ctx context.Context, paths []string, variant string) map[string]string
}

// Access reports which items a viewer may see.
type Access interface {
	EnsureAccessibleIDs(ctx context.Context, contentIDs []string, filter catalog.AccessFilter) (map[string]bool, error)
}

// Settings reads server settings.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// Role is one way a person worked on a title.
type Role struct {
	Department   string `json:"department"`
	Character    string `json:"character,omitempty"`
	Job          string `json:"job,omitempty"`
	EpisodeCount int    `json:"episode_count,omitempty"`
	// Self marks an appearance as themselves, such as a talk-show guest spot.
	Self bool `json:"self,omitempty"`
}

// Credit is one title with every role the person had on it.
type Credit struct {
	Key           string            `json:"key"`
	ItemType      string            `json:"item_type"`
	Title         string            `json:"title"`
	OriginalTitle string            `json:"original_title,omitempty"`
	ReleaseDate   string            `json:"release_date,omitempty"`
	Year          int               `json:"year,omitempty"`
	Overview      string            `json:"overview,omitempty"`
	PosterURL     string            `json:"poster_url,omitempty"`
	BackdropURL   string            `json:"backdrop_url,omitempty"`
	Roles         []Role            `json:"roles"`
	ProviderIDs   map[string]string `json:"provider_ids"`
	VoteAverage   float64           `json:"vote_average,omitempty"`
	Popularity    float64           `json:"popularity,omitempty"`
	// ItemID is the library item, when a library the viewer can see has it.
	ItemID string `json:"item_id,omitempty"`
	// RequestURL opens the title on the request server, for titles the
	// viewer's libraries lack, when one is configured.
	RequestURL string `json:"request_url,omitempty"`
}

// Filmography is a person's credits, newest first.
type Filmography struct {
	PersonID int64 `json:"person_id"`
	// Available is false when no provider could list credits for the person.
	Available bool      `json:"available"`
	FetchedAt time.Time `json:"fetched_at,omitzero"`
	Credits   []Credit  `json:"credits"`
}

// Service builds filmographies.
type Service struct {
	pool      *pgxpool.Pool
	providers Providers
	images    Images
	access    Access
	settings  Settings
	now       func() time.Time
	group     singleflight.Group

	mu       sync.Mutex
	failures map[int64]time.Time
}

// New creates a Service.
func New(pool *pgxpool.Pool, providers Providers, images Images, access Access, settings Settings) *Service {
	return &Service{pool: pool, providers: providers, images: images, access: access, settings: settings, now: time.Now, failures: map[int64]time.Time{}}
}

// RequestURL returns the configured request server, or "".
func (s *Service) RequestURL(ctx context.Context) (string, error) {
	value, err := s.settings.Get(ctx, SettingRequestURL)
	return strings.TrimRight(strings.TrimSpace(value), "/"), err
}

// SetRequestURL stores the request server; "" removes it.
func (s *Service) SetRequestURL(ctx context.Context, value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value != "" {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("%w: the request server must be an http or https URL", ErrInvalidRequestURL)
		}
	}
	return value, s.settings.Set(ctx, SettingRequestURL, value)
}

// ErrInvalidRequestURL rejects a request server that is not a web URL.
var ErrInvalidRequestURL = errors.New("invalid request server")

// Get returns a person's filmography as the viewer sees it.
func (s *Service) Get(ctx context.Context, personID int64, filter catalog.AccessFilter) (Filmography, error) {
	out := Filmography{PersonID: personID, Credits: []Credit{}}
	ids, err := s.personIDs(ctx, personID)
	if err != nil {
		return out, err
	}
	raw, fetchedAt, err := s.credits(ctx, personID, ids)
	if err != nil {
		return out, err
	}
	if fetchedAt.IsZero() {
		return out, nil
	}
	out.Available, out.FetchedAt = true, fetchedAt
	out.Credits = group(raw)
	if err := s.match(ctx, out.Credits, filter); err != nil {
		return out, err
	}
	s.artwork(ctx, raw, out.Credits)
	if base, err := s.RequestURL(ctx); err == nil && base != "" {
		for i := range out.Credits {
			c := &out.Credits[i]
			if c.ItemID == "" && c.ProviderIDs["tmdb"] != "" {
				kind := "movie"
				if c.ItemType == ItemTypeSeries {
					kind = "tv"
				}
				c.RequestURL = base + "/" + kind + "/" + url.PathEscape(c.ProviderIDs["tmdb"])
			}
		}
	}
	return out, nil
}

func (s *Service) personIDs(ctx context.Context, personID int64) (map[string]string, error) {
	var tmdb, imdb, tvdb string
	err := s.pool.QueryRow(ctx, `SELECT tmdb_id, imdb_id, tvdb_id FROM people WHERE id = $1`, personID).Scan(&tmdb, &imdb, &tvdb)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPersonNotFound
	}
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for key, value := range map[string]string{"tmdb": tmdb, "imdb": imdb, "tvdb": tvdb} {
		if value != "" {
			ids[key] = value
		}
	}
	return ids, nil
}

// credits returns the cached provider credits, refreshing them when stale. A
// zero time means no provider could list them.
func (s *Service) credits(ctx context.Context, personID int64, ids map[string]string) ([]metadata.PersonCredit, time.Time, error) {
	var cached []metadata.PersonCredit
	var raw []byte
	var fetchedAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT credits, fetched_at FROM person_credits WHERE person_id = $1`, personID).Scan(&raw, &fetchedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &cached); err != nil {
			fetchedAt = time.Time{}
		}
	}
	if !fetchedAt.IsZero() && s.now().Sub(fetchedAt) < cacheTTL {
		return cached, fetchedAt, nil
	}
	if s.recentlyFailed(personID) {
		return cached, fetchedAt, nil
	}
	v, err, _ := s.group.Do(strconv.FormatInt(personID, 10), func() (any, error) {
		return s.fetch(ctx, personID, ids)
	})
	if err != nil {
		s.markFailed(personID)
		slog.WarnContext(ctx, "filmography: provider credits unavailable", "component", "filmography", "person_id", personID, "error", err)
		// A stale list is better than none.
		return cached, fetchedAt, nil
	}
	fresh := v.(fetched)
	return fresh.credits, fresh.at, nil
}

type fetched struct {
	credits []metadata.PersonCredit
	at      time.Time
}

// fetch asks each provider that can list credits, in provider order, and
// stores the first answer.
func (s *Service) fetch(ctx context.Context, personID int64, ids map[string]string) (fetched, error) {
	if len(ids) == 0 {
		return fetched{}, nil
	}
	providers, err := s.providers(ctx)
	if err != nil {
		return fetched{}, err
	}
	var lastErr error
	for _, provider := range providers {
		creditsProvider, ok := provider.(metadata.PersonCreditsProvider)
		if !ok {
			continue
		}
		credits, err := creditsProvider.GetPersonCredits(ctx, metadata.PersonDetailRequest{ProviderIDs: ids, Language: "en"})
		if errors.Is(err, metadata.ErrPersonCreditsUnsupported) {
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		if credits == nil {
			credits = []metadata.PersonCredit{}
		}
		raw, err := json.Marshal(credits)
		if err != nil {
			return fetched{}, err
		}
		at := s.now().Truncate(time.Microsecond)
		if _, err := s.pool.Exec(ctx, `INSERT INTO person_credits (person_id, credits, fetched_at) VALUES ($1, $2, $3)
			ON CONFLICT (person_id) DO UPDATE SET credits = EXCLUDED.credits, fetched_at = EXCLUDED.fetched_at`, personID, raw, at); err != nil {
			return fetched{}, err
		}
		return fetched{credits: credits, at: at}, nil
	}
	return fetched{}, lastErr
}

func (s *Service) recentlyFailed(personID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.failures[personID]
	return ok && s.now().Sub(at) < failureTTL
}

func (s *Service) markFailed(personID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, at := range s.failures {
		if now.Sub(at) >= failureTTL {
			delete(s.failures, id)
		}
	}
	s.failures[personID] = now
}

// selfCharacters are how providers credit a person appearing as themselves.
var selfCharacters = []string{"self", "himself", "herself", "themselves", "themself"}

func isSelf(character string) bool {
	c := strings.ToLower(strings.TrimSpace(character))
	for _, self := range selfCharacters {
		if c == self || strings.HasPrefix(c, self+" ") || strings.HasPrefix(c, self+"-") || strings.HasPrefix(c, self+"(") || strings.HasPrefix(c, self+",") {
			return true
		}
	}
	return false
}

// group folds a person's credits into one entry per title, newest first,
// dropping item types this server does not catalog.
func group(raw []metadata.PersonCredit) []Credit {
	byKey := map[string]int{}
	out := []Credit{}
	for _, c := range raw {
		if c.ItemType != ItemTypeMovie && c.ItemType != ItemTypeSeries {
			continue
		}
		id := c.ProviderIDs[c.Provider]
		if id == "" {
			continue
		}
		key := c.ItemType + ":" + c.Provider + ":" + id
		role := Role{Department: c.Department, Character: c.Character, Job: c.Job, EpisodeCount: c.EpisodeCount}
		if role.Department == "Acting" && isSelf(role.Character) {
			role.Self = true
		}
		if i, ok := byKey[key]; ok {
			if !slices.Contains(out[i].Roles, role) {
				out[i].Roles = append(out[i].Roles, role)
			}
			continue
		}
		byKey[key] = len(out)
		credit := Credit{
			Key: key, ItemType: c.ItemType, Title: c.Title, OriginalTitle: c.OriginalTitle,
			ReleaseDate: c.ReleaseDate, Overview: c.Overview, Roles: []Role{role},
			ProviderIDs: c.ProviderIDs, VoteAverage: c.VoteAverage, Popularity: c.Popularity,
		}
		if d, err := time.Parse(time.DateOnly, c.ReleaseDate); err == nil {
			credit.Year = d.Year()
		} else {
			credit.ReleaseDate = ""
		}
		out = append(out, credit)
	}
	// Newest first; titles without a date (usually not yet released) lead,
	// as Plex shows upcoming work at the top.
	slices.SortStableFunc(out, func(a, b Credit) int {
		switch {
		case a.ReleaseDate == b.ReleaseDate:
			return strings.Compare(a.Title, b.Title)
		case a.ReleaseDate == "":
			return -1
		case b.ReleaseDate == "":
			return 1
		}
		return strings.Compare(b.ReleaseDate, a.ReleaseDate)
	})
	return out
}

// match links credits to library items the viewer can see, by provider ID.
func (s *Service) match(ctx context.Context, credits []Credit, filter catalog.AccessFilter) error {
	tmdb := []string{}
	imdb := []string{}
	for _, c := range credits {
		if id := c.ProviderIDs["tmdb"]; id != "" {
			tmdb = append(tmdb, id)
		}
		if id := c.ProviderIDs["imdb"]; id != "" {
			imdb = append(imdb, id)
		}
	}
	if len(tmdb) == 0 && len(imdb) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT content_id, type, tmdb_id, imdb_id FROM media_items
		WHERE type IN ('movie', 'series') AND ((tmdb_id <> '' AND tmdb_id = ANY($1)) OR (imdb_id <> '' AND imdb_id = ANY($2)))
		ORDER BY content_id`, tmdb, imdb)
	if err != nil {
		return err
	}
	byTMDB, byIMDb := map[string]string{}, map[string]string{}
	var items []string
	for rows.Next() {
		var id, kind, t, i string
		if err := rows.Scan(&id, &kind, &t, &i); err != nil {
			rows.Close()
			return err
		}
		// TMDB numbers movies and TV separately, so the type is part of the key.
		if t != "" {
			if _, ok := byTMDB[kind+":"+t]; !ok {
				byTMDB[kind+":"+t] = id
			}
		}
		if i != "" {
			if _, ok := byIMDb[kind+":"+i]; !ok {
				byIMDb[kind+":"+i] = id
			}
		}
		items = append(items, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	visible, err := s.access.EnsureAccessibleIDs(ctx, items, filter)
	if err != nil {
		return fmt.Errorf("checking filmography access: %w", err)
	}
	for i := range credits {
		c := &credits[i]
		id := byTMDB[c.ItemType+":"+c.ProviderIDs["tmdb"]]
		if id == "" || !visible[id] {
			id = byIMDb[c.ItemType+":"+c.ProviderIDs["imdb"]]
		}
		if id != "" && visible[id] {
			c.ItemID = id
		}
	}
	return nil
}

// artwork resolves each credit's poster and backdrop to URLs.
func (s *Service) artwork(ctx context.Context, raw []metadata.PersonCredit, credits []Credit) {
	if s.images == nil {
		return
	}
	posters, backdrops := map[string]string{}, map[string]string{}
	for _, c := range raw {
		key := c.ItemType + ":" + c.Provider + ":" + c.ProviderIDs[c.Provider]
		if c.PosterPath != "" && posters[key] == "" {
			posters[key] = c.PosterPath
		}
		if c.BackdropPath != "" && backdrops[key] == "" {
			backdrops[key] = c.BackdropPath
		}
	}
	resolve := func(paths map[string]string, variant string) map[string]string {
		list := make([]string, 0, len(paths))
		for _, p := range paths {
			list = append(list, p)
		}
		if len(list) == 0 {
			return map[string]string{}
		}
		return s.images.ResolveImageURLs(ctx, list, variant)
	}
	posterURLs := resolve(posters, "card")
	backdropURLs := resolve(backdrops, "featured")
	for i := range credits {
		c := &credits[i]
		c.PosterURL = posterURLs[posters[c.Key]]
		c.BackdropURL = backdropURLs[backdrops[c.Key]]
	}
}
