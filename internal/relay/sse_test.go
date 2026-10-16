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

func TestSSERecordBoundaryAcceptsMixedTerminatorsAndDefersTrailingCR(t *testing.T) {
	for _, delimiter := range []string{"\n\n", "\r\r", "\r\n\r\n", "\r\n\n", "\n\r\n", "\r\n\r", "\n\r"} {
		wire := "data: native" + delimiter
		if got := eventEnd([]byte(wire), true); got != len(wire) {
			t.Fatalf("EOF lost complete blank line %q: end=%d", delimiter, got)
		}
		want := len(wire)
		if strings.HasSuffix(wire, "\r") {
			want = -1 // The next transport byte might be the LF half of CRLF.
		}
		if got := eventEnd([]byte(wire), false); got != want {
			t.Fatalf("transport boundary misread %q: end=%d want=%d", delimiter, got, want)
		}
		if got := eventEnd([]byte(wire+"data: next"), false); got != len(wire) {
			t.Fatalf("following record merged with %q: end=%d", delimiter, got)
		}
	}
	for _, wire := range []string{"data: native", "data: native\r", "data: native\n", "data: native\r\n"} {
		if got := eventEnd([]byte(wire), true); got != -1 {
			t.Fatalf("one line ending became an event delimiter: %q end=%d", wire, got)
		}
	}
}

func TestSSEFieldsRespectEmptyDataAndLastEventAcrossMixedLineEndings(t *testing.T) {
	frame := ": keepalive\r\nevent: vendor.future\revent: done\ndata:\r\ndata: [DONE]\rid: private\nretry: 900\r\n\n"
	event, data, seen := parseSSEFields([]byte(frame))
	if event != "done" || string(data) != "\n[DONE]" || !seen || !validSSEDone(event, data, seen) {
		t.Fatalf("SSE field semantics changed: event=%q data=%q seen=%v", event, data, seen)
	}
	for _, frame := range []string{"event: done\ndata: [DONE]\nevent: error", "event: error\rdata:[DONE]\r\r", "event: done\ndata: [DON]", "event: done\nid: [DONE]"} {
		event, data, seen := parseSSEFields([]byte(frame))
		if validSSEDone(event, data, seen) {
			t.Fatalf("unconfirmed sentinel accepted: %q", frame)
		}
	}
}

type desktopByteReader struct{ io.Reader }

func (r desktopByteReader) Read(buffer []byte) (int, error) { return r.Reader.Read(buffer[:1]) }

func desktopSSEEndings(wire string, endings []string) string {
	var out strings.Builder
	line := 0
	for _, value := range wire {
		if value == '\n' {
			out.WriteString(endings[line%len(endings)])
			line++
		} else {
			out.WriteRune(value)
		}
	}
	return out.String()
}

func TestDesktopSSEByteFragmentsPreserveNativeDataAcrossEveryLineEnding(t *testing.T) {
	s, _ := setup(t)
	unknown := ": keepalive\nid: native-id\nretry: 1900\nevent: vendor.future\ndata: {\"opaque\":\ndata: \"签名+/=\"}\n\n"
	for _, fixture := range []struct{ name, endpoint, wire string }{
		{"chat", "openai", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\nevent: done\ndata: [DONE]\n\n"},
		{"responses", "openai-response", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response_line_endings\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"},
		{"anthropic", "anthropic", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", "gemini", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n"},
	} {
		for _, format := range []struct {
			name    string
			endings []string
		}{{"lf", []string{"\n"}}, {"crlf-split-after-cr", []string{"\r\n"}}, {"cr-including-final-eof", []string{"\r"}}, {"mixed", []string{"\r\n", "\n", "\r"}}} {
			t.Run(fixture.name+"/"+format.name, func(t *testing.T) {
				wire := desktopSSEEndings(unknown+fixture.wire, format.endings)
				p := newUsage(fixture.endpoint)
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(desktopByteReader{strings.NewReader(wire)})}
				w := httptest.NewRecorder()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, err := s.stream(w, response, ctx, cancel, p, 1, 1, "", "synthetic-stream-key")
				usage := p.finish(err == nil)
				if !started || err != nil || !usage.Complete || usage.InputTokens != 3 || usage.OutputTokens != 2 || p.failed {
					t.Fatalf("fragmented native stream lost completion: started=%v err=%v usage=%+v", started, err, usage)
				}
				if w.Body.String() != wire || strings.Contains(usage.RawJSON, "签名") {
					t.Fatal("framing rewrote native bytes or retained generated content in usage")
				}
			})
		}
	}
}
