// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

const discoverySyntheticKey = "synthetic-discovery-credential"

func discoveryFixture(t *testing.T, handler http.HandlerFunc) (*Service, string) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	s, _ := setup(t)
	return s, upstream.URL
}

func TestDiscoveryNativeMetadataAndAuthentication(t *testing.T) {
	for _, test := range []struct {
		kind, baseSuffix, path, body, authHeader, authValue string
	}{
		{"openai", "/proxy/v1", "/proxy/v1/models", `{"data":[{"id":"gpt-synthetic","owned_by":"operator","created":42,"unknown":{"tasks":["image"]}}]}`, "Authorization", "Bearer " + discoverySyntheticKey},
		{"compatible", "/proxy/v1/", "/proxy/v1/models", `{"data":[{"id":"model-synthetic"}]}`, "Authorization", "Bearer " + discoverySyntheticKey},
		{"anthropic", "/proxy/v1", "/proxy/v1/models", `{"data":[{"id":"claude-synthetic","display_name":"Synthetic Claude","created_at":"2026-10-01T00:00:00Z","max_input_tokens":1000,"max_tokens":100}],"has_more":false,"last_id":"claude-synthetic"}`, "x-api-key", discoverySyntheticKey},
		{"gemini", "/proxy/v1beta", "/proxy/v1beta/models", `{"models":[{"name":"models/gemini-synthetic-001","displayName":"Synthetic Gemini","description":"Native description","inputTokenLimit":1000,"outputTokenLimit":100,"supportedGenerationMethods":["generateContent"]}]}`, "x-goog-api-key", discoverySyntheticKey},
	} {
		t.Run(test.kind, func(t *testing.T) {
			s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != test.path || r.Header.Get(test.authHeader) != test.authValue || r.URL.Query().Get("key") != "" {
					t.Error("native list path or header authentication changed")
				}
				if test.kind == "anthropic" && (r.Header.Get("anthropic-version") != "2023-06-01" || r.URL.Query().Get("limit") != "100") {
					t.Error("Anthropic native version or pagination limit missing")
				}
				if test.kind == "gemini" && r.URL.Query().Get("pageSize") != "100" {
					t.Error("Gemini native page size missing")
				}
				_, _ = w.Write([]byte(test.body))
			})
			out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: test.kind, BaseURL: base + test.baseSuffix, APIKey: discoverySyntheticKey})
			if err != nil || !out.Complete || out.Pages != 1 || len(out.Models) != 1 || len(out.Warnings) != 0 {
				t.Fatalf("native discovery: %#v %v", out, err)
			}
			model := out.Models[0]
			if test.kind == "anthropic" && (model.CreatedAt == "" || model.DisplayName == "" || model.InputTokenLimit == nil || *model.InputTokenLimit != 1000 || model.OutputTokenLimit == nil || *model.OutputTokenLimit != 100) {
				t.Fatalf("Anthropic metadata lost: %#v", model)
			}
			if test.kind == "gemini" && (model.ID != "gemini-synthetic-001" || model.NativeName != "models/gemini-synthetic-001" || model.InputTokenLimit == nil || *model.InputTokenLimit != 1000 || model.OutputTokenLimit == nil || *model.OutputTokenLimit != 100 || len(model.SupportedGenerationMethods) != 1) {
				t.Fatalf("Gemini native metadata lost: %#v", model)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "tasks") || strings.Contains(string(encoded), "pricing") || strings.Contains(string(encoded), discoverySyntheticKey) {
				t.Fatal("discovery invented capabilities/prices or exposed credentials")
			}
		})
	}
}

func TestDiscoveryPaginationDeduplicatesAndKeepsFirstMetadata(t *testing.T) {
	var calls atomic.Int64
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"first","display_name":"Original"},{"id":"shared"}],"has_more":true,"last_id":"shared"}`))
		} else {
			if r.URL.Query().Get("after_id") != "shared" {
				t.Error("cursor not sent through native after_id")
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"first","display_name":"Changed"},{"id":"second"}],"has_more":false,"last_id":"second"}`))
		}
	})
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "anthropic", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || !out.Complete || out.Pages != 2 || calls.Load() != 2 || len(out.Models) != 3 || out.Models[0].DisplayName != "Original" {
		t.Fatalf("paginated discovery: %#v %v", out, err)
	}
}

