// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func TestStrictNativeUsageCounterRequirements(t *testing.T) {
	for _, f := range []struct {
		name, endpoint, body string
		complete             bool
		input, output        int64
	}{
		{"chat-missing-output", "openai", `{"usage":{"prompt_tokens":5}}`, false, 5, 0},
		{"chat-total-only", "openai", `{"usage":{"total_tokens":5}}`, false, 0, 0},
		{"chat-null-input", "openai", `{"usage":{"prompt_tokens":null,"completion_tokens":0}}`, false, 0, 0},
		{"chat-nested-null", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":null}}}`, false, 0, 0},
		{"chat-nested-fraction", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":0.5}}}`, false, 0, 0},
		{"chat-duplicate-output", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens":4}}`, false, 0, 0},
		{"chat-duplicate-nested-cache", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":1,"cached_tokens":4}}}`, false, 0, 0},
		{"chat-cache-subset", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":6}}}`, false, 0, 0},
		{"chat-total-mismatch", "openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":9}}`, false, 0, 0},
		{"chat-sum-overflow", "openai", `{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1}}`, false, 0, 0},
		{"chat-valid-zero", "openai", `{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"completion_tokens_details":{"reasoning_tokens":0}}}`, true, 0, 0},
		{"responses-missing-output", "openai-response", `{"usage":{"input_tokens":5}}`, false, 5, 0},
		{"responses-nested-fraction", "openai-response", `{"usage":{"input_tokens":5,"output_tokens":3,"input_tokens_details":{"cached_tokens":1.5}}}`, false, 0, 0},
		{"responses-reasoning-subset", "openai-response", `{"usage":{"input_tokens":5,"output_tokens":3,"output_tokens_details":{"reasoning_tokens":4}}}`, false, 0, 0},
		{"responses-valid-zero", "openai-response", `{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`, true, 0, 0},
		{"anthropic-missing-output", "anthropic", `{"usage":{"input_tokens":5}}`, false, 5, 0},
		{"anthropic-count-response-on-messages", "anthropic", `{"input_tokens":5}`, false, 0, 0},
		{"anthropic-cache-overflow", "anthropic", `{"usage":{"input_tokens":9223372036854775807,"output_tokens":0,"cache_read_input_tokens":1}}`, false, 0, 0},
		{"anthropic-cache-breakdown-null", "anthropic", `{"usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":null}}}`, false, 0, 0},
		{"anthropic-valid-zero", "anthropic", `{"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`, true, 0, 0},
		{"gemini-nonzero-total-only", "gemini", `{"usageMetadata":{"totalTokenCount":5}}`, false, 0, 0},
		{"gemini-zero-total-is-partial-without-base-counts", "gemini", `{"usageMetadata":{"totalTokenCount":0}}`, false, 0, 0},
		{"gemini-missing-output-without-total", "gemini", `{"usageMetadata":{"promptTokenCount":5}}`, false, 5, 0},
		{"gemini-omitted-output-is-partial", "gemini", `{"usageMetadata":{"promptTokenCount":5,"totalTokenCount":5}}`, false, 5, 0},
		{"gemini-inconsistent-total", "gemini", `{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":9}}`, false, 0, 0},
		{"gemini-nested-fraction", "gemini", `{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8,"promptTokensDetails":[{"modality":"TEXT","tokenCount":1.5}]}}`, false, 0, 0},
		{"gemini-duplicate-nested-counter", "gemini", `{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8,"promptTokensDetails":[{"modality":"TEXT","tokenCount":1,"tokenCount":4}]}}`, false, 0, 0},
		{"gemini-thinking-overflow", "gemini", `{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1}}`, false, 0, 0},
		{"gemini-included-toolprompt", "gemini", `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"thoughtsTokenCount":2,"toolUsePromptTokenCount":3,"cachedContentTokenCount":4,"totalTokenCount":27}}`, true, 20, 7},
		{"gemini-separate-toolprompt", "gemini", `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"thoughtsTokenCount":2,"toolUsePromptTokenCount":3,"cachedContentTokenCount":4,"totalTokenCount":30}}`, true, 23, 7},
		{"gemini-ambiguous-toolprompt", "gemini", `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"thoughtsTokenCount":2,"toolUsePromptTokenCount":3}}`, false, 20, 7},
		{"gemini-fractional-toolprompt", "gemini", `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"toolUsePromptTokenCount":0.5,"totalTokenCount":25}}`, false, 0, 0},
		{"embeddings-input-total", "embeddings", `{"usage":{"prompt_tokens":5,"total_tokens":5}}`, true, 5, 0},
		{"embeddings-total-only", "embeddings", `{"usage":{"total_tokens":5}}`, true, 5, 0},
		{"embeddings-mismatched-total", "embeddings", `{"usage":{"prompt_tokens":5,"total_tokens":6}}`, false, 0, 0},
		{"rerank-total-only", "jina-rerank", `{"usage":{"total_tokens":5}}`, true, 5, 0},
		{"rerank-null-total", "jina-rerank", `{"usage":{"total_tokens":null}}`, false, 0, 0},
	} {
		t.Run(f.name, func(t *testing.T) {
			p := newUsage(f.endpoint)
			p.json([]byte(f.body))
			u := p.finish(true)
			if u.Complete != f.complete || u.InputTokens != f.input || u.OutputTokens != f.output {
				t.Fatalf("unexpected usage %+v invalid=%v ready=%v", u, p.invalid, p.ready)
			}
			if f.complete && u.TotalTokens != f.input+f.output {
				t.Fatal("canonical total not conserved")
			}
		})
	}
}

func TestAnthropicStreamRequiresFinalCumulativeOutput(t *testing.T) {
	p := newUsage("anthropic")
	p.event([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"))
	p.event([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"private generated text\"}}\n\n"))
	p.event([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	u := p.finish(true)
	if u.Complete || u.InputTokens != 5 || strings.Contains(u.RawJSON, "private generated text") {
		t.Fatal("message_start provisional output became final usage or retained content")
	}
	count := newUsage("anthropic", "/v1/messages/count_tokens")
	count.json([]byte(`{"input_tokens":5,"future":{"content":"not usage"}}`))
	u = count.finish(true)
	if !u.Complete || u.InputTokens != 5 || u.RawJSON != `{"input_tokens":5}` {
		t.Fatal("count_tokens endpoint confused with generation usage")
	}
}

func TestCumulativeNativeUsageRegressionRetainsLastValidPartial(t *testing.T) {
	anth := newUsage("anthropic")
	anth.event([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"))
	anth.event([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}\n\n"))
	anth.event([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"))
	anth.event([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	usage := anth.finish(true)
	if usage.Complete || !anth.invalid || usage.InputTokens != 5 || usage.OutputTokens != 9 || !strings.Contains(usage.RawJSON, `"output_tokens":9`) {
		t.Fatalf("Anthropic cumulative usage overwritten by regression %+v", usage)
	}
	for _, f := range []struct{ name, second string }{{"output", `{"promptTokenCount":5,"candidatesTokenCount":4,"thoughtsTokenCount":2,"cachedContentTokenCount":1,"totalTokenCount":11}`}, {"input", `{"promptTokenCount":4,"candidatesTokenCount":9,"thoughtsTokenCount":2,"cachedContentTokenCount":1,"totalTokenCount":15}`}, {"reasoning", `{"promptTokenCount":5,"candidatesTokenCount":10,"thoughtsTokenCount":1,"cachedContentTokenCount":1,"totalTokenCount":16}`}, {"cache", `{"promptTokenCount":5,"candidatesTokenCount":9,"thoughtsTokenCount":2,"cachedContentTokenCount":0,"totalTokenCount":16}`}} {
		t.Run(f.name, func(t *testing.T) {
			gem := newUsage("gemini")
			gem.event([]byte("data: {\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":9,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":1,\"totalTokenCount\":16}}\n\n"))
			gem.event([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":" + f.second + "}\n\n"))
			usage := gem.finish(true)
			if usage.Complete || !gem.invalid || usage.InputTokens != 5 || usage.OutputTokens != 11 || usage.ReasoningTokens != 2 || usage.CacheReadTokens != 1 {
				t.Fatalf("Gemini cumulative snapshot erased observed counts %+v", usage)
			}
		})
	}
}

func TestMalformedTerminalUsageHoldsActualHTTPAccounting(t *testing.T) {
	cases := []struct {
		name, kind, path, request, response string
		endpoint                            v1.Endpoint
		stream, complete                    bool
	}{
		{"chat-missing-output", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[{"message":{"content":"private result"}}],"usage":{"prompt_tokens":5}}`, "openai", false, false},
		{"responses-missing-output", "openai", "/v1/responses", `{"model":"public","max_output_tokens":20}`, `{"id":"strict-response","output":[{"content":"private result"}],"usage":{"input_tokens":5}}`, "openai-response", false, false},
		{"gemini-total-only", "gemini", "/v1beta/models/public:generateContent", `{"contents":[],"generationConfig":{"maxOutputTokens":20}}`, `{"candidates":[{"content":{"parts":[{"text":"private result"}]}}],"usageMetadata":{"totalTokenCount":5}}`, "gemini", false, false},
		{"anthropic-json-missing-output", "anthropic", "/v1/messages", `{"model":"public","max_tokens":20}`, `{"type":"message","content":[{"text":"private result"}],"usage":{"input_tokens":5}}`, "anthropic", false, false},
		{"nested-fraction", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":0.5}}}`, "openai", false, false},
		{"null-top", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":null,"completion_tokens":0}}`, "openai", false, false},
		{"null-nested", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":null}}}`, "openai", false, false},
		{"counter-overflow", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1}}`, "openai", false, false},
		{"duplicate-known-counter", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":9,"completion_tokens":4}}`, "openai", false, false},
		{"total-mismatch", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":9}}`, "openai", false, false},
		{"anthropic-no-final-delta", "anthropic", "/v1/messages", `{"model":"public","max_tokens":20,"stream":true}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"private result\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "anthropic", true, false},
		{"anthropic-regressive-delta", "anthropic", "/v1/messages", `{"model":"public","max_tokens":20,"stream":true}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "anthropic", true, false},
		{"gemini-regressive-snapshot", "gemini", "/v1beta/models/public:streamGenerateContent", `{"contents":[],"generationConfig":{"maxOutputTokens":20}}`, "data: {\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":9,\"thoughtsTokenCount\":2,\"totalTokenCount\":16}}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":4,\"thoughtsTokenCount\":1,\"totalTokenCount\":10}}\n\n", "gemini", true, false},
		{"chat-stream-input-only", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20,"stream":true}`, "data: {\"usage\":{\"prompt_tokens\":5}}\n\ndata: [DONE]\n\n", "openai", true, false},
		{"valid-zero-chat", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20}`, `{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, "openai", false, true},
		{"valid-zero-anthropic", "anthropic", "/v1/messages", `{"model":"public","max_tokens":20}`, `{"type":"message","content":[],"usage":{"input_tokens":0,"output_tokens":0}}`, "anthropic", false, true},
		{"valid-zero-gemini", "gemini", "/v1beta/models/public:generateContent", `{"contents":[],"generationConfig":{"maxOutputTokens":20}}`, `{"candidates":[],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`, "gemini", false, true},
	}
	for _, f := range cases {
		t.Run(f.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if f.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.Write([]byte(f.response))
			}))
			defer upstream.Close()
			s, db := setup(t, upstream)
			db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": f.kind, "endpoints_json": `["` + string(f.endpoint) + `"]`})
			limit := int64(1000000)
			db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
			db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]", QuotaLimit: &limit})
			s.billing = billing.New(db)
			m := model(f.endpoint, "chat")
			m.Pricing = []v1.Price{{Task: "chat", Meter: "input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}, {Task: "chat", Meter: "output_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}}
			r := httptest.NewRequest("POST", f.path, strings.NewReader(f.request))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: m, RequestID: "strict-usage"})
			if w.Code != 200 || w.Body.String() != f.response {
				t.Fatalf("native result was normalized or changed %d %s", w.Code, w.Body.String())
			}
			var hold core.Reservation
			db.First(&hold, "request_id = ?", "strict-usage")
			var user core.User
			db.First(&user, 1)
			var key core.APIKey
			db.First(&key, 1)
			var usage core.Usage
			json.Unmarshal([]byte(hold.UsageJSON), &usage)
			if strings.Contains(hold.UsageJSON, "private result") {
				t.Fatal("response content retained with usage")
			}
			if f.complete {
				if hold.State != "settled" || !usage.Complete || hold.ActualTokens != 0 || user.Balance != 100 || user.ReservedBalance != 0 || key.QuotaReserved != 0 {
					t.Fatalf("valid zero usage did not settle zero-token exact bill %+v %+v", hold, user)
				}
			} else {
				var ledger int64
				db.Model(&core.LedgerEntry{}).Count(&ledger)
				if hold.State != "reconciliation" || usage.Complete || user.Balance != 100 || user.ReservedBalance != hold.ReservedAmount || key.QuotaUsed != 0 || key.QuotaReserved != hold.ReservedTokens || ledger != 0 {
					t.Fatalf("partial/malformed usage charged or dropped accepted hold %+v %+v %+v", hold, user, key)
				}
				if f.name == "anthropic-regressive-delta" && usage.OutputTokens != 9 {
					t.Fatal("regressed Anthropic evidence erased previous output9")
				}
				if f.name == "gemini-regressive-snapshot" && (usage.InputTokens != 5 || usage.OutputTokens != 11 || usage.ReasoningTokens != 2) {
					t.Fatal("regressed Gemini evidence erased previous valid usage")
				}
			}
		})
	}
}
