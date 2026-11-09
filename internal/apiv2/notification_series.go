package apiv2

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/imagesize"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// The notified-series list: every series the acting profile follows or
// muted. A profile hears about new episodes of the series it follows, and of
// everything else too when it asked for everything new, except the series it
// muted.

const (
	opListNotificationSeries     = "listNotificationSeries"
	opMuteNotificationSeries     = "muteNotificationSeries"
	opUnmuteNotificationSeries   = "unmuteNotificationSeries"
	opFollowNotificationSeries   = "followNotificationSeries"
	opUnfollowNotificationSeries = "unfollowNotificationSeries"
)

// The listNotificationSeries sort fields besides the default title.
const (
	notificationSeriesSortYear          = "year"
	notificationSeriesSortAddedAt       = "added_at"
	notificationSeriesSortLatestEpisode = "latest_episode_added"
)

// The listNotificationSeries status filters besides the default "all".
const (
	notificationSeriesFollowed = "followed"
	notificationSeriesMuted    = "muted"
)

// NotificationSeriesInput is the listNotificationSeries query.
type NotificationSeriesInput struct {
	ArtworkQuery
	LimitParam
	Cursor string `query:"cursor" doc:"Opaque cursor from page.next_cursor" example:"eyJ0IjoiYmVhciJ9"`
	Status string `query:"status" default:"all" enum:"all,followed,muted" doc:"followed keeps the series the profile follows; muted keeps the ones it muted"`
	Q      string `query:"q" maxLength:"200" doc:"Keeps the series whose title or sort title contains this text, case-insensitively" example:"bear"`
	Sort   string `query:"sort" default:"title" enum:"title,-title,year,-year,added_at,-added_at,latest_episode_added,-latest_episode_added" doc:"One field, descending with a leading '-'. added_at is when the series joined the catalog; latest_episode_added when it last gained an episode. A series without a year or an episode sorts last either way; ties order by title."`
}

// NotificationSeries is one series on the list.
type NotificationSeries struct {
	Item     CatalogItem `json:"item" doc:"The series as a catalog card"`
	Followed bool        `json:"followed" doc:"The profile follows the series: its new episodes notify the profile"`
	Muted    bool        `json:"muted" doc:"The profile muted the series: the profile hears nothing about it, followed or not, until it unmutes"`
}

// NotificationSeriesCollection is the listNotificationSeries envelope.
type NotificationSeriesCollection struct {
	Collection[NotificationSeries]
}

// NotificationSeriesOutput is the listNotificationSeries response.
type NotificationSeriesOutput struct {
	Body NotificationSeriesCollection
}

// notificationSeriesPosition is the keyset of the last series a page
// emitted: every field an order compares, so the next page resumes strictly
// after it in any sort, even once that series has left the list.
type notificationSeriesPosition struct {
	SortKey  string     `json:"t"`
	Year     int        `json:"y,omitempty"`
	AddedAt  time.Time  `json:"a"`
	Latest   *time.Time `json:"l,omitempty"`
	SeriesID string     `json:"s"`
}

func notificationSeriesPositionOf(s notifications.NotifiedSeries) notificationSeriesPosition {
	return notificationSeriesPosition{SortKey: s.SortKey, Year: s.Year, AddedAt: s.AddedAt, Latest: s.LatestEpisodeAddedAt, SeriesID: s.SeriesID}
}

func (p notificationSeriesPosition) series() notifications.NotifiedSeries {
	return notifications.NotifiedSeries{SeriesID: p.SeriesID, SeriesSortFields: notifications.SeriesSortFields{SortKey: p.SortKey, Year: p.Year, AddedAt: p.AddedAt, LatestEpisodeAddedAt: p.Latest}}
}

