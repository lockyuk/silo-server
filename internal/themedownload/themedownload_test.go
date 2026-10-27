package themedownload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

// fakeMP3 passes looksLikeMP3; the tests replace ffprobe.
func fakeMP3(fill byte) []byte {
	return append([]byte("ID3"), bytes.Repeat([]byte{fill}, minThemeBytes)...)
}

func TestLooksLikeMP3(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		want bool
	}{
		"id3":        {fakeMP3(1), true},
		"frame sync": {append([]byte{0xFF, 0xFB}, make([]byte, minThemeBytes)...), true},
		"html":       {append([]byte("<html>"), make([]byte, minThemeBytes)...), false},
		"too short":  {[]byte("ID3abc"), false},
	} {
		if got := looksLikeMP3(tc.data); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestDownloadOutcomes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".mp3") {
		case "1":
			_, _ = w.Write(fakeMP3(1))
		case "2":
			http.NotFound(w, r)
		case "3":
			_, _ = w.Write([]byte("<html>not a theme</html>"))
		case "4":
			w.WriteHeader(http.StatusBadGateway)
		case "5":
			_, _ = w.Write(append([]byte("ID3"), make([]byte, maxThemeBytes)...))
		}
	}))
	defer srv.Close()
	s := &Service{client: srv.Client(), sourceURL: srv.URL + "/%s.mp3"}
	if data, err := s.download(t.Context(), "1"); err != nil || !looksLikeMP3(data) {
		t.Fatalf("theme: %v", err)
	}
	for _, id := range []string{"2", "3"} {
		if _, err := s.download(t.Context(), id); !errors.Is(err, errNotAvailable) {
			t.Errorf("%s: want not available, got %v", id, err)
		}
	}
	for _, id := range []string{"4", "5"} {
		if _, err := s.download(t.Context(), id); err == nil || errors.Is(err, errNotAvailable) {
			t.Errorf("%s: want a failure, got %v", id, err)
		}
	}
}

func TestBlobNamespaceGroups(t *testing.T) {
	ns := BlobNamespace()
	for key, want := range map[string]bool{
		"theme-songs/series-1/abc.mp3":    true,
		"theme-songs/series-1/abc.txt":    false,
		"theme-songs/abc.mp3":             false,
		"theme-songs/series-1/x/abc.mp3":  false,
		"chapter-images/series-1/abc.mp3": false,
		"theme-songs//abc.mp3":            false,
	} {
		group, ok := ns.Group(key)
		if ok != want || (ok && group != key) {
			t.Errorf("%s: got %q %v", key, group, ok)
		}
	}
}

type memBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memBlobs) Put(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), data...)
	return nil
}

func (m *memBlobs) Delete(_ context.Context, keys []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
	}
	return len(keys), nil
}

