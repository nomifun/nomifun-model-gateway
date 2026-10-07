// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

type usageParser struct {
	endpoint                                                          string
	usage                                                             core.Usage
	seen, terminal, failed, invalid, ready                            bool
	countTokens                                                       bool
	anthropicInput                                                    int64
	anthropicInputSeen, anthropicOutputSeen, anthropicFinalOutputSeen bool
	anthropic                                                         map[string]json.RawMessage
}

func newUsage(endpoint string, path ...string) *usageParser {
	p := &usageParser{endpoint: endpoint, usage: core.Usage{Requests: 1}, anthropic: map[string]json.RawMessage{}}
	p.countTokens = endpoint == "anthropic" && len(path) > 0 && path[0] == "/v1/messages/count_tokens"
	return p
}
func object(raw []byte) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	json.Unmarshal(raw, &value)
	return value
}
func integer(fields map[string]json.RawMessage, key string) int64 {
	n, _, err := count(fields, key)
	if err != nil {
		return 0
	}
	return n
}

var errUsage = errors.New("invalid native usage counters")

func count(fields map[string]json.RawMessage, key string) (int64, bool, error) {
	raw, present := fields[key]
	if !present {
		return 0, false, nil
	}
	var n int64
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &n) != nil || n < 0 {
		return 0, true, errUsage
	}
	return n, true, nil
}
func checkedSum(values ...int64) (int64, error) {
	total := int64(0)
	for _, n := range values {
		if n < 0 || total > math.MaxInt64-n {
			return 0, errUsage
		}
		total += n
	}
	return total, nil
}
func strictObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if _, err := objectFields(raw); err != nil {
		return nil, errUsage
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errUsage
	}
	return fields, nil
}
func detail(fields map[string]json.RawMessage, name, meter string, limit int64) (int64, error) {
	raw, exists := fields[name]
	if !exists {
		return 0, nil
	}
	nested, err := strictObject(raw)
	if err != nil {
		return 0, err
	}
	if nested == nil {
		return 0, nil
	}
	// Validate published details even when they are not separately priced.
	for _, key := range []string{"cached_tokens", "reasoning_tokens", "audio_tokens", "image_tokens", "text_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"} {
		n, present, e := count(nested, key)
		if e != nil || present && n > limit {
			return 0, errUsage
		}
	}
	n, _, err := count(nested, meter)
	return n, err
}
func conserved(fields map[string]json.RawMessage, name string, total int64) error {
	n, present, err := count(fields, name)
	if err != nil || present && n != total {
		return errUsage
	}
	return nil
}
func validCanonical(u core.Usage) error {
	cache, err := checkedSum(u.CacheReadTokens, u.CacheCreationTokens)
	if err != nil || cache > u.InputTokens || u.ReasoningTokens > u.OutputTokens {
		return errUsage
	}
	total, err := checkedSum(u.InputTokens, u.OutputTokens)
	if err != nil || u.TotalTokens != total {
		return errUsage
	}
	return nil
}

