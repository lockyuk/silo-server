package themedownload

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/blobgc"
)

// BlobNamespace describes downloaded themes to blobgc. Each object is its own
// group, live while a downloaded_theme_songs row names it.
func BlobNamespace() blobgc.Namespace {
	return blobgc.Namespace{
		Root: KeyRoot,
		Group: func(key string) (string, bool) {
			rest, ok := strings.CutPrefix(key, KeyRoot)
			if !ok || strings.Count(rest, "/") != 1 || !strings.HasSuffix(rest, ".mp3") || strings.HasPrefix(rest, "/") {
				return "", false
			}
			return key, true
		},
		Live: func(ctx context.Context, db blobgc.Querier, prefixes []string) (map[string]bool, error) {
			rows, err := db.Query(ctx, `SELECT blob_key FROM public.downloaded_theme_songs WHERE blob_key = ANY($1)`, prefixes)
			if err != nil {
				return nil, fmt.Errorf("look up downloaded themes: %w", err)
			}
			defer rows.Close()
			live := map[string]bool{}
			for rows.Next() {
				var key string
				if err := rows.Scan(&key); err != nil {
					return nil, err
				}
				live[key] = true
			}
			return live, rows.Err()
		},
	}
}
