package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/models"
)

// AutoMovieCollectionsTaskKey is the task that runs SyncAutoMovieCollections.
const AutoMovieCollectionsTaskKey = "sync_auto_movie_collections"

const (
	// collectionManagementModeAuto marks collections the automatic sync
	// owns: it creates, refreshes and removes them.
	collectionManagementModeAuto = "auto"
	autoCollectionsSource        = "tmdb_collection"
	// autoCollectionMinFilms is how many of a franchise's films a library
	// must hold before it gets a collection.
	autoCollectionMinFilms = 2
	// autoCollectionRecheck is how long a movie's TMDB collection is trusted
	// before it is looked up again.
	autoCollectionRecheck = 30 * 24 * time.Hour
	tmdbOriginalImageBase = "https://image.tmdb.org/t/p/original"
)

// AutoCollectionInfo is a TMDB collection as the automatic sync uses it.
type AutoCollectionInfo struct {
	Name         string
	Overview     string
	PosterPath   string // TMDB image path; empty when TMDB has none
	BackdropPath string
}

// AutoCollectionsTMDB is the TMDB surface the automatic sync needs.
type AutoCollectionsTMDB interface {
	// MovieCollectionID returns the TMDB collection a movie belongs to, or 0.
	MovieCollectionID(ctx context.Context, movieID int) (int, error)
	CollectionInfo(ctx context.Context, collectionID int) (AutoCollectionInfo, error)
}

// AutoCollectionsReport summarises one run of SyncAutoMovieCollections.
type AutoCollectionsReport struct {
	Libraries      int `json:"libraries"`
	Movies         int `json:"movies"`
	Lookups        int `json:"lookups"`
	LookupFailures int `json:"lookup_failures"`
	Collections    int `json:"collections"`
	Created        int `json:"created"`
	Removed        int `json:"removed"`
	Failures       int `json:"failures"`
}

// SyncAutoMovieCollections gives every movie library with automatic
// collections on one collection per TMDB collection it holds at least two
// films of, synced from TMDB, with TMDB's poster and backdrop when the
// library's collection artwork is on and the generated collage when it is
// off. Collections whose franchise falls below two films, and all of a
// library's once the setting is off, are removed.
func (h *LibraryCollectionHandler) SyncAutoMovieCollections(ctx context.Context, progress func(percent float64, message string)) (AutoCollectionsReport, error) {
	var report AutoCollectionsReport
	if h.AutoTMDB == nil || h.FolderRepo == nil {
		return report, errors.New("automatic collections need TMDB and the library repository")
	}
	if progress == nil {
		progress = func(float64, string) {}
	}
	folders, err := h.FolderRepo.List(ctx)
	if err != nil {
		return report, fmt.Errorf("listing libraries: %w", err)
	}
	infos := map[int]AutoCollectionInfo{}
	for i, folder := range folders {
		kind := librarykind.Of(folder.Type)
		if !kind.Movie && !kind.Mixed {
			continue
		}
		base := float64(i) / float64(len(folders)) * 100
		progress(base, "Library "+folder.Name)
		if err := h.syncLibraryAutoCollections(ctx, folder, infos, &report); err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.Failures++
			slog.WarnContext(ctx, "automatic collections: library failed", "component", "collections", "library_id", folder.ID, "error", err)
		}
	}
	progress(100, fmt.Sprintf("%d collections, %d new, %d removed", report.Collections, report.Created, report.Removed))
	return report, nil
}