func TestDiscoveryGeminiCursorIsOpaqueAndStaysOnConfiguredOrigin(t *testing.T) {
	const cursor = "https://elsewhere.invalid/path?other=x&token=y"
	var calls atomic.Int64
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/one"}],"nextPageToken":"` + cursor + `"}`))
		} else {
			if r.URL.Path != "/v1beta/models" || r.URL.Query().Get("pageToken") != cursor || len(r.URL.Query()) != 2 {
				t.Error("opaque cursor changed URL origin/path or escaped query boundary")
			}
			_, _ = w.Write([]byte(`{"models":[{"name":"models/two"}]}`))
		}
	})
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "gemini", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || !out.Complete || out.Pages != 2 || calls.Load() != 2 || len(out.Models) != 2 {
		t.Fatalf("Gemini cursor: %#v %v", out, err)
	}
}

func TestDiscoveryFailedPageIsExplicitlyPartialAndDoesNotExposeUpstreamBody(t *testing.T) {
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"private upstream body ` + discoverySyntheticKey + `"}`))
		}
	})
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "anthropic", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || out.Complete || out.Pages != 1 || len(out.Models) != 1 || len(out.Warnings) != 1 {
		t.Fatalf("partial failure: %#v %v", out, err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), discoverySyntheticKey) || strings.Contains(string(encoded), "private upstream body") {
		t.Fatal("upstream body/credential exposed")
	}
}

func TestDiscoveryNeverFollowsCredentialRedirect(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"?key="+discoverySyntheticKey, http.StatusTemporaryRedirect)
	})
	_, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "openai", BaseURL: base, APIKey: discoverySyntheticKey})
	var own *Error
	if !errors.As(err, &own) || own.Status != 502 || strings.Contains(own.Message, discoverySyntheticKey) || targetCalls.Load() != 0 {
		t.Fatalf("credential redirect was followed or leaked: %v", err)
	}
}

func TestDiscoveryMissingRepeatedAndUnsupportedPagination(t *testing.T) {
	for _, test := range []struct{ name, kind, body string }{
		{"missing", "anthropic", `{"data":[{"id":"first"}],"has_more":true}`},
		{"repeated", "anthropic", `{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`},
		{"compatible", "compatible", `{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(test.body)) })
			out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: test.kind, BaseURL: base, APIKey: discoverySyntheticKey})
			wantCalls := int64(1)
			if test.name == "repeated" {
				wantCalls = 2
			}
			if err != nil || out.Complete || len(out.Models) != 1 || len(out.Warnings) != 1 || calls.Load() != wantCalls {
				t.Fatalf("pagination boundary: %#v %v calls=%d", out, err, calls.Load())
			}
		})
	}
}

func TestDiscoveryBounds(t *testing.T) {
	for _, test := range []struct {
		name, body string
		limits     discoveryLimits
		wantError  bool
		models     int
	}{
		{"models", `{"data":[{"id":"one"},{"id":"two"},{"id":"three"}],"has_more":false}`, discoveryLimits{pages: 10, models: 2, pageBytes: 2048, allBytes: 4096, timeout: time.Second}, false, 2},
		{"page", `{"data":[{"id":"one"}],"has_more":true,"last_id":"one"}`, discoveryLimits{pages: 1, models: 10, pageBytes: 2048, allBytes: 4096, timeout: time.Second}, false, 1},
		{"page bytes", `{"data":[{"id":"one"}],"has_more":false}`, discoveryLimits{pages: 10, models: 10, pageBytes: 8, allBytes: 4096, timeout: time.Second}, true, 0},
		{"total bytes", `{"data":[{"id":"one"}],"has_more":true,"last_id":"one"}`, discoveryLimits{pages: 10, models: 10, pageBytes: 2048, allBytes: 70, timeout: time.Second}, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) })
			out, err := s.discoverModels(context.Background(), DiscoveryInput{Kind: "anthropic", BaseURL: base, APIKey: discoverySyntheticKey}, test.limits)
			if (err != nil) != test.wantError || out.Complete || len(out.Models) != test.models || (!test.wantError && len(out.Warnings) != 1) {
				t.Fatalf("bounded discovery: %#v %v", out, err)
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		limits := defaultDiscoveryLimits
		limits.timeout = 20 * time.Millisecond
		started := time.Now()
		_, err := s.discoverModels(context.Background(), DiscoveryInput{Kind: "openai", BaseURL: base, APIKey: discoverySyntheticKey}, limits)
		if err == nil || time.Since(started) > time.Second {
			t.Fatal("discovery deadline was not enforced")
		}
	})
}