func (m *memBlobs) open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.data[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type memSettings map[string]string

func (m memSettings) Get(_ context.Context, key string) (string, error) { return m[key], nil }
func (m memSettings) Set(_ context.Context, key, value string) error    { m[key] = value; return nil }

func TestRunStoresServesAndRemovesThemesPostgres(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	prefix := fmt.Sprintf("theme-dl-%d", time.Now().UnixNano())
	var library int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('series',$1,true) RETURNING id`, prefix).Scan(&library); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM media_items WHERE content_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(cleanup, `DELETE FROM media_folders WHERE id=$1`, library)
	})
	// found has a theme at the source, missing does not, and local has a
	// theme file in its folder.
	found, missing, local := prefix+"-found", prefix+"-missing", prefix+"-local"
	for i, series := range []struct{ id, tvdb string }{{found, "111"}, {missing, "222"}, {local, "333"}} {
		root := "/tv/" + series.id
		episode := series.id + "-e1"
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO media_items(content_id,type,title,status,tvdb_id) VALUES($1,'series',$1,'released',$2)`, []any{series.id, series.tvdb}},
			{`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, []any{series.id, library}},
			{`INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,1,'e1')`, []any{episode, series.id}},
			{`INSERT INTO media_files(content_id,episode_id,media_folder_id,file_path,canonical_root_path) VALUES($1,$2,$3,$4,$5)`, []any{series.id, episode, library, root + "/Season 1/e1.mkv", root}},
		} {
			if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
				t.Fatalf("seed %d: %v", i, err)
			}
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO item_theme_songs(media_folder_id,owner_path,file_path,title,duration_seconds,container,audio_codec,audio_channels,bitrate_kbps,sample_rate,file_size,file_modified_at)
		VALUES($1,$2,$3,'theme',30,'mp3','mp3',2,128,44100,1000,now())`, library, "/tv/"+local, "/tv/"+local+"/theme.mp3"); err != nil {
		t.Fatal(err)
	}

	var requests sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".mp3")
		n, _ := requests.LoadOrStore(id, new(int))
		*n.(*int)++
		if id == "111" {
			_, _ = w.Write(fakeMP3(7))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	blobs := &memBlobs{data: map[string][]byte{}}
	settings := memSettings{SettingEnabled: "true"}
	s := New(pool, blobs, settings, "")
	s.client, s.sourceURL, s.gap = srv.Client(), srv.URL+"/%s.mp3", 0
	s.probeAudio = func(context.Context, []byte) (themesongs.File, error) {
		return themesongs.File{Song: themesongs.Song{DurationSeconds: 31}, AudioCodec: "mp3", AudioChannels: 2, BitrateKbps: 128, SampleRate: 44100}, nil
	}

	report, err := s.Run(ctx, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Downloaded < 1 || report.NotAvailable < 1 || report.Failed != 0 {
		t.Fatalf("first run: %+v", report)
	}
	if n, ok := requests.Load("333"); ok {
		t.Fatalf("a series with a folder theme was downloaded %d times", *n.(*int))
	}
	states := map[string]string{}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sr := range list {
		states[sr.ID] = sr.State
	}
	if states[found] != StateDownloaded || states[missing] != StateNotAvailable || states[local] != StateFolder {
		t.Fatalf("states: %v", states)
	}
	if len(blobs.data) != 1 {
		t.Fatalf("stored %d blobs", len(blobs.data))
	}

	// A recent miss and a stored theme are not asked for again; force asks
	// for the miss only.
	if _, err := s.Run(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"111": 1, "222": 1} {
		if n, _ := requests.Load(id); *n.(*int) != want {
			t.Fatalf("%s requested %d times after the second run", id, *n.(*int))
		}
	}
	if _, err := s.Run(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := requests.Load("222"); *n.(*int) != 2 {
		t.Fatalf("forced run did not ask for the miss again")
	}
	if n, _ := requests.Load("111"); *n.(*int) != 1 {
		t.Fatalf("forced run downloaded a stored theme again")
	}

	// Playback sees the download through the theme repository, from a cache
	// copy that Open accepts, for the series and for its episodes.
	themesongs.UseDownloads(pool, blobs.open, t.TempDir())
	repo := themesongs.NewRepository(pool)
	owner, files, err := repo.Resolve(ctx, found, false, catalog.AccessFilter{})
	if err != nil || owner != found || len(files) != 1 || !files[0].Downloaded {
		t.Fatalf("resolve series: %q %+v %v", owner, files, err)
	}
	f, err := themesongs.Open(files[0])
	if err != nil {
		t.Fatalf("open cached copy: %v", err)
	}
	got, _ := io.ReadAll(f)
	_ = f.Close()
	if !bytes.Equal(got, fakeMP3(7)) {
		t.Fatal("cached copy differs from the download")
	}
	owner, episodeFiles, err := repo.Resolve(ctx, found+"-e1", true, catalog.AccessFilter{})
	if err != nil || owner != found || len(episodeFiles) != 1 || episodeFiles[0].ID != files[0].ID {
		t.Fatalf("resolve episode: %q %+v %v", owner, episodeFiles, err)
	}
	if byID, err := repo.Find(ctx, files[0].ID, catalog.AccessFilter{}); err != nil || byID.ID != files[0].ID {
		t.Fatalf("find: %+v %v", byID, err)
	}
	// The folder theme still wins for its series.
	if _, files, err := repo.Resolve(ctx, local, false, catalog.AccessFilter{}); err != nil || len(files) != 1 || files[0].Downloaded {
		t.Fatalf("folder theme: %+v %v", files, err)
	}
	// A series viewers cannot see does not leak its download.
	if _, _, err := repo.Resolve(ctx, found, false, catalog.AccessFilter{AllowedLibraryIDs: []int{}}); err == nil {
		t.Fatal("download resolved for a viewer without the library")
	}

	sr, err := s.Remove(ctx, found)
	if err != nil || sr.State != StatePending || len(blobs.data) != 0 {
		t.Fatalf("remove: %+v %v blobs=%d", sr, err, len(blobs.data))
	}
	if _, files, err := repo.Resolve(ctx, found, false, catalog.AccessFilter{}); err != nil || len(files) != 0 {
		t.Fatalf("removed theme still resolves: %+v %v", files, err)
	}
	sr, err = s.Retry(ctx, found)
	if err != nil || sr.State != StateDownloaded {
		t.Fatalf("retry: %+v %v", sr, err)
	}
}
