package notifications

import (
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/titleutil"
)

// NotificationDisplay is the compact, user-facing display metadata clients use
// when they need a native notification title/body without fetching a full inbox
// page.
type NotificationDisplay struct {
	DeliveryID string `json:"delivery_id"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	Category   string `json:"category"`
	URL        string `json:"url"`
	// ImageURL is the wide hero image (the title's backdrop) and
	// ThumbnailURL its poster, for rich notifications. Both are absolute,
	// short-lived artwork links set by System.DisplayForRow; absent when the
	// title has no artwork or no image resolver is configured.
	ImageURL     string `json:"image_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	// Actions are the buttons a rich notification offers, in display order.
	Actions []NotificationAction `json:"actions,omitempty"`
}

// Notification action IDs. Clients show the actions they implement and skip
// unknown IDs.
const (
	// NotificationActionPlay starts playback of the delivery's episode or
	// movie at URL (the web player route).
	NotificationActionPlay = "play"
	// NotificationActionMuteSeries mutes further notifications for the
	// series: PUT /api/v2/notifications/series/{series_id}/mute.
	NotificationActionMuteSeries = "mute_series"
	// NotificationActionMarkRead marks the delivery read.
	NotificationActionMarkRead = "mark_read"
)

// NotificationAction is one button on a rich notification. URL is the
// in-app route the action opens; SeriesID names the series a mute_series
// action mutes.
type NotificationAction struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	SeriesID string `json:"series_id,omitempty"`
}

// notificationActions returns the buttons for a delivery: play for
// something playable, mute for a series episode, and mark read.
func notificationActions(row DeliveryRow) []NotificationAction {
	var actions []NotificationAction
	switch row.Type {
	case DeliveryTypeEpisodeAvailable:
		if row.EpisodeID != nil && *row.EpisodeID != "" {
			actions = append(actions, NotificationAction{ID: NotificationActionPlay, Title: "Play", URL: "/watch/" + *row.EpisodeID})
		}
		if row.SeriesID != nil && *row.SeriesID != "" {
			actions = append(actions, NotificationAction{ID: NotificationActionMuteSeries, Title: "Mute show", SeriesID: *row.SeriesID})
		}
	case DeliveryTypeMovieAvailable, DeliveryTypeRequestFulfilled:
		if row.SeriesID != nil && *row.SeriesID != "" && (row.Type == DeliveryTypeMovieAvailable || row.MediaType == "movie") {
			actions = append(actions, NotificationAction{ID: NotificationActionPlay, Title: "Play", URL: "/watch/" + *row.SeriesID})
		}
	}
	if row.ID != "" {
		actions = append(actions, NotificationAction{ID: NotificationActionMarkRead, Title: "Mark as read"})
	}
	return actions
}

