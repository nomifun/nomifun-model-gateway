// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

// Success/failure of a native model call and completeness of its usage are
// separate facts. Desktop decides whether a new client attempt is safe; the
// gateway never splices a retry into an already-delivered error stream.
func TestDesktopNativeStreamErrorsPreserveRetryEvidenceAndAccounting(t *testing.T) {
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_upgrade\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"future_usage\":true}}}\n\n"
	responseError := "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"private native diagnostic\",\"param\":null,\"sequence_number\":1}\n\n"
	for _, fixture := range []struct {
		name, kind, path, wire string
		endpoint               v1.Endpoint
		settled, failed        bool
	}{
		{"chat-upstream-error", "openai", "/v1/chat/completions", "data: {\"error\":{\"type\":\"upstream_error\",\"code\":null,\"message\":\"private native diagnostic\"}}\n\ndata: [DONE]\n\n", v1.OpenAI, false, true},
		{"chat-specific-error-code", "openai", "/v1/chat/completions", "data: {\"error\":{\"type\":\"upstream_error\",\"code\":\"context_length_exceeded\",\"message\":\"private native diagnostic\"}}\n\ndata: [DONE]\n\n", v1.OpenAI, false, true},
		{"responses-top-level-error", "openai", "/v1/responses", responseError, v1.Responses, false, true},
		{"responses-error-before-terminal", "openai", "/v1/responses", responseError + completed, v1.Responses, true, true},
		{"responses-failed-usage", "openai", "/v1/responses", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_upgrade\",\"error\":{\"code\":\"server_error\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n", v1.Responses, true, true},
		{"responses-failed-no-usage", "openai", "/v1/responses", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_upgrade\",\"error\":{\"code\":\"server_error\"},\"usage\":null}}\n\n", v1.Responses, false, true},
		{"responses-incomplete-usage", "openai", "/v1/responses", "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_upgrade\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n", v1.Responses, true, false},
		{"responses-completed", "openai", "/v1/responses", completed, v1.Responses, true, false},
		{"anthropic-error-no-final-usage", "anthropic", "/v1/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"private native diagnostic\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", v1.Anthropic, false, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var primary, fallback atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				primary.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, fixture.wire)
			}))
			defer upstream.Close()
			alternative := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fallback.Add(1); w.WriteHeader(500) }))
			defer alternative.Close()
			s, db := setup(t, upstream, alternative)
			if err := db.Model(&core.Channel{}).Where("id IN ?", []int64{1, 2}).Updates(map[string]any{"kind": fixture.kind, "endpoints_json": `["` + string(fixture.endpoint) + `"]`}).Error; err != nil {
				t.Fatal(err)
			}
			quota := int64(1000000)
			for _, record := range []any{&core.User{ID: 1, Balance: 100, Currency: "USD"}, &core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]", QuotaLimit: &quota}} {
				if err := db.Create(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			s.billing = billing.New(db)
			r := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(`{"model":"public","stream":true}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			serveBound(s, w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model(fixture.endpoint, v1.Chat), RequestID: "native-upgrade-error"})
			if w.Code != 200 || w.Body.String() != fixture.wire || primary.Load() != 1 || fallback.Load() != 0 {
				t.Fatalf("native error was rewritten or retried: status=%d primary=%d fallback=%d body=%q", w.Code, primary.Load(), fallback.Load(), w.Body.String())
			}
			var hold core.Reservation
			var account core.User
			var apiKey core.APIKey
			var channel core.Channel
			if err := db.First(&hold, "request_id = ?", "native-upgrade-error").Error; err != nil {
				t.Fatal(err)
			}
			for _, record := range []any{&account, &apiKey, &channel} {
				if err := db.First(record, 1).Error; err != nil {
					t.Fatal(err)
				}
			}
			var usage core.Usage
			if err := json.Unmarshal([]byte(hold.UsageJSON), &usage); err != nil {
				t.Fatal(err)
			}
			var entries int64
			if err := db.Model(&core.LedgerEntry{}).Where("kind = ?", "usage").Count(&entries).Error; err != nil {
				t.Fatal(err)
			}
			if (channel.Failures > 0) != fixture.failed || (channel.CooldownUntil != nil) != fixture.failed {
				t.Fatalf("native model failure recorded as successful channel: %+v", channel)
			}
			if strings.Contains(hold.UsageJSON, "private native diagnostic") {
				t.Fatal("native error message entered durable usage")
			}
			if fixture.settled {
				if hold.State != "settled" || !usage.Complete || account.Balance != 99 || apiKey.QuotaUsed != 5 || entries != 1 {
					t.Fatalf("confirmed native terminal usage changed billing policy: hold=%+v account=%+v key=%+v entries=%d", hold, account, apiKey, entries)
				}
			} else if hold.State != "reconciliation" || usage.Complete || account.Balance != 100 || apiKey.QuotaUsed != 0 || entries != 0 || account.ReservedBalance != hold.ReservedAmount || !hold.UpstreamAccepted {
				t.Fatalf("native error without final usage charged or lost accepted hold: hold=%+v account=%+v key=%+v entries=%d", hold, account, apiKey, entries)
			}
		})
	}
}
