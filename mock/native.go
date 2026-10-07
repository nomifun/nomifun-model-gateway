// SPDX-License-Identifier: Apache-2.0
package mock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

const syntheticText = "Hello from the synthetic NomiFun Model Gateway mock."
const syntheticReasoning = "Synthetic reasoning for protocol development only."
const syntheticSignature = "bW9jay1zaWduYXR1cmUtc3ludGhldGljLW9ubHk="

// A valid one-pixel PNG, generated entirely as a fixture.
var syntheticPNG = func() string {
	pixel := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	pixel.SetNRGBA(0, 0, color.NRGBA{R: 80, G: 120, B: 220, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixel); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}()

type event struct {
	name string
	data any
	done bool
}

func (s *server) infer(w http.ResponseWriter, r *http.Request, id, model string, endpoint v1.Endpoint, body map[string]json.RawMessage, stream bool) {
	tool := toolName(body, endpoint)
	switch endpoint {
	case v1.OpenAI:
		response := chatResponse(id, model, tool)
		if !stream {
			writeJSON(w, 200, response)
			return
		}
		s.stream(w, r, chatEvents(id, model, tool))
	case v1.Responses:
		response := responsesResponse(id, model, tool, wantsEncrypted(body))
		if !stream {
			writeJSON(w, 200, response)
			return
		}
		s.stream(w, r, responsesEvents(response))
	case v1.Anthropic:
		if r.URL.Path == "/v1/messages/count_tokens" {
			writeJSON(w, 200, map[string]any{"input_tokens": 12})
			return
		}
		response := anthropicResponse(id, model, tool)
		if !stream {
			writeJSON(w, 200, response)
			return
		}
		s.stream(w, r, anthropicEvents(response))
	case v1.Gemini:
		response := geminiResponse(id, model, tool)
		if !stream {
			writeJSON(w, 200, response)
			return
		}
		s.stream(w, r, geminiEvents(response))
	case v1.Images:
		n := 1
		if raw, ok := body["n"]; ok {
			_ = json.Unmarshal(raw, &n)
		}
		data := []any{}
		for i := 0; i < n; i++ {
			data = append(data, map[string]any{"b64_json": syntheticPNG, "revised_prompt": "Synthetic one-pixel mock image."})
		}
		writeJSON(w, 200, map[string]any{"created": int64(0), "data": data})
	case v1.Embeddings:
		var values []json.RawMessage
		count := 1
		if json.Unmarshal(body["input"], &values) == nil {
			count = len(values)
			if count > 0 && strings.HasPrefix(strings.TrimSpace(string(values[0])), "\"") == false {
				var token int64
				if json.Unmarshal(values[0], &token) == nil {
					count = 1
				}
			}
		}
		data := []any{}
		for i := 0; i < count; i++ {
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": []float64{0.25, 0.5, 0.75}})
		}
		writeJSON(w, 200, map[string]any{"object": "list", "model": model, "data": data, "usage": map[string]any{"prompt_tokens": 12, "total_tokens": 12}})
	case v1.Rerank:
		var docs []json.RawMessage
		_ = json.Unmarshal(body["documents"], &docs)
		n := len(docs)
		if raw, ok := body["top_n"]; ok {
			var limit int
			if json.Unmarshal(raw, &limit) == nil && limit >= 0 && limit < n {
				n = limit
			}
		}
		returnDocs := true
		if _, present := body["return_documents"]; present {
			returnDocs, _ = boolField(body, "return_documents")
		}
		results := []any{}
		for i := 0; i < n; i++ {
			result := map[string]any{"index": i, "relevance_score": 1.0 - float64(i)/float64(len(docs)+1)}
			if returnDocs {
				var doc any
				_ = json.Unmarshal(docs[i], &doc)
				if value, ok := doc.(string); ok {
					result["document"] = map[string]any{"text": value}
				} else {
					result["document"] = doc
				}
			}
			results = append(results, result)
		}
		writeJSON(w, 200, map[string]any{"object": "list", "id": strings.Replace(id, "req_", "rerank_", 1), "model": model, "results": results, "usage": map[string]any{"total_tokens": 12}})
	}
}

func toolName(body map[string]json.RawMessage, endpoint v1.Endpoint) string {
	if string(body["tool_choice"]) == `"none"` {
		return ""
	}
	var choice map[string]any
	_ = json.Unmarshal(body["tool_choice"], &choice)
	if choice["type"] == "none" {
		return ""
	}
	forced, _ := choice["name"].(string)
	if fn, ok := choice["function"].(map[string]any); ok {
		forced, _ = fn["name"].(string)
	}
	var tools []map[string]any
	if json.Unmarshal(body["tools"], &tools) != nil {
		return ""
	}
	for _, tool := range tools {
		if endpoint == v1.Gemini {
			if declarations, ok := tool["functionDeclarations"].([]any); ok {
				for _, decl := range declarations {
					if obj, ok := decl.(map[string]any); ok {
						if name, ok := obj["name"].(string); ok && (forced == "" || name == forced) {
							return name
						}
					}
				}
			}
		}
		if name, ok := tool["name"].(string); ok && (forced == "" || name == forced) {
			return name
		}
		if fn, ok := tool["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && (forced == "" || name == forced) {
				return name
			}
		}
	}
	return ""
}
func wantsEncrypted(body map[string]json.RawMessage) bool {
	if stored, present := body["store"]; present && string(stored) == "false" {
		return true
	}
	var fields []string
	if json.Unmarshal(body["include"], &fields) != nil {
		return false
	}
	for _, field := range fields {
		if field == "reasoning.encrypted_content" {
			return true
		}
	}
	return false
}

