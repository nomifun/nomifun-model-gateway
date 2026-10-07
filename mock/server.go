// SPDX-License-Identifier: Apache-2.0
// Package mock provides a synthetic, local development implementation of v1.
// It never contacts an upstream, meters real usage, or accepts payment.
package mock

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

const (
	DefaultAPIKey             = "nmg_mock_development"
	LimitedAPIKey             = "nmg_mock_limited"
	OtherAPIKey               = "nmg_mock_other"
	InsufficientBalanceAPIKey = "nmg_mock_insufficient_balance"
	SubscriptionExpiredAPIKey = "nmg_mock_subscription_expired"
	ModelNotInPlanAPIKey      = "nmg_mock_model_not_in_plan"
	KeyExpiredAPIKey          = "nmg_mock_key_expired"
	RateLimitedAPIKey         = "nmg_mock_rate_limited"
	MaxRequestBytes           = 2 << 20
	PurchaseURL               = "https://operator.example/purchase"
)

// Config contains mock-only configuration. APIKey is never logged or returned.
// A custom APIKey replaces the full-access demo key; fixed fixtures remain.
type Config struct {
	APIKey      string
	StreamDelay time.Duration
}

type responseBinding struct {
	owner   [32]byte
	channel string
}
type sessionBinding struct {
	channel  string
	lastUsed time.Time
	expired  bool
}
type server struct {
	config    Config
	sequence  atomic.Uint64
	mu        sync.Mutex
	responses map[string]responseBinding
	sessions  map[[32]byte]sessionBinding
}

func New(config Config) http.Handler {
	if config.APIKey == "" {
		config.APIKey = DefaultAPIKey
	}
	if config.StreamDelay < 0 {
		config.StreamDelay = 0
	}
	return &server{config: config, responses: make(map[string]responseBinding), sessions: make(map[[32]byte]sessionBinding)}
}

type apiError struct {
	status        int
	code, message string
}

