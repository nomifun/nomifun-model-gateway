// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

type NativeRequest struct {
	Model                         string
	Endpoint                      v1.Endpoint
	Task                          v1.Task
	Stream                        bool
	MaxOutput                     int64
	Count                         int64
	PreviousResponseID, SessionID string
	Body                          []byte
	ContentType                   string
	Multipart                     bool
}

// EstimateUsage is shared by financial and rate admission. Stored Responses
// history and opaque media are not measurable from the current body's size,
// so those requests reserve the operator's declared full context capacity.
// Plain text retains a byte estimate, not a universal tokenizer guarantee.
// Actual billing uses confirmed native usage.
func EstimateUsage(n NativeRequest, m v1.Model) (core.Usage, error) {
	if n.Count < 1 || n.Count > 16 {
		return core.Usage{}, &Error{400, "invalid_request_error", "n must be an integer from 1 through 16."}
	}
	u := core.Usage{Requests: 1, InputTokens: int64(len(n.Body)), OutputTokens: n.MaxOutput}
	if n.Task == "chat" && ((n.Endpoint == "openai-response" && n.PreviousResponseID != "") || hasOpaqueInput(n.Body)) {
		if m.ContextWindow == nil || *m.ContextWindow <= 0 {
			return core.Usage{}, &Error{400, "missing_context_bound", "Opaque or stored model input requires a configured positive model context window for safe reservation."}
		}
		u.InputTokens = *m.ContextWindow
	}
	if n.Endpoint == "openai" {
		if n.MaxOutput > math.MaxInt64/n.Count {
			return core.Usage{}, &Error{400, "invalid_request_error", "Aggregate completion limit exceeds the supported integer range."}
		}
		u.OutputTokens = n.MaxOutput * n.Count
	}
	if n.Task == "image_generation" || n.Task == "image_edit" {
		u.InputTokens = 0
		u.OutputTokens = 0
		u.Images = n.Count
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.InputTokens > math.MaxInt64-u.OutputTokens {
		return core.Usage{}, &Error{400, "invalid_request_error", "Aggregate token limit exceeds the supported integer range."}
	}
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return u, nil
}

// hasOpaqueInput examines actual native input containers, never tool schemas.
// It is an admission classifier only: the original request remains raw native
// JSON. Plain text keeps the byte estimate; unknown or externally represented
// content needs the operator's accurately configured full model context bound.
func hasOpaqueInput(body []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return false
	}
	for _, name := range []string{"cachedContent", "cached_content", "conversation", "state"} {
		if value, ok := root[name]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return true
		}
	}
	for _, name := range []string{"messages", "content", "input", "contents", "system"} {
		if value, ok := root[name]; ok && opaquePart(value) {
			return true
		}
	}
	return false
}
func opaquePart(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '"' {
		return false
	}
	if trimmed[0] == '[' {
		var parts []json.RawMessage
		if json.Unmarshal(trimmed, &parts) != nil {
			return true
		}
		for _, part := range parts {
			if opaquePart(part) {
				return true
			}
		}
		return false
	}
	if trimmed[0] != '{' {
		return true
	}
	var part map[string]json.RawMessage
	if json.Unmarshal(trimmed, &part) != nil {
		return true
	}
	for _, name := range []string{"image_url", "input_image", "input_file", "file_data", "file_id", "file_url", "inlineData", "inline_data", "fileData", "input_audio", "audio", "audio_url", "video", "video_url", "image", "data", "base64", "b64_json", "mimeType", "mime_type", "thoughtSignature", "thought_signature", "encrypted_content", "signature"} {
		if _, ok := part[name]; ok {
			return true
		}
	}
	var kind string
	if value, ok := part["type"]; ok {
		if json.Unmarshal(value, &kind) != nil {
			return true
		}
		switch kind {
		case "text", "input_text", "output_text", "message":
		default:
			return true
		}
	}
	known := false
	for _, name := range []string{"content", "parts", "contents", "messages"} {
		if value, ok := part[name]; ok {
			known = true
			if opaquePart(value) {
				return true
			}
		}
	}
	if value, ok := part["text"]; ok {
		known = true
		var text string
		if json.Unmarshal(value, &text) != nil {
			return true
		}
	}
	if kind == "message" && known {
		return false
	}
	// A message container is recognized by its native role and content/parts.
	if _, role := part["role"]; role && known {
		return false
	}
	if known {
		return false
	}
	// Unrecognized actual-input objects could contain external data or native
	// protocol extensions. Their token cost is opaque even if their JSON is tiny.
	return true
}

