package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeNotificationDestinations struct {
	deleted string
	calls   int
	user    int
	profile string
	limit   int
	// loginSession is the login session the last subscribe recorded.
	loginSession string
}

func (f *fakeNotificationDestinations) ListNotificationWebhookPage(_ context.Context, user int, profile string, limit int, after *notifications.Cursor) ([]notifications.Webhook, error) {
	f.user, f.profile, f.limit = user, profile, limit
	rows := []notifications.Webhook{{ID: "01", ProfileID: profile, Revision: 7, Name: "Example", Type: "generic", URLHost: "example.test", URLCiphertext: "sensitive-url", SigningSecretCiphertext: new("sensitive-secret"), CreatedAt: fixedTime()}, {ID: "02", ProfileID: profile, Revision: 7, Name: "Other", Type: "generic", URLHost: "example.test", CreatedAt: fixedTime()}}
	if after != nil {
		rows = rows[1:]
	}
	return rows, nil
}
func TestNotificationDestinationCursors(t *testing.T) {
	fake := new(fakeNotificationDestinations)
	deps := pilotDeps(nil, nil)
	deps.NotificationDestinations = fake
	h := NewHandler(deps)
	for _, tc := range []struct {
		path    string
		headers map[string]string
		profile string
	}{
		{Prefix + "/notifications/webhooks", profileOwner(), "p-owner"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			fake.user = 0
			rec := do(t, h, http.MethodGet, tc.path+"?limit=1", "", tc.headers)
			if rec.Code != 200 || fake.limit != 2 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "sensitive") {
				t.Fatal("destination credential exposed")
			}
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Page PageInfo `json:"page"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != "01" || !page.Page.HasMore || page.Page.NextCursor == "" {
				t.Fatalf("%+v", page)
			}
			query := "?limit=1&cursor=" + url.QueryEscape(page.Page.NextCursor)
			rec = do(t, h, http.MethodGet, tc.path+query, "", tc.headers)
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != "02" || page.Page.HasMore {
				t.Fatalf("%+v", page)
			}
			requireProblem(t, do(t, h, http.MethodGet, tc.path+strings.Replace(query, "limit=1", "limit=2", 1), "", tc.headers), TypeInvalidCursor)
			if tc.profile != "" && (fake.user != 1 || fake.profile != tc.profile) {
				t.Fatalf("listed for user %d profile %q, want the acting account's profile", fake.user, fake.profile)
			}
			if tc.profile != "" {
				requireProblem(t, do(t, h, http.MethodGet, tc.path+query, "", with(bearer(memberToken), "X-Profile-Id", "p-primary")), TypeInvalidCursor)
			}
		})
	}
}

func notificationDestinationFixtureCases() []fixtureCase {
	cases := []fixtureCase{
		{name: "notification_webhooks", operationID: listNotificationWebhooksOperation, path: Prefix + "/notifications/webhooks", headers: profileOwner(), schema: "#/components/schemas/CollectionNotificationWebhookDestination"},
	}
	for i := range cases {
		cases[i].method = "GET"
		cases[i].status = 200
		cases[i].scenario = "Bounded notification destination metadata excludes reusable keys and webhook URL ciphertext."
		cases[i].assertHeaders = []string{"Content-Type", "Cache-Control"}
	}
	return cases
}

func (f *fakeNotificationDestinations) DeleteNotificationWebhook(_ context.Context, user int, profile, id string, check func(int64) error) error {
	if err := check(7); err != nil {
		return err
	}
	f.calls++
	f.user, f.profile, f.deleted = user, profile, id
	return nil
}
func TestNotificationWebhookDelete(t *testing.T) {
	f := new(fakeNotificationDestinations)
	deps := pilotDeps(nil, nil)
	deps.NotificationDestinations = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/webhooks/01"
	page := do(t, h, http.MethodGet, Prefix+"/notifications/webhooks?limit=1", "", profileOwner())
	var observed struct {
		Items []struct {
			ETag string `json:"etag"`
		} `json:"items"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &observed); err != nil || len(observed.Items) != 1 {
		t.Fatalf("list: %s %v", page.Body.String(), err)
	}
	for range 2 {
		rec := do(t, h, http.MethodDelete, path, "", with(profileOwner(), "If-Match", observed.Items[0].ETag))
		if rec.Code != 204 || rec.Body.Len() != 0 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	if f.calls != 2 || f.user != 1 || f.profile != "p-owner" || f.deleted != "01" {
		t.Fatalf("%+v", f)
	}
	requireProblem(t, do(t, h, http.MethodDelete, path, "", nil), TypeAuthenticationRequired)
	requireProblem(t, do(t, h, http.MethodDelete, path, "", bearer(memberToken)), TypeValidationFailed)
	if f.calls != 2 {
		t.Fatal("unauthorized dispatch")
	}
	requireProblem(t, do(t, h, http.MethodDelete, path, "", profileOwner()), TypePreconditionRequired)
	stale := do(t, h, http.MethodDelete, path, "", with(profileOwner(), "If-Match", notificationWebhookTag("p-owner", "01", 6).String()))
	requireProblem(t, stale, TypePreconditionFailed)
	if stale.Header().Get("ETag") != notificationWebhookTag("p-owner", "01", 7).String() || f.calls != 2 {
		t.Fatal("stale deletion passed or validator missing")
	}
	deps.NotificationDestinations = nil
	requireProblem(t, do(t, NewHandler(deps), http.MethodDelete, path, "", profileOwner()), TypeDependencyUnavailable)
}

func (f *fakeNotificationDestinations) RotateNotificationWebhookSecret(_ context.Context, user int, profile, id string) (string, error) {
	f.calls++
	f.user, f.profile, f.deleted = user, profile, id
	if id == "discord" {
		return "", notifications.ErrWebhookInvalid
	}
	if id == "missing" {
		return "", notifications.ErrWebhookNotFound
	}
	return "synthetic-secret", nil
}
func TestNotificationWebhookRotate(t *testing.T) {
	f := new(fakeNotificationDestinations)
	deps := pilotDeps(nil, nil)
	deps.NotificationDestinations = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/webhooks/row-one/rotate-secret"
	rec := do(t, h, http.MethodPost, path, "", profileOwner())
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), `"signing_secret":"synthetic-secret"`) || f.calls != 1 || f.user != 1 || f.profile != "p-owner" {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body.String(), f)
	}
	requireProblem(t, do(t, h, http.MethodPost, path, "", nil), TypeAuthenticationRequired)
	if f.calls != 1 {
		t.Fatal("unauthorized dispatch")
	}
	requireProblem(t, do(t, h, http.MethodPost, strings.Replace(path, "row-one", "discord", 1), "", profileOwner()), TypeValidationFailed)
	requireProblem(t, do(t, h, http.MethodPost, strings.Replace(path, "row-one", "missing", 1), "", profileOwner()), TypeNotFound)
	deps.NotificationDestinations = nil
	requireProblem(t, do(t, NewHandler(deps), http.MethodPost, path, "", profileOwner()), TypeDependencyUnavailable)
}

