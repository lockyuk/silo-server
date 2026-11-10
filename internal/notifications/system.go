package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/serveridentity"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// UserLister enumerates login accounts for the interest backfill. Satisfied
// by *auth.UserRepository.
type UserLister interface {
	List(ctx context.Context) ([]*models.User, error)
}

// ImageURLResolver presigns stored image paths into client-fetchable URLs.
// Satisfied by *catalog.DetailService.
type ImageURLResolver interface {
	PresignImageURL(ctx context.Context, path, imageType, size string) string
}

// System bundles the user-facing release-notification services: availability
// detection, interest maintenance, fanout, inbox repositories, and websocket
// tickets. It is distinct from the operational Hub in hub.go.
type System struct {
	Settings    *Settings
	Releases    *ReleaseRepository
	Follows     *FollowRepository
	Deliveries  *DeliveryRepository
	Preferences *PreferencesRepository
	Detector    *AvailabilityDetector
	Fanout      *FanoutWorker
	Tickets     TicketStore
	// Webhooks is nil when no at-rest cipher is configured (webhook URLs are
	// credentials and must not be stored in plaintext).
	Webhooks *WebhookService
	// PushDevices is nil when no at-rest cipher is configured; APNs tokens are
	// credentials and must not be stored in plaintext.
	PushDevices *PushDeviceService
	// EmailPrefs is nil when no mail sender was provided.
	EmailPrefs *EmailPrefsRepository

	// EmailVerification admits durable requests and reports whether the
	// dispatcher below can hand them to the provider.
	EmailVerification *EmailVerificationService

	mailSender                mail.Sender
	emailBrand                *mail.BrandLoader
	emailVerificationDispatch *emailVerificationDispatcher
	// EmailAlerts sends the new-media emails; nil without a mail sender.
	EmailAlerts *EmailAlerts
	// PushStyle holds the admin's push templates, batching and quiet hours.
	PushStyle *PushStyleRepository
	// publicURL is the server's externally reachable base URL, used as the
	// fallback for tokenized email links (see SetPublicURL).
	publicURL string

	webhookRepo       *WebhookRepository
	webhookDispatcher *WebhookDispatcher
	webhookRetry      *WebhookRetryWorker
	pushDeviceRepo    *PushDeviceRepository
	pushDispatcher    *PushDispatcher
	pushSender        *pushSender
	// dispatcher is the same MultiDispatcher the fanout worker uses; the
	// operational dispatch path shares it so every delivery reaches every
	// configured channel the same way.
	dispatcher Dispatcher

	pool   *pgxpool.Pool
	stores userstore.UserStoreProvider
	// scopes resolves a recipient's current access before a delivery that
	// names a catalog item is created.
	scopes ScopeResolver
	users  UserLister
	images ImageURLResolver
	// EmailArtwork serves the signed poster and backdrop links in
	// notification emails; nil until SetEmailArtwork.
	EmailArtwork *EmailArtwork
	logger       *slog.Logger
	wg           sync.WaitGroup
}

