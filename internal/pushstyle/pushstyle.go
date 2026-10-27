// Package pushstyle renders the text of phone and browser push notifications
// from admin templates, and decides when a push waits: overnight quiet hours,
// and a short window that gathers several episodes of one show into one push.
package pushstyle

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cbroglie/mustache"
)

// Image choices for a rich notification.
const (
	ImagePoster   = "poster"
	ImageBackdrop = "backdrop"
	ImageNone     = "none"
)

// Kinds of push a template renders.
const (
	KindEpisode = "episode"
	KindMovie   = "movie"
	KindGroup   = "group"
)

// Settings are the admin's push notification choices. They apply to every
// profile.
type Settings struct {
	EpisodeTitle string `json:"episode_title"`
	EpisodeBody  string `json:"episode_body"`
	MovieTitle   string `json:"movie_title"`
	MovieBody    string `json:"movie_body"`
	GroupTitle   string `json:"group_title"`
	GroupBody    string `json:"group_body"`
	// Image is poster, backdrop or none.
	Image string `json:"image"`
	// CollapsedImage and ExpandedImage show the image in the collapsed
	// notification and in its expanded view.
	CollapsedImage bool `json:"collapsed_image"`
	ExpandedImage  bool `json:"expanded_image"`
	// Expanded offers an expanded view at all. Android opens the newest
	// notification expanded when it has one; off keeps every push collapsed.
	Expanded bool `json:"expanded"`
	// Synopsis shows the episode's or movie's synopsis when expanded, cut to
	// SynopsisMax characters.
	Synopsis    bool `json:"synopsis"`
	SynopsisMax int  `json:"synopsis_max"`
	// BatchMinutes holds an episode's push this long so later episodes of
	// the same show join it; 0 sends each at once.
	BatchMinutes int `json:"batch_minutes"`
	// Quiet hours hold pushes from QuietStart to QuietEnd (HH:MM, in
	// Timezone, or the server's zone when empty) and send them at the end.
	QuietEnabled bool   `json:"quiet_enabled"`
	QuietStart   string `json:"quiet_start"`
	QuietEnd     string `json:"quiet_end"`
	Timezone     string `json:"timezone"`
}

// DefaultSettings reproduce the built-in notification text.
func DefaultSettings() Settings {
	return Settings{
		EpisodeTitle:   "{{series}}",
		EpisodeBody:    "New episode · {{code}}{{#episode_title}} · {{episode_title}}{{/episode_title}}",
		MovieTitle:     "{{title}}{{#year}} ({{year}}){{/year}}",
		MovieBody:      "New movie",
		GroupTitle:     "{{series}}",
		GroupBody:      "{{count}} new episodes · {{codes}}",
		Image:          ImagePoster,
		CollapsedImage: true,
		ExpandedImage:  true,
		Expanded:       true,
		Synopsis:       true,
		SynopsisMax:    300,
		BatchMinutes:   5,
		QuietStart:     "23:00",
		QuietEnd:       "07:00",
	}
}

// Limits on the numbers an admin enters.
const (
	MaxTemplateLen  = 500
	MaxSynopsis     = 1000
	MaxBatchMinutes = 240
)

