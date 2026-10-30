package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// AutoMovieCollectionsSyncer is the collections surface the task runs.
type AutoMovieCollectionsSyncer interface {
	SyncAutoMovieCollections(ctx context.Context, progress func(percent float64, message string)) (handlers.AutoCollectionsReport, error)
}

// SyncAutoMovieCollectionsTask builds the TMDB franchise collections of
// movie libraries that have automatic collections on.
type SyncAutoMovieCollectionsTask struct {
	syncer AutoMovieCollectionsSyncer
}

func NewSyncAutoMovieCollectionsTask(syncer AutoMovieCollectionsSyncer) *SyncAutoMovieCollectionsTask {
	return &SyncAutoMovieCollectionsTask{syncer: syncer}
}

func (t *SyncAutoMovieCollectionsTask) Key() string  { return handlers.AutoMovieCollectionsTaskKey }
func (t *SyncAutoMovieCollectionsTask) Name() string { return "Sync Automatic Movie Collections" }
func (t *SyncAutoMovieCollectionsTask) Description() string {
	return "Creates a collection for each TMDB collection a movie library holds two or more films of, where the library has automatic collections on"
}
func (t *SyncAutoMovieCollectionsTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryMetadata
}
func (t *SyncAutoMovieCollectionsTask) IsHidden() bool { return false }

func (t *SyncAutoMovieCollectionsTask) ServesLibrary(libraryType string) bool {
	kind := librarykind.Of(libraryType)
	return kind.Movie || kind.Mixed
}

func (t *SyncAutoMovieCollectionsTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeDaily, TimeOfDay: "04:30"}}
}

func (t *SyncAutoMovieCollectionsTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	report, err := t.syncer.SyncAutoMovieCollections(ctx, progress.Report)
	if data, marshalErr := json.Marshal(report); marshalErr == nil {
		progress.SetResultData(data)
	}
	if err != nil {
		return fmt.Errorf("sync automatic movie collections: %w", err)
	}
	return nil
}
