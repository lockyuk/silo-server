package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/episodeframes"
)

type sequenceGenerator struct {
	reports []episodeframes.Report
	calls   int
	cancel  context.CancelFunc
	cancelAt int
}

func (g *sequenceGenerator) Generate(context.Context, int, int) (episodeframes.Report, error) {
	r := g.reports[min(g.calls, len(g.reports)-1)]
	g.calls++
	if g.cancel != nil && g.calls == g.cancelAt {
		g.cancel()
	}
	return r, nil
}

type quietProgress struct{}

func (quietProgress) Report(float64, string)       {}
func (quietProgress) SetResultData(json.RawMessage) {}

func TestEpisodeFrameStillsRunsUntilNothingIsLeft(t *testing.T) {
	g := &sequenceGenerator{reports: []episodeframes.Report{
		{Measured: 5, Framed: 3},
		{ProviderOK: 4},
		{},
	}}
	task := NewGenerateEpisodeFrameStillsTask(g, 5000, 150)
	if err := task.Execute(context.Background(), quietProgress{}); err != nil {
		t.Fatal(err)
	}
	if g.calls != 3 {
		t.Fatalf("batches = %d, want 3 (two with work, one empty)", g.calls)
	}
}

func TestEpisodeFrameStillsStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &sequenceGenerator{reports: []episodeframes.Report{{Framed: 1}}, cancel: cancel, cancelAt: 2}
	task := NewGenerateEpisodeFrameStillsTask(g, 5000, 150)
	if err := task.Execute(ctx, quietProgress{}); err != nil {
		t.Fatal(err)
	}
	if g.calls != 2 {
		t.Fatalf("batches = %d, want 2 (stopped after the second)", g.calls)
	}
}

func TestEpisodeFrameStillsEndsWhenABatchMakesNoProgress(t *testing.T) {
	// A batch that only "checked" episodes (for instance no transcode node
	// could be reserved) must not loop.
	g := &sequenceGenerator{reports: []episodeframes.Report{{Checked: 1}}}
	task := NewGenerateEpisodeFrameStillsTask(g, 5000, 150)
	if err := task.Execute(context.Background(), quietProgress{}); err != nil {
		t.Fatal(err)
	}
	if g.calls != 1 {
		t.Fatalf("batches = %d, want 1", g.calls)
	}
}