// Native counters are interpreted transactionally. A malformed snapshot cannot
// replace the last valid partial metrics, and absent required counters cannot
// become zero-price complete usage. Only the native usage subtree is retained.
func (p *usageParser) native(raw []byte, phase string) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		p.ready = false
		return
	}
	fields, err := strictObject(raw)
	p.usage.Complete = false
	p.ready = false
	if err != nil {
		p.invalid = true
		return
	}
	known := false
	for _, name := range []string{"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "promptTokenCount", "candidatesTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "toolUsePromptTokenCount", "totalTokenCount"} {
		_, present, e := count(fields, name)
		known = known || present
		if e != nil {
			p.invalid = true
			return
		}
	}
	if !known {
		return
	}
	next := p.usage
	next.Complete = false
	next.RawJSON = string(raw)
	ready := false
	switch p.endpoint {
	case "anthropic":
		base, hasInput, _ := count(fields, "input_tokens")
		output, hasOutput, _ := count(fields, "output_tokens")
		if phase == "message_delta" && hasOutput && p.anthropicOutputSeen && output < p.usage.OutputTokens {
			p.invalid = true
			return
		}
		read, hasRead, _ := count(fields, "cache_read_input_tokens")
		creation, hasCreation, _ := count(fields, "cache_creation_input_tokens")
		if !hasRead {
			read = next.CacheReadTokens
		}
		if !hasCreation {
			creation = next.CacheCreationTokens
		}
		if !hasInput {
			base = p.anthropicInput
		}
		if hasOutput {
			next.OutputTokens = output
		}
		next.CacheReadTokens = read
		next.CacheCreationTokens = creation
		next.InputTokens, err = checkedSum(base, read, creation)
		if err != nil {
			p.invalid = true
			return
		}
		if rawBreakdown, ok := fields["cache_creation"]; ok {
			breakdown, e := strictObject(rawBreakdown)
			if e != nil {
				p.invalid = true
				return
			}
			short, _, e1 := count(breakdown, "ephemeral_5m_input_tokens")
			long, _, e2 := count(breakdown, "ephemeral_1h_input_tokens")
			sum, e3 := checkedSum(short, long)
			if e1 != nil || e2 != nil || e3 != nil || sum > creation {
				p.invalid = true
				return
			}
		}
		next.TotalTokens, err = checkedSum(next.InputTokens, next.OutputTokens)
		if err != nil || conserved(fields, "total_tokens", next.TotalTokens) != nil || validCanonical(next) != nil {
			p.invalid = true
			return
		}
		if hasInput {
			p.anthropicInput = base
			p.anthropicInputSeen = true
		}
		if hasOutput {
			p.anthropicOutputSeen = true
			if phase == "message_delta" {
				p.anthropicFinalOutputSeen = true
			}
		}
		p.anthropic[phase] = append(json.RawMessage(nil), raw...)
		if len(p.anthropic) > 1 {
			combined, _ := json.Marshal(p.anthropic)
			next.RawJSON = string(combined)
		}
		ready = p.anthropicInputSeen && p.anthropicOutputSeen
		if phase != "json" {
			ready = p.anthropicInputSeen && p.anthropicFinalOutputSeen
		}
	case "gemini":
		input, hasInput, _ := count(fields, "promptTokenCount")
		output, hasOutput, _ := count(fields, "candidatesTokenCount")
		thoughts, _, _ := count(fields, "thoughtsTokenCount")
		tool, _, _ := count(fields, "toolUsePromptTokenCount")
		cache, _, _ := count(fields, "cachedContentTokenCount")
		total, hasTotal, _ := count(fields, "totalTokenCount")
		baseTotal, e := checkedSum(input, output, thoughts)
		if e != nil {
			p.invalid = true
			return
		}
		separateTotal, separateErr := checkedSum(baseTotal, tool)
		if hasTotal {
			if total == baseTotal {
			} else if separateErr == nil && total == separateTotal {
				input, err = checkedSum(input, tool)
				if err != nil {
					p.invalid = true
					return
				}
			} else {
				p.invalid = true
				return
			}
			ready = hasInput && hasOutput
		} else {
			ready = hasInput && hasOutput && tool == 0
		}
		next.InputTokens = input
		next.OutputTokens, err = checkedSum(output, thoughts)
		next.ReasoningTokens = thoughts
		next.CacheReadTokens = cache
		next.CacheCreationTokens = 0
		if err != nil {
			p.invalid = true
			return
		}
		next.TotalTokens, err = checkedSum(next.InputTokens, next.OutputTokens)
		if err != nil || validCanonical(next) != nil {
			p.invalid = true
			return
		}
		if phase == "stream" && p.seen && (next.InputTokens < p.usage.InputTokens || next.OutputTokens < p.usage.OutputTokens || next.ReasoningTokens < p.usage.ReasoningTokens || next.CacheReadTokens < p.usage.CacheReadTokens) {
			p.invalid = true
			return
		}
		for _, item := range []struct {
			name  string
			limit int64
		}{{"promptTokensDetails", next.InputTokens}, {"cacheTokensDetails", cache}, {"candidatesTokensDetails", output}, {"toolUsePromptTokensDetails", tool}} {
			if data, ok := fields[item.name]; ok {
				var details []json.RawMessage
				if !bytes.Equal(bytes.TrimSpace(data), []byte("null")) && json.Unmarshal(data, &details) != nil {
					p.invalid = true
					return
				}
				sum := int64(0)
				for _, entry := range details {
					field, e := strictObject(entry)
					if e != nil {
						p.invalid = true
						return
					}
					n, _, e := count(field, "tokenCount")
					if e != nil {
						p.invalid = true
						return
					}
					sum, e = checkedSum(sum, n)
					if e != nil {
						p.invalid = true
						return
					}
				}
				if sum > item.limit {
					p.invalid = true
					return
				}
			}
		}
	case "embeddings", "jina-rerank":
		input, hasInput, _ := count(fields, "prompt_tokens")
		if !hasInput {
			input, hasInput, _ = count(fields, "input_tokens")
		}
		total, hasTotal, _ := count(fields, "total_tokens")
		if !hasInput && hasTotal {
			input = total
			hasInput = true
		}
		output, _, _ := count(fields, "output_tokens")
		completion, _, _ := count(fields, "completion_tokens")
		if output != 0 || completion != 0 {
			p.invalid = true
			return
		}
		next.InputTokens = input
		next.OutputTokens = 0
		next.ReasoningTokens = 0
		next.CacheCreationTokens = 0
		next.TotalTokens = input
		detailName := "prompt_tokens_details"
		if _, has := fields["input_tokens"]; has {
			detailName = "input_tokens_details"
		}
		next.CacheReadTokens, err = detail(fields, detailName, "cached_tokens", input)
		if err != nil || conserved(fields, "total_tokens", input) != nil || validCanonical(next) != nil {
			p.invalid = true
			return
		}
		ready = hasInput
	default:
		inputName, outputName, inputDetail, outputDetail := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
		if p.endpoint == "openai-response" || p.endpoint == "image-generation" {
			inputName, outputName, inputDetail, outputDetail = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
		}
		input, hasInput, _ := count(fields, inputName)
		output, hasOutput, _ := count(fields, outputName)
		next.InputTokens = input
		next.OutputTokens = output
		next.CacheCreationTokens = 0
		next.CacheReadTokens, err = detail(fields, inputDetail, "cached_tokens", input)
		if err != nil {
			p.invalid = true
			return
		}
		next.ReasoningTokens, err = detail(fields, outputDetail, "reasoning_tokens", output)
		if err != nil {
			p.invalid = true
			return
		}
		next.TotalTokens, err = checkedSum(input, output)
		if err != nil || conserved(fields, "total_tokens", next.TotalTokens) != nil || validCanonical(next) != nil {
			p.invalid = true
			return
		}
		ready = hasInput && hasOutput
	}
	p.seen = true
	p.usage = next
	p.ready = ready
}
func (p *usageParser) json(raw []byte) {
	value := object(raw)
	if p.countTokens {
		if input, ok := value["input_tokens"]; ok {
			fields := map[string]json.RawMessage{"input_tokens": input}
			n, _, err := count(fields, "input_tokens")
			if err != nil {
				p.invalid = true
			} else {
				p.usage.InputTokens = n
				p.usage.OutputTokens = 0
				p.usage.TotalTokens = n
				p.usage.RawJSON = `{"input_tokens":` + string(input) + `}`
				p.seen = true
				p.ready = true
			}
		}
	} else if p.endpoint == "gemini" {
		p.native(value["usageMetadata"], "json")
	} else {
		p.native(value["usage"], "json")
	}
	if p.endpoint == "image-generation" {
		var images []json.RawMessage
		if json.Unmarshal(value["data"], &images) == nil && len(images) > 0 {
			p.usage.Images = int64(len(images))
			if _, published := value["usage"]; !published || bytes.Equal(bytes.TrimSpace(value["usage"]), []byte("null")) {
				p.seen = true
				p.ready = true
			}
		}
	}
	p.terminal = true
	p.usage.Complete = p.seen && p.ready && !p.invalid
}
func (p *usageParser) event(frame []byte) (string, error) {
	var data []byte
	for _, line := range bytes.Split(frame, []byte{'\n'}) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			part := bytes.TrimPrefix(line, []byte("data:"))
			part = bytes.TrimPrefix(part, []byte{' '})
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, part...)
		}
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		p.terminal = true
		p.usage.Complete = p.seen && p.ready && !p.invalid
		return "", nil
	}
	value := object(data)
	if _, hasError := value["error"]; hasError {
		p.failed = true
	}
	var kind string
	json.Unmarshal(value["type"], &kind)
	var id string
	switch p.endpoint {
	case "openai-response":
		response := object(value["response"])
		json.Unmarshal(response["id"], &id)
		switch kind {
		case "response.completed", "response.failed", "response.incomplete":
			p.native(response["usage"], kind)
			p.terminal = true
			p.failed = kind == "response.failed"
			p.usage.Complete = p.seen && p.ready && !p.invalid
		}
	case "anthropic":
		switch kind {
		case "message_start":
			p.native(object(value["message"])["usage"], kind)
		case "message_delta":
			p.native(value["usage"], kind)
		case "message_stop":
			p.terminal = true
			p.usage.Complete = p.seen && p.ready && !p.invalid && p.anthropicInputSeen && p.anthropicFinalOutputSeen
		case "error":
			p.failed = true
		}
	case "gemini":
		p.native(value["usageMetadata"], "stream")
		var candidates []map[string]json.RawMessage
		if json.Unmarshal(value["candidates"], &candidates) == nil {
			for _, candidate := range candidates {
				var reason string
				if json.Unmarshal(candidate["finishReason"], &reason) == nil && reason != "" {
					p.terminal = true
				}
			}
		}
	default:
		if usage, exists := value["usage"]; exists && !bytes.Equal(bytes.TrimSpace(usage), []byte("null")) {
			p.native(usage, "stream")
		}
	}
	return id, nil
}
func (p *usageParser) finish(clean bool) core.Usage {
	if p.endpoint == "gemini" && clean && p.seen && p.ready && p.terminal && !p.invalid {
		p.usage.Complete = true
	}
	return p.usage
}
