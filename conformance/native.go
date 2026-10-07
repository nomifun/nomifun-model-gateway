// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
)

func decodeNative(data []byte) (map[string]any, error) {
	var o map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&o) != nil || o == nil {
		return nil, errors.New("invalid native JSON response")
	}
	return o, nil
}

func str(v any) string { s, _ := v.(string); return s }
func arr(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	if a, ok := v.([]string); ok {
		values := make([]any, len(a))
		for i, s := range a {
			values[i] = s
		}
		return values
	}
	return nil
}
func obj(v any) map[string]any { o, _ := v.(map[string]any); return o }
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	case float64:
		return n, true
	}
	return 0, false
}
func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := n.Int64()
		return i, e == nil
	case float64:
		if math.Trunc(n) != n || n > math.MaxInt64 || n < math.MinInt64 {
			return 0, false
		}
		return int64(n), true
	}
	return 0, false
}
func token(v any) (int64, bool) { n, ok := integer(v); return n, ok && n >= 0 }

func chatUsage(v any) error {
	u := obj(v)
	p, ok := token(u["prompt_tokens"])
	if !ok {
		return errors.New("prompt token usage missing or invalid")
	}
	c, ok := token(u["completion_tokens"])
	if !ok {
		return errors.New("completion token usage missing or invalid")
	}
	t, ok := token(u["total_tokens"])
	if !ok || p > math.MaxInt64-c || t != p+c {
		return errors.New("total token usage must equal prompt plus completion")
	}
	return nil
}
func responsesUsage(v any) error {
	u := obj(v)
	p, ok := token(u["input_tokens"])
	if !ok {
		return errors.New("input token usage missing or invalid")
	}
	c, ok := token(u["output_tokens"])
	if !ok {
		return errors.New("output token usage missing or invalid")
	}
	t, ok := token(u["total_tokens"])
	if !ok || p > math.MaxInt64-c || t != p+c {
		return errors.New("response total token usage must equal input plus output")
	}
	for _, pair := range []struct {
		field, inner string
		limit        int64
	}{{"input_tokens_details", "cached_tokens", p}, {"output_tokens_details", "reasoning_tokens", c}} {
		if detail := obj(u[pair.field]); detail != nil {
			n, valid := token(detail[pair.inner])
			if !valid || n > pair.limit {
				return errors.New("invalid cached or reasoning token detail")
			}
		}
	}
	return nil
}
func anthropicUsage(v any) error {
	u := obj(v)
	if _, ok := token(u["input_tokens"]); !ok {
		return errors.New("Anthropic input token usage missing")
	}
	if _, ok := token(u["output_tokens"]); !ok {
		return errors.New("Anthropic output token usage missing")
	}
	for _, field := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if value, exists := u[field]; exists {
			if _, ok := token(value); !ok {
				return errors.New("invalid Anthropic cache token usage")
			}
		}
	}
	return nil
}
func geminiUsage(v any) error {
	u := obj(v)
	p, ok := token(u["promptTokenCount"])
	if !ok {
		return errors.New("Gemini prompt token usage missing")
	}
	c, ok := token(u["candidatesTokenCount"])
	if !ok {
		return errors.New("Gemini candidate token usage missing")
	}
	t, ok := token(u["totalTokenCount"])
	if !ok {
		return errors.New("Gemini total token usage missing")
	}
	thought := int64(0)
	if value, exists := u["thoughtsTokenCount"]; exists {
		thought, ok = token(value)
		if !ok {
			return errors.New("invalid Gemini thoughts usage")
		}
	}
	tool := int64(0)
	if value, exists := u["toolUsePromptTokenCount"]; exists {
		tool, ok = token(value)
		if !ok {
			return errors.New("invalid Gemini tool prompt usage")
		}
	}
	if p > math.MaxInt64-c || p+c > math.MaxInt64-thought || p+c+thought > math.MaxInt64-tool || (t != p+c+thought && t != p+c+thought+tool) {
		return errors.New("incoherent Gemini total token usage")
	}
	if value, exists := u["cachedContentTokenCount"]; exists {
		n, valid := token(value)
		if !valid || n > p {
			return errors.New("invalid Gemini cached prompt usage")
		}
	}
	return nil
}

