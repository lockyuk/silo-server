package themesongs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BlobOpener reads a stored object, such as blobstore.Store's Get.
type BlobOpener func(ctx context.Context, key string) (io.ReadCloser, error)

// Downloads serves theme songs Silo downloaded for a series. They live in blob
// storage, never in a media folder; a theme in the series folder always wins.
// Before one is returned it is copied to a local cache file, so delivery,
// grants and conversion treat it like any other theme file. A downloaded theme
// is served only by an API node: workers do not have the cache.
type Downloads struct {
	pool     *pgxpool.Pool
	blobs    BlobOpener
	cacheDir string
	mu       sync.Mutex
}

var downloads atomic.Pointer[Downloads]

// UseDownloads makes every Repository fall back to downloaded themes. It is
// called once at startup by processes that own blob storage; without it only
// folder themes exist.
func UseDownloads(pool *pgxpool.Pool, blobs BlobOpener, cacheDir string) {
	if pool == nil || blobs == nil || cacheDir == "" {
		return
	}
	downloads.Store(&Downloads{pool: pool, blobs: blobs, cacheDir: cacheDir})
}

const downloadedColumns = `id, blob_key, sha256, title, duration_seconds, container, audio_codec, audio_channels, bitrate_kbps, sample_rate, file_size, downloaded_at`

// forSeries returns the downloaded theme of a series, or none.
func (d *Downloads) forSeries(ctx context.Context, seriesID string) ([]File, error) {
	return d.load(ctx, `SELECT `+downloadedColumns+` FROM downloaded_theme_songs WHERE series_id=$1`, seriesID)
}

// seriesOf names the series a downloaded theme id belongs to.
func (d *Downloads) seriesOf(ctx context.Context, id int64) (string, error) {
	var series string
	err := d.pool.QueryRow(ctx, `SELECT series_id FROM downloaded_theme_songs WHERE id=$1`, id).Scan(&series)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return series, err
}

func (d *Downloads) load(ctx context.Context, query string, args ...any) ([]File, error) {
	var f File
	var id int64
	var key, sum string
	err := d.pool.QueryRow(ctx, query, args...).Scan(&id, &key, &sum, &f.Title, &f.DurationSeconds, &f.Container, &f.AudioCodec, &f.AudioChannels, &f.BitrateKbps, &f.SampleRate, &f.Size, &f.Modified)
	if errors.Is(err, pgx.ErrNoRows) {
		return []File{}, nil
	}
	if err != nil {
		return nil, err
	}
	f.ID = strconv.FormatInt(id, 10)
	f.Modified = f.Modified.Truncate(time.Microsecond)
	f.Downloaded = true
	f.OwnerPath = d.cacheDir
	f.Path = filepath.Join(d.cacheDir, sum+"."+f.Container)
	if err := d.materialize(ctx, key, f); err != nil {
		// Unreadable storage hides the theme rather than failing the page.
		slog.WarnContext(ctx, "theme songs: downloaded theme unavailable", "component", "themesongs", "blob_key", key, "error", err)
		return []File{}, nil
	}
	return []File{f}, nil
}

// materialize copies a downloaded theme into the cache unless an identical
// copy is already there. The copy carries the row's size and time, which is
// what Open and grants check.
func (d *Downloads) materialize(ctx context.Context, key string, f File) error {
	if info, err := os.Stat(f.Path); err == nil && matches(info, f.Size, f.Modified) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if info, err := os.Stat(f.Path); err == nil && matches(info, f.Size, f.Modified) {
		return nil
	}
	if err := os.MkdirAll(d.cacheDir, 0o750); err != nil {
		return err
	}
	body, err := d.blobs(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	tmp, err := os.CreateTemp(d.cacheDir, ".theme-*.part")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(body, f.Size+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n != f.Size {
		return fmt.Errorf("stored theme is %d bytes, expected %d", n, f.Size)
	}
	if err := os.Chtimes(tmp.Name(), f.Modified, f.Modified); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}