func invalid(message string) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_request_error", message}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("req_mock_%06d", s.sequence.Add(1))
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/nomifun/v1/meta" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if _, hasKey := query["key"]; hasKey || err != nil {
			s.writeError(w, r, id, &apiError{401, "invalid_api_key", "API keys in URLs are accepted only by native Gemini endpoints."})
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			s.writeError(w, r, id, &apiError{405, "method_not_allowed", "Use GET for this endpoint."})
			return
		}
		writeJSON(w, 200, v1.MockMeta())
		return
	}
	key, authErr := authenticate(r)
	if authErr != nil {
		s.writeError(w, r, id, authErr)
		return
	}
	limited, keyErr := s.authorize(key)
	if keyErr != nil {
		s.writeError(w, r, id, keyErr)
		return
	}
	if r.URL.Path == "/nomifun/v1/catalog" || r.URL.Path == "/nomifun/v1/account" || r.URL.Path == "/v1/models" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			s.writeError(w, r, id, &apiError{405, "method_not_allowed", "Use GET for this endpoint."})
			return
		}
		switch r.URL.Path {
		case "/nomifun/v1/catalog":
			writeJSON(w, 200, v1.MockCatalog(limited))
		case "/nomifun/v1/account":
			account := v1.MockAccount()
			if limited {
				account.Key.Name = "Mock limited key"
			}
			writeJSON(w, 200, account)
		case "/v1/models":
			list := []any{}
			for _, m := range v1.MockCatalog(limited).Models {
				list = append(list, map[string]any{"id": m.ID, "object": "model", "created": int64(0), "owned_by": m.Vendor})
			}
			writeJSON(w, 200, map[string]any{"object": "list", "data": list})
		}
		return
	}
	endpoint, modelFromPath, task := route(r.URL.Path)
	if endpoint == "" {
		s.writeError(w, r, id, &apiError{404, "not_found", "Unknown mock endpoint."})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.writeError(w, r, id, &apiError{405, "method_not_allowed", "Use POST for this endpoint."})
		return
	}
	var body map[string]json.RawMessage
	var err *apiError
	if r.URL.Path == "/v1/images/edits" {
		body, err = readImageEdit(w, r)
	} else {
		body, err = readJSONBody(w, r)
	}
	if err != nil {
		s.writeError(w, r, id, err)
		return
	}
	model := modelFromPath
	if model == "" {
		model, err = stringField(body, "model", true)
		if err != nil {
			s.writeError(w, r, id, err)
			return
		}
	}
	modelInfo, err := validateModel(model, endpoint, task, limited)
	if err != nil {
		s.writeError(w, r, id, err)
		return
	}
	stream, err := boolField(body, "stream")
	if err != nil {
		s.writeError(w, r, id, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
		if values, present := r.URL.Query()["alt"]; present && (len(values) != 1 || values[0] != "sse") {
			s.writeError(w, r, id, invalid("alt must be a single sse value when provided for Gemini streaming."))
			return
		}
		stream = true
	}
	if stream && endpoint != v1.OpenAI && endpoint != v1.Responses && endpoint != v1.Anthropic && endpoint != v1.Gemini {
		s.writeError(w, r, id, invalid("This endpoint does not support streaming."))
		return
	}
	if err = validateNative(body, endpoint, r.URL.Path, modelInfo); err != nil {
		s.writeError(w, r, id, err)
		return
	}
	channel := ""
	if endpoint == v1.Responses {
		previous, e := stringField(body, "previous_response_id", false)
		if e != nil {
			s.writeError(w, r, id, e)
			return
		}
		if previous != "" {
			s.mu.Lock()
			binding, ok := s.responses[previous]
			s.mu.Unlock()
			if !ok || binding.owner != sha256.Sum256([]byte(key)) {
				s.writeError(w, r, id, &apiError{400, "invalid_previous_response_id", "The previous response is unavailable for this key."})
				return
			}
			channel = binding.channel
		}
		if channel == "" {
			channel = fixtureChannel(s.sequence.Load())
		}
		responseID := strings.Replace(id, "req_", "resp_", 1)
		store, e := boolField(body, "store")
		if e != nil {
			s.writeError(w, r, id, e)
			return
		}
		// Native Responses defaults to store=true. Explicit false is not bound.
		if _, set := body["store"]; !set {
			store = true
		}
		if store {
			s.mu.Lock()
			s.responses[responseID] = responseBinding{sha256.Sum256([]byte(key)), channel}
			s.mu.Unlock()
		}
	}
	if endpoint == v1.Anthropic && len(r.Header.Values("X-NomiFun-Session-ID")) > 0 {
		values := r.Header.Values("X-NomiFun-Session-ID")
		if len(values) != 1 || len(values[0]) > 256 || !validToken(values[0]) {
			s.writeError(w, r, id, invalid("X-NomiFun-Session-ID must be one nonempty token of at most 256 bytes."))
			return
		}
		scope := sha256.Sum256([]byte(key + "\x00" + values[0]))
		now := time.Now()
		s.mu.Lock()
		binding, found := s.sessions[scope]
		if found && (binding.expired || now.Sub(binding.lastUsed) >= 24*time.Hour) {
			binding.expired = true
			s.sessions[scope] = binding
			s.mu.Unlock()
			s.writeError(w, r, id, invalid("This session affinity binding expired. Create a new X-NomiFun-Session-ID."))
			return
		}
		if !found {
			binding.channel = fixtureChannel(s.sequence.Load())
		}
		binding.lastUsed = now
		s.sessions[scope] = binding
		channel = binding.channel
		s.mu.Unlock()
	}
	if channel != "" {
		w.Header().Set("X-NomiFun-Mock-Channel-ID", channel)
	}
	s.infer(w, r, id, model, endpoint, body, stream)
}

func fixtureChannel(n uint64) string {
	if n%2 == 0 {
		return "mock_fixture_a"
	}
	return "mock_fixture_b"
}

func authenticate(r *http.Request) (string, *apiError) {
	unauthorized := func() (string, *apiError) {
		return "", &apiError{401, "invalid_api_key", "Provide one valid API key; conflicting or malformed credentials are rejected."}
	}
	keys := []string{}
	for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		values, exists := r.Header[http.CanonicalHeaderKey(name)]
		if !exists {
			continue
		}
		if len(values) != 1 {
			return unauthorized()
		}
		value := values[0]
		if name == "Authorization" {
			parts := strings.Split(value, " ")
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				return unauthorized()
			}
			value = parts[1]
		}
		if !validToken(value) {
			return unauthorized()
		}
		keys = append(keys, value)
	}
	parsed, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		return unauthorized()
	}
	query, present := parsed["key"]
	if present {
		endpoint, _, _ := route(r.URL.Path)
		if endpoint != v1.Gemini || len(query) != 1 || !validToken(query[0]) {
			return unauthorized()
		}
		keys = append(keys, query[0])
	}
	if len(keys) == 0 {
		return unauthorized()
	}
	for _, key := range keys {
		if key != keys[0] {
			return unauthorized()
		}
	}
	return keys[0], nil
}
func validToken(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c <= 32 || c >= 127 || c == ',' {
			return false
		}
	}
	return true
}

