package apiv2

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	catalogpkg "github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/ratingsources"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// The history domain: what a profile has watched.

// HistoryWatch is the most recent watch record behind a history card.
type HistoryWatch struct {
	MediaItemID     ID      `json:"media_item_id" doc:"The item that was watched; an episode when the card is its series" example:"episode:heat-s01e01"`
	WatchedAt       Instant `json:"watched_at" doc:"When the watch was recorded" example:"2026-01-02T03:04:05.000Z"`
	DurationSeconds float64 `json:"duration_seconds" doc:"Known runtime at the time; 0 when unknown" example:"5400"`
	Completed       bool    `json:"completed" doc:"Whether the watch counted as finished" example:"true"`
	Source          string  `json:"source,omitempty" doc:"How the watch was recorded: playback, manual, import, legacy" example:"playback"`
}

// HistoryCard is a catalog card with the watch it stands for. An episode
// watch is shown as its series card, as v1 does.
type HistoryCard struct {
	CatalogItem
	Watch HistoryWatch `json:"watch" doc:"The most recent watch of the card's item"`
}

// HistoryListInput is the listHistory query.
type HistoryListInput struct {
	ArtworkQuery
	LimitParam
	Cursor string `query:"cursor" doc:"Opaque cursor from page.next_cursor" example:"eyJvIjo1MH0"`
}

// HistoryCollection is the named envelope the contract carries.
type HistoryCollection struct {
	Collection[HistoryCard]
}

// HistoryCollectionOutput is the listHistory response.
type HistoryCollectionOutput struct {
	Body HistoryCollection
}

// HistoryRemovalTarget is one thing to remove from history.
type HistoryRemovalTarget struct {
	ContentID string `json:"content_id" minLength:"1" doc:"A movie, ebook, series, season or episode" example:"episode:heat-s01e01"`
	Scope     string `json:"scope,omitempty" enum:"item,show" doc:"item (default) removes the target itself, a series or season expanding to its episodes; show widens a season or episode to its whole series" example:"item"`
}

// HistoryRemoveInput is the removeHistoryEntries command.
type HistoryRemoveInput struct {
	Body struct {
		Targets []HistoryRemovalTarget `json:"targets" minItems:"1" doc:"Targets to hide from history"`
	}
}

// opListHistory is the operation id; the cursor scope is bound to it.
const opListHistory = "listHistory"

// historyPosition is the cursor payload: the keyset (watched_at, id) of the
// last row the previous page emitted, in the store's own string form. The
// next page resumes strictly after it in (watched_at DESC, id DESC), so a
// watch recorded or hidden mid-pagination neither repeats nor skips a row,
// and equal timestamps are ordered by the unique row id.
type historyPosition struct {
	WatchedAt string `json:"w"`
	ID        string `json:"i"`
}

func registerHistory(reg *Registry) {
	cursors := NewCursors(reg.deps.CursorSecret)
	Register(reg, Operation{
		Operation: humaOp(http.MethodGet, Prefix+"/history", opListHistory, "history",
			"List the acting profile's watch history as catalog cards, most recent watch first."),
		Class:         ClassProfileScoped,
		ServiceBacked: true,
	}, func(ctx context.Context, in *HistoryListInput) (*HistoryCollectionOutput, error) {
		return reg.listHistory(ctx, cursors, in)
	})

	Register(reg, Operation{
		Operation: humaOp(http.MethodGet, Prefix+"/history/watches", "listHistoryWatches", "history",
			"List each watch from the last few days, most recent first: the episode or film watched, its show, and when."),
		Class:         ClassProfileScoped,
		ServiceBacked: true,
	}, reg.listHistoryWatches)

	remove := humaOp(http.MethodPost, Prefix+"/history/remove", "removeHistoryEntries", "history",
		"Hide the targets' watches from the acting profile's history; the server chooses the cutoff when this request runs.")
	remove.DefaultStatus = http.StatusNoContent
	Register(reg, Operation{Operation: remove, Class: ClassProfileScoped, ServiceBacked: true, RetrySafety: RetrySafetyNonRetryable}, reg.removeHistoryEntries)
}

