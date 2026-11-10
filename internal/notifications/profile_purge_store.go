package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// WrapUserStoreProvider decorates the shared user-store provider so deleting a
// profile also purges its notification state. Every other store capability is
// forwarded unchanged.
func WrapUserStoreProvider(inner userstore.UserStoreProvider, system *System) userstore.UserStoreProvider {
	if inner == nil || system == nil {
		return inner
	}
	tracked := &interestTrackingProvider{inner: inner, system: system}
	if profiles, ok := inner.(transactionalProfileCreator); ok {
		return &interestTrackingProviderWithProfileTransaction{interestTrackingProvider: tracked, transactionalProfileCreator: profiles}
	}
	return tracked
}

// Account creation probes this whole-provider capability before inserting its
// default profile. Preserve it only when the selected backend can join that
// PostgreSQL transaction; advertising it for SQLite would change its fallback.
type transactionalProfileCreator interface {
	CreateProfileInTransaction(context.Context, pgx.Tx, int, userstore.Profile) error
}

type interestTrackingProviderWithProfileTransaction struct {
	*interestTrackingProvider
	transactionalProfileCreator
}

type interestTrackingProvider struct {
	inner  userstore.UserStoreProvider
	system *System
}

// ListAllSectionOverrides preserves the optional account-wide enumeration
// capability of the wrapped store.
func (s *interestTrackingStore) ListAllSectionOverrides(ctx context.Context) ([]userstore.SectionOverride, error) {
	enumerator, ok := s.UserStore.(userstore.SectionOverrideEnumerator)
	if !ok {
		return nil, errors.New("section override enumeration is not supported")
	}
	return enumerator.ListAllSectionOverrides(ctx)
}

// ManualCollectionsHolding preserves Add to collection's membership read;
// both backing stores implement it.
func (s *interestTrackingStore) ManualCollectionsHolding(ctx context.Context, creatorProfileID, mediaItemID string) ([]string, error) {
	reader, ok := s.UserStore.(userstore.CollectionMembershipReader)
	if !ok {
		return nil, errors.New("collection membership reads are not supported")
	}
	return reader.ManualCollectionsHolding(ctx, creatorProfileID, mediaItemID)
}

func (p *interestTrackingProvider) ForUser(ctx context.Context, userID int) (userstore.UserStore, error) {
	store, err := p.inner.ForUser(ctx, userID)
	if err != nil || store == nil {
		return store, err
	}
	tracked := &interestTrackingStore{UserStore: store, userID: userID, system: p.system}
	// Preserve the interface upgrades callers probe for. These are conditional
	// on the backing store: advertising a capability it does not have would
	// send callers down a fast path that can only fail.
	registry, hasDevices := store.(userstore.DeviceRegistry)
	rollup, hasRollup := store.(userstore.SeriesEpisodeRollupStore)
	completion, hasCompletion := store.(userstore.EpisodeParentCompletionStore)
	var wrapped userstore.UserStore = tracked
	switch {
	case hasDevices && hasRollup && hasCompletion:
		wrapped = &interestTrackingStoreWithDevicesRollupAndCompletion{
			interestTrackingStoreWithDevicesAndRollup: &interestTrackingStoreWithDevicesAndRollup{
				interestTrackingStore: tracked, DeviceRegistry: registry, SeriesEpisodeRollupStore: rollup,
			},
			EpisodeParentCompletionStore: completion,
		}
	case hasDevices && hasCompletion:
		wrapped = &interestTrackingStoreWithDevicesAndCompletion{
			interestTrackingStoreWithDevices: &interestTrackingStoreWithDevices{
				interestTrackingStore: tracked, DeviceRegistry: registry,
			},
			EpisodeParentCompletionStore: completion,
		}
	case hasRollup && hasCompletion:
		wrapped = &interestTrackingStoreWithRollupAndCompletion{
			interestTrackingStoreWithRollup: &interestTrackingStoreWithRollup{
				interestTrackingStore: tracked, SeriesEpisodeRollupStore: rollup,
			},
			EpisodeParentCompletionStore: completion,
		}
	case hasCompletion:
		wrapped = &interestTrackingStoreWithCompletion{
			interestTrackingStore: tracked, EpisodeParentCompletionStore: completion,
		}
	case hasDevices && hasRollup:
		wrapped = &interestTrackingStoreWithDevicesAndRollup{
			interestTrackingStore:    tracked,
			DeviceRegistry:           registry,
			SeriesEpisodeRollupStore: rollup,
		}
	case hasDevices:
		wrapped = &interestTrackingStoreWithDevices{
			interestTrackingStore: tracked,
			DeviceRegistry:        registry,
		}
	case hasRollup:
		wrapped = &interestTrackingStoreWithRollup{
			interestTrackingStore:    tracked,
			SeriesEpisodeRollupStore: rollup,
		}
	}
	return preserveDeviceSettings(wrapped, store), nil
}