// NewSystem wires the notification system. hub may be nil (no realtime
// publishing); redisClient may be nil (in-memory websocket tickets);
// mailSender may be nil (no email channel); emailBrand may be nil (emails
// carry Silo's default branding).
func NewSystem(
	pool *pgxpool.Pool,
	settingsReader SettingReader,
	stores userstore.UserStoreProvider,
	scopes ScopeResolver,
	users UserLister,
	hub *evt.Hub,
	redisClient *redis.Client,
	cipher *secret.Cipher,
	mailSender mail.Sender,
	emailBrand *mail.BrandLoader,
) *System {
	settings := NewSettings(settingsReader)
	releases := NewReleaseRepository(pool)
	follows := NewFollowRepository(pool)
	deliveries := NewDeliveryRepository(pool)
	preferences := NewPreferencesRepository(pool)

	wsDispatcher := NewWebsocketDispatcher(hub)
	dispatchers := []Dispatcher{wsDispatcher}

	// Outbound webhooks require the at-rest cipher: destination URLs are
	// bearer credentials (Discord) and must never be stored in plaintext.
	var webhookRepo *WebhookRepository
	var webhookService *WebhookService
	var webhookDispatcher *WebhookDispatcher
	var webhookRetry *WebhookRetryWorker
	var sender *webhookSender
	var pushDeviceService *PushDeviceService
	var pushDeviceRepo *PushDeviceRepository
	var pushSenderInst *pushSender
	var pushDispatcher *PushDispatcher
	pushStyle := NewPushStyleRepository(pool)
	if cipher != nil {
		webhookRepo = NewWebhookRepository(pool)
		sender = newWebhookSender(webhookRepo, deliveries, cipher, settings)
		webhookService = newWebhookService(webhookRepo, cipher, settings, sender)
		webhookDispatcher = newWebhookDispatcher(sender)
		webhookRetry = newWebhookRetryWorker(sender)
		dispatchers = append(dispatchers, webhookDispatcher)
		pushDeviceRepo = NewPushDeviceRepository(pool)
		pushDeviceService = NewPushDeviceService(pushDeviceRepo, cipher)
		pushSenderInst = newPushSender(pushDeviceRepo, deliveries, cipher, settings)
		pushSenderInst.style = pushStyle
		// Pushes name the identity getServerIdentity reports, read through the
		// raw settings repo as it is: a SECRET_KEY rotation must not change
		// who the server is.
		pushSenderInst.serverID = serveridentity.New(catalog.NewServerSettingsRepo(pool)).ServerID
		pushDispatcher = newPushDispatcher(pushSenderInst)
		dispatchers = append(dispatchers, pushDispatcher)
	}

	// Email rides the shared SMTP core. Unlike the per-target channels it
	// keeps no outbox: its dispatcher only nudges the watermark sweep.
	// Email alerts replace the per-profile email sweep: recipients are
	// profiles (followed shows or everything new) and addresses an admin
	// added, each with instant, daily and weekly legs.
	var emailPrefs *EmailPrefsRepository
	var emailAlerts *EmailAlerts
	if mailSender != nil {
		emailPrefs = NewEmailPrefsRepository(pool)
		emailAlerts = newEmailAlerts(pool, settings, mailSender, emailBrand, scopes)
		dispatchers = append(dispatchers, newNudgeDispatcher(emailAlerts))
	}

	multiDispatcher := NewMultiDispatcher(dispatchers...)
	fanout := NewFanoutWorker(pool, releases, follows, deliveries, preferences, settings, scopes, multiDispatcher)
	if webhookRepo != nil {
		fanout.SetWebhookOutbox(webhookRepo, newProfileRateLimiter())
	}
	if pushDeviceRepo != nil {
		fanout.SetPushOutbox(pushDeviceRepo)
	}
	detector := NewAvailabilityDetector(releases, settings)
	detector.SetFanoutNudge(func() {
		fanout.Nudge()
	})

	system := &System{
		PushStyle:         pushStyle,
		Settings:          settings,
		Releases:          releases,
		Follows:           follows,
		Deliveries:        deliveries,
		Preferences:       preferences,
		Detector:          detector,
		Fanout:            fanout,
		Tickets:           NewTicketStore(redisClient),
		Webhooks:          webhookService,
		PushDevices:       pushDeviceService,
		EmailPrefs:        emailPrefs,
		mailSender:        mailSender,
		emailBrand:        emailBrand,
		EmailAlerts:       emailAlerts,
		webhookRepo:       webhookRepo,
		webhookDispatcher: webhookDispatcher,
		webhookRetry:      webhookRetry,
		pushDeviceRepo:    pushDeviceRepo,
		pushDispatcher:    pushDispatcher,
		pushSender:        pushSenderInst,
		dispatcher:        multiDispatcher,
		pool:              pool,
		stores:            stores,
		scopes:            scopes,
		users:             users,
		logger:            slog.Default().With("component", "notifications.system"),
	}
	if emailPrefs != nil && cipher != nil {
		// emailPrefs is only built with a mail sender, so the dispatcher
		// always accompanies the admission service.
		system.emailVerificationDispatch = newEmailVerificationDispatcher(emailPrefs, cipher, mailSender)
		system.EmailVerification = &EmailVerificationService{store: emailPrefs, cipher: cipher, profile: system.lookupProfile, linkBase: system.emailLinkBase, brand: emailBrand, dispatch: system.emailVerificationDispatch}
	}
	wsDispatcher.payload = system.PayloadForRow
	if emailAlerts != nil {
		emailAlerts.profileName = system.lookupProfileName
	}
	if sender != nil {
		sender.operational = system.DispatchOperational
		sender.posterURL = system.discordPosterURL
	}
	return system
}

