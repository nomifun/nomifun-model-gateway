// SPDX-License-Identifier: Apache-2.0
// provider-onboarding-fixture is a loopback-only synthetic upstream for local
// provider onboarding acceptance. It never contacts a merchant or model service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const modelID = "synthetic-chat-v1"

type fixture struct {
	key                                string
	mu                                 sync.Mutex
	requests, streams, unknownRequests int
}

func main() {
	address := os.Getenv("NMG_FIXTURE_LISTEN")
	if address == "" {
		address = "127.0.0.1:18892"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("fixture requires a loopback listen address")
	}
	key := os.Getenv("NMG_FIXTURE_API_KEY")
	if key == "" {
		log.Fatal("fixture requires an explicitly configured synthetic key")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal("fixture listen address is unavailable")
	}
	app := &fixture{key: key}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	log.Print("synthetic provider fixture started")
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		<-finished
	case err = <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("fixture HTTP server failed")
		}
	}
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		writeJSON(w, 200, map[string]any{"ok": true, "synthetic": true})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.key && r.Header.Get("x-api-key") != f.key {
		writeJSON(w, 401, map[string]any{"error": map[string]any{"message": "Synthetic fixture authorization required", "type": "authentication_error"}})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		// OpenAI lists have no documented cursor contract. Keep their normal
		// list complete and leave capabilities/limits/prices explicitly unknown.
		writeJSON(w, 200, map[string]any{"object": "list", "data": []any{map[string]any{"id": modelID, "object": "model", "owned_by": "synthetic-local-fixture"}, map[string]any{"id": modelID, "object": "model"}, map[string]any{"id": "synthetic-unconfirmed-v1", "object": "model"}}})
	case r.Method == http.MethodGet && (r.URL.Path == "/paginated/v1/models" || r.URL.Path == "/partial/v1/models"):
		if r.URL.Query().Get("after_id") == "" {
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": modelID, "type": "model", "display_name": "Synthetic local chat"}}, "has_more": true, "last_id": modelID})
		} else if r.URL.Query().Get("after_id") == modelID && r.URL.Path == "/partial/v1/models" {
			writeJSON(w, 503, map[string]any{"error": map[string]any{"message": "Synthetic later-page failure"}})
		} else if r.URL.Query().Get("after_id") == modelID {
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": modelID, "type": "model"}, map[string]any{"id": "synthetic-unconfirmed-v1", "type": "model"}}, "has_more": false, "last_id": "synthetic-unconfirmed-v1"})
		} else {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "Unknown synthetic pagination cursor"}})
		}
	case r.Method == http.MethodGet && r.URL.Path == "/failed/v1/models":
		writeJSON(w, 503, map[string]any{"error": map[string]any{"message": "Synthetic model list unavailable"}})
	case r.Method == http.MethodGet && r.URL.Path == "/fixture/evidence":
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"synthetic": true, "chat_requests": f.requests, "stream_requests": f.streams, "unknown_request_fields_received": f.unknownRequests})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		f.chat(w, r)
	default:
		writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "Synthetic fixture endpoint not implemented", "type": "invalid_request_error"}})
	}
}

func (f *fixture) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "Invalid synthetic request size"}})
		return
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": "Invalid synthetic request JSON"}})
		return
	}
	var model string
	var stream bool
	_ = json.Unmarshal(request["model"], &model)
	_ = json.Unmarshal(request["stream"], &stream)
	if model != modelID {
		writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "Synthetic model is unavailable", "code": "model_not_found"}})
		return
	}
	f.mu.Lock()
	f.requests++
	if stream {
		f.streams++
	}
	if _, ok := request["fixture_request_unknown"]; ok {
		f.unknownRequests++
	}
	f.mu.Unlock()
	var errorMode, usageMode string
	_ = json.Unmarshal(request["fixture_error_mode"], &errorMode)
	_ = json.Unmarshal(request["fixture_usage_mode"], &usageMode)
	if errorMode == "rate-limited" {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, 429, map[string]any{"error": map[string]any{"message": "Synthetic native rate limit", "type": "rate_limit_error", "code": "fixture_rate_limit", "fixture_error_unknown": "retained"}})
		return
	}
	usage := map[string]any{"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17, "prompt_tokens_details": map[string]any{"cached_tokens": 4}, "completion_tokens_details": map[string]any{"reasoning_tokens": 2}, "fixture_usage_unknown": 3}
	usageMode = strings.TrimSpace(usageMode)
	if usageMode == "partial" {
		usage = map[string]any{"prompt_tokens": 12, "fixture_usage_unknown": 3}
	}
	if usageMode == "missing" {
		usage = nil
	}
	if !stream {
		response := map[string]any{"id": "chatcmpl-synthetic-onboarding", "object": "chat.completion", "created": int64(1700000000), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Synthetic local provider acceptance succeeded."}, "finish_reason": "stop"}}, "fixture_response_unknown": map[string]any{"retained": true}}
		if usage != nil {
			response["usage"] = usage
		}
		writeJSON(w, 200, response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	events := []map[string]any{
		{"id": "chatcmpl-synthetic-stream", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Synthetic streaming acceptance."}, "finish_reason": nil}}, "fixture_event_unknown": "retained"},
		{"id": "chatcmpl-synthetic-stream", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}},
	}
	if usage != nil {
		events = append(events, map[string]any{"id": "chatcmpl-synthetic-stream", "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": usage})
	}
	for _, event := range events {
		payload, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
