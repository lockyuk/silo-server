package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/pushstyle"
	"github.com/Silo-Server/silo-server/internal/titleutil"
)

// pushGroupMaxMembers bounds how many deliveries one grouped push names.
const pushGroupMaxMembers = 50

// pushBatchRecheck is the shortest wait before a held attempt is looked at
// again, so attempts waiting on a newer one do not spin.
const pushBatchRecheck = 30 * time.Second

// PushContent is a push notification's text rendered from the admin's push
// templates, with the image the admin chose. Clients that know it show it in
// place of the text they build themselves.
type PushContent struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Synopsis string `json:"synopsis,omitempty"`
	// Image is poster, backdrop or none; ImageURL is that image's link.
	Image    string `json:"image"`
	ImageURL string `json:"image_url,omitempty"`
	// CollapsedImage and ExpandedImage say where the image shows; Expanded
	// whether there is an expanded view.
	CollapsedImage bool `json:"collapsed_image"`
	ExpandedImage  bool `json:"expanded_image"`
	Expanded       bool `json:"expanded"`
	// Count is how many episodes the push stands for; above 1 for a grouped
	// push.
	Count int `json:"count,omitempty"`
}

// PushStyleRepository stores the push settings and grouped pushes.
type PushStyleRepository struct {
	pool *pgxpool.Pool
}

func NewPushStyleRepository(pool *pgxpool.Pool) *PushStyleRepository {
	return &PushStyleRepository{pool: pool}
}

// Settings returns the saved settings, or the defaults.
func (r *PushStyleRepository) Settings(ctx context.Context) (pushstyle.Settings, error) {
	if r == nil || r.pool == nil {
		return pushstyle.DefaultSettings(), nil
	}
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT settings FROM notification_push_style_settings WHERE id = 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return pushstyle.DefaultSettings(), nil
	}
	if err != nil {
		return pushstyle.Settings{}, fmt.Errorf("load push settings: %w", err)
	}
	s := pushstyle.DefaultSettings()
	if err := json.Unmarshal(raw, &s); err != nil {
		return pushstyle.Settings{}, fmt.Errorf("decode push settings: %w", err)
	}
	return s, nil
}

// SaveSettings stores the settings after normalizing them.
func (r *PushStyleRepository) SaveSettings(ctx context.Context, s pushstyle.Settings) (pushstyle.Settings, error) {
	s, err := s.Normalize()
	if err != nil {
		return s, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO notification_push_style_settings (id, settings, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`, raw)
	if err != nil {
		return s, fmt.Errorf("save push settings: %w", err)
	}
	return s, nil
}

// recordGroup adds members to the grouped push sent for deliveryID.
func (r *PushStyleRepository) recordGroup(ctx context.Context, deliveryID string, members []string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_push_groups (delivery_id, member_ids, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (delivery_id) DO UPDATE SET
			member_ids = ARRAY(SELECT DISTINCT unnest(notification_push_groups.member_ids || EXCLUDED.member_ids)),
			updated_at = now()`, deliveryID, members)
	if err != nil {
		return fmt.Errorf("record push group: %w", err)
	}
	return nil
}

// groupMembers returns the deliveries the push for deliveryID stands for,
// or nil when it was sent alone.
func (r *PushStyleRepository) groupMembers(ctx context.Context, deliveryID string) ([]string, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	var members []string
	err := r.pool.QueryRow(ctx, `SELECT member_ids FROM notification_push_groups WHERE delivery_id = $1`, deliveryID).Scan(&members)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return members, err
}

// openSeriesAttempt is an unsent push attempt for an episode of one show.
type openSeriesAttempt struct {
	ID         string
	DeliveryID string
	CreatedAt  time.Time
}

// openSeriesAttempts lists the device's unsent, never-tried push attempts
// for new episodes of seriesID, oldest first.
func (r *PushDeviceRepository) openSeriesAttempts(ctx context.Context, deviceID, seriesID string) ([]openSeriesAttempt, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.notification_delivery_id, a.created_at
		FROM push_delivery_attempts a
		JOIN notification_deliveries d ON d.id = a.notification_delivery_id
		WHERE a.push_device_id = $1 AND a.trigger_type = 'delivery'
		  AND a.outcome IN ('pending', 'retrying') AND a.attempt_number = 0
		  AND d.series_id = $2 AND d.type = $3
		ORDER BY a.created_at, a.id
		LIMIT $4`, deviceID, seriesID, DeliveryTypeEpisodeAvailable, pushGroupMaxMembers)
	if err != nil {
		return nil, fmt.Errorf("list open series push attempts: %w", err)
	}
	defer rows.Close()
	var out []openSeriesAttempt
	for rows.Next() {
		var a openSeriesAttempt
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// deferPushAttempt holds an attempt until until without counting a try.
func (r *PushDeviceRepository) deferPushAttempt(ctx context.Context, id string, until time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE push_delivery_attempts SET outcome = 'retrying', next_retry_at = $2, updated_at = now()
		WHERE id = $1`, id, until)
	if err != nil {
		return fmt.Errorf("defer push attempt: %w", err)
	}
	return nil
}

