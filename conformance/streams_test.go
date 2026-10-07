package conformance

import (
	"strings"
	"testing"
)

func testSSE(data ...string) string {
	var stream strings.Builder
	for _, value := range data {
		stream.WriteString("data: " + value + "\n\n")
	}
	return stream.String()
}

func testNamedSSE(event, value string) string {
	return "event: " + event + "\ndata: " + value + "\n\n"
}

func testChatStream() string {
	return testSSE(
		`{"id":"chat-1","object":"chat.completion.chunk","model":"test-chat","choices":[{"index":0,"delta":{"role":"assistant","content":"Any answer"},"finish_reason":null}]}`,
		`{"id":"chat-1","object":"chat.completion.chunk","model":"test-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chat-1","object":"chat.completion.chunk","model":"test-chat","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":1}}}`,
		`[DONE]`,
	)
}

func testResponsesStream() string {
	return testNamedSSE("response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","status":"in_progress"}}`) +
		testNamedSSE("response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"item":{"id":"msg-1","type":"message","status":"in_progress","content":[]}}`) +
		testNamedSSE("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":2,"item_id":"msg-1","content_index":0,"delta":"Any answer"}`) +
		testNamedSSE("response.output_text.done", `{"type":"response.output_text.done","sequence_number":3,"item_id":"msg-1","content_index":0,"text":"Any answer"}`) +
		testNamedSSE("response.output_item.done", `{"type":"response.output_item.done","sequence_number":4,"item":{"id":"msg-1","type":"message","status":"completed","content":[{"type":"output_text","text":"Any answer"}]}}`) +
		testNamedSSE("response.completed", `{"type":"response.completed","sequence_number":5,"response":{"id":"resp-1","status":"completed","output":[{"id":"msg-1","type":"message","content":[{"type":"output_text","text":"Any answer"}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":3}}}}`)
}

func testAnthropicStream() string {
	return testNamedSSE("message_start", `{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"test-claude","content":[],"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":8,"cache_read_input_tokens":12,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":3}}}}`) +
		testNamedSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		testNamedSSE("ping", `{"type":"ping"}`) +
		testNamedSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Any answer"}}`) +
		testNamedSSE("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		testNamedSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`) +
		testNamedSSE("message_stop", `{"type":"message_stop"}`)
}

func testGeminiStream() string {
	return testSSE(
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Any "}]}}]}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":20,"cachedContentTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":2}}`,
	)
}

func TestValidateNativeStreams(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		body     string
	}{
		{"openai", testChatStream()},
		{"openai-response", testResponsesStream()},
		{"anthropic", testAnthropicStream()},
		{"gemini", testGeminiStream()},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			if err := validateStream(test.endpoint, []byte(test.body)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateToolStreams(t *testing.T) {
	chat := testSSE(
		`{"id":"tool-chat","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":"}}]}}]}`,
		`{"id":"tool-chat","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"id":"tool-chat","object":"chat.completion.chunk","model":"test","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`, `[DONE]`,
	)
	responses := testNamedSSE("response.created", `{"type":"response.created","response":{"id":"tool-resp","status":"in_progress"}}`) +
		testNamedSSE("response.output_item.added", `{"type":"response.output_item.added","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"weather","arguments":""}}`) +
		testNamedSSE("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc-1","delta":"{}"}`) +
		testNamedSSE("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc-1","arguments":"{}"}`) +
		testNamedSSE("response.output_item.done", `{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"weather","arguments":"{}"}}`) +
		testNamedSSE("response.completed", `{"type":"response.completed","response":{"id":"tool-resp","status":"completed","output":[{"id":"fc-1","type":"function_call","call_id":"call-1","name":"weather","arguments":"{}"}],"usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10}}}`)
	anthropic := testNamedSSE("message_start", `{"type":"message_start","message":{"id":"tool-msg","type":"message","role":"assistant","model":"test","usage":{"input_tokens":4,"output_tokens":1}}}`) +
		testNamedSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"weather","input":{}}}`) +
		testNamedSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`) +
		testNamedSSE("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		testNamedSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":6}}`) +
		testNamedSSE("message_stop", `{"type":"message_stop"}`)
	gemini := testSSE(`{"candidates":[{"index":0,"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10}}`)
	for _, test := range []struct {
		endpoint string
		body     string
	}{
		{"openai", chat}, {"openai-response", responses}, {"anthropic", anthropic}, {"gemini", gemini},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			if err := validateStream(test.endpoint, []byte(test.body)); err != nil {
				t.Fatal(err)
			}
			if err := validateStreamTool(test.endpoint, []byte(strings.ReplaceAll(test.body, "weather", "conformance_echo"))); err != nil {
				t.Fatal(err)
			}
			if err := validateStreamTool(test.endpoint, []byte(test.body)); err == nil {
				t.Fatal("wrong forced tool name accepted")
			}
			invalidArgs := test.body
			switch test.endpoint {
			case "openai", "anthropic":
				invalidArgs = strings.ReplaceAll(invalidArgs, `\"Paris\"}`, `\"Paris\"`)
			case "openai-response":
				invalidArgs = strings.ReplaceAll(invalidArgs, `"arguments":"{}"`, `"arguments":"[]"`)
			case "gemini":
				invalidArgs = strings.ReplaceAll(invalidArgs, `"args":{"city":"Paris"}`, `"args":[]`)
			}
			if err := validateStream(test.endpoint, []byte(invalidArgs)); err == nil {
				t.Fatal("malformed or non-object native tool arguments accepted")
			}
		})
	}
}