func TestDiscoveryInvalidAndCredentialEchoMetadataIsOmitted(t *testing.T) {
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"valid"},{"id":""},{"id":"echo","description":"` + discoverySyntheticKey + `"}]}`))
	})
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "openai", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || out.Complete || len(out.Models) != 1 || out.Models[0].ID != "valid" || len(out.Warnings) != 1 {
		t.Fatalf("invalid metadata was not omitted: %#v %v", out, err)
	}
}

func TestDiscoveryGeminiRejectsMalformedNativeResourceNames(t *testing.T) {
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"models/valid-001"},{"name":"publisher/invalid"},{"name":"bare-name"},{"name":"models/nested/invalid"},{"name":"models/"}]}`))
	})
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "gemini", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || out.Complete || len(out.Models) != 1 || out.Models[0].ID != "valid-001" || len(out.Warnings) != 1 {
		t.Fatalf("malformed native names became usable mapping IDs: %#v %v", out, err)
	}
}

func TestDiscoverySavedChannelDoesNotChangeRoutingOrMapping(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"new-upstream-model"}]}`))
	}))
	defer upstream.Close()
	s, db := setup(t, upstream)
	until := time.Now().UTC().Add(time.Minute)
	db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"failures": 3, "cooldown_until": until, "enabled": false})
	var before, after core.Channel
	if err := db.First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.DiscoverChannel(context.Background(), before.ID)
	if err != nil || !out.Complete || len(out.Models) != 1 {
		t.Fatalf("saved discovery: %#v %v", out, err)
	}
	if err := db.First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("discovery mutated channel mapping, routing health or operational state")
	}
	var reservations int64
	db.Model(&core.Reservation{}).Count(&reservations)
	if reservations != 0 {
		t.Fatal("discovery charged an inference reservation")
	}
}

func TestDiscoveryValidationAndAzureManualFallback(t *testing.T) {
	var calls atomic.Int64
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, in := range []DiscoveryInput{
		{Kind: "other", BaseURL: base, APIKey: discoverySyntheticKey},
		{Kind: "openai", BaseURL: base + "?key=" + discoverySyntheticKey, APIKey: discoverySyntheticKey},
		{Kind: "openai", BaseURL: "https://username:password@example.test", APIKey: discoverySyntheticKey},
		{Kind: "azure", BaseURL: "https://YOUR-RESOURCE-NAME.openai.azure.com", APIKey: discoverySyntheticKey},
		{Kind: "compatible", BaseURL: "https://YOUR-WORKSPACE-ID.example.test/v1", APIKey: discoverySyntheticKey},
		{Kind: "openai", BaseURL: base, APIKey: "invalid\r\ncredential"},
		{Kind: "openai", BaseURL: base},
	} {
		_, err := s.DiscoverPreview(context.Background(), in)
		var own *Error
		if !errors.As(err, &own) || own.Status != 400 || strings.Contains(own.Message, discoverySyntheticKey) {
			t.Fatalf("invalid discovery accepted or leaked: %v", err)
		}
	}
	out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: "azure", BaseURL: base, APIKey: discoverySyntheticKey})
	if err != nil || out.Complete || out.Pages != 0 || len(out.Models) != 0 || len(out.Warnings) != 1 || calls.Load() != 0 {
		t.Fatalf("Azure manual fallback performed a misleading/paid operation: %#v %v", out, err)
	}
}
