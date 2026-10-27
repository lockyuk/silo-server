package notifications

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/mail"
)

// emailMaxItemsRendered caps how many lines one email renders; the remainder
// collapses into a "+N more" line (the inbox always has everything).
const emailMaxItemsRendered = 30

// untitledSeriesGroup is the group heading for episode rows whose series
// metadata is missing or was deleted.
const untitledSeriesGroup = "New episodes"

// emailContent is one rendered notification email.
type emailContent struct {
	Subject string
	Text    string
	HTML    string
}

// emailSeriesGroup is one series' new episodes, in first-appearance order.
type emailSeriesGroup struct {
	seriesID string
	title    string
	episodes []DeliveryRow
}

// emailItems is the collated, deduplicated content of one email. Accounts
// with several profiles following the same series have one delivery row per
// profile; an email reports the episode once.
type emailItems struct {
	series   []emailSeriesGroup
	episodes int
	movies   []DeliveryRow
	requests []DeliveryRow
	others   []DeliveryRow
}

// collateEmailItems groups and dedupes delivery rows for rendering.
func collateEmailItems(rows []DeliveryRow) emailItems {
	var items emailItems
	seenEpisodes := make(map[string]struct{}, len(rows))
	seenMovies := make(map[string]struct{}, 4)
	seenRequests := make(map[string]struct{}, 4)
	groupIndex := make(map[string]int, 4)

	for _, row := range rows {
		switch row.Type {
		case DeliveryTypeEpisodeAvailable:
			key := row.ID
			if row.EpisodeID != nil && *row.EpisodeID != "" {
				key = *row.EpisodeID
			}
			if _, ok := seenEpisodes[key]; ok {
				continue
			}
			seenEpisodes[key] = struct{}{}
			seriesID := ""
			if row.SeriesID != nil {
				seriesID = *row.SeriesID
			}
			idx, ok := groupIndex[seriesID]
			if !ok {
				idx = len(items.series)
				groupIndex[seriesID] = idx
				title := row.SeriesTitle
				if title == "" {
					title = untitledSeriesGroup
				}
				items.series = append(items.series, emailSeriesGroup{seriesID: seriesID, title: title})
			}
			items.series[idx].episodes = append(items.series[idx].episodes, row)
			items.episodes++
		case DeliveryTypeMovieAvailable:
			key := row.ID
			if row.SeriesID != nil && *row.SeriesID != "" {
				key = *row.SeriesID
			}
			if _, ok := seenMovies[key]; ok {
				continue
			}
			seenMovies[key] = struct{}{}
			items.movies = append(items.movies, row)
		case DeliveryTypeRequestFulfilled, DeliveryTypeRequestApproved, DeliveryTypeRequestDeclined:
			// Key by (type, request): one request may legitimately produce an
			// approved row and a fulfilled row in the same window.
			key := row.ID
			if requestID := parseRequestFlags(row.ReasonFlags).RequestID; requestID != "" {
				key = row.Type + ":" + requestID
			}
			if _, ok := seenRequests[key]; ok {
				continue
			}
			seenRequests[key] = struct{}{}
			items.requests = append(items.requests, row)
		default:
			items.others = append(items.others, row)
		}
	}

	for i := range items.series {
		sort.SliceStable(items.series[i].episodes, func(a, b int) bool {
			ea, eb := items.series[i].episodes[a], items.series[i].episodes[b]
			if ea.SeasonNumber == nil || eb.SeasonNumber == nil ||
				ea.EpisodeNumber == nil || eb.EpisodeNumber == nil {
				return false
			}
			if *ea.SeasonNumber != *eb.SeasonNumber {
				return *ea.SeasonNumber < *eb.SeasonNumber
			}
			return *ea.EpisodeNumber < *eb.EpisodeNumber
		})
	}
	return items
}