func TestStreamRejectsInvalidNativeEvidence(t *testing.T) {
	chat, responses, anthropic, gemini := testChatStream(), testResponsesStream(), testAnthropicStream(), testGeminiStream()
	for _, test := range []struct {
		name, endpoint, body string
	}{
		{"chat missing done", "openai", strings.TrimSuffix(chat, "data: [DONE]\n\n")},
		{"chat after done", "openai", chat + testSSE(`{"secret":"sensitive-body"}`)},
		{"chat unfinished choice", "openai", strings.Replace(chat, `"finish_reason":"stop"`, `"finish_reason":null`, 1)},
		{"chat missing usage", "openai", strings.Replace(chat, `"usage":{`, `"other":{`, 1)},
		{"chat wrong total", "openai", strings.Replace(chat, `"total_tokens":13`, `"total_tokens":99`, 1)},
		{"chat negative usage", "openai", strings.Replace(chat, `"prompt_tokens":9`, `"prompt_tokens":-9`, 1)},
		{"chat fractional usage", "openai", strings.Replace(chat, `"prompt_tokens":9`, `"prompt_tokens":9.5`, 1)},
		{"chat overflowing usage", "openai", strings.Replace(chat, `"prompt_tokens":9`, `"prompt_tokens":9223372036854775807`, 1)},
		{"chat cached count exceeds input", "openai", strings.Replace(chat, `"cached_tokens":3`, `"cached_tokens":30`, 1)},
		{"chat changed identity", "openai", strings.Replace(chat, `"id":"chat-1"`, `"id":"different"`, 1)},
		{"responses missing created", "openai-response", responses[strings.Index(responses, "event: response.output_item.added"):]},
		{"responses no completed", "openai-response", responses[:strings.Index(responses, "event: response.completed")]},
		{"responses event mismatch", "openai-response", strings.Replace(responses, "event: response.created", "event: response.completed", 1)},
		{"responses sequence regression", "openai-response", strings.Replace(responses, `"sequence_number":3`, `"sequence_number":1`, 1)},
		{"responses missing text done", "openai-response", strings.Replace(responses, testNamedSSE("response.output_text.done", `{"type":"response.output_text.done","sequence_number":3,"item_id":"msg-1","content_index":0,"text":"Any answer"}`), "", 1)},
		{"responses incomplete item", "openai-response", strings.Replace(responses, `"type":"response.output_item.done"`, `"type":"extension.event"`, 1)},
		{"responses wrong usage", "openai-response", strings.Replace(responses, `"total_tokens":18`, `"total_tokens":11`, 1)},
		{"responses completed no output", "openai-response", strings.Replace(responses, `"output":[`, `"other":[`, 1)},
		{"responses reasoning exceeds output", "openai-response", strings.Replace(responses, `"reasoning_tokens":3`, `"reasoning_tokens":30`, 1)},
		{"anthropic missing stop", "anthropic", strings.TrimSuffix(anthropic, testNamedSSE("message_stop", `{"type":"message_stop"}`))},
		{"anthropic unclosed block", "anthropic", strings.Replace(anthropic, `"type":"content_block_stop"`, `"type":"extension.event"`, 1)},
		{"anthropic no stop reason", "anthropic", strings.Replace(anthropic, `"stop_reason":"end_turn"`, `"stop_reason":null`, 1)},
		{"anthropic missing input count", "anthropic", strings.Replace(anthropic, `"input_tokens":10`, `"other_tokens":10`, 1)},
		{"anthropic negative cache", "anthropic", strings.Replace(anthropic, `"cache_read_input_tokens":12`, `"cache_read_input_tokens":-12`, 1)},
		{"anthropic cache subtotal mismatch", "anthropic", strings.Replace(anthropic, `"ephemeral_1h_input_tokens":3`, `"ephemeral_1h_input_tokens":4`, 1)},
		{"anthropic usage regression", "anthropic", strings.Replace(anthropic, `"output_tokens":8`, `"output_tokens":0`, 1)},
		{"gemini missing finish", "gemini", strings.Replace(gemini, `"finishReason":"STOP"`, `"finishReason":"FINISH_REASON_UNSPECIFIED"`, 1)},
		{"gemini wrong usage", "gemini", strings.Replace(gemini, `"totalTokenCount":20`, `"totalTokenCount":15`, 1)},
		{"gemini negative thoughts", "gemini", strings.Replace(gemini, `"thoughtsTokenCount":3`, `"thoughtsTokenCount":-3`, 1)},
		{"gemini cache exceeds prompt", "gemini", strings.Replace(gemini, `"cachedContentTokenCount":2`, `"cachedContentTokenCount":20`, 1)},
		{"gemini empty candidates", "gemini", testSSE(`{"candidates":[],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10}}`)},
		{"gemini invalid tool args", "gemini", testSSE(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":[]}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateStream(test.endpoint, []byte(test.body)); err == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}

func TestGeminiNativeAccountingAndUsageTail(t *testing.T) {
	// toolUsePromptTokenCount may be reported separately or included in the
	// prompt count. Both published native accounting forms are accepted.
	body := strings.Replace(testGeminiStream(), `"totalTokenCount":20`, `"totalTokenCount":18`, 1)
	if err := validateStream("gemini", []byte(body)); err != nil {
		t.Fatal(err)
	}
	body = testSSE(`{"candidates":[{"index":0,"content":{"parts":[{"text":"Answer"}]},"finishReason":"STOP"}]}`,
		`{"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":4,"totalTokenCount":6}}`)
	if err := validateStream("gemini", []byte(body)); err != nil {
		t.Fatal(err)
	}
	body += testSSE(`{"candidates":[{"index":0,"content":{"parts":[{"text":"Unexpected"}]},"finishReason":"STOP"}]}`)
	if err := validateStream("gemini", []byte(body)); err == nil {
		t.Fatal("text after completed candidate accepted")
	}
}

func TestSSEFramingAndErrorRedaction(t *testing.T) {
	for _, endpoint := range []string{"openai", "openai-response", "anthropic", "gemini"} {
		for _, body := range []string{"", "data: {sensitive-body}\n\n", "data: []\n\n", "data: null\n\n", "data: {}\n"} {
			if err := validateStream(endpoint, []byte(body)); err == nil || strings.Contains(err.Error(), "sensitive-body") {
				t.Fatalf("%s invalid or unredacted failure: %v", endpoint, err)
			}
		}
	}
	if err := validateStream("unsupported", []byte(testChatStream())); err == nil {
		t.Fatal("unsupported endpoint accepted")
	}
	// Comments, CRLF and split data fields are standard SSE framing.
	body := ": heartbeat\r\nid: ignored\r\nretry: 1000\r\n" + strings.ReplaceAll(testChatStream(), "\n", "\r\n")
	if err := validateStream("openai", []byte(body)); err != nil {
		t.Fatal(err)
	}
	multiline := "event: response.created\ndata: {\ndata: \"type\":\"response.created\",\ndata: \"response\":{\"id\":\"resp-1\",\"status\":\"in_progress\"}\ndata: }\n\n"
	responses := testResponsesStream()
	body = multiline + responses[strings.Index(responses, "event: response.output_item.added"):]
	if err := validateStream("openai-response", []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicThinkingSignature(t *testing.T) {
	body := testNamedSSE("message_start", `{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"test","usage":{"input_tokens":1,"output_tokens":1}}}`) +
		testNamedSSE("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`) +
		testNamedSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Thoughts"}}`) +
		testNamedSSE("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`) +
		testNamedSSE("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		testNamedSSE("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"Answer"}}`) +
		testNamedSSE("content_block_stop", `{"type":"content_block_stop","index":1}`) +
		testNamedSSE("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`) +
		testNamedSSE("message_stop", `{"type":"message_stop"}`)
	if err := validateStream("anthropic", []byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := validateStream("anthropic", []byte(strings.Replace(body, `"signature":"opaque-signature"`, `"signature":""`, 1))); err == nil {
		t.Fatal("thinking content without native signature accepted")
	}
}
