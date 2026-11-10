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
	return "Measures episode thumbnails and, in libraries that ask for it, takes a frame from the video when the image is below the library's quality target. Keeps going until nothing is left to do or you stop it"
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

// Execute keeps generating, batch after batch, until a batch finds nothing
// left to measure or decide, or the administrator stops the task. The 15
// minute trigger only starts it again after it has run out of work. A batch
// that makes no progress (for example, no transcode node could be reserved)
// ends the run so it does not spin.
func (t *GenerateEpisodeFrameStillsTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	var total episodeframes.Report
	batches := 0
	for {
		progress.Report(0, fmt.Sprintf("Working through episode thumbnails (batch %d, %d frames saved so far)", batches+1, total.Framed))
		report, err := t.generator.Generate(ctx, t.measureLimit, t.frameLimit)
		total.Measured += report.Measured
		total.Checked += report.Checked
		total.Framed += report.Framed
		total.ProviderOK += report.ProviderOK
		total.Failed += report.Failed
		batches++
		if ctx.Err() != nil {
			progress.Report(100, t.summary(total, batches, "stopped"))
			return nil
		}
		if err != nil {
			return fmt.Errorf("episode thumbnails from video: %w", err)
		}
		if report.Measured+report.Framed+report.ProviderOK+report.Failed == 0 {
			break
		}
	}
	progress.Report(100, t.summary(total, batches, "finished"))
	return nil
}

func (t *GenerateEpisodeFrameStillsTask) summary(total episodeframes.Report, batches int, how string) string {
	return fmt.Sprintf("%s after %d batches: measured %d thumbnails; checked %d episodes: %d frames saved, %d provider images kept, %d failed",
		how, batches, total.Measured, total.Checked, total.Framed, total.ProviderOK, total.Failed)
}
