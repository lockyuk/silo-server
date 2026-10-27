package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// splitMovieEvents separates movie events, which fan out to profiles that
// asked for every new movie, from the other flat item kinds.
func splitMovieEvents(flat []ReleaseEvent) (movies, others []ReleaseEvent) {
	for _, event := range flat {
		if normalizeEventKind(event.Kind) == EventKindMovie && event.ItemID != "" {
			movies = append(movies, event)
		} else {
			others = append(others, event)
		}
	}
	return movies, others
}

// captureMovieRecipients picks, per movie event, the profiles that asked for
// every new movie and can open the movie in the event's library now. Like
// episode capture, it runs before the inbox locks are taken so the batch
// locks every profile at once.
//
// A second copy of a movie (a 4K library beside an HD one) is held back by
// the per-profile movie dedupe key while the first delivery is retained.
func (w *FanoutWorker) captureMovieRecipients(ctx context.Context, tx pgx.Tx, movies []ReleaseEvent, recipients *recipientAccess) (map[string]map[profileKey]struct{}, error) {
	out := make(map[string]map[profileKey]struct{}, len(movies))
	if len(movies) == 0 {
		return out, nil
	}
	candidates, err := w.preferences.ListNewMovieRecipients(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, event := range movies {
		captured := make(map[profileKey]struct{}, len(candidates))
		for _, prefs := range candidates {
			if _, eligible := EvaluateMovieRecipient(prefs); !eligible {
				continue
			}
			allowed, err := recipients.canOpen(ctx, tx, prefs.UserID, prefs.ProfileID, event.ItemID, event.LibraryID)
			if err != nil {
				return nil, err
			}
			if allowed {
				captured[profileKey{prefs.UserID, prefs.ProfileID}] = struct{}{}
			}
		}
		out[event.ID] = captured
	}
	return out, nil
}

// fanOutMovieEvent inserts movie.available deliveries for the captured
// profiles, enqueues their channel outboxes, and returns dispatch payloads
// for the rows actually inserted.
func (w *FanoutWorker) fanOutMovieEvent(ctx context.Context, tx pgx.Tx, event ReleaseEvent, captured map[profileKey]struct{}) ([]DeliveryRow, error) {
	if len(captured) == 0 {
		return nil, nil
	}
	flags := ReasonFlags{NewRelease: true}
	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		return nil, fmt.Errorf("marshal reason flags: %w", err)
	}
	pending := make(map[profileKey]pendingDelivery, len(captured))
	toInsert := make([]Delivery, 0, len(captured))
	for key := range captured {
		eventID := event.ID
		libraryID := event.LibraryID
		itemID := event.ItemID
		delivery := Delivery{
			ID:             ulid.Make().String(),
			ReleaseEventID: &eventID,
			UserID:         key.userID,
			ProfileID:      key.profileID,
			LibraryID:      &libraryID,
			SeriesID:       &itemID,
			Type:           DeliveryTypeMovieAvailable,
			ReasonFlags:    flagsJSON,
		}
		pending[key] = pendingDelivery{delivery: delivery, flags: flags}
		toInsert = append(toInsert, delivery)
	}
	inserted, err := w.deliveries.BulkInsert(ctx, tx, toInsert)
	if err != nil {
		return nil, err
	}
	if err := w.enqueueWebhookOutbox(ctx, tx, inserted, pending); err != nil {
		return nil, err
	}
	if err := w.enqueueWebPushOutbox(ctx, tx, inserted); err != nil {
		return nil, err
	}
	if err := w.enqueuePushOutbox(ctx, tx, inserted); err != nil {
		return nil, err
	}

	display, err := loadMovieDisplay(ctx, tx, event.ItemID)
	if err != nil {
		return nil, err
	}
	rows := make([]DeliveryRow, 0, len(inserted))
	for _, row := range inserted {
		entry, ok := pending[row.key()]
		if !ok {
			continue
		}
		delivery := entry.delivery
		delivery.ID = row.ID
		delivery.CreatedAt = row.CreatedAt
		rows = append(rows, DeliveryRow{
			Delivery:         delivery,
			SeriesTitle:      display.seriesTitle,
			PosterPath:       display.posterPath,
			PosterThumbhash:  display.posterThumbhash,
			PosterSourcePath: display.posterSourcePath,
			MediaType:        "movie",
		})
	}
	return rows, nil
}

// loadMovieDisplay fetches a movie's display metadata once per event.
func loadMovieDisplay(ctx context.Context, tx pgx.Tx, itemID string) (eventDisplay, error) {
	var display eventDisplay
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(title, ''), COALESCE(poster_path, ''),
		       COALESCE(poster_thumbhash, ''), COALESCE(poster_source_path, '')
		FROM media_items
		WHERE content_id = $1`,
		itemID,
	).Scan(&display.seriesTitle, &display.posterPath, &display.posterThumbhash, &display.posterSourcePath)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventDisplay{}, nil
	}
	if err != nil {
		return display, fmt.Errorf("load movie display metadata: %w", err)
	}
	return display, nil
}