// listHistory pages the same rows v1 GET /history does, by keyset rather
// than v1's offset; the cursor carries the key of the last row emitted.
func (reg *Registry) listHistory(ctx context.Context, cursors *Cursors, in *HistoryListInput) (*HistoryCollectionOutput, error) {
	if reg.deps.History == nil {
		return nil, unavailable("history")
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return nil, p
	}
	scope := CursorScope{
		OperationID: opListHistory,
		// The cards are access-filtered, so the cursor dies with the viewer
		// policy it was minted under.
		Security:   strconv.Itoa(userID) + "/" + profileID + "/" + viewerScopeDigest(ctx),
		Filter:     in.ImageSize + in.widthsCursorFilter(),
		Sort:       "-watched_at,-id",
		Tiebreaker: "id",
	}
	var after *userstore.HistoryKey
	if in.Cursor != "" {
		var pos historyPosition
		if p := cursors.Decode(scope, in.Cursor, &pos); p != nil {
			return nil, p
		}
		after = &userstore.HistoryKey{WatchedAt: pos.WatchedAt, ID: pos.ID}
	}
	// Bound work to one raw window. The renderer keeps only global latest
	// display witnesses; a page may therefore be short or empty. Advance on
	// the last scanned row, and let next_cursor (not item count) signal more.
	entries, err := reg.deps.History.HistoryPage(ctx, userID, profileID, after, in.Limit+1)
	if err != nil {
		return nil, serviceProblem(err)
	}
	next := ""
	if len(entries) > in.Limit {
		entries = entries[:in.Limit]
		last := entries[len(entries)-1]
		next, err = cursors.Encode(scope, historyPosition{WatchedAt: last.WatchedAt, ID: last.ID})
		if err != nil {
			return nil, NewProblem(TypeInternalError, "An unexpected error occurred.")
		}
	}
	cards, err := reg.deps.History.HistoryPageCards(ctx, sectionViewer(ctx, in.Artwork(), SectionClient{}), entries)
	if err != nil {
		return nil, serviceProblem(err)
	}
	items := make([]HistoryCard, 0, len(cards))
	sel := reg.ratingSelection(ctx)
	for _, c := range cards {
		card, p := historyCardOf(c, sel)
		if p != nil {
			return nil, p
		}
		items = append(items, card)
	}
	return &HistoryCollectionOutput{Body: HistoryCollection{Collection: Paginated(items, next)}}, nil
}

func historyCardOf(v handlers.HistoryCardView, sel ratingsources.Selection) (HistoryCard, *Problem) {
	watched, p := storeInstant(v.Entry.WatchedAt)
	if p != nil {
		return HistoryCard{}, p
	}
	return HistoryCard{
		CatalogItem: catalogItemOfListing(v.Item, sel),
		Watch: HistoryWatch{
			MediaItemID:     ID(v.Entry.MediaItemID),
			WatchedAt:       watched,
			DurationSeconds: v.Entry.DurationSeconds,
			Completed:       v.Entry.Completed,
			Source:          string(v.Entry.Source),
		},
	}, nil
}

// removeHistoryEntries runs the same command as v1 POST /history/remove.
func (reg *Registry) removeHistoryEntries(ctx context.Context, in *HistoryRemoveInput) (*struct{}, error) {
	if reg.deps.History == nil {
		return nil, unavailable("history")
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return nil, p
	}
	targets := make([]handlers.HistoryRemovalTarget, 0, len(in.Body.Targets))
	for _, t := range in.Body.Targets {
		targets = append(targets, handlers.HistoryRemovalTarget{ContentID: t.ContentID, Scope: t.Scope})
	}
	if err := reg.deps.History.RemoveHistory(ctx, userID, profileID, handlers.AccessFilterFromContext(ctx, ""), targets); err != nil {
		return nil, fieldProblem(err)
	}
	return nil, nil
}

// fieldProblem maps a seam's decision onto problem types: a rejected member
// is a validation failure naming it; the rest follow the status.
func fieldProblem(err error) *Problem {
	apiErr, ok := err.(*handlers.APIError) //nolint:errorlint // the seams return the value directly
	if ok && apiErr.Field != "" {
		return NewProblem(TypeValidationFailed, "The request did not pass validation; see errors.").
			WithErrors(ProblemError{Location: locationBody + "." + apiErr.Field, Code: codeInvalid, Detail: apiErr.Message})
	}
	return serviceProblem(err)
}