// notificationSeriesOrder compares series in a listNotificationSeries sort:
// the field, then sort title and item ID ascending.
func notificationSeriesOrder(sort string) func(a, b notifications.NotifiedSeries) int {
	field, desc := strings.CutPrefix(sort, "-")
	return func(a, b notifications.NotifiedSeries) int {
		var primary int
		switch field {
		case notificationSeriesSortYear:
			primary = orderMissingLast(a.Year == 0, b.Year == 0, cmp.Compare(a.Year, b.Year), desc)
		case notificationSeriesSortAddedAt:
			primary = orderMissingLast(false, false, a.AddedAt.Compare(b.AddedAt), desc)
		case notificationSeriesSortLatestEpisode:
			aMissing, bMissing := a.LatestEpisodeAddedAt == nil, b.LatestEpisodeAddedAt == nil
			c := 0
			if !aMissing && !bMissing {
				c = a.LatestEpisodeAddedAt.Compare(*b.LatestEpisodeAddedAt)
			}
			primary = orderMissingLast(aMissing, bMissing, c, desc)
		default:
			primary = orderMissingLast(false, false, strings.Compare(a.SortKey, b.SortKey), desc)
		}
		return cmp.Or(primary, strings.Compare(a.SortKey, b.SortKey), strings.Compare(a.SeriesID, b.SeriesID))
	}
}

// orderMissingLast applies a direction to c, keeping missing values last.
func orderMissingLast(aMissing, bMissing bool, c int, desc bool) int {
	switch {
	case aMissing && bMissing:
		return 0
	case aMissing:
		return 1
	case bMissing:
		return -1
	case desc:
		return -c
	}
	return c
}

// notificationSeriesMatch reports whether a series passes the query's status
// and search filters.
func notificationSeriesMatch(in *NotificationSeriesInput, query string) func(notifications.NotifiedSeries) bool {
	return func(s notifications.NotifiedSeries) bool {
		switch {
		case in.Status == notificationSeriesFollowed && !s.Followed,
			in.Status == notificationSeriesMuted && !s.Muted:
			return false
		}
		return query == "" || strings.Contains(strings.ToLower(s.Title), query) || strings.Contains(s.SortKey, query)
	}
}

func registerNotificationSeries(reg *Registry) {
	cursors := NewCursors(reg.deps.CursorSecret)
	op := notificationOperation(http.MethodGet, "/series", opListNotificationSeries)
	op.Summary = "List the series the acting profile follows or muted."
	op.Description = "Every series the profile follows and every series it muted, filtered and sorted as asked (by title by default). Each says whether the profile follows it and whether it muted it. Series the viewer may not see are omitted."
	Register(reg, op, func(ctx context.Context, in *NotificationSeriesInput) (*NotificationSeriesOutput, error) {
		return reg.listNotificationSeries(ctx, cursors, in)
	})

	mute := notificationOperation(http.MethodPut, "/series/{series_id}/mute", opMuteNotificationSeries)
	mute.DefaultStatus = http.StatusNoContent
	mute.Summary = "Mute new-episode notifications for one series."
	mute.Description = "The acting profile gets no new-episode notification for the series on any channel, whether or not it follows it or asked for everything new, until it unmutes. Following a series lifts its mute. Other profiles are unaffected. Muting a series already muted succeeds and changes nothing. A series the viewer may not see, or an item that is not a series, is 404."
	Register(reg, mute, reg.muteNotificationSeries)

	unmute := notificationOperation(http.MethodDelete, "/series/{series_id}/mute", opUnmuteNotificationSeries)
	unmute.DefaultStatus = http.StatusNoContent
	unmute.Summary = "Unmute new-episode notifications for one series."
	unmute.Description = "The series notifies the acting profile again. The mute applies when notification delivery evaluates a new episode, normally within a minute of it arriving: an episode evaluated while the series was muted is not announced later, and one still waiting to be evaluated when the profile unmutes is announced. Unmuting a series that is not muted succeeds and changes nothing. No access check runs, so a profile can lift a mute on a series it can no longer see."
	Register(reg, unmute, reg.unmuteNotificationSeries)

	follow := notificationOperation(http.MethodPut, "/series/{series_id}/follow", opFollowNotificationSeries)
	follow.DefaultStatus = http.StatusNoContent
	follow.Summary = "Follow one series for new-episode notifications."
	follow.Description = "The acting profile gets a notification for each new episode of the series, whether or not it asked for everything new. Following lifts any mute of the series. Following a series already followed succeeds and changes nothing. A series the viewer may not see, or an item that is not a series, is 404."
	Register(reg, follow, reg.followNotificationSeries)

	unfollow := notificationOperation(http.MethodDelete, "/series/{series_id}/follow", opUnfollowNotificationSeries)
	unfollow.DefaultStatus = http.StatusNoContent
	unfollow.Summary = "Stop following one series."
	unfollow.Description = "The series no longer notifies the acting profile unless it asked for everything new, in which case it notifies it like any other series. Unfollowing a series that is not followed succeeds and changes nothing. No access check runs, so a profile can unfollow a series it can no longer see."
	Register(reg, unfollow, reg.unfollowNotificationSeries)
}