func Route(path string) (v1.Endpoint, v1.Task, bool) {
	switch path {
	case "/v1/chat/completions":
		return "openai", "chat", true
	case "/v1/responses":
		return "openai-response", "chat", true
	case "/v1/messages", "/v1/messages/count_tokens":
		return "anthropic", "chat", true
	case "/v1/embeddings":
		return "embeddings", "embedding", true
	case "/v1/images/generations":
		return "image-generation", "image_generation", true
	case "/v1/images/edits":
		return "image-generation", "image_edit", true
	case "/v1/rerank":
		return "jina-rerank", "rerank", true
	}
	if strings.HasPrefix(path, "/v1beta/models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")) {
		return "gemini", "chat", true
	}
	return "", "", false
}
func InspectRequest(r *http.Request, maxBytes int64) (NativeRequest, error) {
	n := NativeRequest{ContentType: r.Header.Get("Content-Type"), Count: 1}
	var ok bool
	n.Endpoint, n.Task, ok = Route(r.URL.Path)
	if !ok {
		return n, &Error{404, "not_found", "Unknown model endpoint."}
	}
	if r.Method != http.MethodPost {
		return n, &Error{405, "method_not_allowed", "Use POST for this endpoint."}
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	if len(r.Header.Values("X-NomiFun-Session-ID")) > 1 {
		return n, &Error{400, "invalid_request_error", "Ambiguous session ID."}
	}
	n.SessionID = r.Header.Get("X-NomiFun-Session-ID")
	if len(n.SessionID) > 256 || strings.ContainsAny(n.SessionID, "\r\n") {
		return n, &Error{400, "invalid_request_error", "Invalid session ID."}
	}
	if n.Endpoint != "anthropic" {
		n.SessionID = ""
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return n, &Error{400, "invalid_request_error", "Unable to read request."}
	}
	if int64(len(body)) > maxBytes {
		return n, &Error{413, "request_too_large", "Request exceeds the configured body limit."}
	}
	n.Body = body
	r.Body = io.NopCloser(bytes.NewReader(body))
	mediatype, params, err := mime.ParseMediaType(n.ContentType)
	if err != nil {
		return n, &Error{415, "unsupported_media_type", "Provide a native request Content-Type."}
	}
	if r.URL.Path == "/v1/images/edits" {
		if mediatype != "multipart/form-data" || params["boundary"] == "" {
			return n, &Error{415, "unsupported_media_type", "Image edits require multipart/form-data."}
		}
		n.Multipart = true
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		found := false
		countFound := false
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				return n, &Error{400, "invalid_request_error", "Invalid multipart request."}
			}
			if part.FormName() == "model" {
				if found {
					return n, &Error{400, "invalid_request_error", "Duplicate model."}
				}
				value, e := io.ReadAll(io.LimitReader(part, 1001))
				if e != nil || len(value) > 1000 {
					return n, &Error{400, "invalid_request_error", "Invalid model."}
				}
				n.Model = string(value)
				found = true
			}
			if part.FormName() == "n" {
				if countFound {
					return n, &Error{400, "invalid_request_error", "Duplicate image count."}
				}
				countFound = true
				value, e := io.ReadAll(io.LimitReader(part, 32))
				if e != nil {
					return n, &Error{400, "invalid_request_error", "Invalid image count."}
				}
				if json.Unmarshal(value, &n.Count) != nil || n.Count < 1 || n.Count > 16 {
					return n, &Error{400, "invalid_request_error", "n must be an integer from 1 through 16."}
				}
			}
			part.Close()
		}
		if n.Model == "" {
			return n, &Error{400, "invalid_request_error", "A model is required."}
		}
		return n, nil
	}
	if mediatype != "application/json" {
		return n, &Error{415, "unsupported_media_type", "Use application/json."}
	}
	fields, err := objectFields(body)
	if err != nil {
		return n, &Error{400, "invalid_request_error", "Request must be an unambiguous JSON object."}
	}
	if n.Endpoint == "gemini" {
		suffix := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
		n.Model = strings.TrimSuffix(strings.TrimSuffix(suffix, ":streamGenerateContent"), ":generateContent")
		if n.Model == "" || strings.Contains(n.Model, "/") {
			return n, &Error{400, "invalid_request_error", "Invalid Gemini model path."}
		}
		n.Stream = strings.HasSuffix(r.URL.Path, ":streamGenerateContent")
		if alt := r.URL.Query().Get("alt"); alt != "" && alt != "sse" {
			return n, &Error{400, "invalid_request_error", "Gemini streaming supports alt=sse."}
		}
	} else {
		if err := decodeStringField(body, fields, "model", &n.Model); err != nil || n.Model == "" {
			return n, &Error{400, "invalid_request_error", "A string model is required."}
		}
		if span, ok := fields["stream"]; ok {
			if json.Unmarshal(body[span.start:span.end], &n.Stream) != nil {
				return n, &Error{400, "invalid_request_error", "stream must be boolean."}
			}
		}
	}
	if span, ok := fields["previous_response_id"]; ok && n.Endpoint == "openai-response" {
		if json.Unmarshal(body[span.start:span.end], &n.PreviousResponseID) != nil {
			return n, &Error{400, "invalid_request_error", "previous_response_id must be string or null."}
		}
		if len(n.PreviousResponseID) > 300 {
			return n, &Error{400, "invalid_previous_response_id", "Invalid previous response ID."}
		}
	}
	outputFields := []string{}
	switch n.Endpoint {
	case "openai":
		outputFields = []string{"max_tokens", "max_completion_tokens"}
	case "openai-response":
		outputFields = []string{"max_output_tokens"}
	case "anthropic":
		outputFields = []string{"max_tokens"}
	}
	for _, name := range outputFields {
		if span, ok := fields[name]; ok {
			var value int64
			if json.Unmarshal(body[span.start:span.end], &value) != nil || value < 1 {
				return n, &Error{400, "invalid_request_error", name + " must be a positive integer."}
			}
			if value > n.MaxOutput {
				n.MaxOutput = value
			}
		}
	}
	if span, ok := fields["generationConfig"]; ok && n.Endpoint == "gemini" {
		var config struct {
			MaxOutputTokens int64 `json:"maxOutputTokens"`
		}
		if json.Unmarshal(body[span.start:span.end], &config) != nil {
			return n, &Error{400, "invalid_request_error", "Invalid generationConfig."}
		}
		n.MaxOutput = config.MaxOutputTokens
	}
	if n.Task == "chat" && r.URL.Path != "/v1/messages/count_tokens" && n.MaxOutput < 1 {
		return n, &Error{400, "invalid_request_error", "Provide an explicit positive native output-token limit for metered generation."}
	}
	if n.Endpoint == "openai" || n.Endpoint == "image-generation" {
		if value, ok := fields["n"]; ok {
			if json.Unmarshal(body[value.start:value.end], &n.Count) != nil || n.Count < 1 || n.Count > 16 {
				return n, &Error{400, "invalid_request_error", "n must be an integer from 1 through 16."}
			}
		}
	}
	if n.Endpoint == "openai" && n.Stream {
		if value, ok := fields["stream_options"]; ok {
			options, e := objectFields(body[value.start:value.end])
			if e != nil {
				return n, &Error{400, "invalid_request_error", "stream_options must be an object."}
			}
			if usage, explicit := options["include_usage"]; explicit {
				var included bool
				raw := body[value.start:value.end]
				if json.Unmarshal(raw[usage.start:usage.end], &included) != nil || !included {
					return n, &Error{400, "invalid_request_error", "Metered Chat streams require stream_options.include_usage: true."}
				}
			}
		}
	}
	return n, nil
}