// markPushAttemptsBatched closes attempts whose deliveries another push
// carried.
func (r *PushDeviceRepository) markPushAttemptsBatched(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE push_delivery_attempts SET outcome = 'batched', next_retry_at = NULL, updated_at = now()
		WHERE id = ANY($1) AND outcome IN ('pending', 'retrying')`, ids)
	if err != nil {
		return fmt.Errorf("mark push attempts batched: %w", err)
	}
	return nil
}

// holdOrGroup decides whether a delivery's push waits: through quiet hours,
// or while newer episodes of the same show may still join it. When the push
// goes now and stands for several episodes, it records the group and closes
// the other attempts. It returns the time to hold until, or zero to send.
func (s *pushSender) holdOrGroup(ctx context.Context, attempt PushDeliveryAttempt, device *PushDevice, row *DeliveryRow) time.Time {
	if s.style == nil || attempt.TriggerType != PushTriggerDelivery || row == nil {
		return time.Time{}
	}
	cfg, err := s.style.Settings(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "push settings unreadable; sending without holding", "error", err)
		return time.Time{}
	}
	now := s.now()
	if until, ok := cfg.QuietUntil(now); ok {
		return until
	}
	if cfg.BatchMinutes <= 0 || row.Type != DeliveryTypeEpisodeAvailable || row.SeriesID == nil || *row.SeriesID == "" {
		return time.Time{}
	}
	window := time.Duration(cfg.BatchMinutes) * time.Minute
	open, err := s.devices.openSeriesAttempts(ctx, device.ID, *row.SeriesID)
	if err != nil {
		s.logger.WarnContext(ctx, "push batching unavailable; sending alone", "attempt_id", attempt.ID, "error", err)
		return time.Time{}
	}
	if len(open) == 0 {
		return time.Time{}
	}
	newest := open[len(open)-1]
	if newest.ID != attempt.ID {
		// The newest attempt sends for all of them.
		return laterOf(newest.CreatedAt.Add(window), now.Add(pushBatchRecheck))
	}
	if ready := attempt.CreatedAt.Add(window); ready.After(now) {
		return ready
	}
	if len(open) == 1 {
		return time.Time{}
	}
	members := make([]string, 0, len(open))
	others := make([]string, 0, len(open)-1)
	for _, a := range open {
		members = append(members, a.DeliveryID)
		if a.ID != attempt.ID {
			others = append(others, a.ID)
		}
	}
	if err := s.style.recordGroup(ctx, row.ID, members); err != nil {
		s.logger.WarnContext(ctx, "push group not recorded; sending alone", "attempt_id", attempt.ID, "error", err)
		return time.Time{}
	}
	if err := s.devices.markPushAttemptsBatched(ctx, others); err != nil {
		s.logger.WarnContext(ctx, "batched push attempts not closed", "attempt_id", attempt.ID, "error", err)
	}
	return time.Time{}
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// pushItem describes row, and the other episodes its grouped push carries,
// for the push templates.
func pushItem(row DeliveryRow, group []DeliveryRow) pushstyle.Item {
	episode := func(r DeliveryRow) pushstyle.Episode {
		return pushstyle.Episode{Season: r.SeasonNumber, Number: r.EpisodeNumber, Title: displayEpisodeTitle(r), Overview: r.EpisodeOverview}
	}
	switch {
	case row.Type == DeliveryTypeMovieAvailable || (row.Type == DeliveryTypeRequestFulfilled && row.MediaType == "movie"):
		return pushstyle.Item{Kind: pushstyle.KindMovie, Title: row.SeriesTitle, Year: row.Year, Overview: firstNonEmpty(row.SeriesOverview, row.EpisodeOverview)}
	case len(group) > 1:
		item := pushstyle.Item{Kind: pushstyle.KindGroup, Title: row.SeriesTitle, Year: row.Year, Overview: row.SeriesOverview}
		for _, g := range group {
			item.Episodes = append(item.Episodes, episode(g))
		}
		return item
	default:
		return pushstyle.Item{Kind: pushstyle.KindEpisode, Title: row.SeriesTitle, Year: row.Year, Overview: firstNonEmpty(row.EpisodeOverview, row.SeriesOverview), Episodes: []pushstyle.Episode{episode(row)}}
	}
}

func displayEpisodeTitle(row DeliveryRow) string {
	title := strings.TrimSpace(row.EpisodeTitle)
	if title == "" || titleutil.IsProvisionalEpisodeTitle(title) {
		return ""
	}
	return title
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// pushStyled reports whether the push templates cover a delivery type.
func pushStyled(row DeliveryRow) bool {
	switch row.Type {
	case DeliveryTypeEpisodeAvailable, DeliveryTypeMovieAvailable:
		return true
	case DeliveryTypeRequestFulfilled:
		return row.SeriesTitle != ""
	}
	return false
}

// PushContentForRow renders row with the admin's push templates; nil for a
// delivery the templates do not cover.
func (s *System) PushContentForRow(ctx context.Context, row DeliveryRow) *PushContent {
	if s == nil || s.PushStyle == nil || !pushStyled(row) {
		return nil
	}
	cfg, err := s.PushStyle.Settings(ctx)
	if err != nil {
		slog.WarnContext(ctx, "push settings unreadable", "component", "notifications", "error", err)
		return nil
	}
	var group []DeliveryRow
	if row.Type == DeliveryTypeEpisodeAvailable {
		group = s.pushGroupRows(ctx, row)
	}
	item := pushItem(row, group)
	rendered, err := pushstyle.Render(cfg, item)
	if err != nil {
		slog.WarnContext(ctx, "push template failed; clients use their own text", "component", "notifications", "error", err)
		return nil
	}
	out := &PushContent{Title: rendered.Title, Body: rendered.Body, Synopsis: rendered.Synopsis, Image: cfg.Image,
		CollapsedImage: cfg.CollapsedImage, ExpandedImage: cfg.ExpandedImage, Expanded: cfg.Expanded}
	if len(group) > 1 {
		out.Count = len(group)
	}
	switch cfg.Image {
	case pushstyle.ImagePoster:
		out.ImageURL = s.richArtworkURL(ctx, row.PosterPath, row.PosterSourcePath, "poster", "w342")
	case pushstyle.ImageBackdrop:
		out.ImageURL = s.richArtworkURL(ctx, row.BackdropPath, row.BackdropSourcePath, "backdrop", "w780")
	}
	return out
}

// pushGroupRows loads the deliveries the push for row stands for; nil when
// it was sent alone.
func (s *System) pushGroupRows(ctx context.Context, row DeliveryRow) []DeliveryRow {
	members, err := s.PushStyle.groupMembers(ctx, row.ID)
	if err != nil || len(members) < 2 || s.Deliveries == nil {
		return nil
	}
	rows := make([]DeliveryRow, 0, len(members))
	for _, id := range members {
		if id == row.ID {
			rows = append(rows, row)
			continue
		}
		member, err := s.Deliveries.GetRowByID(ctx, id)
		if err != nil || member == nil || member.ProfileID != row.ProfileID {
			continue
		}
		rows = append(rows, *member)
	}
	return rows
}

// PreviewPush renders the sample item of kind with settings, for the admin
// page.
func PreviewPush(settings pushstyle.Settings, kind string) (pushstyle.Rendered, error) {
	return pushstyle.Render(settings, pushstyle.Sample(kind))
}

// PushStyleSettings returns the saved push settings.
func (s *System) PushStyleSettings(ctx context.Context) (pushstyle.Settings, error) {
	return s.PushStyle.Settings(ctx)
}

// SavePushStyleSettings validates and stores the push settings.
func (s *System) SavePushStyleSettings(ctx context.Context, settings pushstyle.Settings) (pushstyle.Settings, error) {
	return s.PushStyle.SaveSettings(ctx, settings)
}

// SendPushStyleTest sends a test push to each of profileID's Android
// devices. Each carries the profile's newest notification, rendered with
// the saved settings; quiet hours and batching do not hold it.
func (s *System) SendPushStyleTest(ctx context.Context, profileID string) (sent, devices int, err error) {
	if s == nil || s.pool == nil {
		return 0, 0, ErrPushDeliveryUnavailable
	}
	rows, err := s.pool.Query(ctx, `
		SELECT server_device_id FROM push_devices
		WHERE profile_id = $1 AND platform = $2 AND enabled AND push_mode = $3
		ORDER BY last_seen_at DESC NULLS LAST`, profileID, PushPlatformAndroid, PushModePrivatePush)
	if err != nil {
		return 0, 0, fmt.Errorf("list push devices: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, 0, fmt.Errorf("list push devices: %w", err)
	}
	for _, id := range ids {
		result, err := s.SendAndroidPushTest(ctx, profileID, id)
		if err != nil {
			return sent, len(ids), err
		}
		if result.Outcome == PushOutcomeDelivered {
			sent++
		}
	}
	return sent, len(ids), nil
}