// NotificationSeriesMuteInput names the series to mute, unmute, follow or unfollow.
type NotificationSeriesMuteInput struct {
	SeriesID ID `path:"series_id" minLength:"1" doc:"Catalog item identifier of the series" example:"series-tvdb-81189"`
}

func (reg *Registry) muteNotificationSeries(ctx context.Context, in *NotificationSeriesMuteInput) (*struct{}, error) {
	svc, p := reg.notificationInbox()
	if p != nil {
		return nil, p
	}
	if reg.deps.PersonalLists == nil {
		return nil, unavailable("notification series")
	}
	viewer, p := personalListViewer(ctx, imagesize.Request{})
	if p != nil {
		return nil, p
	}
	seriesID := string(in.SeriesID)
	visible, err := reg.deps.PersonalLists.VisibleItemIDs(ctx, viewer, []string{seriesID})
	if err != nil {
		return nil, serviceProblem(err)
	}
	if !visible[seriesID] {
		return nil, NewProblem(TypeNotFound, "Series not found.")
	}
	if err := svc.MuteNotificationSeries(ctx, viewer.UserID, viewer.ProfileID, seriesID); err != nil {
		return nil, serviceProblem(err)
	}
	return nil, nil
}

func (reg *Registry) unmuteNotificationSeries(ctx context.Context, in *NotificationSeriesMuteInput) (*struct{}, error) {
	svc, p := reg.notificationInbox()
	if p != nil {
		return nil, p
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return nil, p
	}
	if err := svc.UnmuteNotificationSeries(ctx, userID, profileID, string(in.SeriesID)); err != nil {
		return nil, serviceProblem(err)
	}
	return nil, nil
}

func (reg *Registry) followNotificationSeries(ctx context.Context, in *NotificationSeriesMuteInput) (*struct{}, error) {
	svc, p := reg.notificationInbox()
	if p != nil {
		return nil, p
	}
	if reg.deps.PersonalLists == nil {
		return nil, unavailable("notification series")
	}
	viewer, p := personalListViewer(ctx, imagesize.Request{})
	if p != nil {
		return nil, p
	}
	seriesID := string(in.SeriesID)
	visible, err := reg.deps.PersonalLists.VisibleItemIDs(ctx, viewer, []string{seriesID})
	if err != nil {
		return nil, serviceProblem(err)
	}
	if !visible[seriesID] {
		return nil, NewProblem(TypeNotFound, "Series not found.")
	}
	if err := svc.FollowNotificationSeries(ctx, viewer.UserID, viewer.ProfileID, seriesID); err != nil {
		return nil, serviceProblem(err)
	}
	return nil, nil
}

func (reg *Registry) unfollowNotificationSeries(ctx context.Context, in *NotificationSeriesMuteInput) (*struct{}, error) {
	svc, p := reg.notificationInbox()
	if p != nil {
		return nil, p
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return nil, p
	}
	if err := svc.UnfollowNotificationSeries(ctx, userID, profileID, string(in.SeriesID)); err != nil {
		return nil, serviceProblem(err)
	}
	return nil, nil
}