// SetEmailArtwork enables posters and dimmed backdrops in notification
// emails, read from store (nil serves provider-hosted artwork only) and
// signed with a key derived from jwtSecret.
func (s *System) SetEmailArtwork(store blobstore.Store, jwtSecret string) {
	if s == nil {
		return
	}
	s.EmailArtwork = NewEmailArtwork(s.pool, store, jwtSecret)
	if s.EmailAlerts != nil {
		s.EmailAlerts.setArtwork(s.EmailArtwork)
	}
}

// ServeEmailArtwork returns the JPEG behind a signed email artwork link.
func (s *System) ServeEmailArtwork(ctx context.Context, token string) ([]byte, error) {
	if s == nil || s.EmailArtwork == nil {
		return nil, ErrEmailArtworkNotFound
	}
	return s.EmailArtwork.Serve(ctx, token)
}

// SetFCMDirect enables the fcm_direct Android push provider. nil leaves it
// off.
func (s *System) SetFCMDirect(direct *FCMDirect) {
	if s == nil || direct == nil {
		return
	}
	if s.PushDevices != nil {
		s.PushDevices.fcmDirect = direct
	}
	if s.pushSender != nil {
		s.pushSender.fcmDirect = direct
	}
}

// SetImageResolver wires presigned poster URLs into notification payloads.
// Optional; without it clients fall back to thumbhash placeholders.
func (s *System) SetImageResolver(resolver ImageURLResolver) {
	if s != nil {
		s.images = resolver
	}
}

// discordPosterURL resolves a delivery's poster to a URL a Discord embed may
// carry, honoring the admin's poster mode: nothing when posters are off,
// public provider CDN URLs when derivable, and — only on the explicit
// "server" opt-in — a presigned URL from this server's own image storage.
// Wired into the Discord send paths as their posterURL hook.
func (s *System) discordPosterURL(ctx context.Context, posterPath, posterSourcePath string) string {
	mode := s.Settings.DiscordPosterMode(ctx)
	if mode == DiscordPostersOff {
		return ""
	}
	if url := embedPosterURL(posterPath, posterSourcePath); url != "" {
		return url
	}
	if mode != DiscordPostersServer || s.images == nil || posterPath == "" {
		return ""
	}
	return s.absoluteArtworkURL(ctx, s.images.PresignImageURL(ctx, posterPath, "poster", ""))
}

// absoluteArtworkURL makes a resolved artwork URL usable outside this server.
// S3 delivery already presigns absolute URLs; local artwork storage signs
// root-relative /api/v2/artwork paths that only a client of this server can
// follow, so they are anchored to server.public_url. Without a public URL the
// embed carries no image rather than a link Discord cannot fetch.
func (s *System) absoluteArtworkURL(ctx context.Context, url string) string {
	if url == "" || !strings.HasPrefix(url, "/") {
		return url
	}
	base := s.emailLinkBase(ctx)
	if base == "" {
		return ""
	}
	return base + url
}

// DisplayForRow renders a delivery's native-notification display and
// attaches absolute backdrop and poster links for rich notifications.
func (s *System) DisplayForRow(ctx context.Context, row DeliveryRow) NotificationDisplay {
	display := BuildNotificationDisplay(row)
	if s == nil {
		return display
	}
	display.ImageURL = s.richArtworkURL(ctx, row.BackdropPath, row.BackdropSourcePath, "backdrop", "w780")
	display.ThumbnailURL = s.richArtworkURL(ctx, row.PosterPath, row.PosterSourcePath, "poster", "w342")
	display.Push = s.PushContentForRow(ctx, row)
	return display
}

// richArtworkURL resolves artwork for a notification a device fetches right
// away: a public provider URL when one exists, else a presigned link to this
// server's storage made absolute.
func (s *System) richArtworkURL(ctx context.Context, path, sourcePath, imageType, size string) string {
	if url := embedPosterURL(path, sourcePath); url != "" {
		return url
	}
	if s.images == nil || path == "" {
		return ""
	}
	return s.absoluteArtworkURL(ctx, s.images.PresignImageURL(ctx, path, imageType, size))
}

