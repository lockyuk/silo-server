package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

func (h *NotificationsHandler) ListNotificationWebhookPage(ctx context.Context, user int, profile string, limit int, after *notifications.Cursor) ([]notifications.Webhook, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.ListPage(ctx, user, profile, limit, after)
}

func (h *NotificationsHandler) TestNotificationWebhook(ctx context.Context, user int, profile, id string) (*notifications.WebhookTestResult, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.Test(ctx, user, profile, id)
}

func (h *NotificationsHandler) CreateNotificationWebhook(ctx context.Context, userID int, profileID string, input notifications.WebhookInput) (*notifications.Webhook, string, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, "", apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.Create(ctx, userID, profileID, input)
}

func (h *NotificationsHandler) DeleteNotificationWebhook(ctx context.Context, user int, profile, id string, check func(int64) error) error {
	svc := h.webhooks()
	if svc == nil {
		return apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.DeleteGuarded(ctx, user, profile, id, check)
}

func (h *NotificationsHandler) RotateNotificationWebhookSecret(ctx context.Context, user int, profile, id string) (string, error) {
	svc := h.webhooks()
	if svc == nil {
		return "", apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.RotateSecretV2(ctx, user, profile, id)
}

func (h *NotificationsHandler) UpdateNotificationWebhook(ctx context.Context, user int, profile, id string, input notifications.WebhookInput, check func(int64) error) (*notifications.Webhook, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.UpdateGuarded(ctx, user, profile, id, input, check)
}