func (p *interestTrackingProvider) Close() error {
	return p.inner.Close()
}

type interestTrackingStore struct {
	userstore.UserStore
	userID int
	system *System
}

// Onboarding progress is forwarded explicitly: the decorator intercepts no
// onboarding write, and both backing stores (SQLite and Postgres) implement it.
func (s *interestTrackingStore) ReadOnboardingProgress(ctx context.Context, profileID, tourID string) (*userstore.OnboardingProgress, error) {
	progress, ok := s.UserStore.(userstore.OnboardingProgressStore)
	if !ok {
		return nil, errors.New("onboarding progress is unavailable on the backing store")
	}
	return progress.ReadOnboardingProgress(ctx, profileID, tourID)
}
func (s *interestTrackingStore) SaveOnboardingProgress(ctx context.Context, state userstore.OnboardingState, expected int64) (*userstore.OnboardingProgress, error) {
	progress, ok := s.UserStore.(userstore.OnboardingProgressStore)
	if !ok {
		return nil, errors.New("onboarding progress is unavailable on the backing store")
	}
	return progress.SaveOnboardingProgress(ctx, state, expected)
}

type interestTrackingStoreWithDevices struct {
	*interestTrackingStore
	userstore.DeviceRegistry
}

// interestTrackingStoreWithRollup adds the series-rollup capability only when
// the backing store actually has it. Unlike the other capabilities, this one
// cannot be forwarded unconditionally: the per-user SQLite backend has no
// catalog tables and genuinely cannot answer the query, and callers treat
// "implements the interface" as "can do this". Advertising it anyway would
// send every jellycompat series request down the fast path to fail, logging a
// warning each time before falling back — turning an expected capability
// absence into recurring noise on successful requests.
type interestTrackingStoreWithRollup struct {
	*interestTrackingStore
	userstore.SeriesEpisodeRollupStore
}

type interestTrackingStoreWithDevicesAndRollup struct {
	*interestTrackingStore
	userstore.DeviceRegistry
	userstore.SeriesEpisodeRollupStore
}

// Completion reads also need catalog tables, so preserve this capability only
// for supporting backends while retaining all mutation hooks on the base wrapper.
type interestTrackingStoreWithCompletion struct {
	*interestTrackingStore
	userstore.EpisodeParentCompletionStore
}

type interestTrackingStoreWithDevicesAndCompletion struct {
	*interestTrackingStoreWithDevices
	userstore.EpisodeParentCompletionStore
}

type interestTrackingStoreWithRollupAndCompletion struct {
	*interestTrackingStoreWithRollup
	userstore.EpisodeParentCompletionStore
}

type interestTrackingStoreWithDevicesRollupAndCompletion struct {
	*interestTrackingStoreWithDevicesAndRollup
	userstore.EpisodeParentCompletionStore
}

var _ userstore.SettingValueCompareAndSetter = (*interestTrackingStore)(nil)
var _ userstore.SettingMutationTransactioner = (*interestTrackingStore)(nil)
var _ userstore.SettingValueCompareAndSetter = (*interestTrackingStoreWithDevices)(nil)
var _ userstore.SettingMutationTransactioner = (*interestTrackingStoreWithDevices)(nil)

