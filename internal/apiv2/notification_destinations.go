package apiv2

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

const (
	listNotificationWebhooksOperation = "listNotificationWebhooks"
)

type NotificationDestinationService interface {
	UpdateNotificationWebhook(context.Context, int, string, string, notifications.WebhookInput, func(int64) error) (*notifications.Webhook, error)
	RotateNotificationWebhookSecret(context.Context, int, string, string) (string, error)
	DeleteNotificationWebhook(context.Context, int, string, string, func(int64) error) error

	ListNotificationWebhookPage(context.Context, int, string, int, *notifications.Cursor) ([]notifications.Webhook, error)
}

type NotificationDestinationListInput struct {
	LimitParam
	Cursor string `query:"cursor"`
}

func destinationInstant(t *time.Time) NullableInstant {
	if t == nil {
		return NullableInstant{}
	}
	return NullableInstant{Valid: true, Time: NewInstant(*t)}
}

type NotificationWebhookDestination struct {
	ETag                string          `json:"etag" doc:"Original validator for conditional deletion; preserve unchanged with the observed row."`
	ID                  ID              `json:"id"`
	Name                string          `json:"name"`
	Type                string          `json:"type" enum:"discord,generic"`
	URLHost             string          `json:"url_host"`
	Enabled             bool            `json:"enabled"`
	NotifyNewReleases   bool            `json:"notify_new_releases"`
	NotifyRequests      bool            `json:"notify_requests"`
	ConsecutiveFailures int             `json:"consecutive_failures"`
	DisabledReason      *string         `json:"disabled_reason"`
	LastSuccessAt       NullableInstant `json:"last_success_at"`
	LastFailureAt       NullableInstant `json:"last_failure_at"`
	LastFailureStatus   *int            `json:"last_failure_status"`
	LastFailureMessage  *string         `json:"last_failure_message"`
}

func notificationWebhookDestinationOf(row notifications.Webhook) NotificationWebhookDestination {
	return NotificationWebhookDestination{
		ETag:                notificationWebhookTag(row.ProfileID, row.ID, row.Revision).String(),
		ID:                  ID(row.ID),
		Name:                row.Name,
		Type:                row.Type,
		URLHost:             row.URLHost,
		Enabled:             row.Enabled,
		NotifyNewReleases:   row.NotifyNewReleases,
		NotifyRequests:      row.NotifyRequests,
		ConsecutiveFailures: row.ConsecutiveFailures,
		DisabledReason:      row.DisabledReason,
		LastSuccessAt:       destinationInstant(row.LastSuccessAt),
		LastFailureAt:       destinationInstant(row.LastFailureAt),
		LastFailureStatus:   row.LastFailureStatus,
		LastFailureMessage:  row.LastFailureMessage,
	}
}

type NotificationWebhookDestinationListOutput struct {
	Body Collection[NotificationWebhookDestination]
}

func notificationWebhookTag(profile, id string, revision int64) EntityTag {
	return RenderETag("notification-webhook:"+profile, id, revision)
}

type NotificationWebhookDeleteInput struct {
	ID          string `path:"id"`
	IfMatch     string `header:"If-Match"`
	IfNoneMatch string `header:"If-None-Match"`
}

type NotificationWebhookRotateInput struct {
	ID string `path:"id"`
}

const notificationSecretNoStore = "no-store"

type NotificationWebhookSecretOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		SigningSecret string `json:"signing_secret"`
	}
}

func registerNotificationDestinations(reg *Registry) {
	registerNotificationWebhookUpdate(reg)

	rotate := notificationOperation(http.MethodPost, "/webhooks/{id}/rotate-secret", "rotateNotificationWebhookSecret")
	rotate.RetrySafety = RetrySafetyNonRetryable
	rotate.Summary = "Replace the generic webhook signing secret and reveal it once. Never replay an uncertain rotation."
	Register(reg, rotate, func(ctx context.Context, in *NotificationWebhookRotateInput) (*NotificationWebhookSecretOutput, error) {
		if reg.deps.NotificationDestinations == nil {
			return nil, unavailable("notification destinations")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		value, err := reg.deps.NotificationDestinations.RotateNotificationWebhookSecret(ctx, user, profile, in.ID)
		if errors.Is(err, notifications.ErrWebhookNotFound) {
			return nil, NewProblem(TypeNotFound, "Webhook not found.")
		}
		if errors.Is(err, notifications.ErrWebhookInvalid) {
			return nil, NewProblem(TypeValidationFailed, "Only generic webhooks have signing secrets.")
		}
		if err != nil {
			return nil, serviceProblem(err)
		}
		out := &NotificationWebhookSecretOutput{CacheControl: notificationSecretNoStore}
		out.Body.SigningSecret = value
		return out, nil
	})

	removeWebhook := notificationOperation(http.MethodDelete, "/webhooks/{id}", "deleteNotificationWebhook")
	removeWebhook.DefaultStatus = http.StatusNoContent
	removeWebhook.Guarded = true
	removeWebhook.RetrySafety = RetrySafetyNaturalIdempotent
	removeWebhook.Summary = "Delete the profile's exact webhook ID and stored attempts. Does not recall an already-dispatched webhook."
	Register(reg, removeWebhook, func(ctx context.Context, in *NotificationWebhookDeleteInput) (*struct{}, error) {
		if reg.deps.NotificationDestinations == nil {
			return nil, unavailable("notification destinations")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		err := reg.deps.NotificationDestinations.DeleteNotificationWebhook(ctx, user, profile, in.ID, func(revision int64) error {
			if p := EvaluateGuardedPreconditions(in.IfMatch, in.IfNoneMatch, notificationWebhookTag(profile, in.ID, revision)); p != nil {
				return p
			}
			return nil
		})
		if err != nil {
			if p, ok := errors.AsType[*Problem](err); ok {
				return nil, p
			}
			if errors.Is(err, notifications.ErrWebhookNotFound) {
				return nil, NewProblem(TypeNotFound, "Webhook not found.")
			}
			return nil, serviceProblem(err)
		}
		return &struct{}{}, nil
	})

	cursors := NewCursors(reg.deps.CursorSecret)

	Register(reg, notificationOperation(http.MethodGet, "/webhooks", listNotificationWebhooksOperation), func(ctx context.Context, in *NotificationDestinationListInput) (*NotificationWebhookDestinationListOutput, error) {
		svc := reg.deps.NotificationDestinations
		if svc == nil {
			return nil, unavailable("notification destinations")
		}
		scope := notificationCursorScope(ctx, listNotificationWebhooksOperation, strconv.Itoa(in.Limit))
		var after *notifications.Cursor
		if in.Cursor != "" {
			after = new(notifications.Cursor)
			if p := cursors.Decode(scope, in.Cursor, after); p != nil {
				return nil, p
			}
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		rows, err := svc.ListNotificationWebhookPage(ctx, user, profile, in.Limit+1, after)
		if err != nil {
			return nil, serviceProblem(err)
		}
		more := len(rows) > in.Limit
		if more {
			rows = rows[:in.Limit]
		}
		items := make([]NotificationWebhookDestination, 0, len(rows))
		for _, row := range rows {
			items = append(items, notificationWebhookDestinationOf(row))
		}
		next := ""
		if more {
			last := rows[len(rows)-1]
			next, err = cursors.Encode(scope, notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			if err != nil {
				return nil, serviceProblem(err)
			}
		}
		return &NotificationWebhookDestinationListOutput{Body: Paginated(items, next)}, nil
	})
}