func (h *LibraryCollectionHandler) syncLibraryAutoCollections(ctx context.Context, folder *models.MediaFolder, infos map[int]AutoCollectionInfo, report *AutoCollectionsReport) error {
	existing, err := h.repo.ListByLibrary(ctx, folder.ID, catalog.ListLibraryCollectionsOptions{IncludeHidden: true})
	if err != nil {
		return fmt.Errorf("listing collections: %w", err)
	}
	owned := map[string]*models.LibraryCollection{}
	for _, c := range existing {
		if c.ManagementMode == collectionManagementModeAuto && c.LibraryID == folder.ID {
			owned[c.ManagementKey] = c
		}
	}

	wanted := map[int]int{} // TMDB collection → films the library holds
	if folder.AutoCollectionsEnabled {
		report.Libraries++
		movies, err := h.libraryTMDBMovieIDs(ctx, folder.ID)
		if err != nil {
			return err
		}
		report.Movies += len(movies)
		refs, err := h.movieCollectionRefs(ctx, movies, report)
		if err != nil {
			return err
		}
		for _, collectionID := range refs {
			if collectionID > 0 {
				wanted[collectionID]++
			}
		}
	}

	for collectionID, films := range wanted {
		if films < autoCollectionMinFilms {
			continue
		}
		key := autoCollectionKey(folder.ID, collectionID)
		collection := owned[key]
		delete(owned, key)
		if err := h.syncAutoCollection(ctx, folder, collectionID, collection, infos, report); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report.Failures++
			slog.WarnContext(ctx, "automatic collections: collection failed", "component", "collections",
				"library_id", folder.ID, "tmdb_collection_id", collectionID, "error", err)
			continue
		}
		report.Collections++
	}

	// What is left is no longer wanted: the franchise fell below the
	// minimum, or the library turned automatic collections off.
	for _, c := range owned {
		if err := h.deleteServerCollection(ctx, c.ID); err != nil {
			report.Failures++
			slog.WarnContext(ctx, "automatic collections: remove failed", "component", "collections", "collection_id", c.ID, "error", err)
			continue
		}
		report.Removed++
	}
	return nil
}

func (h *LibraryCollectionHandler) syncAutoCollection(ctx context.Context, folder *models.MediaFolder, collectionID int, collection *models.LibraryCollection, infos map[int]AutoCollectionInfo, report *AutoCollectionsReport) error {
	info, ok := infos[collectionID]
	if !ok {
		var err error
		if info, err = h.AutoTMDB.CollectionInfo(ctx, collectionID); err != nil {
			return fmt.Errorf("reading TMDB collection: %w", err)
		}
		infos[collectionID] = info
	}
	if collection == nil {
		if info.Name == "" {
			return errors.New("TMDB collection has no name")
		}
		created, err := h.createTMDBFranchiseCollection(ctx, importTMDBFranchiseRequest{
			LibraryID:        folder.ID,
			Title:            info.Name,
			Description:      info.Overview,
			CollectionID:     collectionID,
			ManagementMode:   collectionManagementModeAuto,
			ManagementSource: autoCollectionsSource,
			ManagementKey:    autoCollectionKey(folder.ID, collectionID),
		})
		if err != nil {
			return err
		}
		collection = created
		report.Created++
	}
	// Artwork first, so the sync's collage step sees TMDB's poster and skips.
	h.applyAutoCollectionArtwork(ctx, folder.CollectionArtworkEnabled, collection, info)
	if _, err := h.service.SyncCollection(ctx, collection.ID); err != nil {
		return fmt.Errorf("syncing: %w", err)
	}
	return nil
}

