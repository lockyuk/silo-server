package apiv2

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/pushstyle"
)

// AdminPushStyleService manages how push notifications read and when they
// are sent.
type AdminPushStyleService interface {
	PushStyleSettings(context.Context) (pushstyle.Settings, error)
	SavePushStyleSettings(context.Context, pushstyle.Settings) (pushstyle.Settings, error)
	SendPushStyleTest(context.Context, string) (int, int, error)
}

// PushStyleSettings mirrors pushstyle.Settings field for field.
type PushStyleSettings struct {
	EpisodeTitle   string `json:"episode_title" doc:"Mustache template for a new episode's title. May use <b>, <i>, <u>, <s> and <font color>."`
	EpisodeBody    string `json:"episode_body" doc:"Mustache template for a new episode's text."`
	MovieTitle     string `json:"movie_title" doc:"Mustache template for a new movie's title."`
	MovieBody      string `json:"movie_body" doc:"Mustache template for a new movie's text."`
	GroupTitle     string `json:"group_title" doc:"Mustache template for the title of a push that stands for several episodes of one show."`
	GroupBody      string `json:"group_body" doc:"Mustache template for that push's text."`
	Image          string `json:"image" enum:"poster,backdrop,none"`
	CollapsedImage bool   `json:"collapsed_image" doc:"Show the image in the collapsed notification."`
	ExpandedImage  bool   `json:"expanded_image" doc:"Show the image in the expanded view."`
	Expanded       bool   `json:"expanded" doc:"Offer an expanded view. Android shows the newest notification expanded when it has one."`
	Synopsis       bool   `json:"synopsis" doc:"Show the synopsis when the notification is expanded."`
	SynopsisMax    int    `json:"synopsis_max" minimum:"0" maximum:"1000" doc:"Longest synopsis, in characters; 0 uses the default."`
	BatchMinutes   int    `json:"batch_minutes" minimum:"0" maximum:"240" doc:"How long a new episode's push waits for more episodes of the same show; 0 sends at once."`
	QuietEnabled   bool   `json:"quiet_enabled"`
	QuietStart     string `json:"quiet_start" doc:"HH:MM when pushes start being held."`
	QuietEnd       string `json:"quiet_end" doc:"HH:MM when held pushes are sent."`
	Timezone       string `json:"timezone" doc:"IANA time zone for quiet hours; empty uses the server's."`
}

type PushStylePreview struct {
	Title    string `json:"title" doc:"HTML subset; see NotificationPushContent."`
	Body     string `json:"body" doc:"HTML subset; see NotificationPushContent."`
	Synopsis string `json:"synopsis,omitempty"`
	Error    string `json:"error,omitempty" doc:"Why the templates did not render."`
}

type AdminPushStyle struct {
	Settings PushStyleSettings `json:"settings"`
	Defaults PushStyleSettings `json:"defaults"`
}

type AdminPushStyleOutput struct{ Body AdminPushStyle }

type AdminPushStyleSettingsInput struct {
	Body PushStyleSettings
}

type AdminPushStylePreviewInput struct {
	Body struct {
		Settings PushStyleSettings `json:"settings"`
		Kind     string            `json:"kind" enum:"episode,movie,group"`
	}
}

type AdminPushStylePreviewOutput struct{ Body PushStylePreview }

type AdminPushStyleTestInput struct {
	Body struct {
		ProfileID string `json:"profile_id" minLength:"1" maxLength:"128" doc:"Profile whose Android devices get the test."`
	}
}

type AdminPushStyleTestResult struct {
	Sent    int `json:"sent" doc:"Devices the push reached."`
	Devices int `json:"devices" doc:"The profile's Android devices."`
}

type AdminPushStyleTestOutput struct{ Body AdminPushStyleTestResult }

func (reg *Registry) adminPushStyle() (AdminPushStyleService, *Problem) {
	if reg.deps.AdminPushStyle == nil {
		return nil, unavailable("push settings")
	}
	return reg.deps.AdminPushStyle, nil
}