// Optional store capabilities must survive the decorator. Embedding the
// UserStore interface promotes only that interface's methods, so each of these
// needs an explicit forward below; the assertions make a missing one a compile
// error instead of a silent production slowdown.
//
// SeriesEpisodeRollupStore and EpisodeParentCompletionStore are conditional
// on the backing store, so they live on the wrapper types above rather than
// being forwarded unconditionally.
var _ userstore.WatchedBatchWriter = (*interestTrackingStore)(nil)
var _ userstore.ItemViewerStateQueuer = (*interestTrackingStore)(nil)
var _ userstore.VisibleHistoryAdder = (*interestTrackingStore)(nil)
var _ userstore.HistoryVisibilityStore = (*interestTrackingStore)(nil)
var _ userstore.WatchedBatchWriter = (*interestTrackingStoreWithDevices)(nil)
var _ userstore.VisibleHistoryAdder = (*interestTrackingStoreWithDevices)(nil)
var _ userstore.HistoryVisibilityStore = (*interestTrackingStoreWithDevices)(nil)
var _ userstore.ItemUserFlagStore = (*interestTrackingStore)(nil)
var _ userstore.SeriesEpisodeRollupStore = (*interestTrackingStoreWithRollup)(nil)
var _ userstore.SeriesEpisodeRollupStore = (*interestTrackingStoreWithDevicesAndRollup)(nil)
var _ userstore.DeviceRegistry = (*interestTrackingStoreWithDevicesAndRollup)(nil)

var _ userstore.EpisodeParentCompletionStore = (*interestTrackingStoreWithCompletion)(nil)
var _ userstore.EpisodeParentCompletionStore = (*interestTrackingStoreWithDevicesAndCompletion)(nil)
var _ userstore.EpisodeParentCompletionStore = (*interestTrackingStoreWithRollupAndCompletion)(nil)
var _ userstore.EpisodeParentCompletionStore = (*interestTrackingStoreWithDevicesRollupAndCompletion)(nil)

// WithPreferenceSettingsTransaction preserves the optional atomic-settings
// capability of the wrapped store. Preference writes do not affect interest
// signals, so the transaction can pass through unchanged; keeping the method
// on the decorator is what lets settings handlers reach the real backend's
// transaction boundary in production.
func (s *interestTrackingStore) WithPreferenceSettingsTransaction(
	ctx context.Context,
	fn func(userstore.PreferenceSettingsWriter) error,
) error {
	transactioner, ok := s.UserStore.(userstore.PreferenceSettingsTransactioner)
	if !ok {
		return fmt.Errorf("wrapped user store does not support atomic preference settings synchronization")
	}
	return transactioner.WithPreferenceSettingsTransaction(ctx, fn)
}

// CompareAndSetSettingValue preserves the semantic-document CAS capability of
// the wrapped store. Settings writes do not affect notification interests, so
// this decorator must not intercept or downgrade the backend primitive.
func (s *interestTrackingStore) CompareAndSetSettingValue(
	ctx context.Context,
	id userstore.SettingIdentity,
	value json.RawMessage,
	expectedRevision int64,
) (*userstore.SettingValue, error) {
	cas, ok := s.UserStore.(userstore.SettingValueCompareAndSetter)
	if !ok {
		return nil, fmt.Errorf("wrapped user store does not support atomic setting updates")
	}
	return cas.CompareAndSetSettingValue(ctx, id, value, expectedRevision)
}

// WithSettingMutationTransaction preserves the durable setting+receipt
// transaction used by mutation IDs. Passing the transaction-scoped writer
// through unchanged keeps both operations on the concrete backend transaction.
func (s *interestTrackingStore) WithSettingMutationTransaction(
	ctx context.Context,
	mutationID string,
	fn func(userstore.SettingMutationWriter) error,
) error {
	transactioner, ok := s.UserStore.(userstore.SettingMutationTransactioner)
	if !ok {
		return fmt.Errorf("wrapped user store does not support atomic idempotent setting mutations")
	}
	return transactioner.WithSettingMutationTransaction(ctx, mutationID, fn)
}