// applyAutoCollectionArtwork puts TMDB's poster and backdrop on the
// collection, or takes them off again (the sync then builds the collage).
// Posters the sync set are flagged PosterFromTemplate, so an administrator's
// own upload is never replaced or removed.
func (h *LibraryCollectionHandler) applyAutoCollectionArtwork(ctx context.Context, enabled bool, c *models.LibraryCollection, info AutoCollectionInfo) {
	warn := func(kind string, err error) {
		slog.WarnContext(ctx, "automatic collections: artwork failed", "component", "collections", "collection_id", c.ID, "kind", kind, "error", err)
	}
	if !enabled {
		if c.PosterURL != "" && c.PosterFromTemplate {
			if err := h.DeleteAdminCollectionArtwork(ctx, c.ID, adminCollectionBackdrop); err != nil {
				warn(adminCollectionBackdrop, err)
			}
			if err := h.DeleteAdminCollectionArtwork(ctx, c.ID, collectionImagePoster); err != nil {
				warn(collectionImagePoster, err)
			}
		}
		return
	}
	if c.PosterURL != "" && !c.PosterAutoGenerated && !c.PosterFromTemplate {
		return // the administrator's own poster
	}
	if c.PosterURL == "" || c.PosterAutoGenerated {
		if info.PosterPath == "" {
			return // the collage stays
		}
		if err := h.SetAdminCollectionArtworkSource(ctx, c.ID, collectionImagePoster, tmdbOriginalImageBase+info.PosterPath); err != nil {
			warn(collectionImagePoster, err)
			return
		}
		if err := h.repo.Update(ctx, catalog.UpdateLibraryCollectionInput{ID: c.ID, PosterFromTemplate: new(true)}); err != nil {
			warn(collectionImagePoster, err)
		}
	}
	if c.BackdropURL == "" && info.BackdropPath != "" {
		if err := h.SetAdminCollectionArtworkSource(ctx, c.ID, adminCollectionBackdrop, tmdbOriginalImageBase+info.BackdropPath); err != nil {
			warn(adminCollectionBackdrop, err)
		}
	}
}

func autoCollectionKey(libraryID, tmdbCollectionID int) string {
	return fmt.Sprintf("tmdb-collection:%d:%d", libraryID, tmdbCollectionID)
}

// libraryTMDBMovieIDs lists the TMDB ids of the movies present in a library.
func (h *LibraryCollectionHandler) libraryTMDBMovieIDs(ctx context.Context, libraryID int) ([]int, error) {
	rows, err := h.FolderRepo.Pool().Query(ctx, `
		SELECT DISTINCT mi.tmdb_id
		FROM media_items mi
		JOIN media_files mf ON mf.content_id = mi.content_id
		WHERE mf.media_folder_id = $1 AND mf.missing_since IS NULL
		  AND mi.type = 'movie' AND mi.tmdb_id <> ''`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("listing movies: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if id, err := strconv.Atoi(raw); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// movieCollectionRefs maps each movie to its TMDB collection (0 = none),
// looking up on TMDB only movies not checked within autoCollectionRecheck.
// A movie whose lookup fails is left out and tried again next run.
func (h *LibraryCollectionHandler) movieCollectionRefs(ctx context.Context, movies []int, report *AutoCollectionsReport) (map[int]int, error) {
	pool := h.FolderRepo.Pool()
	refs := make(map[int]int, len(movies))
	checked := map[int]time.Time{}
	rows, err := pool.Query(ctx, `
		SELECT tmdb_movie_id, COALESCE(collection_id, 0), checked_at
		FROM movie_tmdb_collection_refs WHERE tmdb_movie_id = ANY($1)`, movies)
	if err != nil {
		return nil, fmt.Errorf("reading collection refs: %w", err)
	}
	for rows.Next() {
		var movie, collection int
		var at time.Time
		if err := rows.Scan(&movie, &collection, &at); err != nil {
			rows.Close()
			return nil, err
		}
		refs[movie], checked[movie] = collection, at
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	for _, movie := range movies {
		if at, ok := checked[movie]; ok && now.Sub(at) < autoCollectionRecheck {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		report.Lookups++
		collection, err := h.AutoTMDB.MovieCollectionID(ctx, movie)
		if err != nil {
			report.LookupFailures++
			delete(refs, movie)
			slog.DebugContext(ctx, "automatic collections: movie lookup failed", "component", "collections", "tmdb_movie_id", movie, "error", err)
			continue
		}
		refs[movie] = collection
		var stored *int
		if collection > 0 {
			stored = &collection
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO movie_tmdb_collection_refs (tmdb_movie_id, collection_id, checked_at)
			VALUES ($1, $2, now())
			ON CONFLICT (tmdb_movie_id) DO UPDATE SET collection_id = EXCLUDED.collection_id, checked_at = EXCLUDED.checked_at`,
			movie, stored); err != nil {
			return nil, fmt.Errorf("saving collection ref: %w", err)
		}
	}
	return refs, nil
}
