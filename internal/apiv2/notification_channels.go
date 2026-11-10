package apiv2

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type NotificationChannelService interface {
	ClearEmailAddress(context.Context, int, string) error
	EmailPreferences(context.Context, int, string) (notifications.EmailPreferencesState, error)
	SetEmailMode(context.Context, int, string, string) error
	SetEmailChoices(context.Context, int, string, notifications.EmailChoices) error
	SetEmailSends(context.Context, int, string, bool, bool, bool) error
}

type NotificationEmailPreferencesInput struct {
	Body struct {
		Mode        string    `json:"mode,omitempty" enum:"off,per_episode,daily_digest,per_episode_and_digest,weekly_digest,per_episode_and_weekly_digest" doc:"One mode for older clients; instant, daily and weekly take precedence when any is sent."`
		Instant     *bool     `json:"instant,omitempty" doc:"An email shortly after something new arrives."`
		Daily       *bool     `json:"daily,omitempty" doc:"A daily digest."`
		Weekly      *bool     `json:"weekly,omitempty" doc:"A weekly digest."`
		Content     *string   `json:"content,omitempty" enum:"followed,all" doc:"What to email: shows the profile follows, or everything new it can open."`
		Types       *[]string `json:"types,omitempty" doc:"Item types to include (movie, episode); empty includes both."`
		LibraryIDs  *[]int    `json:"library_ids,omitempty" doc:"Libraries to include; empty includes every library the profile can open."`
		MaxAgeHours *int      `json:"max_age_hours,omitempty" doc:"How recently an item must have been added to count as new, in hours; 0 uses the server default."`
	}
}
type NotificationEmailPreferences struct {
	Mode           string   `json:"mode" enum:"off,per_episode,daily_digest,per_episode_and_digest,weekly_digest,per_episode_and_weekly_digest"`
	CustomEmail    string   `json:"custom_email"`
	PendingEmail   string   `json:"pending_email"`
	CanEditAddress bool     `json:"can_edit_address"`
	Instant        bool     `json:"instant" doc:"An email shortly after something new arrives."`
	Daily          bool     `json:"daily" doc:"A daily digest."`
	Weekly         bool     `json:"weekly" doc:"A weekly digest."`
	Content        string   `json:"content" enum:"followed,all" doc:"What to email: shows the profile follows, or everything new it can open."`
	Types          []string `json:"types" doc:"Item types to include; empty includes both."`
	LibraryIDs     []int    `json:"library_ids" doc:"Libraries to include; empty includes every library the profile can open."`
	MaxAgeHours    int      `json:"max_age_hours" doc:"How recently an item must have been added to count as new, in hours; 0 uses the server default."`
}
type NotificationEmailPreferencesOutput struct{ Body NotificationEmailPreferences }