func chatUsage() map[string]any {
	return map[string]any{"prompt_tokens": 12, "completion_tokens": 6, "total_tokens": 18, "prompt_tokens_details": map[string]any{"cached_tokens": 5}, "completion_tokens_details": map[string]any{"reasoning_tokens": 2}}
}
func chatResponse(id, model, tool string) map[string]any {
	message := map[string]any{"role": "assistant", "content": syntheticText, "reasoning_content": syntheticReasoning}
	finish := "stop"
	if tool != "" {
		message["tool_calls"] = []any{map[string]any{"id": "call_mock_" + id, "type": "function", "function": map[string]any{"name": tool, "arguments": `{"value":"mock"}`}}}
		finish = "tool_calls"
	}
	return map[string]any{"id": strings.Replace(id, "req_", "chatcmpl_", 1), "object": "chat.completion", "created": int64(0), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": chatUsage()}
}
func chatEvents(id, model, tool string) []event {
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": strings.Replace(id, "req_", "chatcmpl_", 1), "object": "chat.completion.chunk", "created": int64(0), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	events := []event{{data: chunk(map[string]any{"role": "assistant", "content": ""}, nil)}, {data: chunk(map[string]any{"reasoning_content": syntheticReasoning}, nil)}, {data: chunk(map[string]any{"content": syntheticText}, nil)}}
	finish := "stop"
	if tool != "" {
		finish = "tool_calls"
		events = append(events, event{data: chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_mock_" + id, "type": "function", "function": map[string]any{"name": tool, "arguments": `{"value":"mock"}`}}}}, nil)})
	}
	events = append(events, event{data: chunk(map[string]any{}, finish)}, event{data: map[string]any{"id": strings.Replace(id, "req_", "chatcmpl_", 1), "object": "chat.completion.chunk", "created": int64(0), "model": model, "choices": []any{}, "usage": chatUsage()}}, event{done: true})
	return events
}

func responsesResponse(id, model, tool string, encrypted bool) map[string]any {
	responseID := strings.Replace(id, "req_", "resp_", 1)
	reasoning := map[string]any{"id": "rs_" + responseID, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": syntheticReasoning}}}
	if encrypted {
		reasoning["encrypted_content"] = syntheticSignature
	}
	output := []any{reasoning, map[string]any{"id": "msg_" + responseID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": syntheticText, "annotations": []any{}}}}}
	if tool != "" {
		output = append(output, map[string]any{"id": "fc_" + responseID, "type": "function_call", "status": "completed", "call_id": "call_" + responseID, "name": tool, "arguments": `{"value":"mock"}`})
	}
	return map[string]any{"id": responseID, "object": "response", "created_at": int64(0), "status": "completed", "model": model, "output": output, "error": nil, "incomplete_details": nil, "parallel_tool_calls": false, "usage": map[string]any{"input_tokens": 12, "output_tokens": 6, "total_tokens": 18, "input_tokens_details": map[string]any{"cached_tokens": 5}, "output_tokens_details": map[string]any{"reasoning_tokens": 2}}}
}
func responsesEvents(response map[string]any) []event {
	events := []event{}
	seq := 0
	add := func(name string, data map[string]any) {
		data["type"] = name
		data["sequence_number"] = seq
		seq++
		events = append(events, event{name: name, data: data})
	}
	initial := map[string]any{}
	for k, v := range response {
		initial[k] = v
	}
	initial["status"] = "in_progress"
	initial["output"] = []any{}
	initial["usage"] = nil
	add("response.created", map[string]any{"response": initial})
	add("response.in_progress", map[string]any{"response": initial})
	for index, value := range response["output"].([]any) {
		item := value.(map[string]any)
		pending := map[string]any{}
		for k, v := range item {
			pending[k] = v
		}
		switch item["type"] {
		case "reasoning":
			pending["summary"] = []any{}
			add("response.output_item.added", map[string]any{"output_index": index, "item": pending})
			add("response.reasoning_summary_part.added", map[string]any{"item_id": item["id"], "output_index": index, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}})
			add("response.reasoning_summary_text.delta", map[string]any{"item_id": item["id"], "output_index": index, "summary_index": 0, "delta": syntheticReasoning})
			add("response.reasoning_summary_text.done", map[string]any{"item_id": item["id"], "output_index": index, "summary_index": 0, "text": syntheticReasoning})
			add("response.reasoning_summary_part.done", map[string]any{"item_id": item["id"], "output_index": index, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": syntheticReasoning}})
		case "message":
			pending["status"] = "in_progress"
			pending["content"] = []any{}
			add("response.output_item.added", map[string]any{"output_index": index, "item": pending})
			add("response.content_part.added", map[string]any{"item_id": item["id"], "output_index": index, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			add("response.output_text.delta", map[string]any{"item_id": item["id"], "output_index": index, "content_index": 0, "delta": syntheticText})
			add("response.output_text.done", map[string]any{"item_id": item["id"], "output_index": index, "content_index": 0, "text": syntheticText})
			add("response.content_part.done", map[string]any{"item_id": item["id"], "output_index": index, "content_index": 0, "part": item["content"].([]any)[0]})
		case "function_call":
			pending["status"] = "in_progress"
			pending["arguments"] = ""
			add("response.output_item.added", map[string]any{"output_index": index, "item": pending})
			add("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": item["arguments"]})
			add("response.function_call_arguments.done", map[string]any{"item_id": item["id"], "output_index": index, "arguments": item["arguments"]})
		}
		add("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
	add("response.completed", map[string]any{"response": response})
	return events
}

func anthropicResponse(id, model, tool string) map[string]any {
	content := []any{map[string]any{"type": "thinking", "thinking": syntheticReasoning, "signature": syntheticSignature}, map[string]any{"type": "text", "text": syntheticText}}
	stop := "end_turn"
	if tool != "" {
		stop = "tool_use"
		content = append(content, map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": tool, "input": map[string]any{"value": "mock"}})
	}
	return map[string]any{"id": strings.Replace(id, "req_", "msg_", 1), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 12, "output_tokens": 6, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 5}}
}
func anthropicEvents(response map[string]any) []event {
	start := map[string]any{}
	for k, v := range response {
		start[k] = v
	}
	start["content"] = []any{}
	start["stop_reason"] = nil
	start["usage"] = map[string]any{"input_tokens": 12, "output_tokens": 0, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 5}
	events := []event{{name: "message_start", data: map[string]any{"type": "message_start", "message": start}}}
	for index, value := range response["content"].([]any) {
		block := value.(map[string]any)
		pending := map[string]any{}
		for k, v := range block {
			pending[k] = v
		}
		var deltas []map[string]any
		switch block["type"] {
		case "thinking":
			pending["thinking"] = ""
			pending["signature"] = ""
			deltas = []map[string]any{{"type": "thinking_delta", "thinking": syntheticReasoning}, {"type": "signature_delta", "signature": syntheticSignature}}
		case "text":
			pending["text"] = ""
			deltas = []map[string]any{{"type": "text_delta", "text": syntheticText}}
		case "tool_use":
			pending["input"] = map[string]any{}
			deltas = []map[string]any{{"type": "input_json_delta", "partial_json": `{"value":"mock"}`}}
		}
		events = append(events, event{name: "content_block_start", data: map[string]any{"type": "content_block_start", "index": index, "content_block": pending}})
		for _, delta := range deltas {
			events = append(events, event{name: "content_block_delta", data: map[string]any{"type": "content_block_delta", "index": index, "delta": delta}})
		}
		events = append(events, event{name: "content_block_stop", data: map[string]any{"type": "content_block_stop", "index": index}})
	}
	events = append(events, event{name: "message_delta", data: map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": response["stop_reason"], "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 6, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 5}}}, event{name: "message_stop", data: map[string]any{"type": "message_stop"}})
	return events
}

func geminiResponse(id, model, tool string) map[string]any {
	parts := []any{map[string]any{"text": syntheticReasoning, "thought": true, "thoughtSignature": syntheticSignature}, map[string]any{"text": syntheticText}, map[string]any{"text": "", "thoughtSignature": syntheticSignature}}
	if tool != "" {
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tool, "args": map[string]any{"value": "mock"}}, "thoughtSignature": syntheticSignature})
	}
	return map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 12, "candidatesTokenCount": 6, "totalTokenCount": 20, "cachedContentTokenCount": 5, "thoughtsTokenCount": 2}, "modelVersion": model, "responseId": strings.Replace(id, "req_", "gemini_", 1)}
}
func geminiEvents(response map[string]any) []event {
	candidate := response["candidates"].([]any)[0].(map[string]any)
	content := candidate["content"].(map[string]any)
	parts := content["parts"].([]any)
	events := []event{}
	for _, part := range parts {
		events = append(events, event{data: map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{part}}}}, "modelVersion": response["modelVersion"], "responseId": response["responseId"]}})
	}
	events = append(events, event{data: map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{}}, "finishReason": "STOP"}}, "usageMetadata": response["usageMetadata"], "modelVersion": response["modelVersion"], "responseId": response["responseId"]}})
	return events
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, events []event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, r, w.Header().Get("X-Request-ID"), &apiError{500, "stream_unavailable", "This HTTP writer does not support streaming."})
		return
	}
	for index, event := range events {
		if r.Context().Err() != nil {
			return
		}
		if index > 0 && s.config.StreamDelay > 0 {
			timer := time.NewTimer(s.config.StreamDelay)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if event.name != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event.name); err != nil {
				return
			}
		}
		if event.done {
			if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
				return
			}
		} else {
			raw, err := json.Marshal(event.data)
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}