// PayloadForRow converts a row to its wire shape, attaching a presigned
// poster URL when an image resolver is configured.
func (s *System) PayloadForRow(ctx context.Context, row DeliveryRow) DeliveryRowPayload {
	payload := PayloadForRow(row)
	payload.Actions = notificationActions(row)
	if s != nil {
		payload.ImageURL = s.richArtworkURL(ctx, row.BackdropPath, row.BackdropSourcePath, "backdrop", "w780")
		payload.ThumbnailURL = s.richArtworkURL(ctx, row.PosterPath, row.PosterSourcePath, "poster", "w342")
		payload.Push = s.PushContentForRow(ctx, row)
	}
	if payload.PosterPath != "" {
		payload.PosterURL = publicArtworkURL(payload.PosterPath)
		if payload.PosterURL == "" && s != nil && s.images != nil {
			payload.PosterURL = s.images.PresignImageURL(ctx, payload.PosterPath, "poster", "")
		}
	}
	return payload
}

// PayloadsForRows converts rows to their wire shape with poster URLs.
func (s *System) PayloadsForRows(ctx context.Context, rows []DeliveryRow) []DeliveryRowPayload {
	payloads := make([]DeliveryRowPayload, 0, len(rows))
	for _, row := range rows {
		payloads = append(payloads, s.PayloadForRow(ctx, row))
	}
	return payloads
}

// Start launches the fanout worker, interest updater, and (when configured)
// the webhook dispatch pool and retry worker under ctx.
func (s *System) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.Fanout.Run(ctx)
	}()
	if s.webhookDispatcher != nil {
		s.wg.Add(2)
		go func() {
			defer s.wg.Done()
			s.webhookDispatcher.Run(ctx)
		}()
		go func() {
			defer s.wg.Done()
			s.webhookRetry.Run(ctx)
		}()
	}
	if s.EmailAlerts != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.EmailAlerts.Run(ctx)
		}()
	}
	if s.emailVerificationDispatch != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.emailVerificationDispatch.Run(ctx)
		}()
	}
	if s.pushDispatcher != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.pushDispatcher.Run(ctx)
		}()
	}
}

// Wait blocks until the background loops exit (after their context is
// canceled), so shutdown can drain in-flight work.
func (s *System) Wait() {
	if s != nil {
		s.wg.Wait()
	}
}

// PurgeProfile removes all notification state for a deleted profile.
// Profiles may live in per-user SQLite stores, so Postgres cascades cannot
// cover this. Interest rows, series mutes, deliveries, preferences,
// destinations, and email prefs are removed for the profile on userID's
// account only.
func (s *System) PurgeProfile(ctx context.Context, userID int, profileID string) error {
	if s == nil || profileID == "" {
		return nil
	}
	if err := s.Follows.DeleteAllForProfile(ctx, userID, profileID); err != nil {
		return fmt.Errorf("purge series follows and mutes: %w", err)
	}
	if err := s.Deliveries.DeleteAllForProfile(ctx, userID, profileID); err != nil {
		return fmt.Errorf("purge deliveries: %w", err)
	}
	if err := s.Preferences.DeleteForProfile(ctx, userID, profileID); err != nil {
		return fmt.Errorf("purge preferences: %w", err)
	}
	if s.webhookRepo != nil {
		if err := s.webhookRepo.DeleteAllForProfile(ctx, userID, profileID); err != nil {
			return fmt.Errorf("purge webhooks: %w", err)
		}
	}
	if s.pushDeviceRepo != nil {
		if err := s.pushDeviceRepo.DeleteAllForProfile(ctx, userID, profileID); err != nil {
			return fmt.Errorf("purge push devices: %w", err)
		}
	}
	if s.EmailPrefs != nil {
		if err := s.EmailPrefs.DeleteForProfile(ctx, userID, profileID); err != nil {
			return fmt.Errorf("purge email prefs: %w", err)
		}
	}
	return nil
}

