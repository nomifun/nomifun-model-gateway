// SPDX-License-Identifier: Apache-2.0
package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nomifun/nomifun-model-gateway/conformance"
	"github.com/nomifun/nomifun-model-gateway/mock"
)

func fixture(t *testing.T) conformance.Fixture {
	t.Helper()
	f, e := os.Open("../testdata/conformance.json")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	value, e := conformance.LoadFixture(f)
	if e != nil {
		t.Fatal(e)
	}
	return value
}

func TestEncryptedReplayRetainsOriginalInput(t *testing.T) {
	for _, original := range []string{"string", "array"} {
		t.Run(original, func(t *testing.T) {
			f := fixture(t)
			expected := "Reply briefly with hello."
			if original == "array" {
				expected = "custom original prompt"
				f.RequestBodies = map[string]json.RawMessage{"openai-response": json.RawMessage(`{"input":[{"role":"user","content":"custom original prompt"}],"max_output_tokens":64}`)}
			}
			h := mock.New(mock.Config{APIKey: mock.DefaultAPIKey})
			var seen atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/responses" {
					raw, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var body map[string]any
					_ = json.Unmarshal(raw, &body)
					input, _ := body["input"].([]any)
					replay := false
					for _, v := range input {
						item, _ := v.(map[string]any)
						if item["type"] == "function_call_output" {
							replay = true
						}
					}
					if replay {
						seen.Store(true)
						first, _ := input[0].(map[string]any)
						if first["role"] != "user" || first["content"] != expected || body["tool_choice"] != "none" {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Original prompt missing from replay","code":"invalid_input"}}`))
							return
						}
					}
				}
				h.ServeHTTP(w, r)
			}))
			defer server.Close()
			results, e := conformance.Run(context.Background(), conformance.Config{BaseURL: server.URL, APIKey: mock.DefaultAPIKey, Fixture: f})
			if e != nil {
				t.Fatal(e)
			}
			if !seen.Load() {
				t.Fatal("complete encrypted replay was not observed")
			}
			for _, result := range results {
				if result.Name == "responses/encrypted-tool-output-replay" && result.Status != conformance.Pass {
					t.Fatalf("replay failed: %s", result.Detail)
				}
			}
		})
	}
}

func TestRerankNativeDocumentUnion(t *testing.T) {
	h := mock.New(mock.Config{APIKey: mock.DefaultAPIKey})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			h.ServeHTTP(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request map[string]any
		_ = json.Unmarshal(raw, &request)
		capture := httptest.NewRecorder()
		h.ServeHTTP(capture, r)
		if capture.Code != 200 {
			for k, v := range capture.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		var body map[string]any
		_ = json.Unmarshal(capture.Body.Bytes(), &body)
		results, _ := body["results"].([]any)
		for _, v := range results {
			item, _ := v.(map[string]any)
			if request["return_documents"] == false {
				item["document"] = nil
			} else if doc, ok := item["document"].(map[string]any); ok {
				item["document"] = doc["text"]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	results, e := conformance.Run(context.Background(), conformance.Config{BaseURL: server.URL, APIKey: mock.DefaultAPIKey, Fixture: fixture(t)})
	if e != nil {
		t.Fatal(e)
	}
	for _, result := range results {
		if strings.HasPrefix(result.Name, "rerank/") && result.Status != conformance.Pass {
			t.Fatalf("native document union rejected: %s: %s", result.Name, result.Detail)
		}
	}
}
func TestMockPassesExternalHTTPConformance(t *testing.T) {
	server := httptest.NewServer(mock.New(mock.Config{APIKey: mock.DefaultAPIKey}))
	defer server.Close()
	results, e := conformance.Run(context.Background(), conformance.Config{BaseURL: server.URL, APIKey: mock.DefaultAPIKey, Fixture: fixture(t)})
	if e != nil {
		t.Fatal(e)
	}
	if len(results) < 100 {
		t.Fatalf("too few assertions: %d", len(results))
	}
	for _, r := range results {
		if r.Status != conformance.Pass {
			t.Errorf("%s %s: %s", r.Status, r.Name, r.Detail)
		}
	}
	t.Logf("%d public HTTP assertions passed, with no skipped bundled fixture cases", len(results))
}
func TestMalformedContractServerFailsConformance(t *testing.T) {
	h := mock.New(mock.Config{APIKey: mock.DefaultAPIKey})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nomifun/v1/account" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"contract_version":"1.0","balance":{"amount":1.5,"currency":"USD"}}`))
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	results, e := conformance.Run(context.Background(), conformance.Config{BaseURL: server.URL, APIKey: mock.DefaultAPIKey, Fixture: fixture(t)})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range results {
		if r.Name == "contract/account" && r.Status == conformance.Fail {
			found = true
		}
	}
	if !found {
		t.Fatal("malformed contract server incorrectly passed")
	}
}
func TestAdvertisedCapabilityRequiresFixtureModel(t *testing.T) {
	server := httptest.NewServer(mock.New(mock.Config{APIKey: mock.DefaultAPIKey}))
	defer server.Close()
	f := fixture(t)
	delete(f.Models, "gemini")
	results, e := conformance.Run(context.Background(), conformance.Config{BaseURL: server.URL, APIKey: mock.DefaultAPIKey, Fixture: f})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range results {
		if r.Name == "fixture/advertised/gemini" && r.Status == conformance.Fail {
			found = true
		}
	}
	if !found {
		t.Fatal("advertised capability silently skipped")
	}
}
func TestConfigurationAndTransportErrorsAreRedacted(t *testing.T) {
	secret := "synthetic_secret_must_not_appear"
	f := fixture(t)
	_, e := conformance.Run(context.Background(), conformance.Config{BaseURL: "http://invalid.invalid/?key=" + secret, APIKey: secret, Fixture: f})
	if e == nil || strings.Contains(e.Error(), secret) {
		t.Fatal("configuration error missing or leaked credential")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, e := conformance.Run(ctx, conformance.Config{BaseURL: "http://invalid.invalid/", APIKey: secret, Fixture: f})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range results {
		if strings.Contains(r.Detail, secret) || strings.Contains(r.Detail, "invalid.invalid") {
			t.Fatal("transport output disclosed sensitive input")
		}
	}
}