// HistoryService is the slice of *handlers.PersonalDataHandler the history
// operations use.
type HistoryService interface {
	HistoryPage(ctx context.Context, userID int, profileID string, after *userstore.HistoryKey, limit int) ([]userstore.WatchHistoryEntry, error)
	HistoryPageCards(ctx context.Context, viewer handlers.SectionViewer, entries []userstore.WatchHistoryEntry) ([]handlers.HistoryCardView, error)
	HistoryWatchViews(ctx context.Context, viewer handlers.SectionViewer, entries []userstore.WatchHistoryEntry) ([]handlers.HistoryWatchView, error)
	RemoveHistory(ctx context.Context, userID int, profileID string, filter catalogpkg.AccessFilter, targets []handlers.HistoryRemovalTarget) error
}

// HistoryWatchesInput is the listHistoryWatches query.
type HistoryWatchesInput struct {
	ArtworkQuery
	Days int `query:"days" minimum:"1" maximum:"90" default:"7" doc:"How many days back to list" example:"7"`
}

// HistoryWatchRow is one watch in a watch list.
type HistoryWatchRow struct {
	ContentID     ID      `json:"content_id" doc:"The episode or film watched" example:"episode:heat-s01e01"`
	Type          string  `json:"type" doc:"episode or movie" example:"episode"`
	Title         string  `json:"title" doc:"Episode or film title" example:"Pilot"`
	SeriesID      ID      `json:"series_id,omitempty" doc:"The episode's series" example:"series:heat"`
	SeriesTitle   string  `json:"series_title,omitempty" doc:"The episode's show" example:"Heat"`
	SeasonNumber  *int    `json:"season_number,omitempty" example:"1"`
	EpisodeNumber *int    `json:"episode_number,omitempty" example:"1"`
	PosterURL     string  `json:"poster_url,omitempty" doc:"The show's (or film's) poster"`
	WatchedAt     Instant `json:"watched_at" doc:"When the watch was recorded" example:"2026-01-02T03:04:05.000Z"`
	Completed     bool    `json:"completed" doc:"Whether it was watched to the end" example:"true"`
}

// HistoryWatchesOutput is the listHistoryWatches response.
type HistoryWatchesOutput struct {
	Body struct {
		Items []HistoryWatchRow `json:"items"`
	}
}

// maxHistoryWatchRows bounds one watch list (about 90 days of heavy viewing).
const maxHistoryWatchRows = 1000

// listHistoryWatches pages history newest first until the window closes.
func (reg *Registry) listHistoryWatches(ctx context.Context, in *HistoryWatchesInput) (*HistoryWatchesOutput, error) {
	if reg.deps.History == nil {
		return nil, unavailable("history")
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return nil, p
	}
	days := in.Days
	if days <= 0 {
		days = 7
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var entries []userstore.WatchHistoryEntry
	var after *userstore.HistoryKey
	for len(entries) < maxHistoryWatchRows {
		page, err := reg.deps.History.HistoryPage(ctx, userID, profileID, after, 200)
		if err != nil {
			return nil, serviceProblem(err)
		}
		done := len(page) < 200
		for _, e := range page {
			at, perr := time.Parse(time.RFC3339Nano, e.WatchedAt)
			if perr == nil && at.Before(cutoff) {
				done = true
				break
			}
			entries = append(entries, e)
		}
		if done || len(page) == 0 {
			break
		}
		last := page[len(page)-1]
		after = &userstore.HistoryKey{WatchedAt: last.WatchedAt, ID: last.ID}
	}
	views, err := reg.deps.History.HistoryWatchViews(ctx, sectionViewer(ctx, in.Artwork()), entries)
	if err != nil {
		return nil, serviceProblem(err)
	}
	out := &HistoryWatchesOutput{}
	out.Body.Items = make([]HistoryWatchRow, 0, len(views))
	for _, v := range views {
		watched, p := storeInstant(v.Entry.WatchedAt)
		if p != nil {
			return nil, p
		}
		out.Body.Items = append(out.Body.Items, HistoryWatchRow{
			ContentID: ID(v.ContentID), Type: v.Type, Title: v.Title,
			SeriesID: ID(v.SeriesID), SeriesTitle: v.SeriesTitle,
			SeasonNumber: v.SeasonNumber, EpisodeNumber: v.EpisodeNumber,
			PosterURL: v.PosterURL, WatchedAt: watched, Completed: v.Entry.Completed,
		})
	}
	return out, nil
}
