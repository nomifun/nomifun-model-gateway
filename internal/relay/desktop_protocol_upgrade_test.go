// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

// Desktop sends the selected model's full native output limit. A provider's
// rejection must remain visible; retrying with a guessed lower limit changes
// the request and conceals inaccurate operator catalog metadata.
func TestDesktopNativeOutputCeilingIsNeverClampedOrRetried(t *testing.T) {
	for _, fixture := range []struct {
		kind, path, field, rejection string
		endpoint                     v1.Endpoint
	}{
		{"openai", "/v1/responses", "max_output_tokens", `{ "error": { "type":"invalid_request_error", "param":"max_output_tokens", "message":"131072 exceeds this synthetic provider's 128000 limit", "future": {"limit":128000} } }`, v1.Responses},
		{"anthropic", "/v1/messages", "max_tokens", `{ "type":"error", "error": { "type":"invalid_request_error", "message":"131072 exceeds this synthetic provider's 128000 limit", "future": {"limit":128000} }, "request_id":"native-limit-rejection" }`, v1.Anthropic},
	} {
		t.Run(string(fixture.endpoint), func(t *testing.T) {
			body := `{"model":"public","` + fixture.field + `":131072,"input":"synthetic"}`
			if fixture.endpoint == v1.Anthropic {
				body = `{"model":"public","max_tokens":131072,"messages":[{"role":"user","content":"synthetic"}]}`
			}
			var calls, retries atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, err := io.ReadAll(r.Body)
				if err != nil || string(raw) != strings.Replace(body, `"model":"public"`, `"model":"private"`, 1) {
					t.Error("native output limit or request bytes changed before provider rejection")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, fixture.rejection)
			}))
			defer upstream.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				retries.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer fallback.Close()
			s, db := setup(t, upstream, fallback)
			if err := db.Model(&core.Channel{}).Where("id IN ?", []int{1, 2}).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"}).Error; err != nil {
				t.Fatal(err)
			}
			quota := int64(1000000)
			if err := db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]", QuotaLimit: &quota}).Error; err != nil {
				t.Fatal(err)
			}
			s.billing = billing.New(db)
			m := model(fixture.endpoint, v1.Chat)
			maximum := int64(131072)
			m.MaxOutputTokens = &maximum
			dispatch := func(id string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: m, RequestID: id})
				return w
			}
			w := dispatch("desktop-native-limit")
			if w.Code != http.StatusBadRequest || w.Body.String() != fixture.rejection || calls.Load() != 1 || retries.Load() != 0 {
				t.Fatalf("provider ceiling rejection was hidden or retried: HTTP %d, calls=%d, retries=%d, body=%s", w.Code, calls.Load(), retries.Load(), w.Body.String())
			}
			var hold core.Reservation
			var user core.User
			var key core.APIKey
			if err := db.First(&hold, "request_id = ?", "desktop-native-limit").Error; err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{&user, &key} {
				if err := db.First(value, 1).Error; err != nil {
					t.Fatal(err)
				}
			}
			var ledger int64
			if err := db.Model(&core.LedgerEntry{}).Count(&ledger).Error; err != nil {
				t.Fatal(err)
			}
			if hold.State != "released" || hold.ReservedTokens != int64(len(body))+131072 || hold.ActualTokens != 0 || hold.ActualAmount != 0 || user.Balance != 100 || user.ReservedBalance != 0 || key.QuotaReserved != 0 || key.QuotaUsed != 0 || ledger != 0 {
				t.Fatalf("native rejection charged, retained a hold or reserved a reduced ceiling: hold=%+v user=%+v key=%+v ledger=%d", hold, user, key, ledger)
			}

			maximum = 128000
			local := dispatch("desktop-local-limit")
			var localHolds int64
			if err := db.Model(&core.Reservation{}).Where("request_id = ?", "desktop-local-limit").Count(&localHolds).Error; err != nil {
				t.Fatal(err)
			}
			if local.Code != http.StatusBadRequest || !strings.Contains(local.Body.String(), "configured maximum") || calls.Load() != 1 || retries.Load() != 0 || localHolds != 0 {
				t.Fatalf("configured ceiling did not reject before billing and upstream dispatch: HTTP %d, calls=%d, retries=%d, holds=%d", local.Code, calls.Load(), retries.Load(), localHolds)
			}
		})
	}
}