// SeedAvailability inserts episode_availability and movie_availability for
// every currently playable episode and movie without creating release events,
// then writes the per-library, per-kind seed markers. Idempotent and
// rerunnable; it exists for libraries that predate the notifications feature.
//
// Library selection is load-bearing for flood and loss safety:
//   - already-seeded libraries are skipped: availability there is owned by
//     the scan-end detector, and a silent (emitEvents=false) insert racing an
//     in-flight scan would permanently suppress the claimed episode's release
//     event;
//   - never-fully-scanned libraries are skipped: their catalog is empty or
//     partial, so seed-marking them now would make the first real scan emit
//     release events for the entire back catalog. The detector writes their
//     marker when that first full scan completes.
func (s *System) SeedAvailability(ctx context.Context, progress func(percent int, message string)) error {
	report := func(percent int, message string) {
		if progress != nil {
			progress(percent, message)
		}
	}
	// Each kind seeds independently with its own seed markers, because the
	// episode pass historically marked every scanned library (movie libraries
	// included) with zero movie rows. The flat item kinds come from the
	// registry so a new kind cannot be forgotten here.
	type seedPass struct {
		kind          string
		seedCondition string
		record        func(ctx context.Context, libraryID int, emitEvents bool) (int, int, error)
	}
	passes := []seedPass{{
		kind:          EventKindEpisode,
		seedCondition: `SELECT 1 FROM notification_library_seed_state seed WHERE seed.library_id = mf.id`,
		record:        s.Releases.RecordAvailabilityForLibrary,
	}}
	for _, k := range flatItemKinds {
		passes = append(passes, seedPass{
			kind: k.Kind,
			seedCondition: `SELECT 1 FROM notification_content_seed_state seed
				WHERE seed.library_id = mf.id AND seed.kind = '` + k.Kind + `'`,
			record: func(ctx context.Context, libraryID int, emitEvents bool) (int, int, error) {
				return s.Releases.RecordItemAvailabilityForLibrary(ctx, k, libraryID, emitEvents)
			},
		})
	}
	for passIdx, pass := range passes {
		rows, err := s.pool.Query(ctx, `
			SELECT mf.id
			FROM media_folders mf
			WHERE mf.last_scanned_at IS NOT NULL
			  AND NOT EXISTS (`+pass.seedCondition+`)
			ORDER BY mf.id`)
		if err != nil {
			return fmt.Errorf("list libraries: %w", err)
		}
		libraryIDs := make([]int, 0, 8)
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("scan library id: %w", err)
			}
			libraryIDs = append(libraryIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		totalSeeded := 0
		for i, libraryID := range libraryIDs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			inserted, _, err := pass.record(ctx, libraryID, false)
			if err != nil {
				return fmt.Errorf("seed library %d (%s): %w", libraryID, pass.kind, err)
			}
			if err := s.Releases.MarkContentSeeded(ctx, libraryID, pass.kind); err != nil {
				return fmt.Errorf("mark library %d seeded (%s): %w", libraryID, pass.kind, err)
			}
			totalSeeded += inserted
			// Each pass owns an equal slice of the progress range.
			passSpan := 100 / len(passes)
			passBase := passIdx * passSpan
			report(passBase+(i+1)*passSpan/max(len(libraryIDs), 1),
				fmt.Sprintf("Seeded library %d %s availability (%d new rows)", libraryID, pass.kind, inserted))
		}
		s.logger.InfoContext(ctx, "availability seeding completed",
			"kind", pass.kind, "libraries", len(libraryIDs), "availability_rows", totalSeeded)
	}
	report(100, "Availability seeding completed")
	return nil
}

// RetentionStats reports what a retention pass removed.
type RetentionStats struct {
	DeliveriesDeleted      int64
	EventsDeleted          int64
	StaleEventsDeleted     int64
	WebhookAttemptsDeleted int64
	PushRegistrationsDeleted int64
	// UnboundPushRegistrationsDeleted counts registrations from before
	// login_session_id whose device has no live login.
	UnboundPushRegistrationsDeleted int64
	// Email verification outbox: payloads dropped from terminal rows whose
	// link expired, and receipt rows deleted past the retention window.
	EmailVerificationPayloadsRetired int64
	EmailVerificationReceiptsDeleted int64
}