type span struct{ start, end int }

// Raw spans retain unknown values byte for byte; a model alias and an absent
// Chat usage option are the only gateway changes to native JSON.
func objectFields(body []byte) (map[string]span, error) {
	if !json.Valid(body) {
		return nil, io.ErrUnexpectedEOF
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, io.ErrUnexpectedEOF
	}
	result := map[string]span{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return nil, e
		}
		name, ok := key.(string)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		if _, duplicate := result[name]; duplicate {
			return nil, io.ErrUnexpectedEOF
		}
		offset := int(d.InputOffset())
		for offset < len(body) && (body[offset] == ':' || body[offset] == ' ' || body[offset] == '\n' || body[offset] == '\r' || body[offset] == '\t') {
			offset++
		}
		var raw json.RawMessage
		if e = d.Decode(&raw); e != nil {
			return nil, e
		}
		result[name] = span{offset, int(d.InputOffset())}
	}
	return result, nil
}
func decodeStringField(body []byte, fields map[string]span, key string, dest *string) error {
	value, ok := fields[key]
	if !ok {
		return io.ErrUnexpectedEOF
	}
	return json.Unmarshal(body[value.start:value.end], dest)
}
func replaceField(body []byte, key string, value []byte) ([]byte, error) {
	fields, err := objectFields(body)
	if err != nil {
		return nil, err
	}
	if s, ok := fields[key]; ok {
		output := append([]byte(nil), body[:s.start]...)
		output = append(output, value...)
		return append(output, body[s.end:]...), nil
	}
	end := bytes.LastIndexByte(body, '}')
	prefix := append([]byte(nil), body[:end]...)
	if len(fields) > 0 {
		prefix = append(prefix, ',')
	}
	name, _ := json.Marshal(key)
	prefix = append(prefix, name...)
	prefix = append(prefix, ':')
	prefix = append(prefix, value...)
	return append(prefix, body[end:]...), nil
}
func rewriteBody(n NativeRequest, model string) ([]byte, string, error) {
	if n.Multipart {
		_, p, _ := mime.ParseMediaType(n.ContentType)
		reader := multipart.NewReader(bytes.NewReader(n.Body), p["boundary"])
		var out bytes.Buffer
		writer := multipart.NewWriter(&out)
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, "", e
			}
			dst, e := writer.CreatePart(part.Header)
			if e != nil {
				return nil, "", e
			}
			if part.FormName() == "model" {
				_, e = io.WriteString(dst, model)
			} else {
				_, e = io.Copy(dst, part)
			}
			part.Close()
			if e != nil {
				return nil, "", e
			}
		}
		if e := writer.Close(); e != nil {
			return nil, "", e
		}
		return out.Bytes(), writer.FormDataContentType(), nil
	}
	body := n.Body
	if n.Endpoint != "gemini" {
		value, _ := json.Marshal(model)
		var err error
		body, err = replaceField(body, "model", value)
		if err != nil {
			return nil, "", err
		}
	}
	if n.Endpoint == "openai" && n.Stream {
		fields, e := objectFields(body)
		if e != nil {
			return nil, "", e
		}
		if s, ok := fields["stream_options"]; ok {
			options := body[s.start:s.end]
			members, e := objectFields(options)
			if e != nil {
				return nil, "", e
			}
			if _, explicit := members["include_usage"]; !explicit {
				options, e = replaceField(options, "include_usage", []byte("true"))
				if e != nil {
					return nil, "", e
				}
				body, e = replaceField(body, "stream_options", options)
				if e != nil {
					return nil, "", e
				}
			}
		} else {
			var e error
			body, e = replaceField(body, "stream_options", []byte(`{"include_usage":true}`))
			if e != nil {
				return nil, "", e
			}
		}
	}
	return body, n.ContentType, nil
}

