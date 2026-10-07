// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixtureCall(f *fixture, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

func TestSyntheticDiscoveryListsAndFailureBoundaries(t *testing.T) {
	f := &fixture{key: "synthetic-only"}
	if got := fixtureCall(f, "GET", "/v1/models", "", ""); got.Code != 401 {
		t.Fatal("model directory accepted an unauthenticated request")
	}
	for _, test := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/v1/models", 200, `"synthetic-unconfirmed-v1"`},
		{"/paginated/v1/models", 200, `"has_more":true`},
		{"/paginated/v1/models?after_id=synthetic-chat-v1", 200, `"has_more":false`},
		{"/partial/v1/models?after_id=synthetic-chat-v1", 503, "later-page failure"},
		{"/failed/v1/models", 503, "list unavailable"},
	} {
		t.Run(test.path, func(t *testing.T) {
			got := fixtureCall(f, "GET", test.path, "", f.key)
			if got.Code != test.status || !strings.Contains(got.Body.String(), test.contains) {
				t.Fatalf("fixture response: %d %s", got.Code, got.Body.String())
			}
			if strings.Contains(got.Body.String(), f.key) || strings.Contains(got.Body.String(), "pricing") || strings.Contains(got.Body.String(), "tasks") {
				t.Fatal("fixture exposed credentials or invented model capabilities/prices")
			}
		})
	}
}

func TestSyntheticNativeUnknownAndUsageModes(t *testing.T) {
	f := &fixture{key: "synthetic-only"}
	native := fixtureCall(f, "POST", "/v1/chat/completions", `{"model":"synthetic-chat-v1","fixture_request_unknown":{"retained":true}}`, f.key)
	var response map[string]json.RawMessage
	if native.Code != 200 || json.Unmarshal(native.Body.Bytes(), &response) != nil || len(response["fixture_response_unknown"]) == 0 || !strings.Contains(string(response["usage"]), `"cached_tokens":4`) || !strings.Contains(string(response["usage"]), `"reasoning_tokens":2`) {
		t.Fatal("synthetic native/cache/reasoning fixture is incomplete")
	}
	stream := fixtureCall(f, "POST", "/v1/chat/completions", `{"model":"synthetic-chat-v1","stream":true,"fixture_request_unknown":true}`, f.key)
	if stream.Code != 200 || stream.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(stream.Body.String(), "fixture_event_unknown") || !strings.HasSuffix(stream.Body.String(), "data: [DONE]\n\n") {
		t.Fatal("synthetic stream framing or unknown event is incomplete")
	}
	missing := fixtureCall(f, "POST", "/v1/chat/completions", `{"model":"synthetic-chat-v1","fixture_usage_mode":"missing"}`, f.key)
	if strings.Contains(missing.Body.String(), `"usage"`) {
		t.Fatal("missing-usage mode invented usage")
	}
	partial := fixtureCall(f, "POST", "/v1/chat/completions", `{"model":"synthetic-chat-v1","fixture_usage_mode":"partial"}`, f.key)
	if strings.Contains(partial.Body.String(), "completion_tokens") || !strings.Contains(partial.Body.String(), `"prompt_tokens":12`) {
		t.Fatal("partial-usage mode invented output usage")
	}
	nativeError := fixtureCall(f, "POST", "/v1/chat/completions", `{"model":"synthetic-chat-v1","fixture_error_mode":"rate-limited"}`, f.key)
	if nativeError.Code != http.StatusTooManyRequests || !strings.Contains(nativeError.Body.String(), "fixture_error_unknown") {
		t.Fatal("native error fixture is incomplete")
	}
	evidence := fixtureCall(f, "GET", "/fixture/evidence", "", f.key)
	if !strings.Contains(evidence.Body.String(), `"unknown_request_fields_received":2`) || !strings.Contains(evidence.Body.String(), `"stream_requests":1`) {
		t.Fatal("synthetic upstream evidence counts are incorrect")
	}
}