func (s *server) authorize(key string) (bool, *apiError) {
	switch key {
	case OtherAPIKey:
		return false, nil
	case LimitedAPIKey:
		return true, nil
	case InsufficientBalanceAPIKey:
		return false, &apiError{402, "insufficient_balance", "The mock balance is insufficient. Recharge to continue."}
	case SubscriptionExpiredAPIKey:
		return false, &apiError{403, "subscription_expired", "The mock subscription has expired. Renew to continue."}
	case ModelNotInPlanAPIKey:
		return false, &apiError{403, "model_not_in_plan", "The mock model is not included in this plan."}
	case KeyExpiredAPIKey:
		return false, &apiError{401, "key_expired", "The mock API key has expired. Create a new key."}
	case RateLimitedAPIKey:
		return false, &apiError{429, "rate_limited", "The mock request rate limit was reached. Retry later."}
	}
	if key == s.config.APIKey {
		return false, nil
	}
	return false, &apiError{401, "invalid_api_key", "The API key is invalid."}
}

func route(path string) (v1.Endpoint, string, v1.Task) {
	switch path {
	case "/v1/chat/completions":
		return v1.OpenAI, "", v1.Chat
	case "/v1/responses":
		return v1.Responses, "", v1.Chat
	case "/v1/messages", "/v1/messages/count_tokens":
		return v1.Anthropic, "", v1.Chat
	case "/v1/embeddings":
		return v1.Embeddings, "", v1.Embedding
	case "/v1/images/generations":
		return v1.Images, "", v1.ImageGeneration
	case "/v1/images/edits":
		return v1.Images, "", v1.ImageEdit
	case "/v1/rerank":
		return v1.Rerank, "", v1.Reranking
	}
	if strings.HasPrefix(path, "/v1beta/models/") {
		rest := strings.TrimPrefix(path, "/v1beta/models/")
		name, op, found := strings.Cut(rest, ":")
		if found && name != "" && !strings.ContainsAny(name, "/:") && (op == "generateContent" || op == "streamGenerateContent") {
			return v1.Gemini, name, v1.Chat
		}
	}
	return "", "", ""
}

func validateModel(id string, endpoint v1.Endpoint, task v1.Task, limited bool) (v1.Model, *apiError) {
	for _, model := range v1.MockCatalog(false).Models {
		if model.ID != id {
			continue
		}
		if limited && id != "mock-compatible" {
			return v1.Model{}, &apiError{403, "model_not_in_plan", "This model is not included in the mock limited plan."}
		}
		for _, supported := range model.TaskEndpoints[task].Endpoints {
			if supported == endpoint {
				return model, nil
			}
		}
		return v1.Model{}, &apiError{400, "unsupported_endpoint", "This model does not support the requested native endpoint."}
	}
	return v1.Model{}, &apiError{404, "model_not_found", "The requested mock model does not exist."}
}

func readJSONBody(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, *apiError) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, &apiError{415, "unsupported_media_type", "Use application/json for this endpoint."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &apiError{413, "request_too_large", "The mock request exceeds 2 MiB."}
		}
		return nil, invalid("The request must be a valid JSON object.")
	}
	if body == nil {
		return nil, invalid("The request must be a JSON object.")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &apiError{413, "request_too_large", "The mock request exceeds 2 MiB."}
		}
		return nil, invalid("Only one JSON object is allowed.")
	}
	return body, nil
}

func readImageEdit(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, *apiError) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		return nil, &apiError{415, "unsupported_media_type", "Use multipart/form-data with an image file for image edits."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	if err = r.ParseMultipartForm(MaxRequestBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &apiError{413, "request_too_large", "The mock request exceeds 2 MiB."}
		}
		return nil, invalid("The multipart request is malformed.")
	}
	defer r.MultipartForm.RemoveAll()
	body := map[string]json.RawMessage{}
	for name, values := range r.MultipartForm.Value {
		if len(values) != 1 {
			return nil, invalid("Multipart fields must have one value.")
		}
		body[name], _ = json.Marshal(values[0])
	}
	images := r.MultipartForm.File["image"]
	if len(images) == 0 {
		images = r.MultipartForm.File["image[]"]
	}
	if len(images) == 0 {
		return nil, invalid("Image edits require an image file.")
	}
	for _, file := range images {
		if file.Size == 0 {
			return nil, invalid("Image files must not be empty.")
		}
	}
	if n := body["n"]; n != nil {
		var value string
		_ = json.Unmarshal(n, &value)
		body["n"] = json.RawMessage(value)
	}
	return body, nil
}