// BuildNotificationDisplay renders a delivery into native-notification display
// metadata. Keep this in sync with inbox/websocket semantics by only deriving
// from DeliveryRow.
func BuildNotificationDisplay(row DeliveryRow) NotificationDisplay {
	display := NotificationDisplay{
		DeliveryID: row.ID,
		Title:      genericNotificationTitle,
		Category:   "notification",
		URL:        "/notifications",
	}
	switch row.Type {
	case DeliveryTypeEpisodeAvailable:
		display.Category = "episode_available"
		display.Title = episodeDisplayTitle(row)
		display.Body = episodeDisplayBody(row)
		if row.SeriesID != nil && *row.SeriesID != "" {
			display.ThreadID = "series:" + *row.SeriesID
		}
		if row.EpisodeID != nil && *row.EpisodeID != "" {
			display.URL = "/item/" + *row.EpisodeID
		}
	case DeliveryTypeMovieAvailable:
		display.Category = "movie_available"
		display.Title = "New movie available"
		if row.SeriesTitle != "" {
			display.Title = titleWithYear(row.SeriesTitle, row.Year)
			display.Body = "New movie"
		}
		if row.SeriesID != nil && *row.SeriesID != "" {
			display.ThreadID = "item:" + *row.SeriesID
			display.URL = "/item/" + *row.SeriesID
		}
	case DeliveryTypeRequestFulfilled:
		flags := parseRequestFlags(row.ReasonFlags)
		display.Category = "request_fulfilled"
		display.Title = "Your request is now available"
		display.Body = "Your media request has arrived in the library."
		if flags.Follower {
			display.Title = followedTitleAvailable
			display.Body = "A title you asked to hear about has arrived in the library."
		}
		if row.SeriesTitle != "" {
			display.Title = row.SeriesTitle + " is now available"
		}
		if flags.RequestID != "" {
			display.ThreadID = "request:" + flags.RequestID
		} else if row.SeriesID != nil && *row.SeriesID != "" {
			display.ThreadID = "item:" + *row.SeriesID
		}
		if row.SeriesID != nil && *row.SeriesID != "" {
			display.URL = "/item/" + *row.SeriesID
		}
	case DeliveryTypeRequestApproved:
		flags := parseRequestFlags(row.ReasonFlags)
		display.Category = "request_approved"
		display.Title = "Your request was approved"
		if flags.Title != "" {
			display.Title = flags.Title + " was approved"
		}
		display.Body = "Your media request was approved."
		display.ThreadID = requestThreadID(flags)
	case DeliveryTypeRequestDeclined:
		flags := parseRequestFlags(row.ReasonFlags)
		display.Category = "request_declined"
		display.Title = "Your request was declined"
		if flags.Title != "" {
			display.Title = flags.Title + " was declined"
		}
		display.Body = "Your media request was declined."
		if flags.Reason != "" {
			// Admin-typed free text with no upstream length cap; keep native
			// notification bodies bounded.
			display.Body = "Reason: " + truncateDisplayText(flags.Reason, displayBodyMaxLen)
		}
		display.ThreadID = requestThreadID(flags)
	case DeliveryTypeWebhookAutoDisabled:
		display.Category = "webhook_auto_disabled"
		display.Title = "A webhook stopped working"
		display.Body = "Open notification settings to fix it."
		display.ThreadID = "settings:notifications"
		display.URL = "/settings/notifications"
	default:
		if row.Type != "" {
			display.Category = strings.ReplaceAll(row.Type, ".", "_")
		}
	}
	display.Actions = notificationActions(row)
	return display
}

// episodeDisplayTitle leads with the series name alone: native notifications
// give the title one line, and any fixed wording around the name pushes it
// past the cut. The episode details go in the body, which wraps.
func episodeDisplayTitle(row DeliveryRow) string {
	if row.SeriesTitle != "" {
		return row.SeriesTitle
	}
	return "New episode available"
}

// episodeDisplayBody reads "New episode · S02E01 · Hello, Ms. Cobel", dropping
// the parts that are missing. A placeholder title such as "TBA" says nothing
// the episode code doesn't, so it is left out.
func episodeDisplayBody(row DeliveryRow) string {
	parts := make([]string, 0, 3)
	if row.SeriesTitle != "" {
		parts = append(parts, "New episode")
	}
	if code := episodeDisplayCode(row); code != "" {
		parts = append(parts, code)
	}
	if title := strings.TrimSpace(row.EpisodeTitle); title != "" && !titleutil.IsProvisionalEpisodeTitle(title) {
		parts = append(parts, title)
	}
	return strings.Join(parts, " · ")
}

func episodeDisplayCode(row DeliveryRow) string {
	if row.SeasonNumber != nil && row.EpisodeNumber != nil {
		return fmt.Sprintf("S%02dE%02d", *row.SeasonNumber, *row.EpisodeNumber)
	}
	return ""
}

// displayBodyMaxLen bounds free-text notification bodies.
const displayBodyMaxLen = 240

func truncateDisplayText(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func requestThreadID(flags RequestFlags) string {
	if flags.RequestID == "" {
		return ""
	}
	return "request:" + flags.RequestID
}
