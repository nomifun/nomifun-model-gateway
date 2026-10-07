// SPDX-License-Identifier: Apache-2.0
package mock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func request(t *testing.T, h http.Handler, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	method := http.MethodPost
	if body == "" {
		method = http.MethodGet
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func responseMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; body %s", err, w.Body.String())
	}
	return result
}

func TestAuthenticationHasNoCredentialPrecedence(t *testing.T) {
	h := New(Config{})
	for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/nomifun/v1/catalog", nil)
			value := DefaultAPIKey
			if name == "Authorization" {
				value = "Bearer " + value
			}
			r.Header.Set(name, value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	cases := []struct {
		name   string
		header http.Header
		url    string
		status int
	}{
		{"same sources", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}, "X-Api-Key": {DefaultAPIKey}, "X-Goog-Api-Key": {DefaultAPIKey}}, "/nomifun/v1/catalog", 200},
		{"conflict", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}, "X-Api-Key": {OtherAPIKey}}, "/nomifun/v1/catalog", 401},
		{"multiple", http.Header{"X-Api-Key": {DefaultAPIKey, DefaultAPIKey}}, "/nomifun/v1/catalog", 401},
		{"empty source", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}, "X-Api-Key": {""}}, "/nomifun/v1/catalog", 401},
		{"malformed bearer", http.Header{"Authorization": {"Bearer  " + DefaultAPIKey}}, "/nomifun/v1/catalog", 401},
		{"wrong scheme", http.Header{"Authorization": {"Basic " + DefaultAPIKey}}, "/nomifun/v1/catalog", 401},
		{"comma", http.Header{"X-Api-Key": {DefaultAPIKey + "," + DefaultAPIKey}}, "/nomifun/v1/catalog", 401},
		{"query outside Gemini", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}}, "/nomifun/v1/catalog?key=" + DefaultAPIKey, 401},
		{"query unknown Gemini method", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}}, "/v1beta/models/mock-gemini:unknown?key=" + DefaultAPIKey, 401},
		{"query malformed", http.Header{"Authorization": {"Bearer " + DefaultAPIKey}}, "/nomifun/v1/catalog?key=%zz", 401},
		{"query duplicate", http.Header{}, "/v1beta/models/mock-gemini:generateContent?key=" + DefaultAPIKey + "&key=" + DefaultAPIKey, 401},
		{"query conflict", http.Header{"X-Api-Key": {OtherAPIKey}}, "/v1beta/models/mock-gemini:generateContent?key=" + DefaultAPIKey, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.url, nil)
			r.Header = tc.header
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
				t.Fatal("protected response is missing cache/request headers")
			}
		})
	}
	w := request(t, h, "/v1beta/models/mock-gemini:generateContent?key="+DefaultAPIKey, "", `{"contents":[{"role":"user","parts":[{"text":"Hi"}]}]}`)
	if w.Code != 200 {
		t.Fatalf("query auth got %d", w.Code)
	}
}

func TestCustomKeyAndPlanFiltering(t *testing.T) {
	h := New(Config{APIKey: "nmg_custom_synthetic"})
	if got := request(t, h, "/nomifun/v1/catalog", DefaultAPIKey, "").Code; got != 401 {
		t.Fatalf("default key survives custom override: %d", got)
	}
	if got := request(t, h, "/nomifun/v1/catalog", "nmg_custom_synthetic", "").Code; got != 200 {
		t.Fatalf("custom key: %d", got)
	}
	limited := responseMap(t, request(t, h, "/nomifun/v1/catalog", LimitedAPIKey, ""))["models"].([]any)
	if len(limited) != 1 || limited[0].(map[string]any)["id"] != "mock-compatible" {
		t.Fatalf("wrong limited catalog: %#v", limited)
	}
	w := request(t, h, "/v1/responses", LimitedAPIKey, `{"model":"mock-gpt","input":"Hi"}`)
	if w.Code != 403 || responseMap(t, w)["error"].(map[string]any)["code"] != "model_not_in_plan" {
		t.Fatalf("wrong limited inference: %d %s", w.Code, w.Body.String())
	}
	if got := request(t, h, "/nomifun/v1/meta", "", "").Code; got != 200 {
		t.Fatalf("anonymous meta: %d", got)
	}
	if got := request(t, h, "/nomifun/v1/meta?key="+DefaultAPIKey, "", "").Code; got != 401 {
		t.Fatalf("meta accepted URL credential: %d", got)
	}
}

