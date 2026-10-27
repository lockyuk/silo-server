package notifications

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// FCMServiceAccountEnv names the operator's Firebase service account JSON
// file. When it is set and readable, Android installations may register with
// the fcm_direct provider and this server sends their pushes straight to
// Firebase Cloud Messaging, without the Silo relay. It exists for builds of
// the Android app signed into the operator's own Firebase project.
const FCMServiceAccountEnv = "SILO_FCM_SERVICE_ACCOUNT_FILE"

const (
	fcmScope            = "https://www.googleapis.com/auth/firebase.messaging"
	fcmDefaultTokenURI  = "https://oauth2.googleapis.com/token"
	fcmSendURLFormat    = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
	fcmAccessTokenSlack = 5 * time.Minute
)

// ErrPushProviderUnconfigured means a device asked for a provider this server
// cannot send through.
var ErrPushProviderUnconfigured = errors.New("push provider is not configured on this server")

type fcmServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// FCMDirect sends data-only pushes through FCM HTTP v1 with a service
// account. Like the relay, the push carries no notification text: the app
// fetches the delivery from this server and renders it.
type FCMDirect struct {
	projectID string
	email     string
	key       *rsa.PrivateKey
	tokenURI  string
	client    *http.Client
	sendURL   string

	mu          sync.Mutex
	accessToken string
	expires     time.Time
}

// LoadFCMDirectFromEnv reads the service account named by
// FCMServiceAccountEnv. It returns nil, nil when the variable is unset.
func LoadFCMDirectFromEnv() (*FCMDirect, error) {
	path := strings.TrimSpace(os.Getenv(FCMServiceAccountEnv))
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FCMServiceAccountEnv, err)
	}
	return NewFCMDirect(raw, nil)
}

// NewFCMDirect parses a service account JSON document.
func NewFCMDirect(serviceAccount []byte, client *http.Client) (*FCMDirect, error) {
	var account fcmServiceAccount
	if err := json.Unmarshal(serviceAccount, &account); err != nil {
		return nil, fmt.Errorf("parse firebase service account: %w", err)
	}
	if account.Type != "service_account" || account.ProjectID == "" || account.ClientEmail == "" || account.PrivateKey == "" {
		return nil, errors.New("firebase service account is incomplete")
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return nil, errors.New("firebase service account private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse firebase private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("firebase private key is not RSA")
	}
	tokenURI := account.TokenURI
	if tokenURI == "" {
		tokenURI = fcmDefaultTokenURI
	}
	if client == nil {
		client = newNotificationHTTPClient(nil, pushRelayRequestTimeout)
	}
	return &FCMDirect{
		projectID: account.ProjectID,
		email:     account.ClientEmail,
		key:       key,
		tokenURI:  tokenURI,
		client:    client,
		sendURL:   fmt.Sprintf(fcmSendURLFormat, url.PathEscape(account.ProjectID)),
	}, nil
}

// ProjectID is the Firebase project pushes go through.
func (f *FCMDirect) ProjectID() string {
	if f == nil {
		return ""
	}
	return f.projectID
}

// token returns a cached OAuth access token, minting one with a signed JWT
// assertion when the cached one is about to expire.
func (f *FCMDirect) token(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.accessToken != "" && now.Add(fcmAccessTokenSlack).Before(f.expires) {
		return f.accessToken, nil
	}
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   f.email,
		"scope": fcmScope,
		"aud":   f.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}).SignedString(f.key)
	if err != nil {
		return "", fmt.Errorf("sign firebase assertion: %w", err)
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("firebase token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&body)
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("firebase token request: HTTP %d %s", resp.StatusCode, body.Error)
	}
	f.accessToken = body.AccessToken
	f.expires = now.Add(time.Duration(body.ExpiresIn) * time.Second)
	return f.accessToken, nil
}

// fcmDirectMessage is the FCM HTTP v1 request: a high-priority data message
// with the keys the Silo Android app reads from relay pushes.
type fcmDirectMessage struct {
	Message struct {
		Token   string            `json:"token"`
		Data    map[string]string `json:"data"`
		Android struct {
			Priority    string `json:"priority"`
			CollapseKey string `json:"collapse_key,omitempty"`
		} `json:"android"`
	} `json:"message"`
}

// Send delivers one push. The result follows the relay's shape so the push
// sender's retry and device bookkeeping treat both providers alike.
func (f *FCMDirect) Send(ctx context.Context, token, deliveryID string, target pushRelayTarget) pushSendResult {
	if f == nil {
		return pushSendResult{HTTPStatus: http.StatusServiceUnavailable, UpstreamReason: "fcm_direct_unconfigured", Message: ErrPushProviderUnconfigured.Error()}
	}
	access, err := f.token(ctx)
	if err != nil {
		return pushSendResult{HTTPStatus: http.StatusServiceUnavailable, UpstreamReason: "fcm_direct_auth_failed", Message: err.Error()}
	}
	var msg fcmDirectMessage
	msg.Message.Token = token
	msg.Message.Data = map[string]string{
		"silo_delivery_id": deliveryID,
		"silo_mode":        relayModePrivateAlert,
	}
	if target.ServerID != "" && target.ProfileID != "" {
		msg.Message.Data["silo_server_id"] = target.ServerID
		msg.Message.Data["silo_profile_id"] = target.ProfileID
	}
	msg.Message.Android.Priority = "HIGH"
	msg.Message.Android.CollapseKey = deliveryID
	body, err := json.Marshal(msg)
	if err != nil {
		return pushSendResult{UpstreamReason: "payload_build_failed", Message: "fcm payload build failed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sendURL, bytes.NewReader(body))
	if err != nil {
		return pushSendResult{UpstreamReason: "invalid_fcm_url", Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return pushSendResult{UpstreamReason: "fcm_unreachable", Message: classifyWebhookError(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ok struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(data, &ok)
		return pushSendResult{OK: true, HTTPStatus: resp.StatusCode, RelayRequestID: ok.Name}
	}
	var failure struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &failure)
	code := failure.Error.Status
	for _, detail := range failure.Error.Details {
		if detail.ErrorCode != "" {
			code = detail.ErrorCode
		}
	}
	if code == "" {
		code = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		f.mu.Lock()
		f.accessToken = ""
		f.mu.Unlock()
	}
	return pushSendResult{
		HTTPStatus:     resp.StatusCode,
		RetryAfter:     parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		UpstreamReason: code,
		Message:        strings.TrimSpace(failure.Error.Message),
		// UNREGISTERED: the app was uninstalled or the token rotated.
		TerminalDevice: code == "UNREGISTERED",
	}
}