func (f *fakeNotificationDestinations) UpdateNotificationWebhook(_ context.Context, user int, profile, id string, input notifications.WebhookInput, check func(int64) error) (*notifications.Webhook, error) {
	f.calls++
	f.user, f.profile = user, profile
	if id == "missing" {
		return nil, notifications.ErrWebhookNotFound
	}
	if err := check(7); err != nil {
		return nil, err
	}
	if input.Name != nil && *input.Name == "" {
		return nil, notifications.ErrWebhookInvalid
	}
	return &notifications.Webhook{ID: id, ProfileID: profile, Name: "updated", Type: "generic", Revision: 8}, nil
}

func TestNotificationWebhookUpdate(t *testing.T) {
	f := new(fakeNotificationDestinations)
	deps := pilotDeps(nil, nil)
	deps.NotificationDestinations = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/webhooks/row-one"
	tag := notificationWebhookTag("p-owner", "row-one", 7).String()
	headers := profileOwner()
	headers["If-Match"] = tag
	rec := do(t, h, http.MethodPut, path, `{"name":"updated","enabled":false}`, headers)
	if rec.Code != 200 || rec.Header().Get("ETag") != notificationWebhookTag("p-owner", "row-one", 8).String() || !strings.Contains(rec.Body.String(), `"name":"updated"`) || f.user != 1 || f.profile != "p-owner" {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body.String(), f)
	}
	requireProblem(t, do(t, h, http.MethodPut, path, `{}`, profileOwner()), TypePreconditionRequired)
	headers["If-Match"] = notificationWebhookTag("p-owner", "row-one", 6).String()
	requireProblem(t, do(t, h, http.MethodPut, path, `{}`, headers), TypePreconditionFailed)
	requireProblem(t, do(t, h, http.MethodPut, Prefix+"/notifications/webhooks/missing", `{}`, profileOwner()), TypeNotFound)
	headers["If-Match"] = tag
	requireProblem(t, do(t, h, http.MethodPut, path, `{"name":""}`, headers), TypeValidationFailed)
	before := f.calls
	requireProblem(t, do(t, h, http.MethodPut, path, `{}`, nil), TypeAuthenticationRequired)
	if f.calls != before {
		t.Fatal("unauthorized dispatch")
	}
	deps.NotificationDestinations = nil
	requireProblem(t, do(t, NewHandler(deps), http.MethodPut, path, `{}`, headers), TypeDependencyUnavailable)
}