func TestNativeBusinessErrors(t *testing.T) {
	cases := []struct {
		key, code string
		status    int
	}{{InsufficientBalanceAPIKey, "insufficient_balance", 402}, {SubscriptionExpiredAPIKey, "subscription_expired", 403}, {ModelNotInPlanAPIKey, "model_not_in_plan", 403}, {KeyExpiredAPIKey, "key_expired", 401}, {RateLimitedAPIKey, "rate_limited", 429}}
	for _, path := range []string{"/nomifun/v1/account", "/v1/chat/completions", "/v1/messages", "/v1beta/models/mock-gemini:generateContent"} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.code, func(t *testing.T) {
				w := request(t, New(Config{}), path, tc.key, `{}`)
				if w.Code != tc.status {
					t.Fatalf("got %d want %d", w.Code, tc.status)
				}
				result := responseMap(t, w)
				e := result["error"].(map[string]any)
				if strings.HasPrefix(path, "/v1beta/") {
					if e["code"] != float64(tc.status) {
						t.Fatalf("Gemini numeric error code lost: %#v", e)
					}
					detail := e["details"].([]any)[0].(map[string]any)
					if detail["reason"] != strings.ToUpper(tc.code) || detail["domain"] != "nomifun-model-gateway" {
						t.Fatalf("wrong ErrorInfo: %#v", detail)
					}
					metadata := detail["metadata"].(map[string]any)
					if metadata["nomifun_code"] != tc.code || metadata["purchase_url"] != PurchaseURL {
						t.Fatalf("wrong business metadata: %#v", metadata)
					}
				} else if e["code"] != tc.code || e["purchase_url"] != PurchaseURL || result["request_id"] != w.Header().Get("X-Request-ID") {
					t.Fatalf("wrong native error: %#v", result)
				}
				if path == "/v1/messages" && result["type"] != "error" {
					t.Fatal("missing Anthropic top-level type")
				}
				if tc.status == 429 && w.Header().Get("Retry-After") != "1" {
					t.Fatal("missing Retry-After")
				}
			})
		}
	}
}

func TestResponsesBindingOwnershipAndStoreFalse(t *testing.T) {
	h := New(Config{})
	parent := request(t, h, "/v1/responses", DefaultAPIKey, `{"model":"mock-gpt","input":"Hi","store":true,"include":["reasoning.encrypted_content"]}`)
	parentID := responseMap(t, parent)["id"].(string)
	parentChannel := parent.Header().Get("X-NomiFun-Mock-Channel-ID")
	if parentChannel == "" {
		t.Fatal("missing fixture channel")
	}
	for i := 0; i < 3; i++ {
		body := `{"model":"mock-gpt","input":"Next","previous_response_id":"` + parentID + `"}`
		next := request(t, h, "/v1/responses", DefaultAPIKey, body)
		if next.Code != 200 || next.Header().Get("X-NomiFun-Mock-Channel-ID") != parentChannel {
			t.Fatalf("affinity lost: %d %s", next.Code, next.Body.String())
		}
		parentID = responseMap(t, next)["id"].(string)
	}
	for _, pair := range []struct{ key, id string }{{OtherAPIKey, parentID}, {DefaultAPIKey, "resp_missing"}} {
		w := request(t, h, "/v1/responses", pair.key, `{"model":"mock-gpt","input":"Next","previous_response_id":"`+pair.id+`"}`)
		if w.Code != 400 || responseMap(t, w)["error"].(map[string]any)["code"] != "invalid_previous_response_id" {
			t.Fatalf("ownership failure: %d %s", w.Code, w.Body.String())
		}
	}
	unstored := request(t, h, "/v1/responses", DefaultAPIKey, `{"model":"mock-gpt","input":"Hi","store":false}`)
	unstoredID := responseMap(t, unstored)["id"].(string)
	if request(t, h, "/v1/responses", DefaultAPIKey, `{"model":"mock-gpt","input":"Next","previous_response_id":"`+unstoredID+`"}`).Code != 400 {
		t.Fatal("store=false response was bound")
	}
	parentOutput := responseMap(t, parent)["output"]
	replay, _ := json.Marshal(map[string]any{"model": "mock-gpt", "input": parentOutput, "store": false, "include": []string{"reasoning.encrypted_content"}})
	if got := request(t, h, "/v1/responses", DefaultAPIKey, string(replay)).Code; got != 200 {
		t.Fatalf("native output replay rejected: %d", got)
	}
}