// These are synthetic native payloads, not schema conversion fixtures. Desktop
// owns provider-specific schema preparation; the gateway preserves what it sent,
// including an Anthropic object root and an unrestricted Responses oneOf root.
func TestDesktopNativeToolsThinkingAndCacheRemainProviderSpecific(t *testing.T) {
	for _, fixture := range []struct {
		kind, path, body, response string
		endpoint                   v1.Endpoint
	}{
		{
			"openai", "/v1/responses",
			`{ "model":"public", "max_output_tokens":131072, "input":"synthetic", "reasoning" : { "effort":"high", "summary":"auto" }, "include":["reasoning.encrypted_content"], "prompt_cache_key":"synthetic-cache", "tools" : [ { "type":"function", "name":"synthetic_lookup", "strict":false, "parameters" : { "oneOf":[{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]},{"type":"object","properties":{"filters":{"type":"array","items":{"anyOf":[{"$ref":"#/$defs/filter"},{"type":"null"}]}}}}], "$defs":{"filter":{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"number"}]}},"additionalProperties":{"type":"string"}}}, "additionalProperties":false } } ] }`,
			`{ "id":"resp_desktop_upgrade", "status":"completed", "output":[{"type":"reasoning","encrypted_content":"signed-reasoning+/=","summary":[]},{"type":"function_call","call_id":"synthetic_call","name":"synthetic_lookup","arguments":"{\"query\":\"synthetic\"}"}], "usage":{"input_tokens":7,"output_tokens":9,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":4},"future_cache":{"value":1}} }`,
			v1.Responses,
		},
		{
			"anthropic", "/v1/messages",
			`{ "model":"public", "max_tokens":131072, "messages":[{"role":"user","content":"synthetic"}], "system":[{"type":"text","text":"synthetic","cache_control":{"type":"ephemeral","ttl":"1h"}}], "thinking" : { "type":"adaptive" }, "output_config":{"effort":"high"}, "tools" : [ { "name":"synthetic_lookup", "input_schema" : { "type":"object", "properties":{"query":{"anyOf":[{"type":"string"},{"type":"null"}]},"filters":{"type":"array","items":{"$ref":"#/$defs/filter"}}}, "$defs":{"filter":{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"number"}]}},"additionalProperties":{"type":"string"}}}, "additionalProperties":false }, "cache_control":{"type":"ephemeral"} } ] }`,
			`{ "id":"msg_desktop_upgrade", "type":"message", "role":"assistant", "content":[{"type":"thinking","thinking":"synthetic","signature":"signed-thinking+/="},{"type":"tool_use","id":"synthetic_call","name":"synthetic_lookup","input":{"query":"synthetic"}}], "stop_reason":"tool_use", "usage":{"input_tokens":7,"output_tokens":9,"cache_creation_input_tokens":5,"cache_read_input_tokens":3,"cache_creation":{"ephemeral_1h_input_tokens":5},"future_cache":{"value":1}} }`,
			v1.Anthropic,
		},
	} {
		t.Run(string(fixture.endpoint), func(t *testing.T) {
			betaValues := []string{"synthetic-thinking-2026-10-01,synthetic-cache-2026-10-02", "synthetic-future-2026-10-03"}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil || string(raw) != strings.Replace(fixture.body, `"model":"public"`, `"model":"private"`, 1) {
					t.Error("native schema, reasoning, cache fields or whitespace changed")
				}
				if r.URL.Path != fixture.path {
					t.Errorf("native route changed: %s", r.URL.Path)
				}
				if fixture.endpoint == v1.Anthropic && (r.Header.Get("anthropic-version") != "2023-06-01" || !slices.Equal(r.Header.Values("anthropic-beta"), betaValues)) {
					t.Error("native Anthropic version or beta list changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, fixture.response)
			}))
			defer upstream.Close()
			s, db := setup(t, upstream)
			s.Config.DefaultAnthropicVersion = "synthetic-operator-default"
			if err := db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(fixture.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("anthropic-version", "2023-06-01")
			for _, beta := range betaValues {
				r.Header.Add("anthropic-beta", beta)
			}
			m := model(fixture.endpoint, v1.Chat)
			maximum := int64(131072)
			m.MaxOutputTokens = &maximum
			w := httptest.NewRecorder()
			s.Serve(w, r, Request{Key: core.APIKey{ID: 1}, Model: m})
			if w.Code != http.StatusOK || w.Body.String() != fixture.response {
				t.Fatalf("native response tools, thinking or cache bytes changed: HTTP %d, %s", w.Code, w.Body.String())
			}
		})
	}
}