func stringField(body map[string]json.RawMessage, name string, required bool) (string, *apiError) {
	raw, exists := body[name]
	if !exists || string(raw) == "null" {
		if required {
			return "", invalid(name + " is required.")
		}
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || (required && strings.TrimSpace(value) == "") {
		return "", invalid(name + " must be a nonempty string.")
	}
	return value, nil
}
func boolField(body map[string]json.RawMessage, name string) (bool, *apiError) {
	raw, exists := body[name]
	if !exists {
		return false, nil
	}
	var value bool
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return false, invalid(name + " must be a boolean.")
	}
	return value, nil
}
func nonemptyArray(body map[string]json.RawMessage, name string) bool {
	var values []json.RawMessage
	return json.Unmarshal(body[name], &values) == nil && len(values) > 0
}
func validateNative(body map[string]json.RawMessage, endpoint v1.Endpoint, path string, model v1.Model) *apiError {
	switch endpoint {
	case v1.OpenAI, v1.Anthropic:
		if !nonemptyArray(body, "messages") {
			return invalid("messages must be a nonempty array.")
		}
		if endpoint == v1.Anthropic && path != "/v1/messages/count_tokens" {
			var max int64
			if json.Unmarshal(body["max_tokens"], &max) != nil || max <= 0 {
				return invalid("max_tokens must be an explicit positive integer for Anthropic messages.")
			}
			if model.MaxOutputTokens != nil && max > *model.MaxOutputTokens {
				return invalid("max_tokens exceeds the model output limit.")
			}
		}
	case v1.Responses:
		if raw, ok := body["input"]; ok {
			var value string
			if string(raw) == "null" || (json.Unmarshal(raw, &value) != nil && !nonemptyArray(body, "input")) {
				return invalid("input must be a string or a nonempty array.")
			}
		} else {
			if _, err := stringField(body, "previous_response_id", true); err != nil {
				return invalid("input or a nonempty previous_response_id is required.")
			}
		}
	case v1.Gemini:
		if !nonemptyArray(body, "contents") {
			return invalid("contents must be a nonempty array.")
		}
	case v1.Images:
		if _, err := stringField(body, "prompt", true); err != nil {
			return err
		}
		if raw, ok := body["n"]; ok {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 1 || n > 4 {
				return invalid("n must be an integer between 1 and 4 in the mock.")
			}
		}
	case v1.Embeddings:
		var text string
		if string(body["input"]) == "null" || (json.Unmarshal(body["input"], &text) != nil && !nonemptyArray(body, "input")) {
			return invalid("input must be a string or a nonempty array.")
		}
	case v1.Rerank:
		if _, err := stringField(body, "query", true); err != nil {
			return err
		}
		if !nonemptyArray(body, "documents") {
			return invalid("documents must be a nonempty array.")
		}
		if _, err := boolField(body, "return_documents"); err != nil {
			return err
		}
		if raw, ok := body["top_n"]; ok {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 1 {
				return invalid("top_n must be a positive integer.")
			}
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *server) writeError(w http.ResponseWriter, r *http.Request, id string, e *apiError) {
	if e.status == 429 {
		w.Header().Set("Retry-After", "1")
	}
	if strings.HasPrefix(r.URL.Path, "/v1beta/") {
		status := "INVALID_ARGUMENT"
		switch e.status {
		case 401:
			status = "UNAUTHENTICATED"
		case 402, 403:
			status = "PERMISSION_DENIED"
		case 404:
			status = "NOT_FOUND"
		case 429:
			status = "RESOURCE_EXHAUSTED"
		case 413:
			status = "INVALID_ARGUMENT"
		}
		writeJSON(w, e.status, map[string]any{"error": map[string]any{"code": e.status, "status": status, "message": e.message, "details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": strings.ToUpper(e.code), "domain": "nomifun-model-gateway", "metadata": map[string]string{"nomifun_code": e.code, "purchase_url": PurchaseURL}}}}})
		return
	}
	typ := "invalid_request_error"
	if e.status == 401 {
		typ = "authentication_error"
	}
	if e.status == 403 {
		typ = "permission_error"
	}
	if e.status == 429 {
		typ = "rate_limit_error"
	}
	inner := map[string]any{"message": e.message, "type": typ, "code": e.code, "purchase_url": PurchaseURL}
	if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		writeJSON(w, e.status, map[string]any{"type": "error", "error": inner, "request_id": id})
		return
	}
	writeJSON(w, e.status, map[string]any{"error": inner, "request_id": id})
}
