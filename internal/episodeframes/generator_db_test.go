package episodeframes

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/models"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testJPEG is a gradient, bright and detailed enough to pass the dark check.
func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		v := uint8(40 + x*200/width)
		for y := range height {
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeFiles struct{}

func (fakeFiles) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	return &models.MediaFile{ID: id}, nil
}

// fakeFrames returns a frame as wide as the file's video.
type fakeFrames struct {
	t      *testing.T
	widths map[int]int
	calls  int
}

func (f *fakeFrames) ExtractStill(_ context.Context, file *models.MediaFile, _ float64) ([]byte, error) {
	f.calls++
	w := f.widths[file.ID]
	return testJPEG(f.t, w, w*9/16), nil
}

// fakeArtwork is both the cacher and the store the generator reads.
type fakeArtwork struct {
	objects map[string][]byte
	n       int
}

func (a *fakeArtwork) CacheEpisodeFrameStill(_ context.Context, data []byte, seriesID string, season, episode int) (string, string, error) {
	a.n++
	key := fmt.Sprintf("local/series/%s/seasons/%d/episodes/%d/frame/still/original.%d.jpg", seriesID, season, episode, a.n)
	a.objects[key] = data
	return key, "thumb", nil
}

func (a *fakeArtwork) Get(_ context.Context, key string) (io.ReadCloser, blobstore.ObjectInfo, error) {
	data, ok := a.objects[key]
	if !ok {
		return nil, blobstore.ObjectInfo{}, blobstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), blobstore.ObjectInfo{}, nil
}

func TestGenerateFramesOnlyBelowFullHD(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, episode_frame_stills, episode_frame_position)
		VALUES ('series', 'frames-test', 'below_full_hd', 20) RETURNING id`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id = 'frames-series'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id = $1`, folderID)
	})
	exec(`INSERT INTO media_items (content_id, type, title, status) VALUES ('frames-series', 'series', 'Frames', 'matched')`)

	art := &fakeArtwork{objects: map[string][]byte{
		"tvdb/e2/original.webp": testJPEG(t, 640, 360),
		"tmdb/e3/original.webp": testJPEG(t, 1920, 1080),
		"tmdb/e4/original.webp": testJPEG(t, 1280, 720),
	}}
	episodes := []struct {
		id, path, source string
		fileWidth        int
	}{
		{"frames-e1", "", "", 1920}, // no still: framed
		{"frames-e2", "tvdb/e2/original.webp", "tvdb://banners/e2.jpg", 1920}, // 640 wide: framed
		{"frames-e3", "tmdb/e3/original.webp", "tmdb://still/e3.jpg", 3840},   // full HD: kept
		{"frames-e4", "tmdb/e4/original.webp", "tmdb://still/e4.jpg", 1280},   // video no wider: kept
	}
	frames := &fakeFrames{t: t, widths: map[int]int{}}
	fileIDs := map[string]int{}
	for i, e := range episodes {
		exec(`INSERT INTO episodes (content_id, series_id, season_number, episode_number, still_path, still_source_path)
			VALUES ($1, 'frames-series', 1, $2, NULLIF($3, ''), $4)`, e.id, i+1, e.path, e.source)
		exec(`INSERT INTO episode_libraries (episode_id, media_folder_id) VALUES ($1, $2)`, e.id, folderID)
		var fileID int
		if err := pool.QueryRow(ctx, `INSERT INTO media_files (media_folder_id, file_path, episode_id, duration)
			VALUES ($1, $2, $3, 1500) RETURNING id`, folderID, "/tv/frames/"+e.id+".mkv", e.id).Scan(&fileID); err != nil {
			t.Fatal(err)
		}
		fileIDs[e.id] = fileID
		frames.widths[fileID] = e.fileWidth
	}

	g := NewGenerator(pool, fakeFiles{}, frames, art, art)
	report, err := g.Generate(ctx, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Framed != 2 || report.ProviderOK != 2 || report.Failed != 0 {
		t.Fatalf("report = %+v, want 2 framed and 2 provider images kept", report)
	}

	still := func(id string) (path, source string, width int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT COALESCE(e.still_path, ''), e.still_source_path, COALESCE(s.width, 0)
			FROM episodes e LEFT JOIN episode_still_sizes s ON s.content_id = e.content_id AND s.still_path = e.still_path
			WHERE e.content_id = $1`, id).Scan(&path, &source, &width); err != nil {
			t.Fatal(err)
		}
		return path, source, width
	}
	for _, id := range []string{"frames-e1", "frames-e2"} {
		if _, source, width := still(id); source != fmt.Sprintf("frame:%d", fileIDs[id]) || width != 1920 {
			t.Errorf("%s = %s at %d wide, want its 1920-wide frame", id, source, width)
		}
	}
	if _, source, width := still("frames-e3"); source != "tmdb://still/e3.jpg" || width != 1920 {
		t.Errorf("frames-e3 = %s at %d wide, want the full-HD TMDB still", source, width)
	}
	if _, source, _ := still("frames-e4"); source != "tmdb://still/e4.jpg" {
		t.Errorf("frames-e4 = %s, want the TMDB still the 1280 video cannot beat", source)
	}
	var displaced string
	if err := pool.QueryRow(ctx, `SELECT source_path FROM episode_frame_still_checks WHERE content_id = 'frames-e2' AND outcome = 'frame'`).Scan(&displaced); err != nil || displaced != "tvdb://banners/e2.jpg" {
		t.Errorf("frames-e2 displaced = %q (%v), want the TVDB source", displaced, err)
	}

	// Nothing is left to do, and nothing is extracted again.
	calls := frames.calls
	report, err = g.Generate(ctx, 100, 100)
	if err != nil || report.Checked != 0 || report.Measured != 0 || frames.calls != calls {
		t.Fatalf("second run = %+v (%v), %d extractions; want nothing to do", report, err, frames.calls-calls)
	}

	// A new provider still for e4 is measured and judged again.
	art.objects["tmdb/e4/new.webp"] = testJPEG(t, 3840, 2160)
	exec(`UPDATE episodes SET still_path = 'tmdb/e4/new.webp', still_source_path = 'tmdb://still/e4-new.jpg' WHERE content_id = 'frames-e4'`)
	report, err = g.Generate(ctx, 100, 100)
	if err != nil || report.Measured != 1 || report.ProviderOK != 1 || report.Framed != 0 {
		t.Fatalf("after a new provider still = %+v (%v), want it measured and kept", report, err)
	}
}