func (reg *Registry) listNotificationSeries(ctx context.Context, cursors *Cursors, in *NotificationSeriesInput) (*NotificationSeriesOutput, error) {
	svc, p := reg.notificationInbox()
	if p != nil {
		return nil, p
	}
	if reg.deps.PersonalLists == nil {
		return nil, unavailable("notification series")
	}
	viewer, p := personalListViewer(ctx, in.Artwork())
	if p != nil {
		return nil, p
	}
	query := strings.ToLower(strings.TrimSpace(in.Q))
	scope := CursorScope{
		OperationID: opListNotificationSeries,
		Security:    strconv.Itoa(viewer.UserID) + "/" + viewer.ProfileID + "/" + viewerScopeDigest(ctx),
		Filter:      in.Status + "|" + query,
		Sort:        in.Sort,
		Tiebreaker:  tiebreakerItemID,
	}
	var after *notificationSeriesPosition
	if in.Cursor != "" {
		after = new(notificationSeriesPosition)
		if p := cursors.Decode(scope, in.Cursor, after); p != nil {
			return nil, p
		}
	}
	all, err := svc.ListNotifiedSeries(ctx, viewer.UserID, viewer.ProfileID)
	if err != nil {
		return nil, serviceProblem(err)
	}
	order := notificationSeriesOrder(in.Sort)
	keep := notificationSeriesMatch(in, query)
	candidates := make([]notifications.NotifiedSeries, 0, len(all))
	ids := make([]string, 0, len(all))
	for _, s := range all {
		if keep(s) {
			candidates = append(candidates, s)
			ids = append(ids, s.SeriesID)
		}
	}
	// Pages hold only series the viewer may see, so a cursor never carries a
	// hidden series' sort fields and no page comes back empty with more behind it.
	visible, err := reg.deps.PersonalLists.VisibleItemIDs(ctx, viewer, ids)
	if err != nil {
		return nil, serviceProblem(err)
	}
	matches := slices.DeleteFunc(candidates, func(s notifications.NotifiedSeries) bool { return !visible[s.SeriesID] })
	slices.SortFunc(matches, order)
	start := 0
	if after != nil {
		last := after.series()
		start = len(matches)
		if i := slices.IndexFunc(matches, func(s notifications.NotifiedSeries) bool { return order(s, last) > 0 }); i >= 0 {
			start = i
		}
	}
	// A limit+1 probe decides has_more.
	page := matches[start:min(start+in.Limit+1, len(matches))]
	next := ""
	if len(page) > in.Limit {
		page = page[:in.Limit]
		if next, err = cursors.Encode(scope, notificationSeriesPositionOf(page[len(page)-1])); err != nil {
			return nil, serviceProblem(err)
		}
	}
	pageIDs := make([]string, len(page))
	for i, s := range page {
		pageIDs[i] = s.SeriesID
	}
	cards, err := reg.deps.PersonalLists.ResolveCards(ctx, viewer, pageIDs)
	if err != nil {
		return nil, serviceProblem(err)
	}
	sel := reg.ratingSelection(ctx)
	byID := make(map[string]int, len(cards))
	for i, card := range cards {
		byID[card.ContentID] = i
	}
	items := make([]NotificationSeries, 0, len(cards))
	for _, s := range page {
		if i, ok := byID[s.SeriesID]; ok {
			items = append(items, notificationSeriesOf(s, catalogItemOfListing(cards[i], sel)))
		}
	}
	return &NotificationSeriesOutput{Body: NotificationSeriesCollection{Collection: Paginated(items, next)}}, nil
}

func notificationSeriesOf(s notifications.NotifiedSeries, item CatalogItem) NotificationSeries {
	return NotificationSeries{Item: item, Followed: s.Followed, Muted: s.Muted}
}