// RunRetention applies the retention policy: read deliveries past the read
// window, unread past the unread window, processed release events past the
// debug window, unprocessed release events past the fanout staleness horizon,
// inert interest rows, web push subscriptions whose login session ended or, for
// one with no recorded login, whose account has no live one, and push
// registrations whose login ended or, for one with no recorded login, whose
// device has no live one.
func (s *System) RunRetention(ctx context.Context) (RetentionStats, error) {
	var stats RetentionStats
	now := time.Now().UTC()
	readCutoff := now.AddDate(0, 0, -s.Settings.ReadRetentionDays(ctx))
	unreadCutoff := now.AddDate(0, 0, -s.Settings.UnreadRetentionDays(ctx))
	eventCutoff := now.AddDate(0, 0, -s.Settings.EventRetentionDays(ctx))

	deleted, err := s.Deliveries.DeleteOld(ctx, readCutoff, unreadCutoff)
	if err != nil {
		return stats, fmt.Errorf("prune deliveries: %w", err)
	}
	stats.DeliveriesDeleted = deleted

	events, err := s.Releases.DeleteProcessedBefore(ctx, eventCutoff)
	if err != nil {
		return stats, fmt.Errorf("prune release events: %w", err)
	}
	stats.EventsDeleted = events

	// Unprocessed events accumulate without bound when fanout is disabled
	// while availability detection keeps emitting; the fanout worker would
	// suppress them as stale anyway, so retention reclaims them directly.
	staleEvents, err := s.Releases.DeleteUnprocessedBefore(ctx, now.Add(-s.Settings.MaxEventAge(ctx)))
	if err != nil {
		return stats, fmt.Errorf("prune stale release events: %w", err)
	}
	stats.StaleEventsDeleted = staleEvents

	if s.webhookRepo != nil {
		attempts, err := s.webhookRepo.DeleteOldAttempts(ctx, now)
		if err != nil {
			return stats, fmt.Errorf("prune webhook attempts: %w", err)
		}
		stats.WebhookAttemptsDeleted = attempts
	}
	if s.pushDeviceRepo != nil {
		registrations, err := s.pushDeviceRepo.DeleteEndedLoginRegistrations(ctx)
		if err != nil {
			return stats, fmt.Errorf("prune push registrations of ended logins: %w", err)
		}
		stats.PushRegistrationsDeleted = registrations
		unbound, err := s.pushDeviceRepo.DeleteRegistrationsWithNoLogin(ctx)
		if err != nil {
			return stats, fmt.Errorf("prune unbound push registrations: %w", err)
		}
		stats.UnboundPushRegistrationsDeleted = unbound
	}
	if s.EmailPrefs != nil {
		payloads, rows, err := s.EmailPrefs.RetireVerificationDispatch(ctx, now)
		if err != nil {
			return stats, fmt.Errorf("prune email verification outbox: %w", err)
		}
		stats.EmailVerificationPayloadsRetired = payloads
		stats.EmailVerificationReceiptsDeleted = rows
	}
	return stats, nil
}

func backfillKey(userID int, profileID string) string {
	return fmt.Sprintf("%010d|%s", userID, profileID)
}

func (s *System) loadBackfillCheckpoint(ctx context.Context, task string) (string, *time.Time, error) {
	var key *string
	var completedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT last_processed_key, completed_at FROM notification_backfill_state WHERE task = $1`,
		task,
	).Scan(&key, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("load backfill checkpoint: %w", err)
	}
	checkpoint := ""
	if key != nil {
		checkpoint = *key
	}
	return checkpoint, completedAt, nil
}

func (s *System) saveBackfillCheckpoint(ctx context.Context, task, key string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_backfill_state (task, last_processed_key, started_at, updated_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (task) DO UPDATE SET
			last_processed_key = EXCLUDED.last_processed_key,
			updated_at = now()`,
		task, key)
	if err != nil {
		return fmt.Errorf("save backfill checkpoint: %w", err)
	}
	return nil
}

func (s *System) resetBackfillCheckpoint(ctx context.Context, task string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_backfill_state (task, last_processed_key, started_at, updated_at, completed_at)
		VALUES ($1, NULL, now(), now(), NULL)
		ON CONFLICT (task) DO UPDATE SET
			last_processed_key = NULL,
			started_at = now(),
			updated_at = now(),
			completed_at = NULL`,
		task)
	if err != nil {
		return fmt.Errorf("reset backfill checkpoint: %w", err)
	}
	return nil
}

func (s *System) completeBackfillCheckpoint(ctx context.Context, task string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE notification_backfill_state SET completed_at = now(), updated_at = now() WHERE task = $1`,
		task)
	if err != nil {
		return fmt.Errorf("complete backfill checkpoint: %w", err)
	}
	return nil
}
