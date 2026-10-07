// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func TestNativeHTTPRejectionsPreserveEveryProtocolEnvelope(t *testing.T) {
	for _, fixture := range []struct {
		kind, path, response string
		endpoint             v1.Endpoint
	}{
		{"openai", "/v1/chat/completions", `{ "error": { "message":"synthetic rejection", "type":"invalid_request_error", "future": { "signature":"opaque+/=", "cache_hint":[1, 2] } }, "vendor_field":true }`, v1.OpenAI},
		{"openai", "/v1/responses", `{ "error": { "message":"synthetic rejection", "type":"invalid_request_error", "future": { "encrypted_content":"opaque+/=" } }, "vendor_field":true }`, v1.Responses},
		{"anthropic", "/v1/messages", `{ "type":"error", "error": { "type":"invalid_request_error", "message":"synthetic rejection", "future": { "thinking_signature":"opaque+/=" } }, "request_id":"native-request" }`, v1.Anthropic},
		{"gemini", "/v1beta/models/public:generateContent", `{ "error": { "code":400, "message":"synthetic rejection", "status":"INVALID_ARGUMENT", "details":[{ "future":{"thoughtSignature":"opaque+/="} }] }, "vendor_field":true }`, v1.Gemini},
	} {
		t.Run(string(fixture.endpoint), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Contains(body, []byte(`"future" : { "opaque" : "signed+/=", "cache_control" : [ 1, 2 ] }`)) {
					t.Error("native unknown request fields changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, fixture.response)
			}))
			defer upstream.Close()
			s, db := setup(t, upstream)
			if err := db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
				t.Fatal(err)
			}
			w := execute(s, fixture.path, `{"model":"public","future" : { "opaque" : "signed+/=", "cache_control" : [ 1, 2 ] }}`, 1, fixture.endpoint)
			if w.Code != http.StatusBadRequest || w.Body.String() != fixture.response {
				t.Fatalf("native rejection was rewritten: HTTP %d, %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestFragmentedUnknownNativeSSEPreservesBytesAndStoresOnlyUsage(t *testing.T) {
	unknown := ": native keepalive\r\nretry: 1700\r\nevent: vendor.future\r\ndata: {\"opaque\":\"签名+/=\",\r\ndata: \"cache_hint\":{\"future\":true}}\r\n\r\n"
	for _, fixture := range []struct {
		kind, path, stream string
		endpoint           v1.Endpoint
		tokens             int64
	}{
		{"openai", "/v1/responses", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fragmented\"}}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"signed+/=\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fragmented\",\"usage\":{\"input_tokens\":7,\"output_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":3},\"future_usage\":{\"tier\":1}}}}\n\n", v1.Responses, 16},
		{"anthropic", "/v1/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"cache_creation_input_tokens\":5,\"cache_read_input_tokens\":3,\"output_tokens\":1,\"future_usage\":{\"tier\":1}}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signed+/=\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", v1.Anthropic, 24},
		{"gemini", "/v1beta/models/public:streamGenerateContent", "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\"synthetic\",\"thoughtSignature\":\"signed+/=\"}]}}],\"usageMetadata\":{\"promptTokenCount\":7,\"candidatesTokenCount\":9,\"cachedContentTokenCount\":3,\"thoughtsTokenCount\":2,\"totalTokenCount\":18,\"future_usage\":{\"tier\":1}}}\n\n", v1.Gemini, 18},
	} {
		t.Run(fixture.kind, func(t *testing.T) {
			raw := unknown + fixture.stream
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				// Split delimiters, JSON strings and UTF-8 code points across writes.
				for start := 0; start < len(raw); start += 7 {
					end := min(start+7, len(raw))
					_, _ = io.WriteString(w, raw[start:end])
					w.(http.Flusher).Flush()
				}
			}))
			defer upstream.Close()
			s, db := setup(t, upstream)
			if err := db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"}).Error; err != nil {
				t.Fatal(err)
			}
			s.billing = billing.New(db)
			r := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(`{"model":"public","stream":true}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			serveBound(s, w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model(fixture.endpoint, v1.Chat), RequestID: "fragmented-usage"})
			if w.Code != http.StatusOK || w.Body.String() != raw {
				t.Fatalf("fragmented native stream changed: HTTP %d, %s", w.Code, w.Body.String())
			}
			var hold core.Reservation
			if err := db.First(&hold, "request_id = ?", "fragmented-usage").Error; err != nil {
				t.Fatal(err)
			}
			if hold.State != "settled" || hold.ActualTokens != fixture.tokens || hold.ActualAmount != 1 || !strings.Contains(hold.UsageJSON, "future_usage") {
				t.Fatalf("native terminal usage did not settle: %+v", hold)
			}
			for _, content := range []string{"signed+/=", "签名", "encrypted_content", "thoughtSignature", "signature_delta", "cache_hint"} {
				if strings.Contains(hold.UsageJSON, content) {
					t.Fatalf("native content entered billing evidence: %s", content)
				}
			}
		})
	}
}