// FieldError names the setting that failed validation.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Normalize fills blanks with defaults and checks every field.
func (s Settings) Normalize() (Settings, error) {
	d := DefaultSettings()
	templates := []struct {
		field string
		v     *string
		def   string
	}{
		{"episode_title", &s.EpisodeTitle, d.EpisodeTitle},
		{"episode_body", &s.EpisodeBody, d.EpisodeBody},
		{"movie_title", &s.MovieTitle, d.MovieTitle},
		{"movie_body", &s.MovieBody, d.MovieBody},
		{"group_title", &s.GroupTitle, d.GroupTitle},
		{"group_body", &s.GroupBody, d.GroupBody},
	}
	for _, t := range templates {
		*t.v = strings.TrimSpace(*t.v)
		if *t.v == "" {
			*t.v = t.def
		}
		if len(*t.v) > MaxTemplateLen {
			return s, &FieldError{t.field, fmt.Sprintf("must be at most %d characters", MaxTemplateLen)}
		}
		if _, err := mustache.ParseString(*t.v); err != nil {
			return s, &FieldError{t.field, err.Error()}
		}
	}
	switch s.Image {
	case ImagePoster, ImageBackdrop, ImageNone:
	case "":
		s.Image = d.Image
	default:
		return s, &FieldError{"image", "must be poster, backdrop or none"}
	}
	if s.SynopsisMax <= 0 {
		s.SynopsisMax = d.SynopsisMax
	}
	if s.SynopsisMax > MaxSynopsis {
		return s, &FieldError{"synopsis_max", fmt.Sprintf("must be at most %d", MaxSynopsis)}
	}
	if s.BatchMinutes < 0 || s.BatchMinutes > MaxBatchMinutes {
		return s, &FieldError{"batch_minutes", fmt.Sprintf("must be between 0 and %d", MaxBatchMinutes)}
	}
	if s.QuietStart == "" {
		s.QuietStart = d.QuietStart
	}
	if s.QuietEnd == "" {
		s.QuietEnd = d.QuietEnd
	}
	if _, err := clock(s.QuietStart); err != nil {
		return s, &FieldError{"quiet_start", err.Error()}
	}
	if _, err := clock(s.QuietEnd); err != nil {
		return s, &FieldError{"quiet_end", err.Error()}
	}
	s.Timezone = strings.TrimSpace(s.Timezone)
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return s, &FieldError{"timezone", "unknown time zone"}
		}
	}
	return s, nil
}

// clock parses HH:MM into minutes after midnight.
func clock(v string) (int, error) {
	h, m, ok := strings.Cut(v, ":")
	hours, err1 := strconv.Atoi(h)
	minutes, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hours < 0 || hours > 23 || minutes < 0 || minutes > 59 {
		return 0, fmt.Errorf("must be a time such as 23:00")
	}
	return hours*60 + minutes, nil
}

