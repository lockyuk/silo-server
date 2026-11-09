package apiv2

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeNotificationChannels struct {
	user          int
	profile, mode string
	writes        int
	choices       notifications.EmailChoices
	err           error
}

func (f *fakeNotificationChannels) EmailPreferences(_ context.Context, user int, profile string) (notifications.EmailPreferencesState, error) {
	f.user, f.profile = user, profile
	return notifications.EmailPreferencesState{Mode: "off", CustomEmail: "verified@example.test", PendingEmail: "", IsChild: true}, nil
}
func (f *fakeNotificationChannels) SetEmailMode(_ context.Context, user int, profile, mode string) error {
	f.user, f.profile, f.mode = user, profile, mode
	f.writes++
	return f.err
}
func (f *fakeNotificationChannels) SetEmailChoices(_ context.Context, user int, profile string, c notifications.EmailChoices) error {
	f.user, f.profile, f.choices = user, profile, c
	return f.err
}
func (f *fakeNotificationChannels) SetEmailSends(_ context.Context, user int, profile string, instant, daily, weekly bool) error {
	f.user, f.profile = user, profile
	f.writes++
	return f.err
}
func TestNotificationChannelAuthority(t *testing.T) {
	fake := new(fakeNotificationChannels)
	deps := pilotDeps(nil, nil)
	deps.NotificationChannels = fake
	h := NewHandler(deps)
	emailPath := Prefix + "/notifications/email-preferences"
	rec := do(t, h, http.MethodGet, emailPath, "", bearer(memberToken))
	if rec.Code != 422 {
		t.Fatalf("profileless email: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, emailPath, "", profileOwner())
	if rec.Code != 200 || fake.user != 1 || fake.profile != "p-owner" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{emailPath} {
		rec = do(t, h, http.MethodPut, path, `{"mode":"per_episode_and_digest"}`, profileOwner())
		if rec.Code != 200 || fake.mode != "per_episode_and_digest" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		before := fake.writes
		for _, body := range []string{`{}`, `{"mode":null}`, `{"mode":"unknown"}`, `{"mode":"off","custom_email":"unverified@example.test"}`} {
			requireProblem(t, do(t, h, http.MethodPut, path, body, profileOwner()), TypeValidationFailed)
		}
		if fake.writes != before {
			t.Fatal("invalid mode dispatched")
		}
	}
	for _, err := range []error{notifications.ErrEmailNoAddress, notifications.ErrEmailModeNotAllowed} {
		require := notificationChannelProblem(fmt.Errorf("wrapped: %w", err))
		if require.Status != 422 {
			t.Fatalf("wrong domain error: %v", require)
		}
	}
}

func notificationChannelFixtureCases() []fixtureCase {
	cases := []fixtureCase{
		{name: "notification_email_preferences", operationID: "getNotificationEmailPreferences", method: "GET", path: Prefix + "/notifications/email-preferences", headers: profileOwner(), status: 200, schema: "#/components/schemas/NotificationEmailPreferences", scenario: "A child profile cannot edit its notification email address."},
		{name: "notification_email_mode", operationID: "updateNotificationEmailPreferences", method: "PUT", path: Prefix + "/notifications/email-preferences", body: `{"mode":"off"}`, headers: profileOwner(), status: 200, schema: "#/components/schemas/NotificationEmailPreferences", scenario: "The existing delivery mode setter is invoked once."},
	}
	for i := range cases {
		cases[i].assertHeaders = []string{"Content-Type", "Cache-Control"}
	}
	return cases
}

func (f *fakeNotificationChannels) ClearEmailAddress(_ context.Context, user int, profile string) error {
	f.user, f.profile = user, profile
	f.writes++
	return f.err
}
