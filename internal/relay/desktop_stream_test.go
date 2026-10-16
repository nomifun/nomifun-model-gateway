// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopSSECommitBoundaryAndNativeUnionCompatibility(t *testing.T) {
	chat := "data: {\"choices\":[],\"prompt_filter_results\":[],\"usage\":null}\n\ndata: {\"id\":\"chat_native\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n"
	responses := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"response_native\"}}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"first+/=\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response_native\",\"output\":[{\"type\":\"reasoning\",\"encrypted_content\":\"reencrypted+/=\"}],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}"
	anthropic := "event: message_start\ndata: {\"type\":\"message_start\",\"error\":null,\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: future\ndata: {\"type\":\"future\",\"message\":null,\"delta\":null,\"error\":null}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"message\":null,\"error\":null,\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\",\"error\":null}\n\n"
	for _, fixture := range []struct {
		name, endpoint, wire   string
		complete, framingError bool
	}{
		{"idless-prelude-done-eof", "openai", chat + "data: [DONE]", true, false},
		{"named-done-eof", "openai", chat + "event: done\ndata: [DONE]", true, false},
		{"message-done-eof-mixed", "openai", chat + ": keepalive\revent: message\r\ndata:[DONE]\r", true, false},
		{"complete-chat", "openai", chat + "data: [DONE]\n\n", true, false},
		{"complete-chat-truncated-next-record", "openai", chat + "data: [DONE]\n\ndata: {\"partial\":", false, true},
		{"truncated-done", "openai", chat + "data: [DON", false, true},
		{"unterminated-responses-usage", "openai-response", responses, false, true},
		{"reencrypted-responses", "openai-response", responses + "\n\n", true, false},
		{"complete-responses-truncated-next-record", "openai-response", responses + "\n\ndata: {\"partial\":", false, true},
		{"anthropic-null-unions", "anthropic", anthropic, true, false},
		{"complete-anthropic-truncated-next-record", "anthropic", anthropic + "data: {\"partial\":", false, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, _ := setup(t)
			p := newUsage(fixture.endpoint, "")
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fixture.wire))}
			w := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, err := s.stream(w, response, ctx, cancel, p, 1, 1, "", "synthetic-stream-key")
			usage := p.finish(err == nil)
			if !started || (err != nil) != fixture.framingError || usage.Complete != fixture.complete || p.failed {
				t.Fatalf("wrong stream evidence: started=%v err=%v usage=%+v failed=%v", started, err, usage, p.failed)
			}
			if w.Body.String() != fixture.wire {
				t.Fatal("native stream bytes were changed")
			}
		})
	}
}