func (s Settings) location() *time.Location {
	if s.Timezone != "" {
		if loc, err := time.LoadLocation(s.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// QuietUntil reports when quiet hours covering now end. ok is false outside
// quiet hours, or when they are off or start and end at the same time.
func (s Settings) QuietUntil(now time.Time) (until time.Time, ok bool) {
	if !s.QuietEnabled {
		return time.Time{}, false
	}
	start, err1 := clock(s.QuietStart)
	end, err2 := clock(s.QuietEnd)
	if err1 != nil || err2 != nil || start == end {
		return time.Time{}, false
	}
	local := now.In(s.location())
	minute := local.Hour()*60 + local.Minute()
	inside := (start < end && minute >= start && minute < end) || (start > end && (minute >= start || minute < end))
	if !inside {
		return time.Time{}, false
	}
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	until = midnight.Add(time.Duration(end) * time.Minute)
	if !until.After(local) {
		until = until.AddDate(0, 0, 1)
	}
	return until, true
}

// Episode is one episode as the templates see it.
type Episode struct {
	Season      *int
	Number      *int
	Title       string
	Overview    string
	SeriesTitle string
	Year        int
}

// Code is the S01E02 tag, or empty without numbers.
func (e Episode) Code() string {
	if e.Season == nil || e.Number == nil {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", *e.Season, *e.Number)
}

// Item is what one push is about: an episode, a movie, or several episodes
// of one show.
type Item struct {
	Kind     string
	Title    string // series or movie title
	Year     int
	Overview string
	Episodes []Episode // one for an episode push, several for a group
}

// Rendered is a push's text. Title and Body are the HTML subset templates
// use; Synopsis is plain text.
type Rendered struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Synopsis string `json:"synopsis,omitempty"`
}

// Vars are the template variables for item.
func Vars(item Item) map[string]any {
	vars := map[string]any{
		"series": item.Title,
		"title":  item.Title,
		"year":   yearString(item.Year),
	}
	eps := append([]Episode(nil), item.Episodes...)
	sort.SliceStable(eps, func(i, j int) bool { return episodeOrder(eps[i]) < episodeOrder(eps[j]) })
	list := make([]map[string]any, 0, len(eps))
	codes := make([]string, 0, len(eps))
	for _, e := range eps {
		code := e.Code()
		if code != "" {
			codes = append(codes, code)
		}
		list = append(list, map[string]any{"code": code, "episode_title": e.Title, "season": intString(e.Season), "episode": intString(e.Number)})
	}
	vars["episodes"] = list
	vars["count"] = strconv.Itoa(len(eps))
	vars["codes"] = joinCodes(codes)
	if len(eps) > 0 {
		first := eps[0]
		vars["code"] = first.Code()
		vars["season"] = intString(first.Season)
		vars["episode"] = intString(first.Number)
		vars["episode_title"] = first.Title
	}
	vars["overview"] = item.Overview
	return vars
}

func episodeOrder(e Episode) int {
	if e.Season == nil || e.Number == nil {
		return 1 << 30
	}
	return *e.Season*10000 + *e.Number
}

// joinCodes lists codes, shortening a run inside one season to its ends:
// "S01E03–E05, S02E01".
func joinCodes(codes []string) string {
	if len(codes) <= 2 {
		return strings.Join(codes, ", ")
	}
	first, last := codes[0], codes[len(codes)-1]
	if first[:3] == last[:3] {
		return first + "–" + last[3:]
	}
	return first + " … " + last
}

func yearString(y int) string {
	if y <= 0 {
		return ""
	}
	return strconv.Itoa(y)
}

func intString(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// Render renders item with the settings' templates. A template that fails
// to render leaves its part to the caller's built-in text, so the result
// may hold empty fields.
func Render(s Settings, item Item) (Rendered, error) {
	titleTmpl, bodyTmpl := s.EpisodeTitle, s.EpisodeBody
	switch item.Kind {
	case KindMovie:
		titleTmpl, bodyTmpl = s.MovieTitle, s.MovieBody
	case KindGroup:
		titleTmpl, bodyTmpl = s.GroupTitle, s.GroupBody
	}
	vars := Vars(item)
	title, err := render(titleTmpl, vars)
	if err != nil {
		return Rendered{}, fmt.Errorf("%s title template: %w", item.Kind, err)
	}
	body, err := render(bodyTmpl, vars)
	if err != nil {
		return Rendered{}, fmt.Errorf("%s text template: %w", item.Kind, err)
	}
	out := Rendered{Title: title, Body: body}
	if s.Synopsis && item.Kind != KindGroup {
		out.Synopsis = Truncate(strings.TrimSpace(item.Overview), s.SynopsisMax)
	}
	return out, nil
}

// render fills a template. Templates may format text with a small HTML
// subset (<b>, <i>, <u>, <s>, <font color>), so values are HTML-escaped and
// the phone decodes the result as HTML.
func render(src string, vars map[string]any) (string, error) {
	out, err := mustache.Render(src, vars)
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(out), " "), nil
}

// Truncate cuts s to at most limit characters at a word boundary, adding an
// ellipsis.
func Truncate(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:limit])
	if i := strings.LastIndexAny(cut, " \n\t"); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-–") + "…"
}

// Sample is the item the admin page previews kind with.
func Sample(kind string) Item {
	one, two, five, six, seven := 1, 2, 5, 6, 7
	switch kind {
	case KindMovie:
		return Item{Kind: KindMovie, Title: "Dune: Part Two", Year: 2024, Overview: "Paul Atreides unites with Chani and the Fremen while on a path of revenge against the conspirators who destroyed his family."}
	case KindGroup:
		return Item{Kind: KindGroup, Title: "Severance", Year: 2022, Episodes: []Episode{
			{Season: &two, Number: &five, Title: "Trojan's Horse"},
			{Season: &two, Number: &six, Title: "Attila"},
			{Season: &two, Number: &seven, Title: "Chikhai Bardo"},
		}}
	default:
		return Item{Kind: KindEpisode, Title: "Severance", Year: 2022, Overview: "Mark questions his reality after a mysterious colleague appears at Lumon.", Episodes: []Episode{
			{Season: &two, Number: &one, Title: "Hello, Ms. Cobel"},
		}}
	}
}