// episodeCode renders "S02E03"; empty when numbering is unknown.
func episodeCode(row DeliveryRow) string {
	if row.SeasonNumber == nil || row.EpisodeNumber == nil {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", *row.SeasonNumber, *row.EpisodeNumber)
}

// episodeLine renders one episode entry: "S02E03 — Title", degrading to
// whichever part exists.
func episodeLine(row DeliveryRow) string {
	code := episodeCode(row)
	switch {
	case code != "" && row.EpisodeTitle != "":
		return code + " — " + row.EpisodeTitle
	case code != "":
		return code
	case row.EpisodeTitle != "":
		return row.EpisodeTitle
	default:
		return genericEpisodeTitle
	}
}

// requestLine renders one request-status entry. Fulfilled rows carry the
// catalog title via the join; approved/declined rows carry it in the flags.
func requestLine(row DeliveryRow) string {
	flags := parseRequestFlags(row.ReasonFlags)
	title := row.SeriesTitle
	if title == "" {
		title = flags.Title
	}
	switch row.Type {
	case DeliveryTypeRequestApproved:
		if title != "" {
			return "Your request for " + title + " was approved"
		}
		return "Your media request was approved"
	case DeliveryTypeRequestDeclined:
		line := "Your media request was declined"
		if title != "" {
			line = "Your request for " + title + " was declined"
		}
		if flags.Reason != "" {
			line += " — " + flags.Reason
		}
		return line
	default:
		if title != "" {
			return title + " is now available"
		}
		if flags.Follower {
			return followedTitleAvailable
		}
		return "Your media request is now available"
	}
}

// requestsAllFulfilled reports whether every collated request row is a
// fulfillment; it drives the "requests ready" vs "request updates" copy.
func requestsAllFulfilled(items emailItems) bool {
	for _, row := range items.requests {
		if row.Type != DeliveryTypeRequestFulfilled {
			return false
		}
	}
	return true
}

// otherLine renders operational and unknown delivery types generically.
func otherLine(row DeliveryRow) string {
	if row.Type == DeliveryTypeWebhookAutoDisabled {
		return "A webhook stopped working — open notification settings to fix it"
	}
	return genericNotificationTitle
}

// countPart pluralizes "3 new episodes" style subject fragments.
func countPart(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

// digestLabel names a digest send in copy; empty for per-item sends.
func digestLabel(mode string) string {
	switch mode {
	case EmailModeDailyDigest:
		return "daily"
	case ChannelModeWeeklyDigest:
		return "weekly"
	}
	return ""
}

// emailSubject builds the subject line from the collated items.
func emailSubject(mode string, items emailItems) string {
	parts := make([]string, 0, 4)
	if len(items.movies) > 0 {
		parts = append(parts, countPart(len(items.movies), "new movie", "new movies"))
	}
	if items.episodes > 0 {
		parts = append(parts, countPart(items.episodes, "new episode", "new episodes"))
	}
	if len(items.requests) > 0 {
		singular, plural := "request update", "request updates"
		if requestsAllFulfilled(items) {
			singular, plural = "request ready", "requests ready"
		}
		parts = append(parts, countPart(len(items.requests), singular, plural))
	}
	if len(items.others) > 0 {
		parts = append(parts, countPart(len(items.others), "update", "updates"))
	}
	summary := strings.Join(parts, ", ")

	if label := digestLabel(mode); label != "" {
		return "Silo " + label + " digest: " + summary
	}
	single := len(items.requests) == 0 && len(items.others) == 0
	if single && len(items.movies) == 0 && items.episodes == 1 {
		row := items.series[0].episodes[0]
		subject := genericEpisodeTitle
		if items.series[0].title != untitledSeriesGroup {
			subject += " of " + items.series[0].title
		}
		if line := episodeLine(row); line != genericEpisodeTitle {
			subject += ": " + line
		}
		return subject
	}
	if single && items.episodes == 0 && len(items.movies) == 1 {
		return "New movie: " + movieTitle(items.movies[0])
	}
	if items.episodes == 0 && len(items.movies) == 0 && len(items.requests) == 1 && len(items.others) == 0 {
		return requestLine(items.requests[0])
	}
	return "Silo: " + summary
}

// movieTitle renders "Title (2024)", degrading to whichever part exists.
func movieTitle(row DeliveryRow) string {
	if row.SeriesTitle == "" {
		return "New movie"
	}
	return titleWithYear(row.SeriesTitle, row.Year)
}

// itemURL builds a deep link; empty when no external URL is configured.
func itemURL(baseURL, itemID string) string {
	if baseURL == "" || itemID == "" {
		return ""
	}
	return baseURL + "/item/" + itemID
}

// emailComposeOptions carries the per-send rendering context.
type emailComposeOptions struct {
	// Brand styles the shell; the zero value is Silo's default branding.
	Brand mail.Brand
	// BaseURL is the admin-configured external URL; empty renders without
	// links.
	BaseURL string
	// ProfileName labels whose notifications these are — several profiles on
	// one account may deliver to the same fallback address.
	ProfileName string
	// UnsubscribeURL is the tokenized one-click unsubscribe link; empty
	// renders without one.
	UnsubscribeURL string
	// ArtworkURL returns the signed link for a title's poster or dimmed
	// backdrop (EmailArtworkPoster, EmailArtworkBackdrop); nil renders cards
	// without artwork.
	ArtworkURL func(kind, contentID string) string
}

func (o emailComposeOptions) artwork(kind string, contentID *string) string {
	if o.ArtworkURL == nil || contentID == nil || *contentID == "" {
		return ""
	}
	return o.ArtworkURL(kind, *contentID)
}

// Card copy limits keep a long digest scannable.
const (
	emailSeriesOverviewMax  = 260
	emailEpisodeOverviewMax = 220
	emailMovieOverviewMax   = 360
	emailPosterWidthPx      = 112
)

// emailAccent is the badge and episode-code colour: the server's accent, or
// the default action colour.
func emailAccent(brand mail.Brand) string {
	if brand.AccentColor != "" {
		return brand.AccentColor
	}
	return "#8b9cff"
}

// emailCardBuilder renders the HTML body of a notification email: one card
// per series with its new episodes, one card per movie, then request and
// other updates as plain lists. Every value is escaped here.
type emailCardBuilder struct {
	opts     emailComposeOptions
	accent   string
	body     strings.Builder
	text     strings.Builder
	rendered int
}

func (b *emailCardBuilder) full() bool { return b.rendered >= emailMaxItemsRendered }

func (b *emailCardBuilder) sectionLabel(label string) {
	b.text.WriteString("\n" + strings.ToUpper(label) + "\n")
	top := "22px"
	if b.body.Len() == 0 {
		top = "4px"
	}
	b.body.WriteString(fmt.Sprintf(
		`<div style="margin:%s 0 10px;font:700 12px/1.4 %s;letter-spacing:1.5px;text-transform:uppercase;color:%s;">%s</div>`,
		top, mail.EmailFont, mail.EmailColorMuted, html.EscapeString(label)))
}

// cardOpen starts a card whose header cell carries the dimmed backdrop when
// one exists. The background attribute is for Outlook, which ignores CSS
// background images.
func (b *emailCardBuilder) cardOpen(backdrop string) {
	b.body.WriteString(fmt.Sprintf(
		`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="margin:0 0 16px;background:%s;border:1px solid %s;border-radius:12px;border-collapse:separate;overflow:hidden;">`,
		mail.EmailColorCanvas, mail.EmailColorBorder))
	if backdrop != "" {
		bg := html.EscapeString(backdrop)
		b.body.WriteString(fmt.Sprintf(
			`<tr><td background="%s" style="padding:18px;background-color:%s;background-image:url('%s');background-size:cover;background-position:center;border-radius:12px 12px 0 0;">`,
			bg, mail.EmailColorCanvas, bg))
	} else {
		b.body.WriteString(`<tr><td style="padding:18px;">`)
	}
}

// headerRow lays the poster beside the title block.
func (b *emailCardBuilder) headerRow(poster, alt, titleHTML string) {
	b.body.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>`)
	if poster != "" {
		b.body.WriteString(fmt.Sprintf(
			`<td width="%d" valign="top" style="padding-right:16px;"><img src="%s" alt="%s" width="%d" style="display:block;width:%dpx;max-width:%dpx;border-radius:8px;border:0;"></td>`,
			emailPosterWidthPx, html.EscapeString(poster), html.EscapeString(alt), emailPosterWidthPx, emailPosterWidthPx, emailPosterWidthPx))
	}
	b.body.WriteString(`<td valign="top">` + titleHTML + `</td></tr></table>`)
}

func (b *emailCardBuilder) cardClose() {
	b.body.WriteString(`</td></tr></table>`)
}

func (b *emailCardBuilder) title(label, href, suffix string) string {
	content := html.EscapeString(label)
	if suffix != "" {
		content += fmt.Sprintf(` <span style="font-weight:400;color:%s;">%s</span>`, mail.EmailColorMuted, html.EscapeString(suffix))
	}
	if href != "" {
		content = fmt.Sprintf(`<a href="%s" style="color:%s;text-decoration:none;">%s</a>`, html.EscapeString(href), mail.EmailColorText, content)
	}
	return fmt.Sprintf(`<div style="font:700 19px/1.3 %s;color:%s;">%s</div>`, mail.EmailFont, mail.EmailColorText, content)
}

func metaLine(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return fmt.Sprintf(`<div style="margin-top:4px;font:400 13px/1.5 %s;color:%s;">%s</div>`,
		mail.EmailFont, mail.EmailColorMuted, html.EscapeString(strings.Join(kept, " · ")))
}

func paragraph(text string, max int, muted bool) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	color := mail.EmailColorText
	if muted {
		color = mail.EmailColorMuted
	}
	return fmt.Sprintf(`<div style="margin-top:10px;font:400 14px/1.55 %s;color:%s;">%s</div>`,
		mail.EmailFont, color, html.EscapeString(truncateDisplayText(text, max)))
}

func genresText(genres []string) string {
	if len(genres) > 3 {
		genres = genres[:3]
	}
	return strings.Join(genres, ", ")
}

func runtimeText(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %02dm", minutes/60, minutes%60)
}

func ratingText(imdb, tmdb float64) string {
	switch {
	case imdb > 0:
		return fmt.Sprintf("★ %.1f", imdb)
	case tmdb > 0:
		return fmt.Sprintf("★ %.1f", tmdb)
	}
	return ""
}

func (b *emailCardBuilder) seriesCard(group emailSeriesGroup) {
	lead := group.episodes[0]
	seriesID := &group.seriesID
	b.text.WriteString(group.title + "\n")
	b.cardOpen(b.opts.artwork(EmailArtworkBackdrop, seriesID))

	network := ""
	if len(lead.Networks) > 0 {
		network = lead.Networks[0]
	}
	badge := countPart(len(group.episodes), "new episode", "new episodes")
	header := b.title(group.title, itemURL(b.opts.BaseURL, group.seriesID), "") +
		metaLine(network, genresText(lead.Genres), ratingText(lead.RatingIMDB, lead.RatingTMDB)) +
		fmt.Sprintf(`<div style="display:inline-block;margin-top:10px;padding:3px 10px;border-radius:999px;background:%s;color:#ffffff;font:700 12px/1.6 %s;">%s</div>`,
			b.accent, mail.EmailFont, html.EscapeString(badge))
	if len(group.episodes) == 1 || lead.EpisodeOverview == "" {
		header += paragraph(lead.SeriesOverview, emailSeriesOverviewMax, false)
	}
	b.headerRow(b.opts.artwork(EmailArtworkPoster, seriesID), group.title, header)
	b.body.WriteString(`</td></tr><tr><td style="padding:2px 18px 16px;">`)

	for _, row := range group.episodes {
		b.rendered++
		if b.rendered > emailMaxItemsRendered {
			break
		}
		episodeID := ""
		if row.EpisodeID != nil {
			episodeID = *row.EpisodeID
		}
		b.text.WriteString("  " + episodeLine(row) + "\n")
		code := episodeCode(row)
		label := strings.TrimSpace(row.EpisodeTitle)
		if code == "" && label == "" {
			label = genericEpisodeTitle
		}
		var line strings.Builder
		if code != "" {
			line.WriteString(fmt.Sprintf(`<span style="color:%s;">%s</span>`, b.accent, html.EscapeString(code)))
			if label != "" {
				line.WriteString(" · ")
			}
		}
		line.WriteString(html.EscapeString(label))
		content := line.String()
		if href := itemURL(b.opts.BaseURL, episodeID); href != "" {
			content = fmt.Sprintf(`<a href="%s" style="color:%s;text-decoration:none;">%s</a>`, html.EscapeString(href), mail.EmailColorText, content)
		}
		b.body.WriteString(fmt.Sprintf(
			`<div style="margin-top:12px;padding-top:12px;border-top:1px solid %s;"><div style="font:700 15px/1.4 %s;color:%s;">%s</div>%s</div>`,
			mail.EmailColorRule, mail.EmailFont, mail.EmailColorText, content,
			paragraph(row.EpisodeOverview, emailEpisodeOverviewMax, true)))
	}
	b.cardClose()
}

func (b *emailCardBuilder) movieCard(row DeliveryRow) {
	b.rendered++
	if b.rendered > emailMaxItemsRendered {
		return
	}
	title := row.SeriesTitle
	if title == "" {
		title = "New movie"
	}
	b.text.WriteString("  " + movieTitle(row) + "\n")
	itemID := ""
	if row.SeriesID != nil {
		itemID = *row.SeriesID
	}
	year := ""
	if row.Year > 0 {
		year = fmt.Sprintf("(%d)", row.Year)
	}
	b.cardOpen(b.opts.artwork(EmailArtworkBackdrop, row.SeriesID))
	header := b.title(title, itemURL(b.opts.BaseURL, itemID), year) +
		metaLine(genresText(row.Genres), runtimeText(row.Runtime), row.ContentRating, ratingText(row.RatingIMDB, row.RatingTMDB))
	if tagline := strings.TrimSpace(row.Tagline); tagline != "" {
		header += fmt.Sprintf(`<div style="margin-top:10px;font:italic 400 14px/1.5 %s;color:%s;">%s</div>`,
			mail.EmailFont, b.accent, html.EscapeString(tagline))
	}
	header += paragraph(row.SeriesOverview, emailMovieOverviewMax, false)
	b.headerRow(b.opts.artwork(EmailArtworkPoster, row.SeriesID), title, header)
	b.cardClose()
}

// listItem renders one plain row for request and other updates.
func (b *emailCardBuilder) listItem(plain, href string) {
	b.rendered++
	if b.rendered > emailMaxItemsRendered {
		return
	}
	b.text.WriteString("  " + plain + "\n")
	content := html.EscapeString(plain)
	if href != "" {
		content = fmt.Sprintf(`<a href="%s" style="color:%s;text-decoration:none;">%s</a>`,
			html.EscapeString(href), mail.EmailColorText, content)
	}
	b.body.WriteString(fmt.Sprintf(
		`<div style="padding:8px 2px;border-top:1px solid %s;font:400 14px/1.5 %s;color:%s;">%s</div>`,
		mail.EmailColorRule, mail.EmailFont, mail.EmailColorText, content))
}

// composeNotificationEmail renders one email (text + HTML) for the given
// delivery rows.
func composeNotificationEmail(mode string, rows []DeliveryRow, opts emailComposeOptions) emailContent {
	items := collateEmailItems(rows)
	b := &emailCardBuilder{opts: opts, accent: emailAccent(opts.Brand)}
	total := items.episodes + len(items.movies) + len(items.requests) + len(items.others)

	if len(items.series) > 0 {
		b.sectionLabel("TV")
		for _, group := range items.series {
			if b.full() {
				break
			}
			b.seriesCard(group)
		}
	}
	if len(items.movies) > 0 && !b.full() {
		b.sectionLabel("Movies")
		for _, row := range items.movies {
			if b.full() {
				break
			}
			b.movieCard(row)
		}
	}
	if len(items.requests) > 0 && !b.full() {
		heading := "Request updates"
		if requestsAllFulfilled(items) {
			heading = "Requests ready"
		}
		b.sectionLabel(heading)
		for _, row := range items.requests {
			seriesID := ""
			if row.SeriesID != nil {
				seriesID = *row.SeriesID
			}
			b.listItem(requestLine(row), itemURL(opts.BaseURL, seriesID))
		}
	}
	if len(items.others) > 0 && !b.full() {
		b.sectionLabel("Other updates")
		for _, row := range items.others {
			b.listItem(otherLine(row), "")
		}
	}
	if remainder := total - emailMaxItemsRendered; remainder > 0 {
		more := fmt.Sprintf("…and %d more in your Silo inbox.", remainder)
		b.text.WriteString(more + "\n")
		b.body.WriteString(fmt.Sprintf(`<p style="margin:14px 0 0;font:400 13px/1.5 %s;color:%s;">%s</p>`,
			mail.EmailFont, mail.EmailColorMuted, html.EscapeString(more)))
	}

	forProfile := ""
	if opts.ProfileName != "" {
		forProfile = " for " + opts.ProfileName
	}
	intro := fmt.Sprintf("New in your library%s:", forProfile)
	if label := digestLabel(mode); label != "" {
		intro = fmt.Sprintf("Here's what's new%s since the last digest:", forProfile)
	}
	subjectFor := ""
	if opts.ProfileName != "" {
		subjectFor = " (for " + opts.ProfileName + ")"
	}

	profileLabel := "this profile"
	if opts.ProfileName != "" {
		profileLabel = "the profile “" + opts.ProfileName + "”"
	}
	footer := fmt.Sprintf("You're receiving this because email notifications are enabled for"+
		" %s on your Silo account. Manage them in Settings → Notifications.", profileLabel)
	footerHTML := html.EscapeString(footer)
	if opts.BaseURL != "" {
		settingsURL := html.EscapeString(opts.BaseURL + "/settings/notifications")
		footerHTML = strings.Replace(footerHTML,
			"Settings → Notifications",
			fmt.Sprintf(`<a href="%s" style="color:%s;">Settings → Notifications</a>`,
				settingsURL, mail.EmailColorMuted), 1)
	}
	if opts.UnsubscribeURL != "" {
		footer += " To stop these emails, open: " + opts.UnsubscribeURL
		footerHTML += fmt.Sprintf(` <a href="%s" style="color:%s;">Unsubscribe</a>`,
			html.EscapeString(opts.UnsubscribeURL), mail.EmailColorMuted)
	}

	htmlBody := mail.RenderLayout(mail.LayoutOptions{
		Brand:      opts.Brand,
		Preheader:  emailPreheader(items),
		Title:      strings.TrimSuffix(intro, ":"),
		BodyHTML:   b.body.String(),
		FooterHTML: footerHTML,
	})

	return emailContent{
		Subject: emailSubject(mode, items) + subjectFor,
		Text:    intro + "\n" + b.text.String() + "\n" + footer + "\n",
		HTML:    htmlBody,
	}
}

// emailPreheader picks the inbox-preview snippet: the first item, the same
// way a notification banner would lead with it.
func emailPreheader(items emailItems) string {
	if len(items.series) > 0 && len(items.series[0].episodes) > 0 {
		line := episodeLine(items.series[0].episodes[0])
		if title := items.series[0].title; title != untitledSeriesGroup {
			return title + " · " + line
		}
		return line
	}
	if len(items.movies) > 0 {
		return movieTitle(items.movies[0])
	}
	if len(items.requests) > 0 {
		return requestLine(items.requests[0])
	}
	if len(items.others) > 0 {
		return otherLine(items.others[0])
	}
	return ""
}