func upstreamURL(base string, n NativeRequest, path, model, kind, version string) (*url.URL, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &Error{502, "upstream_unavailable", "Invalid upstream address."}
	}
	root := strings.TrimSuffix(u.Path, "/")
	if kind == "azure" {
		root = strings.TrimSuffix(strings.TrimSuffix(root, "/openai/v1"), "/openai")
		// Current Azure native v1 uses standard paths and api-key authentication.
		// Explicit dated versions retain the deployment-based legacy data plane.
		if version == "" || version == "v1" || version == "preview" {
			u.Path = root + "/openai" + path
			if version != "" {
				q := url.Values{}
				q.Set("api-version", version)
				u.RawQuery = q.Encode()
			}
			return u, nil
		}
		if n.Endpoint == "openai-response" {
			u.Path = root + "/openai/responses"
		} else {
			if strings.ContainsAny(model, "/\\?#") {
				return nil, &Error{502, "upstream_unavailable", "Invalid Azure deployment identifier."}
			}
			u.Path = root + "/openai/deployments/" + model + strings.TrimPrefix(path, "/v1")
		}
		q := url.Values{}
		q.Set("api-version", version)
		u.RawQuery = q.Encode()
	} else if n.Endpoint == "gemini" {
		if strings.HasSuffix(root, "/v1beta") {
			root = strings.TrimSuffix(root, "/v1beta")
		}
		operation := ":generateContent"
		if n.Stream {
			operation = ":streamGenerateContent"
			u.RawQuery = "alt=sse"
		}
		u.Path = root + "/v1beta/models/" + model + operation
	} else {
		if strings.HasSuffix(root, "/v1") {
			root = strings.TrimSuffix(root, "/v1")
		}
		u.Path = root + path
	}
	return u, nil
}