func toolArguments(v any) bool {
	if s, ok := v.(string); ok {
		var a map[string]any
		return json.Unmarshal([]byte(s), &a) == nil && a != nil
	}
	return obj(v) != nil
}

func validateNative(p probe, data []byte, tools bool) error {
	o, e := decodeNative(data)
	if e != nil {
		return e
	}
	if p.name == "count-tokens" {
		if _, ok := token(o["input_tokens"]); !ok {
			return errors.New("count_tokens input token count missing")
		}
		return nil
	}
	switch p.endpoint {
	case "openai":
		if str(o["id"]) == "" || str(o["object"]) != "chat.completion" || str(o["model"]) == "" || len(arr(o["choices"])) == 0 {
			return errors.New("invalid OpenAI chat response identity or choices")
		}
		found := false
		for _, v := range arr(o["choices"]) {
			c := obj(v)
			m := obj(c["message"])
			if str(m["role"]) != "assistant" || str(c["finish_reason"]) == "" {
				return errors.New("invalid Chat assistant message or finish reason")
			}
			if content, ok := m["content"].(string); ok && content != "" && !tools {
				found = true
			}
			for _, v := range arr(m["tool_calls"]) {
				tc := obj(v)
				fn := obj(tc["function"])
				if str(tc["id"]) == "" || str(tc["type"]) != "function" || str(fn["name"]) == "" || !toolArguments(fn["arguments"]) {
					return errors.New("invalid Chat function call")
				}
				if !tools || str(fn["name"]) == "conformance_echo" {
					found = true
				}
			}
		}
		if !found {
			return errors.New("Chat response contains no expected text or tool call")
		}
		return chatUsage(o["usage"])
	case "openai-response":
		if str(o["id"]) == "" || str(o["object"]) != "response" || str(o["status"]) != "completed" || str(o["model"]) == "" {
			return errors.New("invalid completed Responses identity")
		}
		found := false
		for _, v := range arr(o["output"]) {
			item := obj(v)
			switch str(item["type"]) {
			case "message":
				if str(item["role"]) != "assistant" {
					return errors.New("Responses message must be assistant")
				}
				for _, v := range arr(item["content"]) {
					part := obj(v)
					if str(part["type"]) == "output_text" && str(part["text"]) != "" && !tools {
						found = true
					}
				}
			case "function_call":
				if str(item["id"]) == "" || str(item["call_id"]) == "" || str(item["name"]) == "" || !toolArguments(item["arguments"]) {
					return errors.New("invalid Responses function call")
				}
				if !tools || str(item["name"]) == "conformance_echo" {
					found = true
				}
			case "reasoning":
				if content, exists := item["encrypted_content"]; exists && str(content) == "" {
					return errors.New("encrypted reasoning content must be a nonempty string")
				}
			}
		}
		if !found {
			return errors.New("Responses output contains no expected message or tool call")
		}
		return responsesUsage(o["usage"])
	case "anthropic":
		if str(o["id"]) == "" || str(o["type"]) != "message" || str(o["role"]) != "assistant" || str(o["model"]) == "" || str(o["stop_reason"]) == "" {
			return errors.New("invalid Anthropic message identity or stop reason")
		}
		found := false
		for _, v := range arr(o["content"]) {
			part := obj(v)
			switch str(part["type"]) {
			case "text":
				if str(part["text"]) != "" && !tools {
					found = true
				}
			case "tool_use":
				if str(part["id"]) == "" || str(part["name"]) == "" || !toolArguments(part["input"]) {
					return errors.New("invalid Anthropic tool use")
				}
				if !tools || str(part["name"]) == "conformance_echo" {
					found = true
				}
			case "thinking":
				if _, ok := part["thinking"].(string); !ok || str(part["signature"]) == "" {
					return errors.New("thinking block must retain text field and signature")
				}
			case "redacted_thinking":
				if str(part["data"]) == "" {
					return errors.New("redacted thinking data missing")
				}
			}
		}
		if !found {
			return errors.New("Anthropic content contains no expected text or tool")
		}
		return anthropicUsage(o["usage"])
	case "gemini":
		if len(arr(o["candidates"])) == 0 {
			return errors.New("Gemini candidate missing")
		}
		found := false
		for _, v := range arr(o["candidates"]) {
			candidate := obj(v)
			if str(candidate["finishReason"]) == "" {
				return errors.New("Gemini final finish reason missing")
			}
			for _, v := range arr(obj(candidate["content"])["parts"]) {
				part := obj(v)
				if text, ok := part["text"].(string); ok && text != "" && !tools {
					found = true
				}
				if fn := obj(part["functionCall"]); fn != nil {
					if str(fn["name"]) == "" || !toolArguments(fn["args"]) {
						return errors.New("invalid Gemini function call")
					}
					if !tools || str(fn["name"]) == "conformance_echo" {
						found = true
					}
				}
				if value, exists := part["thoughtSignature"]; exists && str(value) == "" {
					return errors.New("Gemini thought signature must be retained")
				}
			}
		}
		if !found {
			return errors.New("Gemini contains no expected content or tool call")
		}
		return geminiUsage(o["usageMetadata"])
	case "image-generation":
		if _, ok := token(o["created"]); !ok {
			return errors.New("image creation timestamp missing")
		}
		if len(arr(o["data"])) == 0 {
			return errors.New("image data missing")
		}
		for _, v := range arr(o["data"]) {
			item := obj(v)
			if s := str(item["b64_json"]); s != "" {
				b, e := base64.StdEncoding.DecodeString(s)
				if e != nil || len(b) == 0 {
					return errors.New("invalid base64 image data")
				}
			} else if !https(str(item["url"])) {
				return errors.New("image must contain nonempty base64 data or HTTPS URL")
			}
		}
		return nil
	case "embeddings":
		if str(o["object"]) != "list" || str(o["model"]) == "" || len(arr(o["data"])) != 2 {
			return errors.New("invalid embedding list")
		}
		dimension := 0
		seen := map[int64]bool{}
		for _, v := range arr(o["data"]) {
			item := obj(v)
			index, ok := token(item["index"])
			if !ok || index > 1 || seen[index] || str(item["object"]) != "embedding" {
				return errors.New("invalid embedding index or type")
			}
			seen[index] = true
			vector := arr(item["embedding"])
			if len(vector) == 0 || (dimension != 0 && dimension != len(vector)) {
				return errors.New("invalid embedding dimensions")
			}
			dimension = len(vector)
			for _, v := range vector {
				f, ok := number(v)
				if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
					return errors.New("non-finite embedding value")
				}
			}
		}
		u := obj(o["usage"])
		pt, ok := token(u["prompt_tokens"])
		if !ok {
			return errors.New("embedding usage missing")
		}
		total, ok := token(u["total_tokens"])
		if !ok || total < pt {
			return errors.New("invalid embedding total usage")
		}
		return nil
	case "jina-rerank":
		if str(o["model"]) == "" || len(arr(o["results"])) == 0 {
			return errors.New("rerank results missing")
		}
		previous := math.Inf(1)
		seen := map[int64]bool{}
		for _, v := range arr(o["results"]) {
			item := obj(v)
			index, ok := token(item["index"])
			if !ok || index > 1 || seen[index] {
				return errors.New("invalid rerank input index")
			}
			seen[index] = true
			score, ok := number(item["relevance_score"])
			if !ok || math.IsNaN(score) || math.IsInf(score, 0) || score > previous {
				return errors.New("rerank scores must be finite and descending")
			}
			previous = score
		}
		usage := obj(o["usage"])
		if _, ok := token(usage["total_tokens"]); !ok {
			return errors.New("rerank usage missing")
		}
		return nil
	}
	return errors.New("unsupported native endpoint")
}
