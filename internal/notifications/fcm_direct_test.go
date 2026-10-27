package notifications

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFCMDirectSend covers the direct Firebase sender: one OAuth exchange
// serves several sends, the data message carries the keys the Android app
// reads, and FCM's UNREGISTERED marks the device as gone.
func TestFCMDirectSend(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	tokenCalls := 0
	var sent []fcmDirectMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.FormValue("assertion") == "" {
				http.Error(w, "bad grant", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case "/send":
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var msg fcmDirectMessage
			_ = json.NewDecoder(r.Body).Decode(&msg)
			sent = append(sent, msg)
			if msg.Message.Token == "gone" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","message":"Requested entity was not found.","details":[{"errorCode":"UNREGISTERED"}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
		}
	}))
	defer srv.Close()
	account, _ := json.Marshal(fcmServiceAccount{
		Type: "service_account", ProjectID: "p", ClientEmail: "svc@p.iam.gserviceaccount.com",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		TokenURI:   srv.URL + "/token",
	})
	direct, err := NewFCMDirect(account, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	direct.sendURL = srv.URL + "/send"

	ok := direct.Send(t.Context(), "device", "delivery-1", pushRelayTarget{ServerID: "srv", ProfileID: "prof"})
	if !ok.OK || ok.RelayRequestID != "projects/p/messages/1" {
		t.Fatalf("send = %+v", ok)
	}
	data := sent[0].Message.Data
	if data["silo_delivery_id"] != "delivery-1" || data["silo_mode"] != relayModePrivateAlert ||
		data["silo_server_id"] != "srv" || data["silo_profile_id"] != "prof" || sent[0].Message.Android.Priority != "HIGH" {
		t.Fatalf("message = %+v", sent[0].Message)
	}
	gone := direct.Send(t.Context(), "gone", "delivery-2", pushRelayTarget{})
	if gone.OK || !gone.TerminalDevice || gone.UpstreamReason != "UNREGISTERED" {
		t.Fatalf("unregistered send = %+v", gone)
	}
	if _, has := sent[1].Message.Data["silo_server_id"]; has {
		t.Fatal("partial target sent")
	}
	if tokenCalls != 1 {
		t.Fatalf("token requests = %d, want 1 cached across sends", tokenCalls)
	}
	if _, err := NewFCMDirect([]byte(strings.Replace(string(account), "service_account", "user", 1)), nil); err == nil {
		t.Fatal("accepted a non service account")
	}
}
