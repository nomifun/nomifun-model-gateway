package conformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// Stream checks intentionally validate native structure rather than mock text
// or exact token counts. Error messages never contain response payloads.
func validateStream(endpoint string, body []byte) error {
	events, err := streamEvents(body)
	if err != nil {
		return err
	}
	switch endpoint {
	case "openai":
		return validateChatStream(events)
	case "openai-response":
		return validateResponsesStream(events)
	case "anthropic":
		return validateAnthropicStream(events)
	case "gemini":
		return validateGeminiStream(events)
	default:
		return errors.New("unsupported streaming endpoint")
	}
}

// validateStreamTool adds evidence that the forced conformance tool was emitted
// in the native protocol. Arguments must be an object, but values are arbitrary.
func validateStreamTool(endpoint string, body []byte) error {
	if err := validateStream(endpoint, body); err != nil {
		return err
	}
	events, _ := streamEvents(body)
	invalid := errors.New("stream lacks a complete native conformance tool call")
	tools := map[string]*streamTool{}
	fragmented := map[string]bool{}
	for _, event := range events {
		if bytes.Equal(event.data, []byte("[DONE]")) {
			continue
		}
		var value struct {
			Type     string              `json:"type"`
			Index    int                 `json:"index"`
			Item     *streamResponseItem `json:"item"`
			Response *streamResponse     `json:"response"`
			Choices  []struct {
				Index int `json:"index"`
				Delta struct {
					Tools []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Block *struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta      json.RawMessage `json:"delta"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Function *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(event.data, &value) != nil {
			return invalid
		}
		switch endpoint {
		case "openai":
			for _, choice := range value.Choices {
				for _, fragment := range choice.Delta.Tools {
					key := strconv.Itoa(choice.Index) + ":" + strconv.Itoa(fragment.Index)
					if tools[key] == nil {
						tools[key] = &streamTool{}
					}
					tool := tools[key]
					tool.id += fragment.ID
					tool.name += fragment.Function.Name
					tool.arguments += fragment.Function.Arguments
				}
			}
		case "openai-response":
			var items []streamResponseItem
			if value.Type == "response.output_item.done" && value.Item != nil {
				items = append(items, *value.Item)
			}
			if value.Type == "response.completed" && value.Response != nil {
				items = append(items, value.Response.Output...)
			}
			for _, item := range items {
				if item.Type == "function_call" {
					tools[item.ID] = &streamTool{id: item.CallID, name: item.Name, arguments: item.Arguments}
				}
			}
		case "anthropic":
			key := strconv.Itoa(value.Index)
			if value.Type == "content_block_start" && value.Block != nil && (value.Block.Type == "tool_use" || value.Block.Type == "server_tool_use") {
				tools[key] = &streamTool{id: value.Block.ID, name: value.Block.Name, arguments: string(value.Block.Input)}
			}
			var delta struct {
				PartialJSON string `json:"partial_json"`
			}
			if value.Type == "content_block_delta" && json.Unmarshal(value.Delta, &delta) == nil && delta.PartialJSON != "" && tools[key] != nil {
				// Native tool starts contain an empty input; deltas replace it.
				if !fragmented[key] {
					tools[key].arguments = ""
					fragmented[key] = true
				}
				tools[key].arguments += delta.PartialJSON
			}
		case "gemini":
			for _, candidate := range value.Candidates {
				for _, part := range candidate.Content.Parts {
					if part.Function != nil {
						tool := &streamTool{id: "native-function", name: part.Function.Name, arguments: string(part.Function.Args)}
						if tool.name == "conformance_echo" && tool.valid() {
							return nil
						}
					}
				}
			}
		}
	}
	for _, tool := range tools {
		if tool.name == "conformance_echo" && tool.valid() {
			return nil
		}
	}
	return invalid
}

type streamEvent struct {
	name string
	data []byte
}

func streamEvents(body []byte) ([]streamEvent, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	var events []streamEvent
	var name string
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				raw := []byte(strings.Join(data, "\n"))
				if !bytes.Equal(raw, []byte("[DONE]")) {
					var value map[string]json.RawMessage
					if json.Unmarshal(raw, &value) != nil || value == nil {
						return nil, errors.New("stream contains invalid JSON event")
					}
				}
				events = append(events, streamEvent{name: name, data: raw})
			}
			name, data = "", nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data = append(data, value)
		}
	}
	if scanner.Err() != nil {
		return nil, errors.New("stream event exceeds supported size")
	}
	// A record without its blank-line delimiter is a truncated stream, even
	// when its JSON payload happens to be syntactically complete.
	if len(data) != 0 || len(events) == 0 {
		return nil, errors.New("stream is empty or has incomplete SSE framing")
	}
	return events, nil
}

type streamTokenDetails struct {
	Cached    *int64 `json:"cached_tokens"`
	Reasoning *int64 `json:"reasoning_tokens"`
}

type streamOpenAIUsage struct {
	Prompt            *int64              `json:"prompt_tokens"`
	Completion        *int64              `json:"completion_tokens"`
	Input             *int64              `json:"input_tokens"`
	Output            *int64              `json:"output_tokens"`
	Total             *int64              `json:"total_tokens"`
	PromptDetails     *streamTokenDetails `json:"prompt_tokens_details"`
	OutputDetails     *streamTokenDetails `json:"output_tokens_details"`
	InputDetails      *streamTokenDetails `json:"input_tokens_details"`
	CompletionDetails *streamTokenDetails `json:"completion_tokens_details"`
}

func streamUsageOK(usage *streamOpenAIUsage, responses bool) bool {
	if usage == nil {
		return false
	}
	input, output := usage.Prompt, usage.Completion
	inputDetails, outputDetails := usage.PromptDetails, usage.CompletionDetails
	if responses {
		input, output = usage.Input, usage.Output
		inputDetails, outputDetails = usage.InputDetails, usage.OutputDetails
	}
	if !streamSumEquals(usage.Total, input, output) {
		return false
	}
	if inputDetails != nil && !streamWithin(inputDetails.Cached, *input) {
		return false
	}
	if outputDetails != nil && !streamWithin(outputDetails.Reasoning, *output) {
		return false
	}
	return true
}

func streamNonnegative(value *int64) bool { return value == nil || *value >= 0 }

func streamWithin(value *int64, total int64) bool {
	return value == nil || (*value >= 0 && *value <= total)
}

func streamSumEquals(total *int64, terms ...*int64) bool {
	if total == nil || *total < 0 {
		return false
	}
	var sum int64
	for _, term := range terms {
		if term == nil || *term < 0 || *term > math.MaxInt64-sum {
			return false
		}
		sum += *term
	}
	return sum == *total
}

type streamTool struct {
	id, name, arguments string
}

func (tool *streamTool) valid() bool {
	if tool.id == "" || tool.name == "" {
		return false
	}
	var input map[string]json.RawMessage
	return json.Unmarshal([]byte(tool.arguments), &input) == nil && input != nil
}

func validateChatStream(events []streamEvent) error {
	invalid := errors.New("invalid OpenAI Chat stream structure, usage or completion")
	var id string
	var text, usage, done bool
	finished := map[int]bool{}
	tools := map[string]*streamTool{}
	for _, event := range events {
		if done {
			return invalid
		}
		if bytes.Equal(event.data, []byte("[DONE]")) {
			done = true
			continue
		}
		var chunk struct {
			ID      string             `json:"id"`
			Object  string             `json:"object"`
			Model   string             `json:"model"`
			Usage   *streamOpenAIUsage `json:"usage"`
			Choices *[]struct {
				Index  *int    `json:"index"`
				Finish *string `json:"finish_reason"`
				Delta  struct {
					Content *string `json:"content"`
					Tools   []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(event.data, &chunk) != nil || chunk.Object != "chat.completion.chunk" || chunk.ID == "" || chunk.Model == "" || chunk.Choices == nil {
			return invalid
		}
		if id != "" && id != chunk.ID {
			return invalid
		}
		id = chunk.ID
		if usage {
			return invalid // The usage chunk is the last native chunk before [DONE].
		}
		for _, choice := range *chunk.Choices {
			if choice.Index == nil || *choice.Index < 0 || finished[*choice.Index] {
				return invalid
			}
			finished[*choice.Index] = false
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				text = true
			}
			for _, delta := range choice.Delta.Tools {
				if delta.Index == nil || *delta.Index < 0 || (delta.Type != "" && delta.Type != "function") {
					return invalid
				}
				key := strconv.Itoa(*choice.Index) + ":" + strconv.Itoa(*delta.Index)
				tool := tools[key]
				if tool == nil {
					tool = &streamTool{}
					tools[key] = tool
				}
				tool.id += delta.ID
				tool.name += delta.Function.Name
				tool.arguments += delta.Function.Arguments
			}
			if choice.Finish != nil {
				if *choice.Finish == "" {
					return invalid
				}
				finished[*choice.Index] = true
			}
		}
		if chunk.Usage != nil {
			if !streamUsageOK(chunk.Usage, false) {
				return invalid
			}
			usage = true
		}
	}
	if !done || !usage || len(finished) == 0 {
		return invalid
	}
	for _, ended := range finished {
		if !ended {
			return invalid
		}
	}
	for _, tool := range tools {
		if !tool.valid() {
			return invalid
		}
	}
	if !text && len(tools) == 0 {
		return invalid
	}
	return nil
}

type streamResponseItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type streamResponse struct {
	ID     string               `json:"id"`
	Status string               `json:"status"`
	Usage  *streamOpenAIUsage   `json:"usage"`
	Output []streamResponseItem `json:"output"`
}

func streamResponseOutputOK(items []streamResponseItem) bool {
	content := false
	for _, item := range items {
		if item.ID == "" || item.Type == "" {
			return false
		}
		if item.Type == "function_call" {
			if !(&streamTool{id: item.CallID, name: item.Name, arguments: item.Arguments}).valid() {
				return false
			}
			content = true
		}
		// Keep checking later items for malformed native tool calls.
		for _, part := range item.Content {
			if item.Type == "message" && part.Type == "output_text" && part.Text != "" {
				content = true
			}
		}
	}
	return content
}

func validateResponsesStream(events []streamEvent) error {
	invalid := errors.New("invalid Responses stream structure, usage or completion")
	var id string
	var created, completed, content bool
	var lastSequence int64 = -1
	items := map[string]bool{}
	closedText := map[string]bool{}
	closedArguments := map[string]bool{}
	for _, event := range events {
		if completed {
			return invalid
		}
		var value struct {
			Type         string              `json:"type"`
			Sequence     *int64              `json:"sequence_number"`
			Response     *streamResponse     `json:"response"`
			Item         *streamResponseItem `json:"item"`
			ItemID       string              `json:"item_id"`
			ContentIndex int                 `json:"content_index"`
			Delta        string              `json:"delta"`
			Text         string              `json:"text"`
			Arguments    string              `json:"arguments"`
		}
		if json.Unmarshal(event.data, &value) != nil || value.Type == "" || (event.name != "" && event.name != value.Type) {
			return invalid
		}
		if value.Sequence != nil {
			if *value.Sequence <= lastSequence {
				return invalid
			}
			lastSequence = *value.Sequence
		}
		if value.Type != "response.created" && !created {
			return invalid
		}
		switch value.Type {
		case "response.created":
			if created || value.Response == nil || value.Response.ID == "" || value.Response.Status != "in_progress" {
				return invalid
			}
			created, id = true, value.Response.ID
		case "response.in_progress":
			if value.Response == nil || value.Response.ID != id || value.Response.Status != "in_progress" {
				return invalid
			}
		case "response.output_item.added":
			if value.Item == nil || value.Item.ID == "" {
				return invalid
			}
			if _, exists := items[value.Item.ID]; exists {
				return invalid
			}
			items[value.Item.ID] = false
		case "response.output_item.done":
			if value.Item == nil || value.Item.ID == "" {
				return invalid
			}
			if closed, exists := items[value.Item.ID]; !exists || closed {
				return invalid
			}
			items[value.Item.ID] = true
			content = content || streamResponseOutputOK([]streamResponseItem{*value.Item})
		case "response.output_text.delta", "response.output_text.done":
			key := value.ItemID + ":" + strconv.Itoa(value.ContentIndex)
			if value.ItemID == "" || value.ContentIndex < 0 || closedText[key] {
				return invalid
			}
			if closed, exists := items[value.ItemID]; !exists || closed {
				return invalid
			}
			content = content || value.Delta != "" || value.Text != ""
			if value.Type == "response.output_text.done" {
				closedText[key] = true
			} else {
				closedText[key] = false
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if closed, exists := items[value.ItemID]; !exists || closed || closedArguments[value.ItemID] {
				return invalid
			}
			if value.Type == "response.function_call_arguments.done" {
				var arguments map[string]json.RawMessage
				if json.Unmarshal([]byte(value.Arguments), &arguments) != nil || arguments == nil {
					return invalid
				}
				closedArguments[value.ItemID] = true
			} else {
				closedArguments[value.ItemID] = false
			}
		case "response.completed":
			if value.Response == nil || value.Response.ID != id || value.Response.Status != "completed" || !streamUsageOK(value.Response.Usage, true) || !streamResponseOutputOK(value.Response.Output) {
				return invalid
			}
			for _, closed := range items {
				if !closed {
					return invalid
				}
			}
			for _, closed := range closedText {
				if !closed {
					return invalid
				}
			}
			for _, closed := range closedArguments {
				if !closed {
					return invalid
				}
			}
			content = content || streamResponseOutputOK(value.Response.Output)
			completed = true
		case "error", "response.failed", "response.incomplete":
			return invalid
		}
	}
	if !created || !completed || !content {
		return invalid
	}
	return nil
}

type streamAnthropicUsage struct {
	Input         *int64 `json:"input_tokens"`
	Output        *int64 `json:"output_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
	CacheDetails  *struct {
		Ephemeral5m *int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func streamAnthropicUsageOK(usage *streamAnthropicUsage, initial bool) bool {
	if usage == nil || usage.Output == nil || !streamNonnegative(usage.Input) || !streamNonnegative(usage.Output) || !streamNonnegative(usage.CacheCreation) || !streamNonnegative(usage.CacheRead) {
		return false
	}
	if initial && usage.Input == nil {
		return false
	}
	if usage.CacheDetails != nil {
		if !streamNonnegative(usage.CacheDetails.Ephemeral5m) || !streamNonnegative(usage.CacheDetails.Ephemeral1h) {
			return false
		}
		if usage.CacheCreation != nil && usage.CacheDetails.Ephemeral5m != nil && usage.CacheDetails.Ephemeral1h != nil && !streamSumEquals(usage.CacheCreation, usage.CacheDetails.Ephemeral5m, usage.CacheDetails.Ephemeral1h) {
			return false
		}
	}
	return true
}

type streamAnthropicBlock struct {
	typeName, text, signature string
	tool                      streamTool
	input                     json.RawMessage
	closed                    bool
}

func validateAnthropicStream(events []streamEvent) error {
	invalid := errors.New("invalid Anthropic stream structure, usage or completion")
	var started, delta, stopped, content, messageDelta bool
	var output int64
	blocks := map[int]*streamAnthropicBlock{}
	for _, event := range events {
		if stopped {
			return invalid
		}
		var value struct {
			Type    string                `json:"type"`
			Index   *int                  `json:"index"`
			Usage   *streamAnthropicUsage `json:"usage"`
			Message *struct {
				ID    string                `json:"id"`
				Type  string                `json:"type"`
				Role  string                `json:"role"`
				Model string                `json:"model"`
				Usage *streamAnthropicUsage `json:"usage"`
			} `json:"message"`
			Block *struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Thinking  string          `json:"thinking"`
				Signature string          `json:"signature"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Input     json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta *struct {
				Type        string  `json:"type"`
				Text        string  `json:"text"`
				Thinking    string  `json:"thinking"`
				Signature   string  `json:"signature"`
				PartialJSON string  `json:"partial_json"`
				StopReason  *string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal(event.data, &value) != nil || value.Type == "" || (event.name != "" && event.name != value.Type) {
			return invalid
		}
		if value.Type == "ping" {
			continue
		}
		if value.Type != "message_start" && !started {
			return invalid
		}
		switch value.Type {
		case "message_start":
			if started || value.Message == nil || value.Message.ID == "" || value.Message.Type != "message" || value.Message.Role != "assistant" || value.Message.Model == "" || !streamAnthropicUsageOK(value.Message.Usage, true) {
				return invalid
			}
			started, output = true, *value.Message.Usage.Output
		case "content_block_start":
			if messageDelta || value.Index == nil || *value.Index < 0 || value.Block == nil || value.Block.Type == "" || blocks[*value.Index] != nil {
				return invalid
			}
			blocks[*value.Index] = &streamAnthropicBlock{
				typeName: value.Block.Type, text: value.Block.Text + value.Block.Thinking, signature: value.Block.Signature,
				tool: streamTool{id: value.Block.ID, name: value.Block.Name}, input: value.Block.Input,
			}
		case "content_block_delta":
			if messageDelta || value.Index == nil || value.Delta == nil || blocks[*value.Index] == nil || blocks[*value.Index].closed {
				return invalid
			}
			block := blocks[*value.Index]
			switch value.Delta.Type {
			case "text_delta":
				if block.typeName != "text" {
					return invalid
				}
				block.text += value.Delta.Text
			case "thinking_delta":
				if block.typeName != "thinking" {
					return invalid
				}
				block.text += value.Delta.Thinking
			case "signature_delta":
				if block.typeName != "thinking" {
					return invalid
				}
				block.signature += value.Delta.Signature
			case "input_json_delta":
				if block.typeName != "tool_use" && block.typeName != "server_tool_use" {
					return invalid
				}
				block.tool.arguments += value.Delta.PartialJSON
			}
		case "content_block_stop":
			if messageDelta || value.Index == nil || blocks[*value.Index] == nil || blocks[*value.Index].closed {
				return invalid
			}
			block := blocks[*value.Index]
			block.closed = true
			switch block.typeName {
			case "text":
				content = content || block.text != ""
			case "thinking":
				if block.signature == "" {
					return invalid
				}
			case "tool_use", "server_tool_use":
				if block.tool.arguments == "" {
					block.tool.arguments = string(block.input)
				}
				if !block.tool.valid() {
					return invalid
				}
				content = true
			}
		case "message_delta":
			if value.Delta == nil || !streamAnthropicUsageOK(value.Usage, false) || *value.Usage.Output < output {
				return invalid
			}
			for _, block := range blocks {
				if !block.closed {
					return invalid
				}
			}
			output = *value.Usage.Output
			messageDelta = true
			if value.Delta.StopReason != nil && *value.Delta.StopReason != "" {
				delta = true
			}
		case "message_stop":
			if !delta {
				return invalid
			}
			stopped = true
		case "error":
			return invalid
		}
	}
	if !started || !delta || !stopped || !content {
		return invalid
	}
	return nil
}

func validateGeminiStream(events []streamEvent) error {
	invalid := errors.New("invalid Gemini stream structure, usage or completion")
	var content, usage bool
	finished := map[int]bool{}
	for _, event := range events {
		var value struct {
			Candidates []struct {
				Index   int    `json:"index"`
				Finish  string `json:"finishReason"`
				Content struct {
					Parts []struct {
						Text     string `json:"text"`
						Function *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			Usage *struct {
				Prompt     *int64 `json:"promptTokenCount"`
				Candidates *int64 `json:"candidatesTokenCount"`
				Total      *int64 `json:"totalTokenCount"`
				Cached     *int64 `json:"cachedContentTokenCount"`
				Thoughts   *int64 `json:"thoughtsTokenCount"`
				ToolPrompt *int64 `json:"toolUsePromptTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.Unmarshal(event.data, &value) != nil || (len(value.Candidates) == 0 && value.Usage == nil) {
			return invalid
		}
		for _, candidate := range value.Candidates {
			if candidate.Index < 0 {
				return invalid
			}
			wasFinished := finished[candidate.Index]
			finished[candidate.Index] = wasFinished || (candidate.Finish != "" && candidate.Finish != "FINISH_REASON_UNSPECIFIED")
			for _, part := range candidate.Content.Parts {
				if wasFinished && (part.Text != "" || part.Function != nil) {
					return invalid
				}
				content = content || part.Text != ""
				if part.Function != nil {
					var args map[string]json.RawMessage
					if part.Function.Name == "" || json.Unmarshal(part.Function.Args, &args) != nil || args == nil {
						return invalid
					}
					content = true
				}
			}
		}
		if value.Usage != nil {
			u := value.Usage
			zero := int64(0)
			thoughts, toolPrompt := u.Thoughts, u.ToolPrompt
			if thoughts == nil {
				thoughts = &zero
			}
			if toolPrompt == nil {
				toolPrompt = &zero
			}
			// Google native API and SDK descriptions differ on whether tool-use
			// prompt tokens are already included in promptTokenCount. Accept
			// either coherent accounting form; never add cached tokens again.
			coherent := streamSumEquals(u.Total, u.Prompt, u.Candidates, thoughts, toolPrompt)
			if !coherent && streamNonnegative(toolPrompt) {
				coherent = streamSumEquals(u.Total, u.Prompt, u.Candidates, thoughts)
			}
			if !coherent || !streamWithin(u.Cached, *u.Prompt) {
				return invalid
			}
			usage = true
		}
	}
	if !content || !usage || len(finished) == 0 {
		return invalid
	}
	for _, ended := range finished {
		if !ended {
			return invalid
		}
	}
	return nil
}
