package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// SeedContentAvailabilityTask inserts episode_availability and
// movie_availability rows for every currently playable episode and movie
// without creating release events, then writes the per-library, per-kind seed
// markers. Running it is what allows release events to flow for libraries
// that predate the notifications feature; rerunning is a cheap idempotent
// repair pass.
type SeedContentAvailabilityTask struct {
	system *notifications.System
}

// NewSeedContentAvailabilityTask creates the seeding task.
func NewSeedContentAvailabilityTask(system *notifications.System) *SeedContentAvailabilityTask {
	return &SeedContentAvailabilityTask{system: system}
}

func (t *SeedContentAvailabilityTask) Key() string  { return "seed_content_availability" }
func (t *SeedContentAvailabilityTask) Name() string { return "Seed Content Availability" }
func (t *SeedContentAvailabilityTask) Description() string {
	return "Records the existing episode and movie back-catalog as already-released so new-content notifications only fire for items that arrive afterwards."
}
func (t *SeedContentAvailabilityTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategorySystem
}
func (t *SeedContentAvailabilityTask) IsHidden() bool { return true }

func (t *SeedContentAvailabilityTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeStartup}}
}

func (t *SeedContentAvailabilityTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t == nil || t.system == nil {
		progress.Report(100, "Notifications are not configured")
		return nil
	}
	progress.Report(0, "Seeding content availability")
	if err := t.system.SeedAvailability(ctx, func(percent int, message string) {
		progress.Report(float64(percent), message)
	}); err != nil {
		return fmt.Errorf("seeding content availability: %w", err)
	}
	progress.Report(100, "Content availability seeded")
	return nil
}

// NotificationsRetentionTask applies the notification retention policy: read
// inbox rows past the read window, unread rows past the unread window,
// and processed release events past the debug window.
type NotificationsRetentionTask struct {
	system *notifications.System
}

// NewNotificationsRetentionTask creates the retention task.
func NewNotificationsRetentionTask(system *notifications.System) *NotificationsRetentionTask {
	return &NotificationsRetentionTask{system: system}
}

func (t *NotificationsRetentionTask) Key() string  { return "notifications_retention" }
func (t *NotificationsRetentionTask) Name() string { return "Clean Up Notifications" }
func (t *NotificationsRetentionTask) Description() string {
	return "Prunes old notifications and processed release events according to the retention settings."
}
func (t *NotificationsRetentionTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategorySystem
}
func (t *NotificationsRetentionTask) IsHidden() bool { return false }

func (t *NotificationsRetentionTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeDaily, TimeOfDay: "05:00"}}
}

func (t *NotificationsRetentionTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t == nil || t.system == nil {
		progress.Report(100, "Notifications are not configured")
		return nil
	}
	progress.Report(0, "Applying notification retention policy")
	stats, err := t.system.RunRetention(ctx)
	if err != nil {
		return fmt.Errorf("notification retention: %w", err)
	}
	if data, err := json.Marshal(stats); err == nil {
		progress.SetResultData(data)
	}
	progress.Report(100, fmt.Sprintf(
		"Removed %d notifications, %d release events",
		stats.DeliveriesDeleted, stats.EventsDeleted))
	return nil
}