func pushStyleOperation(method, path, id, summary string) Operation {
	op := Operation{Operation: humaOp(method, Prefix+"/admin/notifications/push-style"+path, id, "admin", summary), Class: ClassActingAdmin, ServiceBacked: true}
	if method != http.MethodGet {
		op.DemoRestricted = true
		op.RetrySafety = RetrySafetyNonRetryable
	}
	return op
}

func pushStyleOutput(s pushstyle.Settings) *AdminPushStyleOutput {
	return &AdminPushStyleOutput{Body: AdminPushStyle{Settings: PushStyleSettings(s), Defaults: PushStyleSettings(pushstyle.DefaultSettings())}}
}

func pushStyleProblem(err error) error {
	var field *pushstyle.FieldError
	if errors.As(err, &field) {
		return validationProblem(locationBody+"."+field.Field, codeInvalid, field.Message)
	}
	return serviceProblem(err)
}

func registerAdminPushStyle(reg *Registry) {
	Register(reg, pushStyleOperation(http.MethodGet, "", "getAdminPushStyle", "Read the push notification templates, image, batching and quiet hours."),
		func(ctx context.Context, _ *struct{}) (*AdminPushStyleOutput, error) {
			svc, p := reg.adminPushStyle()
			if p != nil {
				return nil, p
			}
			s, err := svc.PushStyleSettings(ctx)
			if err != nil {
				return nil, serviceProblem(err)
			}
			return pushStyleOutput(s), nil
		})

	save := pushStyleOperation(http.MethodPut, "", "updateAdminPushStyle", "Save the push notification settings. Templates that fail to parse are rejected.")
	save.RetrySafety = RetrySafetyNaturalIdempotent
	Register(reg, save, func(ctx context.Context, in *AdminPushStyleSettingsInput) (*AdminPushStyleOutput, error) {
		svc, p := reg.adminPushStyle()
		if p != nil {
			return nil, p
		}
		s, err := svc.SavePushStyleSettings(ctx, pushstyle.Settings(in.Body))
		if err != nil {
			return nil, pushStyleProblem(err)
		}
		return pushStyleOutput(s), nil
	})

	preview := pushStyleOperation(http.MethodPost, "/preview", "previewAdminPushStyle", "Render unsaved settings with a sample episode, movie or group. Sends nothing.")
	preview.RetrySafety = RetrySafetyNaturalIdempotent
	preview.DemoRestricted = false
	Register(reg, preview, func(ctx context.Context, in *AdminPushStylePreviewInput) (*AdminPushStylePreviewOutput, error) {
		if _, p := reg.adminPushStyle(); p != nil {
			return nil, p
		}
		s, err := pushstyle.Settings(in.Body.Settings).Normalize()
		if err != nil {
			return &AdminPushStylePreviewOutput{Body: PushStylePreview{Error: err.Error()}}, nil
		}
		rendered, err := notifications.PreviewPush(s, in.Body.Kind)
		if err != nil {
			return &AdminPushStylePreviewOutput{Body: PushStylePreview{Error: err.Error()}}, nil
		}
		return &AdminPushStylePreviewOutput{Body: PushStylePreview{Title: rendered.Title, Body: rendered.Body, Synopsis: rendered.Synopsis}}, nil
	})

	Register(reg, pushStyleOperation(http.MethodPost, "/test", "sendAdminPushStyleTest", "Push the profile's newest notification to its Android devices with the saved settings. Each call sends."),
		func(ctx context.Context, in *AdminPushStyleTestInput) (*AdminPushStyleTestOutput, error) {
			svc, p := reg.adminPushStyle()
			if p != nil {
				return nil, p
			}
			sent, devices, err := svc.SendPushStyleTest(ctx, in.Body.ProfileID)
			if errors.Is(err, notifications.ErrPushDeliveryUnavailable) {
				return nil, NewProblem(TypeConflict, "Android push is switched off or not configured.")
			}
			if err != nil {
				return nil, serviceProblem(err)
			}
			return &AdminPushStyleTestOutput{Body: AdminPushStyleTestResult{Sent: sent, Devices: devices}}, nil
		})
}
