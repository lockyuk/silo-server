package ratingsources

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LibraryStore keeps each library's own choice of rating sources, in
// library_rating_sources. A library without a row uses the server-wide
// selection.
type LibraryStore struct{ pool *pgxpool.Pool }

// NewLibraryStore binds the store to the database.
func NewLibraryStore(pool *pgxpool.Pool) *LibraryStore { return &LibraryStore{pool: pool} }

// Read returns every library's own choice and, for the items in those
// libraries, the libraries each belongs to.
func (st *LibraryStore) Read(ctx context.Context) (map[int][]string, map[string][]int, error) {
	rows, err := st.pool.Query(ctx, `SELECT media_folder_id, sources FROM library_rating_sources`)
	if err != nil {
		return nil, nil, err
	}
	byLibrary := map[int][]string{}
	var ids []int
	for rows.Next() {
		var id int
		var sources []string
		if err := rows.Scan(&id, &sources); err != nil {
			rows.Close()
			return nil, nil, err
		}
		byLibrary[id] = sources
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return byLibrary, nil, nil
	}
	rows, err = st.pool.Query(ctx, `SELECT content_id, media_folder_id FROM media_item_libraries WHERE media_folder_id = ANY($1)`, ids)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	libraryOf := map[string][]int{}
	for rows.Next() {
		var contentID string
		var id int
		if err := rows.Scan(&contentID, &id); err != nil {
			return nil, nil, err
		}
		libraryOf[contentID] = append(libraryOf[contentID], id)
	}
	return byLibrary, libraryOf, rows.Err()
}

// Get returns one library's own choice; ok is false when it uses the
// server-wide selection.
func (st *LibraryStore) Get(ctx context.Context, libraryID int) (sources []string, ok bool, err error) {
	err = st.pool.QueryRow(ctx, `SELECT sources FROM library_rating_sources WHERE media_folder_id = $1`, libraryID).Scan(&sources)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return sources, err == nil, err
}

// Set stores one library's choice, or with a nil sources makes it use the
// server-wide selection again.
func (st *LibraryStore) Set(ctx context.Context, libraryID int, sources []string) error {
	if sources == nil {
		_, err := st.pool.Exec(ctx, `DELETE FROM library_rating_sources WHERE media_folder_id = $1`, libraryID)
		return err
	}
	_, err := st.pool.Exec(ctx, `INSERT INTO library_rating_sources (media_folder_id, sources) VALUES ($1, $2)
		ON CONFLICT (media_folder_id) DO UPDATE SET sources = EXCLUDED.sources`, libraryID, sources)
	return err
}

// UseLibraryStore adds per-library choices from store to every selection the
// policy reads, and lets LibraryChoice and SetLibraryChoice reach it.
func (p *Policy) UseLibraryStore(store *LibraryStore) {
	if p == nil || store == nil {
		return
	}
	p.store = store
	p.UseLibraries(store.Read)
}

// ErrNoLibraryStore means the policy has no per-library store.
var ErrNoLibraryStore = errors.New("per-library rating sources are not available")

// LibraryChoice returns a library's own choice; ok is false when it uses the
// server-wide selection.
func (p *Policy) LibraryChoice(ctx context.Context, libraryID int) ([]string, bool, error) {
	if p == nil || p.store == nil {
		return nil, false, ErrNoLibraryStore
	}
	return p.store.Get(ctx, libraryID)
}

// SetLibraryChoice stores a library's choice (nil: use the server-wide
// selection) and makes the next Selection read it.
func (p *Policy) SetLibraryChoice(ctx context.Context, libraryID int, sources []string) error {
	if p == nil || p.store == nil {
		return ErrNoLibraryStore
	}
	if err := p.store.Set(ctx, libraryID, sources); err != nil {
		return err
	}
	p.mu.Lock()
	p.expires = p.now()
	p.mu.Unlock()
	return nil
}
