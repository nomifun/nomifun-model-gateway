// SPDX-License-Identifier: Apache-2.0
package conformance

import "errors"

// These assertions are opt-in fixture requirements. Ordinary compatible models
// need not return reasoning, signatures or cache hits on every request.
func validateSyntheticEvidence(p probe, body []byte, tools bool) error {
	if p.name == "count-tokens" {
		return nil
	}
	if p.endpoint != "openai-response" && p.endpoint != "anthropic" && p.endpoint != "gemini" {
		return nil
	}
	var objects []map[string]any
	if p.stream {
		events, e := streamEvents(body)
		if e != nil {
			return e
		}
		for _, event := range events {
			if string(event.data) == "[DONE]" {
				continue
			}
			o, e := decodeNative(event.data)
			if e != nil {
				return e
			}
			objects = append(objects, o)
		}
	} else {
		o, e := decodeNative(body)
		if e != nil {
			return e
		}
		objects = append(objects, o)
	}
	encrypted, thinking, signature, thought, emptyTail, toolSignature, cache, thoughtUsage := false, false, false, false, false, false, false, false
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if str(value["type"]) == "reasoning" && str(value["encrypted_content"]) != "" {
				encrypted = true
			}
			if str(value["type"]) == "thinking" {
				thinking = true
				if str(value["signature"]) != "" {
					signature = true
				}
			}
			if str(value["type"]) == "signature_delta" && str(value["signature"]) != "" {
				signature = true
			}
			if v, ok := value["thought"].(bool); ok && v {
				thought = true
			}
			if text, ok := value["text"].(string); ok && text == "" && str(value["thoughtSignature"]) != "" {
				emptyTail = true
			}
			if obj(value["functionCall"]) != nil && str(value["thoughtSignature"]) != "" {
				toolSignature = true
			}
			if p.endpoint == "anthropic" {
				a, okA := token(value["cache_creation_input_tokens"])
				b, okB := token(value["cache_read_input_tokens"])
				if okA && okB && a > 0 && b > 0 {
					cache = true
				}
			}
			if p.endpoint == "gemini" {
				if n, ok := token(value["cachedContentTokenCount"]); ok && n > 0 {
					cache = true
				}
				if n, ok := token(value["thoughtsTokenCount"]); ok && n > 0 {
					thoughtUsage = true
				}
			}
			for _, v := range value {
				visit(v)
			}
		case []any:
			for _, v := range value {
				visit(v)
			}
		}
	}
	for _, o := range objects {
		visit(o)
	}
	switch p.endpoint {
	case "openai-response":
		if !encrypted {
			return errors.New("synthetic Responses encrypted reasoning was lost")
		}
	case "anthropic":
		if !thinking || !signature || !cache {
			return errors.New("synthetic Anthropic thinking, signature or cache usage was lost")
		}
	case "gemini":
		if !thought || !emptyTail || !cache || !thoughtUsage || (tools && !toolSignature) {
			return errors.New("synthetic Gemini thought/signature parts or cache/thought usage were lost")
		}
	}
	return nil
}