func TestSessionAffinityExpiryFailsClosed(t *testing.T) {
	h := New(Config{}).(*server)
	body := `{"model":"mock-claude","messages":[{"role":"user","content":"Hi"}],"max_tokens":128}`
	call := func(key, session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Api-Key", key)
		r.Header.Set("X-NomiFun-Session-ID", session)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := call(DefaultAPIKey, "session_mock_one")
	other := call(OtherAPIKey, "session_mock_one")
	if first.Code != 200 || other.Code != 200 {
		t.Fatal("session setup failed")
	}
	if len(h.sessions) != 2 {
		t.Fatal("session scope crossed keys")
	}
	if again := call(DefaultAPIKey, "session_mock_one"); again.Header().Get("X-NomiFun-Mock-Channel-ID") != first.Header().Get("X-NomiFun-Mock-Channel-ID") {
		t.Fatal("session channel changed")
	}
	scope := sha256.Sum256([]byte(DefaultAPIKey + "\x00session_mock_one"))
	binding := h.sessions[scope]
	binding.lastUsed = time.Now().Add(-25 * time.Hour)
	h.sessions[scope] = binding
	for i := 0; i < 2; i++ {
		if got := call(DefaultAPIKey, "session_mock_one").Code; got != 400 {
			t.Fatalf("expired session reassigned: %d", got)
		}
	}
	if call(DefaultAPIKey, "session_mock_two").Code != 200 {
		t.Fatal("explicit new session ID rejected")
	}
	if call(OtherAPIKey, "session_mock_one").Code != 200 {
		t.Fatal("expiry leaked between keys")
	}
}

func TestRequestBoundaries(t *testing.T) {
	h := New(Config{})
	cases := []struct {
		path, body string
		status     int
	}{
		{"/v1/responses", `{"model":"mock-gpt","input":null}`, 400},
		{"/v1/responses", `{"model":"mock-gpt","previous_response_id":null}`, 400},
		{"/v1/embeddings", `{"model":"mock-embedding","input":null}`, 400},
		{"/v1/responses", `{"model":"mock-gpt","input":"Hi","stream":null}`, 400},
		{"/v1/responses", `{"model":"mock-gpt","input":"Hi"}{}`, 400},
		{"/v1/messages", `{"model":"mock-claude","messages":[{"role":"user","content":"Hi"}]}`, 400},
		{"/v1/messages", `{"model":"mock-claude","messages":[{}],"max_tokens":1.5}`, 400},
		{"/v1/messages", `{"model":"mock-claude","messages":[{}],"max_tokens":9000}`, 400},
		{"/v1/chat/completions", `{"model":"mock-claude","messages":[{}]}`, 400},
		{"/v1/chat/completions", `{"model":"unknown","messages":[{}]}`, 404},
		{"/v1/rerank", `{"model":"mock-rerank","query":"hi","documents":["doc"],"top_n":0}`, 400},
		{"/v1/responses", `{"model":"mock-gpt","input":"` + strings.Repeat("x", MaxRequestBytes) + `"}`, 413},
	}
	for _, tc := range cases {
		w := request(t, h, tc.path, DefaultAPIKey, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"mock-gpt","input":"Hi"}`))
	r.Header.Set("X-Api-Key", DefaultAPIKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("bad content-type accepted: %d", w.Code)
	}
}

func TestGeminiStreamingAltDefaultsAndValidation(t *testing.T) {
	h := New(Config{})
	for _, tc := range []struct {
		query  string
		status int
	}{{"", 200}, {"?alt=sse", 200}, {"?alt=json", 400}, {"?alt=sse&alt=sse", 400}, {"?alt=", 400}} {
		t.Run(tc.query, func(t *testing.T) {
			w := request(t, h, "/v1beta/models/mock-gemini:streamGenerateContent"+tc.query, DefaultAPIKey, `{"contents":[{"role":"user","parts":[{"text":"Hi"}]}]}`)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.status == 200 {
				if w.Header().Get("Content-Type") != "text/event-stream" {
					t.Fatal("Gemini did not default to SSE")
				}
			} else {
				details := responseMap(t, w)["error"].(map[string]any)["details"].([]any)
				code := details[0].(map[string]any)["metadata"].(map[string]any)["nomifun_code"]
				if code != "invalid_request_error" {
					t.Fatalf("wrong error code: %v", code)
				}
			}
		})
	}
}

func TestImageFixtureAndMultipartEdit(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(syntheticPNG)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 1 || image.Bounds().Dy() != 1 {
		t.Fatal("wrong PNG dimensions")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "mock-image")
	_ = writer.WriteField("prompt", "Edit this synthetic image.")
	file, _ := writer.CreateFormFile("image", "mock.png")
	_, _ = file.Write(raw)
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/v1/images/edits", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+DefaultAPIKey)
	w := httptest.NewRecorder()
	New(Config{}).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("edit failed: %d %s", w.Code, w.Body.String())
	}
	imageData := responseMap(t, w)["data"].([]any)[0].(map[string]any)
	if imageData["b64_json"] != syntheticPNG {
		t.Fatal("image fixture changed")
	}
}

type cancelWriter struct {
	header  http.Header
	bytes   int
	mu      sync.Mutex
	flushed chan struct{}
	once    sync.Once
}

func (w *cancelWriter) Header() http.Header { return w.header }
func (w *cancelWriter) WriteHeader(int)     {}
func (w *cancelWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes += len(data)
	return len(data), nil
}
func (w *cancelWriter) Flush() { w.once.Do(func() { close(w.flushed) }) }
func TestStreamingCancellationStopsWithoutTotalTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"mock-gpt","input":"Hi","stream":true}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+DefaultAPIKey)
	w := &cancelWriter{header: make(http.Header), flushed: make(chan struct{})}
	done := make(chan struct{})
	go func() { New(Config{StreamDelay: time.Hour}).ServeHTTP(w, r); close(done) }()
	select {
	case <-w.flushed:
	case <-time.After(time.Second):
		t.Fatal("stream never flushed its first event")
	}
	// Enter the inter-event delay before cancelling: unconditional sleep fails.
	<-time.After(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled stream waited for its delay")
	}
	if w.bytes == 0 {
		t.Fatal("no first stream event")
	}
	if w.header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not SSE")
	}
}

func TestConcurrentResponsesHaveUniqueIDsAndBindings(t *testing.T) {
	h := New(Config{})
	var group sync.WaitGroup
	ids := make(chan string, 32)
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			w := request(t, h, "/v1/responses", DefaultAPIKey, `{"model":"mock-gpt","input":"Hi"}`)
			if w.Code != 200 {
				t.Errorf("response code %d", w.Code)
				return
			}
			ids <- responseMap(t, w)["id"].(string)
		}()
	}
	group.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 32 {
		t.Fatalf("got %d responses", len(seen))
	}
}