func (reg *Registry) notificationEmailState(ctx context.Context) (*NotificationEmailPreferencesOutput, error) {
	svc := reg.deps.NotificationChannels
	if svc == nil {
		return nil, unavailable("notification channels")
	}
	view, err := svc.EmailPreferences(ctx, claimsFrom(ctx).UserID, profileFrom(ctx))
	if err != nil {
		return nil, serviceProblem(err)
	}
	content := view.Content
	if content == "" {
		content = notifications.EmailContentFollowed
	}
	types, libraries := view.Types, view.LibraryIDs
	if types == nil {
		types = []string{}
	}
	if libraries == nil {
		libraries = []int{}
	}
	return &NotificationEmailPreferencesOutput{Body: NotificationEmailPreferences{Mode: view.Mode, CustomEmail: view.CustomEmail, PendingEmail: view.PendingEmail, CanEditAddress: !view.IsChild, Instant: view.Instant, Daily: view.Daily, Weekly: view.Weekly, Content: content, Types: types, LibraryIDs: libraries, MaxAgeHours: view.MaxAgeHours}}, nil
}
func registerNotificationChannels(reg *Registry) {
	clearAddress := notificationOperation(http.MethodDelete, "/email-preferences/address", "clearNotificationEmailAddress")
	clearAddress.RetrySafety = RetrySafetyNonRetryable
	clearAddress.Summary = "Clear the profile's verified and pending notification address and turn email delivery off. Send once; replay may clear a newer address."
	Register(reg, clearAddress, func(ctx context.Context, _ *struct{}) (*NotificationEmailPreferencesOutput, error) {
		svc := reg.deps.NotificationChannels
		if svc == nil {
			return nil, unavailable("notification channels")
		}
		if err := svc.ClearEmailAddress(ctx, claimsFrom(ctx).UserID, profileFrom(ctx)); err != nil {
			if errors.Is(err, notifications.ErrEmailChildProfile) {
				return nil, NewProblem(TypePermissionDenied, "Child profiles cannot change the notification address.")
			}
			return nil, serviceProblem(err)
		}
		return reg.notificationEmailState(ctx)
	})

	emailRead := notificationOperation(http.MethodGet, "/email-preferences", "getNotificationEmailPreferences")
	emailRead.Summary = "Read the acting profile's email notification preferences."
	Register(reg, emailRead, func(ctx context.Context, _ *struct{}) (*NotificationEmailPreferencesOutput, error) {
		return reg.notificationEmailState(ctx)
	})
	emailWrite := notificationOperation(http.MethodPut, "/email-preferences", "updateNotificationEmailPreferences")
	emailWrite.RetrySafety = RetrySafetyNonRetryable
	emailWrite.Summary = "Set the acting profile's email delivery mode and what its emails include."
	Register(reg, emailWrite, func(ctx context.Context, in *NotificationEmailPreferencesInput) (*NotificationEmailPreferencesOutput, error) {
		svc := reg.deps.NotificationChannels
		if svc == nil {
			return nil, unavailable("notification channels")
		}
		b := in.Body
		sends := b.Instant != nil || b.Daily != nil || b.Weekly != nil
		choices := b.Content != nil || b.Types != nil || b.LibraryIDs != nil || b.MaxAgeHours != nil
		if b.Mode == "" && !sends && !choices {
			return nil, NewProblem(TypeValidationFailed, "Nothing to change: send a mode, the sends or the choices.")
		}
		if b.Content != nil || b.Types != nil || b.LibraryIDs != nil || b.MaxAgeHours != nil {
			choices := notifications.EmailChoices{Content: b.Content, Types: b.Types, LibraryIDs: b.LibraryIDs, MaxAgeHours: b.MaxAgeHours}
			if err := svc.SetEmailChoices(ctx, claimsFrom(ctx).UserID, profileFrom(ctx), choices); err != nil {
				return nil, notificationChannelProblem(err)
			}
		}
		if b.Instant != nil || b.Daily != nil || b.Weekly != nil {
			current, err := svc.EmailPreferences(ctx, claimsFrom(ctx).UserID, profileFrom(ctx))
			if err != nil {
				return nil, serviceProblem(err)
			}
			instant, daily, weekly := current.Instant, current.Daily, current.Weekly
			if b.Instant != nil {
				instant = *b.Instant
			}
			if b.Daily != nil {
				daily = *b.Daily
			}
			if b.Weekly != nil {
				weekly = *b.Weekly
			}
			if err := svc.SetEmailSends(ctx, claimsFrom(ctx).UserID, profileFrom(ctx), instant, daily, weekly); err != nil {
				return nil, notificationChannelProblem(err)
			}
		} else if b.Mode != "" {
			if err := svc.SetEmailMode(ctx, claimsFrom(ctx).UserID, profileFrom(ctx), b.Mode); err != nil {
				return nil, notificationChannelProblem(err)
			}
		}
		return reg.notificationEmailState(ctx)
	})
}

func notificationChannelProblem(err error) *Problem {
	switch {
	case errors.Is(err, notifications.ErrEmailModeInvalid):
		return NewProblem(TypeValidationFailed, "Unknown notification mode.")
	case errors.Is(err, notifications.ErrEmailModeNotAllowed):
		return NewProblem(TypeValidationFailed, "Per-episode delivery is disabled by the administrator.")
	case errors.Is(err, notifications.ErrEmailNoAddress):
		return NewProblem(TypeValidationFailed, "Verify an email address for this profile first.")
	default:
		return serviceProblem(err)
	}
}
