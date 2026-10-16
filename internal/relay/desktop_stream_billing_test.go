// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

type desktopFailedFlushWriter struct {
	*httptest.ResponseRecorder
	flushes, failAt int
}

func (w *desktopFailedFlushWriter) FlushError() error {
	w.flushes++
	if w.flushes == w.failAt {
		return errors.New("synthetic terminal flush failure")
	}
	w.ResponseRecorder.Flush()
	return nil
}

// Even a previously committed native terminal snapshot remains partial evidence
// when framing or client delivery subsequently fails. It must not debit a wallet
// or consume quota, and the accepted reservation must remain reconcilable.
func TestDesktopIncompleteSSEDeliveryCannotSettleCommittedTerminalUsage(t *testing.T) {
	for _, fixture := range []struct {
		name, kind, path, wire string
		endpoint               v1.Endpoint
	}{
		{"chat", "openai", "/v1/chat/completions", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"future_usage\":{\"tier\":1}}}\n\ndata: [DONE]\n\n", v1.OpenAI},
		{"responses", "openai", "/v1/responses", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response_partial_delivery\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"future_usage\":{\"tier\":1}}}}\n\n", v1.Responses},
		{"anthropic", "anthropic", "/v1/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0,\"future_usage\":{\"tier\":1}}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", v1.Anthropic},
		{"gemini", "gemini", "/v1beta/models/public:streamGenerateContent", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5,\"future_usage\":{\"tier\":1}}}\n\n", v1.Gemini},
	} {
		for _, failure := range []string{"truncated-next-record", "terminal-flush-failure", "contradictory-done-event", "missing-native-terminal-with-done"} {
			if fixture.endpoint == v1.OpenAI && failure == "missing-native-terminal-with-done" {
				continue
			}
			t.Run(fixture.name+"/"+failure, func(t *testing.T) {
				wire := fixture.wire
				wantInput, wantOutput := int64(3), int64(2)
				switch failure {
				case "truncated-next-record":
					wire += "event: vendor.future\ndata: {\"private_content\":\"truncated"
				case "contradictory-done-event":
					wire += "event: error\r\ndata: [DONE]\r\n\r\n"
				case "missing-native-terminal-with-done":
					switch fixture.endpoint {
					case v1.Responses:
						wire = strings.ReplaceAll(wire, "response.completed", "response.in_progress")
						wantInput, wantOutput = 0, 0 // Responses usage is authoritative only in a native terminal event.
					case v1.Anthropic:
						wire = strings.ReplaceAll(wire, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "")
					case v1.Gemini:
						wire = strings.ReplaceAll(wire, `"finishReason":"STOP"`, `"index":0`)
					}
					wire += "event: done\ndata: [DONE]\n\n"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				}))
				defer upstream.Close()
				s, db := setup(t, upstream)
				if err := db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
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
				r := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(`{"model":"public","stream":true}`))
				r.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				var writer http.ResponseWriter = recorder
				if failure == "terminal-flush-failure" {
					writer = &desktopFailedFlushWriter{ResponseRecorder: recorder, failAt: strings.Count(fixture.wire, "\n\n")}
				}
				serveBound(s, writer, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model(fixture.endpoint, v1.Chat), RequestID: "partial-terminal-delivery"})
				if recorder.Code != http.StatusOK || recorder.Body.String() != wire {
					t.Fatalf("accepted native stream bytes changed: HTTP %d %q", recorder.Code, recorder.Body.String())
				}
				var hold core.Reservation
				var user core.User
				var key core.APIKey
				if err := db.Where("request_id = ?", "partial-terminal-delivery").First(&hold).Error; err != nil {
					t.Fatal(err)
				}
				for _, value := range []any{&user, &key} {
					if err := db.First(value, 1).Error; err != nil {
						t.Fatal(err)
					}
				}
				var usage core.Usage
				if err := json.Unmarshal([]byte(hold.UsageJSON), &usage); err != nil {
					t.Fatal(err)
				}
				var ledger int64
				if err := db.Model(&core.LedgerEntry{}).Count(&ledger).Error; err != nil {
					t.Fatal(err)
				}
				if hold.State != "reconciliation" || !hold.UpstreamAccepted || usage.Complete || hold.ActualAmount != 0 || hold.ActualTokens != wantInput+wantOutput || user.Balance != 100 || ledger != 0 || key.QuotaUsed != 0 || user.ReservedBalance != hold.ReservedAmount || key.QuotaReserved != hold.ReservedTokens {
					t.Fatalf("incomplete delivery settled or discarded accepted hold: hold=%+v user=%+v key=%+v ledger=%d", hold, user, key, ledger)
				}
				if usage.InputTokens != wantInput || usage.OutputTokens != wantOutput || usage.TotalTokens != wantInput+wantOutput || (wantInput > 0 && !strings.Contains(usage.RawJSON, "future_usage")) || strings.Contains(hold.UsageJSON, "private_content") {
					t.Fatalf("partial usage evidence lost or native content retained: %+v", usage)
				}
			})
		}
	}
}
