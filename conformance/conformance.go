// SPDX-License-Identifier: Apache-2.0
// Package conformance tests protocol v1 through public HTTP interfaces only.
package conformance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

const (
	Pass = "PASS"
	Fail = "FAIL"
	Skip = "SKIP"
)

// Fixture selects actual models and optional synthetic test accounts. Optional
// accounts must be provisioned by an operator; the suite never creates them.
// RequestBodies overrides native JSON requests when a model requires specific
// parameters. The runner supplies model and stream fields after the override.
type Fixture struct {
	Models              map[string]string          `json:"models"`
	ErrorKeys           map[string]string          `json:"error_keys,omitempty"`
	LimitedKey          string                     `json:"limited_key,omitempty"`
	LimitedModels       []string                   `json:"limited_models,omitempty"`
	CrossKey            string                     `json:"cross_key,omitempty"`
	CountTokens         bool                       `json:"count_tokens,omitempty"`
	SyntheticAssertions bool                       `json:"synthetic_assertions,omitempty"`
	RequestBodies       map[string]json.RawMessage `json:"request_bodies,omitempty"`
}

type Config struct {
	BaseURL    string
	APIKey     string
	Fixture    Fixture
	HTTPClient *http.Client
}

type Result struct {
	Name   string
	Status string
	Detail string
}

