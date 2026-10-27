package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
	"github.com/Silo-Server/silo-server/internal/themedownload"
)

// ThemeSongDownloader is the themedownload surface the task runs.
type ThemeSongDownloader interface {
	Enabled(ctx context.Context) (bool, error)
	Run(ctx context.Context, force bool, progress func(percent float64, message string)) (themedownload.Report, error)
}

// DownloadThemeSongsTask fetches TV theme songs into blob storage.
type DownloadThemeSongsTask struct {
	downloader ThemeSongDownloader
}

func NewDownloadThemeSongsTask(downloader ThemeSongDownloader) *DownloadThemeSongsTask {
	return &DownloadThemeSongsTask{downloader: downloader}
}

func (t *DownloadThemeSongsTask) Key() string  { return "download_theme_songs" }
func (t *DownloadThemeSongsTask) Name() string { return "Download TV Theme Songs" }
func (t *DownloadThemeSongsTask) Description() string {
	return "Downloads theme songs for TV series without one in their folder, into Silo's storage"
}
func (t *DownloadThemeSongsTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryMetadata
}
func (t *DownloadThemeSongsTask) IsHidden() bool { return false }

func (t *DownloadThemeSongsTask) ServesLibrary(libraryType string) bool {
	kind := librarykind.Of(libraryType)
	return kind.TV || kind.Mixed
}

func (t *DownloadThemeSongsTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeDaily, TimeOfDay: "04:00"}}
}

// ShouldRun skips scheduled runs while downloading is switched off.
func (t *DownloadThemeSongsTask) ShouldRun(ctx context.Context) (bool, error) {
	return t.downloader.Enabled(ctx)
}

func (t *DownloadThemeSongsTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	enabled, err := t.downloader.Enabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		progress.Report(100, "Theme song downloads are switched off")
		return nil
	}
	report, err := t.downloader.Run(ctx, false, progress.Report)
	if data, marshalErr := json.Marshal(report); marshalErr == nil {
		progress.SetResultData(data)
	}
	if err != nil {
		return fmt.Errorf("download theme songs: %w", err)
	}
	return nil
}