// Preserve coherent preference reads when the store is wrapped for notifications.
func (s *interestTrackingStore) WithPreferenceSettingsSnapshot(ctx context.Context, fn func(userstore.PreferenceSettingsReader) error) error {
	reader, ok := s.UserStore.(userstore.PreferenceSettingsSnapshotter)
	if !ok {
		return fmt.Errorf("wrapped user store does not support preference snapshots")
	}
	return reader.WithPreferenceSettingsSnapshot(ctx, fn)
}

// progressSessionGap separates watch sessions. A progress write that lands
// more than this long after the stored row's stamp, by its own stamp or by
// the clock, queues a recompute even without a state transition: resuming
// playback, or a late import, lifts a Home removal (an active series drop,
// or a Continue Watching dismissal held for the old progress stamp), while
// playback ticks within one session stay free.
const progressSessionGap = 10 * time.Minute

func (s *interestTrackingStore) ListJellycompatProgressDates(ctx context.Context, profileID string, ids []string) (map[string]string, error) {
	reader, ok := s.UserStore.(interface {
		ListJellycompatProgressDates(context.Context, string, []string) (map[string]string, error)
	})
	if !ok {
		return nil, nil
	}
	return reader.ListJellycompatProgressDates(ctx, profileID, ids)
}

// --- Optional store capabilities.
//
// AddVisibleHistory forwards the watermark-aware history insert. The generic
// fallback needs two round-trips (timestamp lookup, then insert) to do the
// same job.
func (s *interestTrackingStore) AddVisibleHistory(ctx context.Context, entry userstore.WatchHistoryEntry) (userstore.WatchHistoryEntry, error) {
	adder, ok := s.UserStore.(userstore.VisibleHistoryAdder)
	if !ok {
		return userstore.AddVisibleHistory(ctx, s.UserStore, entry)
	}
	return adder.AddVisibleHistory(ctx, entry)
}

// VisibleHistoryTimestamps forwards the batched watermark lookup; without it
// callers assume a single wall-clock timestamp for every item.
func (s *interestTrackingStore) VisibleHistoryTimestamps(ctx context.Context, profileID string, mediaItemIDs []string, at time.Time) (map[string]string, error) {
	visibility, ok := s.UserStore.(userstore.HistoryVisibilityStore)
	if !ok {
		return userstore.VisibleHistoryTimestamps(ctx, s.UserStore, profileID, mediaItemIDs, at)
	}
	return visibility.VisibleHistoryTimestamps(ctx, profileID, mediaItemIDs, at)
}

// DeleteProfile purges notification state alongside the profile itself;
// profiles may live outside Postgres, so no cascade covers these tables.
// The purge is best-effort: a failure is logged, never surfaced as a
// profile-deletion failure (the retention task prunes leftovers).
func (s *interestTrackingStore) DeleteProfile(ctx context.Context, id string) error {
	err := s.UserStore.DeleteProfile(ctx, id)
	if err == nil {
		s.purgeProfile(ctx, id)
	}
	return err
}

func (s *interestTrackingStore) purgeProfile(ctx context.Context, id string) {
	purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if purgeErr := s.system.PurgeProfile(purgeCtx, s.userID, id); purgeErr != nil {
		slog.WarnContext(ctx, "notifications: profile purge failed", "component", "notifications", "profile_id", id, "error", purgeErr)
	}
}

// DeleteProfileAudited is DeleteProfile through the wrapped store's audited
// delete. A backend without one deletes the profile without an audit row.
func (s *interestTrackingStore) DeleteProfileAudited(ctx context.Context, id string, audit func(pgx.Tx, *userstore.Profile) error) error {
	deleter, ok := s.UserStore.(userstore.AuditedProfileDeleter)
	if !ok {
		return s.DeleteProfile(ctx, id)
	}
	err := deleter.DeleteProfileAudited(ctx, id, audit)
	if err == nil {
		s.purgeProfile(ctx, id)
	}
	return err
}

