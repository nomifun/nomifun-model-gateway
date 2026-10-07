// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
)

func TestAdminDiscoveryPreviewRequiresAdministratorAndDoesNotSaveAccount(t *testing.T) {
	f := setupServer(t)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-preview-key" {
			t.Error("preview failed native list authentication")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"upstream-new"}]}`))
	}))
	defer upstream.Close()
	input, _ := json.Marshal(relay.DiscoveryInput{Kind: "openai", BaseURL: upstream.URL, APIKey: "synthetic-preview-key"})
	path := "/api/console/v1/admin/channels/discover-preview"
	response := call(t, f.app, "POST", path, string(input), "")
	if response.Code != 401 || calls.Load() != 0 {
		t.Fatal("anonymous discovery reached an upstream")
	}
	registration := call(t, f.app, "POST", "/api/console/v1/register", `{"email":"preview-user@example.test","name":"Preview User","password":"SyntheticPreviewPassword123!"}`, "")
	if registration.Code != 201 {
		t.Fatalf("synthetic user registration failed: %d", registration.Code)
	}
	session := stringField(t, document(t, registration), "session_token")
	response = call(t, f.app, "POST", path, string(input), session)
	if response.Code != 403 || calls.Load() != 0 {
		t.Fatal("non-administrator discovery reached an upstream")
	}
	var beforeChannels, beforeModels int64
	f.st.DB().Model(&core.Channel{}).Count(&beforeChannels)
	f.st.DB().Model(&core.Model{}).Count(&beforeModels)
	response = call(t, f.app, "POST", path, string(input), f.admin)
	var result relay.DiscoveryResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Complete || len(result.Models) != 1 || result.Models[0].ID != "upstream-new" || calls.Load() != 1 {
		t.Fatalf("preview discovery failed: %d %s", response.Code, response.Body.String())
	}
	var channels, models, reservations int64
	f.st.DB().Model(&core.Channel{}).Count(&channels)
	f.st.DB().Model(&core.Model{}).Count(&models)
	f.st.DB().Model(&core.Reservation{}).Count(&reservations)
	if channels != beforeChannels || models != beforeModels || reservations != 0 {
		t.Fatal("preview persisted an account, imported catalog models or charged inference")
	}
	var audits []core.AuditLog
	f.st.DB().Where("action = ?", "channel.discover_preview").Find(&audits)
	if len(audits) != 1 || audits[0].Resource != "openai" {
		t.Fatal("preview audit did not retain only the kind")
	}
	encoded, _ := json.Marshal(audits)
	if strings.Contains(string(encoded), "synthetic-preview-key") || strings.Contains(f.log.String(), "synthetic-preview-key") || strings.Contains(response.Body.String(), "synthetic-preview-key") {
		t.Fatal("preview credential entered response or logs")
	}
}

func TestAdminDiscoverySavedChannelKeepsConfigurationAndUsesSealedCredential(t *testing.T) {
	f := setupServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "synthetic-sealed-discovery-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("saved discovery did not use its own sealed native credential")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"existing-private"},{"id":"new-private"}],"has_more":false}`))
	}))
	defer upstream.Close()
	channel, err := f.st.CreateChannel(context.Background(), core.Channel{Name: "Saved discovery", Kind: "anthropic", BaseURL: upstream.URL, ModelsJSON: `{"configured-public":"existing-private"}`, EndpointsJSON: `["anthropic"]`, Weight: 1, Enabled: false}, "synthetic-sealed-discovery-key")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.st.Channel(context.Background(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := call(t, f.app, "POST", "/api/console/v1/admin/channels/"+strconv.FormatInt(channel.ID, 10)+"/discover", "", f.admin)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "new-private") || strings.Contains(response.Body.String(), "synthetic-sealed-discovery-key") {
		t.Fatalf("saved discovery failed: %d %s", response.Code, response.Body.String())
	}
	after, err := f.st.Channel(context.Background(), channel.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("saved discovery changed mapping, enabled state, sealed account or health")
	}
	missing := call(t, f.app, "POST", "/api/console/v1/admin/channels/999999/discover", "", f.admin)
	if missing.Code != 404 {
		t.Fatalf("missing account discovery: %d", missing.Code)
	}
}

func TestAdminDiscoveryErrorsAndPartialResultsAreSafe(t *testing.T) {
	f := setupServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/first"}],"nextPageToken":"next"}`))
		} else {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":"private-response synthetic-error-key"}`))
		}
	}))
	defer upstream.Close()
	input, _ := json.Marshal(relay.DiscoveryInput{Kind: "gemini", BaseURL: upstream.URL, APIKey: "synthetic-error-key"})
	path := "/api/console/v1/admin/channels/discover-preview"
	response := call(t, f.app, "POST", path, string(input), f.admin)
	var result relay.DiscoveryResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Complete || result.Pages != 1 || len(result.Models) != 1 || len(result.Warnings) != 1 {
		t.Fatalf("partial discovery hidden: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-response") || strings.Contains(response.Body.String(), "synthetic-error-key") || strings.Contains(f.log.String(), "synthetic-error-key") {
		t.Fatal("partial warning leaked upstream body or credential")
	}
	invalid := call(t, f.app, "POST", path, `{"kind":"openai","base_url":"https://example.test?key=synthetic-error-key","api_key":"synthetic-error-key"}`, f.admin)
	if invalid.Code != 400 || strings.Contains(invalid.Body.String(), "synthetic-error-key") {
		t.Fatal("invalid preview URL accepted or disclosed")
	}
	unknown := call(t, f.app, "POST", path, `{"kind":"openai","base_url":"https://example.test","api_key":"synthetic-error-key","save_draft":true}`, f.admin)
	if unknown.Code != 400 {
		t.Fatal("discovery preview accepted an unsupported persistence field")
	}
}
