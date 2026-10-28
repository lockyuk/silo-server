package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/episodeframes"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// EpisodeFrameStillGenerator measures episode stills and takes video frames
// for the ones a library wants replaced; episodeframes.Generator.
type EpisodeFrameStillGenerator interface {
	Generate(ctx context.Context, measureLimit, frameLimit int) (episodeframes.Report, error)
}

// GenerateEpisodeFrameStillsTask measures every episode still and gives
// episodes in libraries that ask for it a thumbnail from a frame of their
// video.
type GenerateEpisodeFrameStillsTask struct {
	generator    EpisodeFrameStillGenerator
	measureLimit int
	frameLimit   int
}

func NewGenerateEpisodeFrameStillsTask(generator EpisodeFrameStillGenerator, measureLimit, frameLimit int) *GenerateEpisodeFrameStillsTask {
	return &GenerateEpisodeFrameStillsTask{generator: generator, measureLimit: measureLimit, frameLimit: frameLimit}
}

func (t *GenerateEpisodeFrameStillsTask) Key() string  { return "generate_episode_frame_stills" }
func (t *GenerateEpisodeFrameStillsTask) Name() string { return "Episode Thumbnails From Video" }
func (t *GenerateEpisodeFrameStillsTask) Description() string {
	return "Measures episode thumbnails and, in libraries that ask for it, takes a frame from the video when no full-HD image exists"
}
func (t *GenerateEpisodeFrameStillsTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryLibrary
}
func (t *GenerateEpisodeFrameStillsTask) IsHidden() bool { return false }

func (t *GenerateEpisodeFrameStillsTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeStartup},
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: int64((15 * time.Minute) / time.Millisecond)},
	}
}

func (t *GenerateEpisodeFrameStillsTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	progress.Report(0, "Measuring episode thumbnails")
	report, err := t.generator.Generate(ctx, t.measureLimit, t.frameLimit)
	if err != nil {
		return fmt.Errorf("episode thumbnails from video: %w", err)
	}
	progress.Report(100, fmt.Sprintf("Measured %d thumbnails; checked %d episodes: %d frames saved, %d provider images kept, %d failed",
		report.Measured, report.Checked, report.Framed, report.ProviderOK, report.Failed))
	return nil
}