// Preserve the optional device settings capability without claiming support
// on backends that cannot page or atomically clear devices. Keep the existing
// concrete decorator so its other optional capabilities survive as well.
func preserveDeviceSettings(wrapped, inner userstore.UserStore) userstore.UserStore {
	devices, ok := inner.(userstore.DeviceSettingsStore)
	if !ok {
		return wrapped
	}
	switch w := wrapped.(type) {
	case *interestTrackingStoreWithDevicesRollupAndCompletion:
		return &struct {
			*interestTrackingStoreWithDevicesRollupAndCompletion
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithDevicesAndCompletion:
		return &struct {
			*interestTrackingStoreWithDevicesAndCompletion
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithRollupAndCompletion:
		return &struct {
			*interestTrackingStoreWithRollupAndCompletion
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithCompletion:
		return &struct {
			*interestTrackingStoreWithCompletion
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithDevicesAndRollup:
		return &struct {
			*interestTrackingStoreWithDevicesAndRollup
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithDevices:
		return &struct {
			*interestTrackingStoreWithDevices
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStoreWithRollup:
		return &struct {
			*interestTrackingStoreWithRollup
			userstore.DeviceSettingsStore
		}{w, devices}
	case *interestTrackingStore:
		return &struct {
			*interestTrackingStore
			userstore.DeviceSettingsStore
		}{w, devices}
	default:
		return wrapped
	}
}

// The notification decorator preserves the provider's storage for every account.
func (p *interestTrackingProvider) SupportsAtomicSectionProfileReset(pool *pgxpool.Pool) bool {
	provider, ok := p.inner.(userstore.SectionProfileResetProvider)
	return ok && provider.SupportsAtomicSectionProfileReset(pool)
}

// The decorator hooks writes only, so reading the shared tables directly
// misses nothing it does.
func (p *interestTrackingProvider) StoresAccountsIn(pool *pgxpool.Pool) bool {
	provider, ok := p.inner.(interface{ StoresAccountsIn(*pgxpool.Pool) bool })
	return ok && provider.StoresAccountsIn(pool)
}

func (s *interestTrackingStore) ListAdminSettingValuesPage(ctx context.Context, after userstore.SettingIdentity, limit int) ([]userstore.SettingValue, bool, error) {
	pager, ok := s.UserStore.(userstore.AdminSettingValuePager)
	if !ok {
		return nil, false, fmt.Errorf("administrator setting pagination is unsupported")
	}
	return pager.ListAdminSettingValuesPage(ctx, after, limit)
}

// MarkWatchedBatch forwards the transactional batch mark-watched write, whose
// whole purpose is that a large series commits as one unit. Falling through to
// the per-target loop would restore the partial-write window it removes.
func (s *interestTrackingStore) MarkWatchedBatch(
	ctx context.Context,
	profileID string,
	targets []userstore.MarkWatchedTarget,
	entries []userstore.WatchHistoryEntry,
) ([]userstore.WatchHistoryEntry, error) {
	writer, ok := s.UserStore.(userstore.WatchedBatchWriter)
	if !ok {
		return userstore.MarkWatchedBatch(ctx, s.UserStore, profileID, targets, entries)
	}
	return writer.MarkWatchedBatch(ctx, profileID, targets, entries)
}

// ApplyJellycompatProgress forwards the atomic leaf edit.
func (s *interestTrackingStore) ApplyJellycompatProgress(ctx context.Context, profileID string, edit userstore.JellycompatProgressEdit) error {
	writer, ok := s.UserStore.(userstore.JellycompatProgressEditor)
	if !ok {
		return fmt.Errorf("atomic user progress updates unavailable")
	}
	return writer.ApplyJellycompatProgress(ctx, profileID, edit)
}

// ApplyJellycompatParent forwards the atomic parent edit.
func (s *interestTrackingStore) ApplyJellycompatParent(ctx context.Context, profileID string, edit userstore.JellycompatParentEdit) error {
	writer, ok := s.UserStore.(userstore.JellycompatParentEditor)
	if !ok {
		return fmt.Errorf("atomic parent user data updates unavailable")
	}
	return writer.ApplyJellycompatParent(ctx, profileID, edit)
}