func LoadFixture(r io.Reader) (Fixture, error) {
	var f Fixture
	d := json.NewDecoder(io.LimitReader(r, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil {
		return f, errors.New("invalid conformance fixture JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return f, errors.New("fixture must contain one JSON object")
	}
	if len(f.Models) == 0 {
		return f, errors.New("fixture models map is required")
	}
	for ep, model := range f.Models {
		if !knownEndpoint(ep) || strings.TrimSpace(model) == "" {
			return f, errors.New("fixture contains an unsupported endpoint or empty model ID")
		}
	}
	return f, nil
}

type runner struct {
	cfg     Config
	base    *url.URL
	client  *http.Client
	results []Result
}
type probe struct {
	name, path, endpoint string
	stream, multipart    bool
}
type response struct {
	status int
	header http.Header
	body   []byte
}

func knownEndpoint(s string) bool {
	switch s {
	case "openai", "openai-response", "anthropic", "gemini", "image-generation", "embeddings", "jina-rerank":
		return true
	}
	return false
}

// Run returns a result for every assertion and explicit SKIPs for fixture cases
// that cannot be exercised. Operational failures become sanitized FAIL results.
// It never includes URLs, credentials, request bodies or raw HTTP errors in output.
func Run(ctx context.Context, cfg Config) ([]Result, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base URL must be absolute HTTP(S), without credentials, query or fragment")
	}
	if strings.TrimSpace(cfg.APIKey) == "" || strings.ContainsAny(cfg.APIKey, "\r\n") {
		return nil, errors.New("API key is required and must be a valid header value")
	}
	if len(cfg.Fixture.Models) == 0 {
		return nil, errors.New("fixture models map is required")
	}
	for ep, model := range cfg.Fixture.Models {
		if !knownEndpoint(ep) || strings.TrimSpace(model) == "" {
			return nil, errors.New("invalid endpoint model selection")
		}
	}
	c := &http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		*c = *cfg.HTTPClient
	}
	// Redirects could disclose authorization to a different gateway or put the
	// Gemini query key into a redirect target. They are never followed.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r := &runner{cfg: cfg, base: u, client: c}
	r.control(ctx)
	r.fixtureCoverage(ctx)
	probes := r.probes()
	for _, p := range probes {
		r.authentication(ctx, p)
	}
	for _, p := range probes {
		if p.endpoint == "" {
			continue
		}
		res, err := r.call(ctx, p, cfg.APIKey, "bearer", nil, false)
		r.check("native/"+p.name, func() error {
			if err != nil {
				return err
			}
			if res.status != http.StatusOK {
				return errors.New("expected HTTP 200")
			}
			if r.cfg.Fixture.SyntheticAssertions {
				if e := validateSyntheticEvidence(p, res.body, false); e != nil {
					return e
				}
			}
			if p.stream {
				if !strings.HasPrefix(strings.ToLower(res.header.Get("Content-Type")), "text/event-stream") {
					return errors.New("expected event-stream content type")
				}
				return validateStream(p.endpoint, res.body)
			}
			return validateNative(p, res.body, false)
		})
	}
	r.tools(ctx)
	r.affinity(ctx)
	r.encryptedReplay(ctx)
	r.rerankDocuments(ctx)
	r.visibility(ctx)
	r.billing(ctx)
	return r.results, nil
}

func (r *runner) check(name string, f func() error) {
	err := f()
	detail := ""
	status := Pass
	if err != nil {
		status = Fail
		detail = err.Error()
	}
	r.results = append(r.results, Result{name, status, detail})
}
func (r *runner) skip(name, detail string) { r.results = append(r.results, Result{name, Skip, detail}) }

func (r *runner) probes() []probe {
	ps := []probe{{name: "catalog", path: "/nomifun/v1/catalog"}, {name: "account", path: "/nomifun/v1/account"}, {name: "models", path: "/v1/models"}}
	for _, ep := range []string{"openai", "openai-response", "anthropic", "gemini", "image-generation", "embeddings", "jina-rerank"} {
		model := r.cfg.Fixture.Models[ep]
		if model == "" {
			r.skip("native/"+ep, "model not configured for endpoint")
			continue
		}
		switch ep {
		case "openai":
			ps = append(ps, probe{"chat-json", "/v1/chat/completions", ep, false, false}, probe{"chat-stream", "/v1/chat/completions", ep, true, false})
		case "openai-response":
			ps = append(ps, probe{"responses-json", "/v1/responses", ep, false, false}, probe{"responses-stream", "/v1/responses", ep, true, false})
		case "anthropic":
			ps = append(ps, probe{"messages-json", "/v1/messages", ep, false, false}, probe{"messages-stream", "/v1/messages", ep, true, false})
			if r.cfg.Fixture.CountTokens {
				ps = append(ps, probe{"count-tokens", "/v1/messages/count_tokens", ep, false, false})
			} else {
				r.skip("native/count-tokens", "optional endpoint not enabled in fixture")
			}
		case "gemini":
			path := "/v1beta/models/" + url.PathEscape(model)
			ps = append(ps, probe{"gemini-json", path + ":generateContent", ep, false, false}, probe{"gemini-stream", path + ":streamGenerateContent", ep, true, false})
		case "image-generation":
			ps = append(ps, probe{"images-generations", "/v1/images/generations", ep, false, false}, probe{"images-edits", "/v1/images/edits", ep, false, true})
		case "embeddings":
			ps = append(ps, probe{"embeddings", "/v1/embeddings", ep, false, false})
		case "jina-rerank":
			ps = append(ps, probe{"rerank", "/v1/rerank", ep, false, false})
		}
	}
	return ps
}

func (r *runner) body(p probe, tools bool) (map[string]any, error) {
	model := r.cfg.Fixture.Models[p.endpoint]
	var b map[string]any
	switch p.endpoint {
	case "openai":
		b = map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Reply briefly with hello."}}, "max_tokens": 64}
	case "openai-response":
		b = map[string]any{"input": "Reply briefly with hello.", "max_output_tokens": 64, "store": true, "include": []string{"reasoning.encrypted_content"}}
	case "anthropic":
		b = map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Reply briefly with hello."}}, "max_tokens": 64}
	case "gemini":
		b = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply briefly with hello."}}}}, "generationConfig": map[string]any{"maxOutputTokens": 64}}
	case "image-generation":
		b = map[string]any{"prompt": "A small blue circle on white.", "n": 1, "size": "1024x1024", "response_format": "b64_json"}
	case "embeddings":
		b = map[string]any{"input": []string{"hello", "world"}}
	case "jina-rerank":
		b = map[string]any{"query": "hello", "documents": []string{"hello world", "unrelated"}, "top_n": 2}
	default:
		return nil, nil
	}
	if raw, ok := r.cfg.Fixture.RequestBodies[p.endpoint]; ok {
		if json.Unmarshal(raw, &b) != nil || b == nil {
			return nil, errors.New("invalid native request override")
		}
	}
	if p.endpoint != "gemini" {
		b["model"] = model
	}
	if p.endpoint == "openai" || p.endpoint == "openai-response" || p.endpoint == "anthropic" {
		b["stream"] = p.stream
	}
	if p.endpoint == "openai" && p.stream {
		b["stream_options"] = map[string]any{"include_usage": true}
	}
	if p.name == "count-tokens" {
		delete(b, "max_tokens")
		delete(b, "stream")
	}
	if tools {
		params := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}
		fn := map[string]any{"name": "conformance_echo", "description": "Echo a short value.", "parameters": params}
		switch p.endpoint {
		case "openai":
			b["tools"] = []any{map[string]any{"type": "function", "function": fn}}
			b["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "conformance_echo"}}
		case "openai-response":
			b["tools"] = []any{map[string]any{"type": "function", "name": "conformance_echo", "description": "Echo a short value.", "parameters": params}}
			b["tool_choice"] = map[string]any{"type": "function", "name": "conformance_echo"}
		case "anthropic":
			b["tools"] = []any{map[string]any{"name": "conformance_echo", "description": "Echo a short value.", "input_schema": params}}
			b["tool_choice"] = map[string]any{"type": "tool", "name": "conformance_echo"}
		case "gemini":
			b["tools"] = []any{map[string]any{"functionDeclarations": []any{fn}}}
			b["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []string{"conformance_echo"}}}
		}
	}
	return b, nil
}

func (r *runner) call(ctx context.Context, p probe, key, scheme string, headers http.Header, tools bool) (response, error) {
	b, err := r.body(p, tools)
	if err != nil {
		return response{}, err
	}
	return r.send(ctx, p, key, scheme, headers, b)
}

func (r *runner) send(ctx context.Context, p probe, key, scheme string, headers http.Header, b map[string]any) (response, error) {
	u := *r.base
	escaped := strings.TrimRight(r.base.EscapedPath(), "/") + p.path
	unescaped, e := url.PathUnescape(escaped)
	if e != nil {
		return response{}, errors.New("invalid endpoint path")
	}
	u.Path = unescaped
	u.RawPath = escaped
	u.RawQuery = ""
	q := u.Query()
	if p.endpoint == "gemini" && p.stream {
		q.Set("alt", "sse")
	}
	if scheme == "query" || scheme == "query-with-bearer" {
		q.Set("key", key)
	}
	u.RawQuery = q.Encode()
	method := http.MethodGet
	var data []byte
	contentType := "application/json"
	if p.endpoint != "" {
		method = http.MethodPost
		if p.multipart {
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			for field, value := range map[string]string{"model": r.cfg.Fixture.Models[p.endpoint], "prompt": "A small blue circle on white.", "n": "1", "size": "1024x1024", "response_format": "b64_json"} {
				if w.WriteField(field, value) != nil {
					return response{}, errors.New("could not construct multipart request")
				}
			}
			part, e := w.CreateFormFile("image", "fixture.png")
			if e != nil {
				return response{}, errors.New("could not construct multipart request")
			}
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aA1sAAAAASUVORK5CYII=")
			if _, e = part.Write(png); e != nil {
				return response{}, errors.New("could not construct multipart request")
			}
			if w.Close() != nil {
				return response{}, errors.New("could not construct multipart request")
			}
			data = buf.Bytes()
			contentType = w.FormDataContentType()
		} else {
			var e error
			data, e = json.Marshal(b)
			if e != nil {
				return response{}, errors.New("could not encode request")
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
	if err != nil {
		return response{}, errors.New("could not construct HTTP request")
	}
	req.Header.Set("Content-Type", contentType)
	if p.endpoint == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	switch scheme {
	case "bearer", "query-with-bearer":
		req.Header.Set("Authorization", "Bearer "+key)
	case "x-api-key":
		req.Header.Set("x-api-key", key)
	case "x-goog-api-key":
		req.Header.Set("x-goog-api-key", key)
	case "all":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
		req.Header.Set("x-goog-api-key", key)
	}
	for k, vs := range headers {
		req.Header[k] = append([]string(nil), vs...)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return response{}, errors.New("HTTP request failed (transport, cancellation or timeout)")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil {
		return response{}, errors.New("HTTP response could not be read")
	}
	if len(body) > 4<<20 {
		return response{}, errors.New("HTTP response exceeded test limit")
	}
	// A contract must not reflect the supplied credential anywhere in a body.
	secrets := []string{key, r.cfg.APIKey, r.cfg.Fixture.LimitedKey, r.cfg.Fixture.CrossKey}
	for _, k := range r.cfg.Fixture.ErrorKeys {
		secrets = append(secrets, k)
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if bytes.Contains(body, []byte(secret)) {
			return response{}, errors.New("response disclosed a supplied API key")
		}
		for _, values := range res.Header {
			for _, value := range values {
				if strings.Contains(value, secret) {
					return response{}, errors.New("response header disclosed a supplied API key")
				}
			}
		}
	}
	return response{res.StatusCode, res.Header.Clone(), body}, nil
}

func (r *runner) fixtureCoverage(ctx context.Context) {
	meta, me := r.call(ctx, probe{path: "/nomifun/v1/meta"}, "", "", nil, false)
	cat, ce := r.call(ctx, probe{path: "/nomifun/v1/catalog"}, r.cfg.APIKey, "bearer", nil, false)
	var m v1.Meta
	var c v1.Catalog
	if me != nil || ce != nil || meta.status != 200 || cat.status != 200 || validateMeta(meta.body) != nil || validateCatalog(cat.body) != nil {
		r.skip("fixture/capability-coverage", "requires valid meta and catalog")
		return
	}
	_ = json.Unmarshal(meta.body, &m)
	_ = json.Unmarshal(cat.body, &c)
	for _, ep := range m.Capabilities {
		r.check("fixture/advertised/"+string(ep), func() error {
			if r.cfg.Fixture.Models[string(ep)] == "" {
				return errors.New("advertised capability has no fixture model; coverage cannot be skipped")
			}
			return nil
		})
	}
	for ep, id := range r.cfg.Fixture.Models {
		r.check("fixture/model/"+ep, func() error {
			if !contains(m.Capabilities, v1.Endpoint(ep)) {
				return errors.New("fixture endpoint is not advertised")
			}
			for _, model := range c.Models {
				if model.ID != id {
					continue
				}
				for _, task := range model.TaskEndpoints {
					if contains(task.Endpoints, v1.Endpoint(ep)) {
						return nil
					}
				}
			}
			return errors.New("fixture model is not visible with the selected endpoint")
		})
	}
	r.check("contract/catalog-capabilities", func() error {
		for _, model := range c.Models {
			for _, task := range model.TaskEndpoints {
				for _, ep := range task.Endpoints {
					if !contains(m.Capabilities, ep) {
						return errors.New("catalog endpoint is absent from meta capabilities")
					}
				}
			}
		}
		return nil
	})
}

func (r *runner) authentication(ctx context.Context, p probe) {
	for _, scheme := range []string{"bearer", "x-api-key", "x-goog-api-key", "all"} {
		s := scheme
		r.check("auth/"+p.name+"/"+s, func() error {
			res, e := r.call(ctx, p, r.cfg.APIKey, s, nil, false)
			if e != nil {
				return e
			}
			if res.status != 200 {
				return errors.New("valid authentication was rejected")
			}
			return nil
		})
	}
	negative := []struct {
		name, scheme, key string
		headers           http.Header
	}{
		{"missing", "", "", nil},
		{"malformed", "", "", http.Header{"Authorization": []string{"Bearer"}}},
		{"conflict", "bearer", r.cfg.APIKey, http.Header{"X-Api-Key": []string{"nmg_conformance_invalid_key"}}},
		{"malformed-with-valid-key", "x-api-key", r.cfg.APIKey, http.Header{"Authorization": []string{"Basic invalid"}}},
		{"multiple-header-values", "", r.cfg.APIKey, http.Header{"X-Api-Key": []string{r.cfg.APIKey, r.cfg.APIKey}}},
	}
	for _, n := range negative {
		r.check("auth/"+p.name+"/"+n.name, func() error {
			res, e := r.call(ctx, p, n.key, n.scheme, n.headers, false)
			if e != nil {
				return e
			}
			if res.status != 401 {
				return errors.New("invalid authentication must return HTTP 401")
			}
			return validateNativeError(p.endpoint, res.body, "")
		})
	}
	r.check("auth/"+p.name+"/query-key", func() error {
		res, e := r.call(ctx, p, r.cfg.APIKey, "query", nil, false)
		if e != nil {
			return e
		}
		if p.endpoint == "gemini" {
			if res.status != 200 {
				return errors.New("Gemini query authentication was rejected")
			}
		} else {
			if res.status != 401 {
				return errors.New("query authentication accepted outside Gemini")
			}
		}
		return nil
	})
	if p.endpoint == "gemini" {
		r.check("auth/"+p.name+"/query-conflict", func() error {
			res, e := r.call(ctx, p, "nmg_conformance_invalid_key", "query-with-bearer", http.Header{"Authorization": []string{"Bearer " + r.cfg.APIKey}}, false)
			if e != nil {
				return e
			}
			if res.status != 401 {
				return errors.New("conflicting query authentication must return HTTP 401")
			}
			return nil
		})
	}
}

func (r *runner) control(ctx context.Context) {
	for _, item := range []struct {
		name, path string
		validate   func([]byte) error
	}{{"meta", "/nomifun/v1/meta", validateMeta}, {"catalog", "/nomifun/v1/catalog", validateCatalog}, {"account", "/nomifun/v1/account", validateAccount}} {
		p := probe{name: item.name, path: item.path}
		key := r.cfg.APIKey
		scheme := "bearer"
		if item.name == "meta" {
			key = ""
			scheme = ""
		}
		res, e := r.call(ctx, p, key, scheme, nil, false)
		r.check("contract/"+item.name, func() error {
			if e != nil {
				return e
			}
			if res.status != 200 {
				return errors.New("expected HTTP 200")
			}
			if !strings.HasPrefix(strings.ToLower(res.header.Get("Content-Type")), "application/json") {
				return errors.New("expected JSON content type")
			}
			return item.validate(res.body)
		})
	}
	r.check("auth/meta/query-key", func() error {
		res, e := r.call(ctx, probe{path: "/nomifun/v1/meta"}, r.cfg.APIKey, "query", nil, false)
		if e != nil {
			return e
		}
		if res.status != 401 {
			return errors.New("query key outside Gemini must return HTTP 401")
		}
		return nil
	})
}

func (r *runner) encryptedReplay(ctx context.Context) {
	if !r.cfg.Fixture.SyntheticAssertions {
		r.skip("responses/encrypted-tool-output-replay", "synthetic replay assertions not enabled in fixture")
		return
	}
	if r.cfg.Fixture.Models["openai-response"] == "" {
		r.skip("responses/encrypted-tool-output-replay", "Responses model not configured")
		return
	}
	p := probe{path: "/v1/responses", endpoint: "openai-response"}
	b, e := r.body(p, true)
	if e != nil {
		r.check("responses/encrypted-tool-output-replay", func() error { return e })
		return
	}
	b["store"] = false
	b["include"] = []string{"reasoning.encrypted_content"}
	first, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
	r.check("responses/encrypted-tool-output-replay", func() error {
		if e != nil {
			return e
		}
		if first.status != 200 {
			return errors.New("encrypted tool response must return HTTP 200")
		}
		if e := validateNative(p, first.body, true); e != nil {
			return e
		}
		o, e := decodeNative(first.body)
		if e != nil {
			return e
		}
		items := arr(o["output"])
		reasoning, tool := false, false
		var replay []any
		switch original := b["input"].(type) {
		case string:
			replay = append(replay, map[string]any{"role": "user", "content": original})
		case []any:
			replay = append(replay, original...)
		default:
			return errors.New("original Responses input must be a string or native input array")
		}
		replay = append(replay, items...)
		for _, value := range items {
			item := obj(value)
			if str(item["type"]) == "reasoning" && str(item["encrypted_content"]) != "" {
				reasoning = true
			}
			if str(item["type"]) == "function_call" {
				tool = true
				replay = append(replay, map[string]any{"type": "function_call_output", "call_id": item["call_id"], "output": "Synthetic conformance tool completed."})
			}
		}
		if !reasoning || !tool {
			return errors.New("synthetic response must retain encrypted reasoning and complete tool output items")
		}
		replay = append(replay, map[string]any{"role": "user", "content": "Continue briefly."})
		b["input"] = replay
		b["tool_choice"] = "none"
		next, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
		if e != nil {
			return e
		}
		if next.status != 200 {
			return errors.New("complete encrypted reasoning/tool output replay was rejected")
		}
		return validateNative(p, next.body, false)
	})
}

func (r *runner) rerankDocuments(ctx context.Context) {
	if r.cfg.Fixture.Models["jina-rerank"] == "" {
		return
	}
	p := probe{path: "/v1/rerank", endpoint: "jina-rerank"}
	b, e := r.body(p, false)
	if e != nil {
		r.check("rerank/document-selection", func() error { return e })
		return
	}
	for _, want := range []bool{true, false} {
		b["return_documents"] = want
		res, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
		name := "rerank/return-documents"
		if !want {
			name = "rerank/no-documents"
		}
		r.check(name, func() error {
			if e != nil {
				return e
			}
			if res.status != 200 {
				return errors.New("rerank request must return HTTP 200")
			}
			if e := validateNative(p, res.body, false); e != nil {
				return e
			}
			o, _ := decodeNative(res.body)
			for _, value := range arr(o["results"]) {
				item := obj(value)
				doc, present := item["document"]
				if want {
					index, _ := integer(item["index"])
					documents := arr(b["documents"])
					text := str(doc)
					if text == "" {
						text = str(obj(doc)["text"])
					}
					if index < 0 || index >= int64(len(documents)) || text != str(documents[index]) {
						return errors.New("rerank document must match its original input index")
					}
				} else if present && doc != nil {
					return errors.New("return_documents false must omit documents")
				}
			}
			return nil
		})
	}
}

func (r *runner) tools(ctx context.Context) {
	if !r.cfg.Fixture.SyntheticAssertions {
		r.skip("native/tool-calls", "synthetic tool assertions not enabled in fixture")
		return
	}
	for _, p := range r.probesForText() {
		res, e := r.call(ctx, p, r.cfg.APIKey, "bearer", nil, true)
		r.check("tools/"+p.name, func() error {
			if e != nil {
				return e
			}
			if res.status != 200 {
				return errors.New("tool request did not return HTTP 200")
			}
			if e := validateSyntheticEvidence(p, res.body, true); e != nil {
				return e
			}
			if p.stream {
				if e := validateStream(p.endpoint, res.body); e != nil {
					return e
				}
				return validateStreamTool(p.endpoint, res.body)
			}
			return validateNative(p, res.body, true)
		})
	}
}

func (r *runner) probesForText() []probe {
	var out []probe
	for _, ep := range []string{"openai", "openai-response", "anthropic", "gemini"} {
		if r.cfg.Fixture.Models[ep] == "" {
			continue
		}
		path := "/v1/chat/completions"
		switch ep {
		case "openai-response":
			path = "/v1/responses"
		case "anthropic":
			path = "/v1/messages"
		case "gemini":
			path = "/v1beta/models/" + url.PathEscape(r.cfg.Fixture.Models[ep]) + ":generateContent"
		}
		out = append(out, probe{ep + "-json", path, ep, false, false})
		if ep == "gemini" {
			path = strings.TrimSuffix(path, ":generateContent") + ":streamGenerateContent"
		}
		out = append(out, probe{ep + "-stream", path, ep, true, false})
	}
	return out
}

func (r *runner) affinity(ctx context.Context) {
	if r.cfg.Fixture.Models["openai-response"] == "" {
		r.skip("responses/previous-response-chain", "Responses model not configured")
		return
	}
	p := probe{"response-chain", "/v1/responses", "openai-response", false, false}
	b, e := r.body(p, false)
	if e != nil {
		r.check("responses/previous-response-chain", func() error { return e })
		return
	}
	b["store"] = true
	b["include"] = []string{"reasoning.encrypted_content"}
	first, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
	var id string
	r.check("responses/stored-response", func() error {
		if e != nil {
			return e
		}
		if first.status != 200 {
			return errors.New("stored response must return HTTP 200")
		}
		if e := validateNative(p, first.body, false); e != nil {
			return e
		}
		var obj map[string]any
		_ = json.Unmarshal(first.body, &obj)
		id, _ = obj["id"].(string)
		if id == "" {
			return errors.New("stored response ID missing")
		}
		return nil
	})
	if id == "" {
		r.skip("responses/previous-response-chain", "no valid stored response ID")
		return
	}
	b["previous_response_id"] = id
	b["input"] = "Continue briefly."
	next, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
	r.check("responses/previous-response-chain", func() error {
		if e != nil {
			return e
		}
		if next.status != 200 {
			return errors.New("previous_response_id chain was rejected")
		}
		if e := validateNative(p, next.body, false); e != nil {
			return e
		}
		var obj map[string]any
		_ = json.Unmarshal(next.body, &obj)
		if obj["id"] == id {
			return errors.New("follow-up reused the prior response ID")
		}
		if r.cfg.Fixture.SyntheticAssertions {
			a := first.header.Get("X-NomiFun-Mock-Channel-ID")
			z := next.header.Get("X-NomiFun-Mock-Channel-ID")
			if a == "" || a != z {
				return errors.New("synthetic channel affinity was not preserved")
			}
		}
		return nil
	})
	if r.cfg.Fixture.CrossKey == "" {
		r.skip("responses/cross-key-isolation", "second authorized key not configured")
	} else {
		res, e := r.send(ctx, p, r.cfg.Fixture.CrossKey, "bearer", nil, b)
		r.check("responses/cross-key-isolation", func() error {
			if e != nil {
				return e
			}
			if res.status != 400 {
				return errors.New("cross-key previous response must be indistinguishable from unknown ID (HTTP 400)")
			}
			return validateNativeError("openai-response", res.body, "invalid_previous_response_id")
		})
	}
	b["previous_response_id"] = "resp_conformance_unknown_id"
	res, e := r.send(ctx, p, r.cfg.APIKey, "bearer", nil, b)
	r.check("responses/unknown-previous-response", func() error {
		if e != nil {
			return e
		}
		if res.status != 400 {
			return errors.New("unknown previous response must return HTTP 400")
		}
		return validateNativeError("openai-response", res.body, "invalid_previous_response_id")
	})
}

func (r *runner) visibility(ctx context.Context) {
	check := func(key string, expected []string) error {
		cat, e := r.call(ctx, probe{path: "/nomifun/v1/catalog"}, key, "bearer", nil, false)
		if e != nil {
			return e
		}
		models, e := r.call(ctx, probe{path: "/v1/models"}, key, "bearer", nil, false)
		if e != nil {
			return e
		}
		if cat.status != 200 || models.status != 200 {
			return errors.New("catalog and models must return HTTP 200")
		}
		if e := validateCatalog(cat.body); e != nil {
			return e
		}
		var c struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if json.Unmarshal(cat.body, &c) != nil {
			return errors.New("invalid catalog")
		}
		var m struct {
			Object string `json:"object"`
			Data   []struct {
				ID     string `json:"id"`
				Object string `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal(models.body, &m) != nil || m.Object != "list" || m.Data == nil {
			return errors.New("invalid OpenAI models list")
		}
		a := []string{}
		z := []string{}
		for _, model := range c.Models {
			a = append(a, model.ID)
		}
		for _, model := range m.Data {
			if model.ID == "" || model.Object != "model" {
				return errors.New("invalid OpenAI model entry")
			}
			z = append(z, model.ID)
		}
		sort.Strings(a)
		sort.Strings(z)
		if fmt.Sprint(a) != fmt.Sprint(z) {
			return errors.New("models list visibility differs from catalog")
		}
		if expected != nil {
			want := append([]string(nil), expected...)
			sort.Strings(want)
			if fmt.Sprint(a) != fmt.Sprint(want) {
				return errors.New("limited key model visibility differs from fixture")
			}
		}
		return nil
	}
	r.check("visibility/models-match-catalog", func() error { return check(r.cfg.APIKey, nil) })
	if r.cfg.Fixture.LimitedKey == "" {
		r.skip("visibility/limited-key", "limited key not configured")
		return
	}
	r.check("visibility/limited-key", func() error { return check(r.cfg.Fixture.LimitedKey, r.cfg.Fixture.LimitedModels) })
}

var billingCodes = []string{"insufficient_balance", "subscription_expired", "model_not_in_plan", "key_expired", "rate_limited"}

func (r *runner) billing(ctx context.Context) {
	for _, code := range billingCodes {
		key := r.cfg.Fixture.ErrorKeys[code]
		if key == "" {
			r.skip("billing/"+code, "synthetic error key not configured")
			continue
		}
		for _, ep := range []string{"openai", "anthropic", "gemini"} {
			if r.cfg.Fixture.Models[ep] == "" {
				r.skip("billing/"+code+"/"+ep, "endpoint model not configured")
				continue
			}
			path := "/v1/chat/completions"
			if ep == "anthropic" {
				path = "/v1/messages"
			}
			if ep == "gemini" {
				path = "/v1beta/models/" + url.PathEscape(r.cfg.Fixture.Models[ep]) + ":generateContent"
			}
			res, e := r.call(ctx, probe{path: path, endpoint: ep}, key, "bearer", nil, false)
			r.check("billing/"+code+"/"+ep, func() error {
				if e != nil {
					return e
				}
				want := 402
				switch code {
				case "subscription_expired", "model_not_in_plan":
					want = 403
				case "key_expired":
					want = 401
				case "rate_limited":
					want = 429
				}
				if res.status != want {
					return errors.New("billing HTTP status differs from protocol")
				}
				return validateNativeError(ep, res.body, code)
			})
		}
	}
}
