package apiv2

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

const createNotificationWebhookOperation = "createNotificationWebhook"

type NotificationDestinationCreateService interface {
	CreateNotificationWebhook(context.Context, int, string, notifications.WebhookInput) (*notifications.Webhook, string, error)
}

type NotificationWebhookCreateInput struct {
	Body struct {
		Name              string  `json:"name" minLength:"1"`
		URL               string  `json:"url" format:"uri" minLength:"1"`
		Type              *string `json:"type,omitempty" enum:"discord,generic"`
		NotifyNewReleases *bool   `json:"notify_new_releases,omitempty"`
		NotifyRequests    *bool   `json:"notify_requests,omitempty"`
	}
}

// NotificationDestinationCreated contains the identity and the one-time
// signing secret. Configuration is read through the destination list.
type NotificationDestinationCreated struct {
	ID            ID     `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type" enum:"discord,generic"`
	URLHost       string `json:"url_host"`
	SigningSecret string `json:"signing_secret,omitempty"`
}
type NotificationDestinationCreatedOutput struct {
	Body NotificationDestinationCreated
}

func notificationDestinationCreateProblem(err error) error {
	switch {
	case errors.Is(err, notifications.ErrWebhooksDisabled):
		return NewProblem(TypePermissionDenied, "Notification channel is disabled.")
	case errors.Is(err, notifications.ErrWebhookLimit):
		return NewProblem(TypeValidationFailed, "Notification destination limit reached.")
	case errors.Is(err, notifications.ErrWebhookInvalid):
		return NewProblem(TypeValidationFailed, "Invalid notification destination configuration.")
	default:
		return serviceProblem(err)
	}
}

func registerNotificationDestinationCreate(reg *Registry) {
	op := notificationOperation(http.MethodPost, "/webhooks", createNotificationWebhookOperation)
	op.Summary = "Create a profile webhook and reveal its signing secret once."
	op.DefaultStatus = http.StatusCreated
	op.RetrySafety = RetrySafetyNonRetryable
	Register(reg, op, func(ctx context.Context, in *NotificationWebhookCreateInput) (*NotificationDestinationCreatedOutput, error) {
		svc := reg.deps.NotificationDestinationCreate
		if svc == nil {
			return nil, unavailable("notification destinations")
		}
		b := in.Body
		row, secret, err := svc.CreateNotificationWebhook(ctx, claimsFrom(ctx).UserID, profileFrom(ctx), notifications.WebhookInput{Name: &b.Name, URL: &b.URL, Type: b.Type, NotifyNewReleases: b.NotifyNewReleases, NotifyRequests: b.NotifyRequests})
		if err != nil {
			return nil, notificationDestinationCreateProblem(err)
		}
		return &NotificationDestinationCreatedOutput{Body: NotificationDestinationCreated{ID: ID(row.ID), Name: row.Name, Type: row.Type, URLHost: row.URLHost, SigningSecret: secret}}, nil
	})
}
