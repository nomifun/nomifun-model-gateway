// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testStore struct{ db *gorm.DB }

func (s testStore) DB() *gorm.DB { return s.db }
func (s testStore) ChannelKey(c core.Channel) (string, error) {
	return "synthetic-upstream-credential", nil
}
func setup(t *testing.T, upstreams ...*httptest.Server) (*Service, *gorm.DB) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gateway.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })
	if e = db.AutoMigrate(&core.Channel{}, &core.ResponseAffinity{}, &core.SessionAffinity{}, &core.User{}, &core.APIKey{}, &core.Reservation{}, &core.Subscription{}, &core.LedgerEntry{}); e != nil {
		t.Fatal(e)
	}
	for i, u := range upstreams {
		c := core.Channel{Name: "synthetic", Kind: "openai", BaseURL: u.URL, ModelsJSON: `{"public":"private"}`, EndpointsJSON: `["openai","openai-response","embeddings","image-generation","jina-rerank"]`, Enabled: true, Priority: 100 - i, Weight: 1}
		if e = db.Create(&c).Error; e != nil {
			t.Fatal(e)
		}
	}
	s := New(testStore{db}, nil, Config{IdleTimeout: 80 * time.Millisecond, ResponseHeaderTimeout: time.Second})
	t.Cleanup(s.Close)
	return s, db
}
func model(endpoint v1.Endpoint, task v1.Task) v1.Model {
	contextWindow := int64(1024)
	return v1.Model{ID: "public", ContextWindow: &contextWindow, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{task: {Endpoints: []v1.Endpoint{endpoint}, PreferredEndpoint: endpoint}}, Pricing: []v1.Price{{Task: task, Meter: "requests", UnitSize: 1, Amount: 1, Currency: "USD"}}}
}
func serveBound(s *Service, w http.ResponseWriter, r *http.Request, request Request) {
	body, _ := io.ReadAll(r.Body)
	endpoint, task, _ := Route(r.URL.Path)
	if task == "chat" {
		fields, e := objectFields(body)
		if e == nil {
			name := "max_tokens"
			if endpoint == "openai-response" {
				name = "max_output_tokens"
			}
			if endpoint == "gemini" {
				if _, ok := fields["generationConfig"]; !ok {
					body, _ = replaceField(body, "generationConfig", []byte(`{"maxOutputTokens":20}`))
				}
			} else if _, ok := fields[name]; !ok {
				body, _ = replaceField(body, name, []byte("20"))
			}
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.Serve(w, r, request)
}
func execute(s *Service, path, body string, key int64, endpoint v1.Endpoint) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveBound(s, w, r, Request{Key: core.APIKey{ID: key}, Model: model(endpoint, "chat"), RequestID: "synthetic-request"})
	return w
}

func TestResponsesPreserveNativeBindBeforeFlushAndCrossKey(t *testing.T) {
	raw := "event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"future\":{\"signature\":\"Eq+/==\"}}}\r\n\r\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"future-encrypted\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"usage\":{\"input_tokens\":7,\"output_tokens\":9,\"output_tokens_details\":{\"reasoning_tokens\":4}}}}\n\n"
	var calls atomic.Int64
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		request, _ := io.ReadAll(r.Body)
		if !bytes.Contains(request, []byte(`"unknown" : [ 1, { "signature" : "opaque" } ]`)) {
			t.Error("unknown request value altered")
		}
		if !bytes.Contains(request, []byte(`"model":"private"`)) {
			t.Error("model alias missing")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-upstream-credential" || r.Header.Get("x-api-key") != "" {
			t.Error("native auth mismatch")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "forbidden=secret")
		w.Write([]byte(raw))
	}))
	defer u.Close()
	s, db := setup(t, u)
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"public","stream":true,"unknown" : [ 1, { "signature" : "opaque" } ]}`))
	r.Header.Set("Content-Type", "application/json")
	w := &bindingWriter{ResponseRecorder: httptest.NewRecorder(), db: db, t: t}
	serveBound(s, w, r, Request{Key: core.APIKey{ID: 1}, Model: model("openai-response", "chat")})
	if w.Body.String() != raw {
		t.Fatal("native stream bytes changed")
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("upstream cookie leaked")
	}
	var binding core.ResponseAffinity
	if db.First(&binding).Error != nil || binding.APIKeyID != 1 {
		t.Fatal("affinity not persisted")
	}
	cross := execute(s, "/v1/responses", `{"model":"public","previous_response_id":"resp_test"}`, 2, "openai-response")
	unknown := execute(s, "/v1/responses", `{"model":"public","previous_response_id":"unknown"}`, 2, "openai-response")
	if cross.Code != 400 || cross.Body.String() != unknown.Body.String() || calls.Load() != 1 {
		t.Fatal("cross-key affinity disclosure or dispatch")
	}
	// Recreate service against the same DB: durable state is authoritative.
	second := New(testStore{db}, nil, Config{})
	defer second.Close()
	continued := execute(second, "/v1/responses", `{"model":"public","stream":true,"previous_response_id":"resp_test","unknown" : [ 1, { "signature" : "opaque" } ]}`, 1, "openai-response")
	if continued.Code != 200 || calls.Load() != 2 {
		t.Fatal("durable response continuation failed")
	}
}

type bindingWriter struct {
	*httptest.ResponseRecorder
	db *gorm.DB
	t  *testing.T
}

func (w *bindingWriter) Write(raw []byte) (int, error) {
	if bytes.Contains(raw, []byte("response.created")) {
		var count int64
		w.db.Model(&core.ResponseAffinity{}).Count(&count)
		if count != 1 {
			w.t.Fatal("response.created flushed before durable binding")
		}
	}
	return w.ResponseRecorder.Write(raw)
}

func TestFailoverOnlyBeforeFirstByteAndIdleCancellation(t *testing.T) {
	var retryCalls atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"error":{"message":"overloaded"}}`))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retryCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"))
	}))
	defer good.Close()
	s, _ := setup(t, bad, good)
	w := execute(s, "/v1/chat/completions", `{"model":"public","stream":true}`, 1, "openai")
	if w.Code != 200 || retryCalls.Load() != 1 {
		t.Fatal("pre-byte failure did not fail over")
	}
	cancelled := make(chan struct{}, 1)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"first-byte\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer first.Close()
	s2, _ := setup(t, first, good)
	before := retryCalls.Load()
	stream := execute(s2, "/v1/chat/completions", `{"model":"public","stream":true}`, 1, "openai")
	if !strings.Contains(stream.Body.String(), "first-byte") || retryCalls.Load() != before {
		t.Fatal("failover after downstream bytes")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("idle timeout did not cancel upstream")
	}
}
func TestClientCancellationReachesUpstream(t *testing.T) {
	ready := make(chan struct{})
	cancelled := make(chan struct{})
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: ping\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer u.Close()
	s, _ := setup(t, u)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","stream":true}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		serveBound(s, httptest.NewRecorder(), r, Request{Key: core.APIKey{ID: 1}, Model: model("openai", "chat")})
		close(done)
	}()
	<-ready
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation failed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay retained cancelled worker")
	}
}

func TestNativeUsageSemanticsAndUnknownEvents(t *testing.T) {
	p := newUsage("anthropic")
	p.event([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1,\"cache_creation_input_tokens\":7,\"cache_read_input_tokens\":11,\"future_cache\":{\"x\":2}}}}\n\n"))
	p.event([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"))
	p.event([]byte("data: {\"type\":\"future-event\",\"signature\":\"opaque\"}\n\n"))
	p.event([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}\n\n"))
	p.event([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	a := p.finish(true)
	if a.InputTokens != 23 || a.OutputTokens != 9 || a.TotalTokens != 32 || !a.Complete || !strings.Contains(a.RawJSON, "future_cache") || strings.Contains(a.RawJSON, "signature") {
		t.Fatalf("bad cumulative/cache meter %+v", a)
	}
	g := newUsage("gemini")
	g.event([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"thoughtSignature\":\"opaque\"}}],\"usageMetadata\":{\"promptTokenCount\":23,\"candidatesTokenCount\":9,\"cachedContentTokenCount\":11,\"thoughtsTokenCount\":4,\"totalTokenCount\":36}}\n\n"))
	v := g.finish(true)
	if v.InputTokens != 23 || v.OutputTokens != 13 || v.TotalTokens != 36 || v.ReasoningTokens != 4 || !v.Complete || strings.Contains(v.RawJSON, "thoughtSignature") {
		t.Fatalf("bad Gemini usage %+v", v)
	}
}

func TestChatUsageInjectionDoesNotOverwriteExplicitOptions(t *testing.T) {
	n := NativeRequest{Endpoint: "openai", Stream: true, ContentType: "application/json", Body: []byte(`{ "model":"public", "unknown": { "signature":"s+/=" },"stream_options":{ "future" : [ 1,2 ] } }`)}
	body, _, e := rewriteBody(n, "private")
	if e != nil || !bytes.Contains(body, []byte(`"future" : [ 1,2 ]`)) || !bytes.Contains(body, []byte(`"include_usage":true`)) {
		t.Fatal("raw usage supplementation failed")
	}
	n.Body = []byte(`{"model":"public","stream_options":{"include_usage":false,"future":true}}`)
	body, _, e = rewriteBody(n, "private")
	if e != nil || !bytes.Contains(body, []byte(`"include_usage":false`)) {
		t.Fatal("explicit usage option changed")
	}
	n.Body = []byte(`{"model":"public","model":"other"}`)
	if _, _, e = rewriteBody(n, "private"); e == nil {
		t.Fatal("ambiguous duplicate key accepted")
	}
}

func TestMultipartFilesAndAzureNativePaths(t *testing.T) {
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	writer.WriteField("model", "public")
	image, _ := writer.CreateFormFile("image", "source.png")
	binary := []byte{0, 255, 1, 0, 254}
	image.Write(binary)
	writer.WriteField("future", "opaque")
	writer.Close()
	r := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(out.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	n, e := InspectRequest(r, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	rewritten, contentType, e := rewriteBody(n, "deployment")
	if e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(rewritten))
	r.Header.Set("Content-Type", contentType)
	if e = r.ParseMultipartForm(1 << 20); e != nil {
		t.Fatal(e)
	}
	if r.FormValue("model") != "deployment" || r.FormValue("future") != "opaque" {
		t.Fatal("multipart form values lost")
	}
	file, header, e := r.FormFile("image")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	got, _ := io.ReadAll(file)
	if header.Filename != "source.png" || !bytes.Equal(got, binary) {
		t.Fatal("multipart file altered")
	}
	address, e := upstreamURL("https://azure.example", NativeRequest{Endpoint: "openai"}, "/v1/chat/completions", "deployment", "azure", "2025-01-01-preview")
	if e != nil || address.Path != "/openai/deployments/deployment/chat/completions" || address.Query().Get("api-version") != "2025-01-01-preview" {
		t.Fatal("bad Azure native URL")
	}
	address, e = upstreamURL("https://azure.example", NativeRequest{Endpoint: "openai-response"}, "/v1/responses", "deployment", "azure", "2025-01-01-preview")
	if e != nil || address.Path != "/openai/responses" {
		t.Fatal("bad Azure Responses URL")
	}
	address, e = upstreamURL("https://azure.example/openai/v1", NativeRequest{Endpoint: "openai-response"}, "/v1/responses", "deployment", "azure", "")
	if e != nil || address.Path != "/openai/v1/responses" || address.RawQuery != "" {
		t.Fatal("bad current Azure v1 Responses URL")
	}
	address, e = upstreamURL("https://azure.example", NativeRequest{Endpoint: "openai"}, "/v1/chat/completions", "deployment", "azure", "v1")
	if e != nil || address.Path != "/openai/v1/chat/completions" || address.Query().Get("api-version") != "v1" {
		t.Fatal("bad current Azure v1 Chat URL")
	}
}

func TestRedirectAndCredentialEchoProtection(t *testing.T) {
	var followed atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer target.Close()
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer u.Close()
	s, _ := setup(t, u)
	w := execute(s, "/v1/responses", `{"model":"public"}`, 1, "openai-response")
	if w.Code != 502 || followed.Load() != 0 || w.Header().Get("Location") != "" {
		t.Fatal("redirect followed or forwarded")
	}
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"synthetic-upstream-credential invalid","future":42}}`))
	}))
	defer echo.Close()
	s, _ = setup(t, echo)
	w = execute(s, "/v1/chat/completions", `{"model":"public"}`, 1, "openai")
	if w.Code != 401 || strings.Contains(w.Body.String(), "synthetic-upstream-credential") || !strings.Contains(w.Body.String(), `"future":42`) {
		t.Fatal("native error redaction lost unknown fields or leaked key")
	}
}

func TestBillingFinalUsageVersusInterruptedHold(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"))
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	s.billing = billing.New(db)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","stream":true}`))
	r.Header.Set("Content-Type", "application/json")
	serveBound(s, httptest.NewRecorder(), r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model("openai", "chat"), RequestID: "settled"})
	var hold core.Reservation
	db.First(&hold, "request_id = ?", "settled")
	if hold.State != "settled" || hold.ActualTokens != 5 || strings.Contains(hold.UsageJSON, "choices") {
		t.Fatalf("usage not settled %+v", hold)
	}
	idle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"private-content\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer idle.Close()
	db.Model(&core.Channel{}).Where("id = ?", 1).Update("base_url", idle.URL)
	r = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","stream":true}`))
	r.Header.Set("Content-Type", "application/json")
	serveBound(s, httptest.NewRecorder(), r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model("openai", "chat"), RequestID: "unknown"})
	hold = core.Reservation{}
	db.First(&hold, "request_id = ?", "unknown")
	var partial core.Usage
	json.Unmarshal([]byte(hold.UsageJSON), &partial)
	if hold.State != "reconciliation" || partial.Complete || partial.RawJSON != "" || strings.Contains(hold.UsageJSON, "private-content") {
		t.Fatalf("unknown accepted work did not hold %+v", hold)
	}
	var user core.User
	db.First(&user, 1)
	if user.Balance != 99 || user.ReservedBalance != 1 {
		t.Fatal("interruption fabricated charges or released accepted hold")
	}
}

func TestErrorEnvelopeGeminiPreservesIntegerCode(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1beta/models/public:generateContent", nil)
	WriteError(w, r, &Error{402, "insufficient_balance", "No balance"}, "https://operator.example/buy")
	var body map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &body)
	var native struct {
		Code    int `json:"code"`
		Details []struct {
			Metadata map[string]string `json:"metadata"`
		} `json:"details"`
	}
	if json.Unmarshal(body["error"], &native) != nil || native.Code != 402 || native.Details[0].Metadata["nomifun_code"] != "insufficient_balance" {
		t.Fatal("Gemini business envelope is non-native")
	}
}

func TestNativeAnthropicAndGeminiHTTPStreams(t *testing.T) {
	fixtures := []struct {
		kind, path, body, stream string
		endpoint                 v1.Endpoint
	}{
		{"anthropic", "/v1/messages", `{"model":"public","stream":true,"max_tokens":20,"thinking":{"type":"enabled","budget_tokens":10},"messages":[{"role":"user","content":"synthetic"}],"future_signature":"opaque+/="}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":5,\"cache_creation_input_tokens\":7,\"cache_read_input_tokens\":11,\"output_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"Eq+/==opaque\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "anthropic"},
		{"gemini", "/v1beta/models/public:streamGenerateContent?key=synthetic-user-key", `{"contents":[{"parts":[{"text":"synthetic","thoughtSignature":"signed+/="}]}],"generationConfig":{"maxOutputTokens":20},"future_signature":"opaque+/="}`, "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"tool\",\"args\":{\"a\":1}},\"thoughtSignature\":\"signed+/=\"}]}}],\"usageMetadata\":{\"promptTokenCount\":23,\"candidatesTokenCount\":9,\"cachedContentTokenCount\":11,\"thoughtsTokenCount\":4,\"totalTokenCount\":36}}\n\n", "gemini"},
	}
	for _, f := range fixtures {
		t.Run(f.kind, func(t *testing.T) {
			u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Contains(raw, []byte(`"future_signature":"opaque+/="`)) {
					t.Error("unknown native field altered")
				}
				if r.Header.Get("X-NomiFun-Session-ID") != "" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("key") != "" {
					t.Error("user credentials/session sent upstream")
				}
				if f.kind == "anthropic" {
					if r.Header.Get("x-api-key") != "synthetic-upstream-credential" || r.Header.Get("anthropic-version") != "2023-06-01" || !bytes.Contains(raw, []byte(`"model":"private"`)) {
						t.Error("wrong Anthropic native credential/version/model")
					}
				} else {
					if r.Header.Get("x-goog-api-key") != "synthetic-upstream-credential" || r.URL.Path != "/v1beta/models/private:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
						t.Error("wrong Gemini native route/auth")
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(f.stream))
			}))
			defer u.Close()
			s, db := setup(t, u)
			db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": f.kind, "endpoints_json": `["` + string(f.endpoint) + `"]`})
			r := httptest.NewRequest("POST", f.path, strings.NewReader(f.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer synthetic-user-key")
			if f.kind == "anthropic" {
				r.Header.Set("X-NomiFun-Session-ID", "fresh-session")
			}
			w := httptest.NewRecorder()
			serveBound(s, w, r, Request{Key: core.APIKey{ID: 1}, Model: model(f.endpoint, "chat")})
			if w.Code != 200 || w.Body.String() != f.stream {
				t.Fatalf("native stream changed status %d body %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProductionNonChatNativeEndpoints(t *testing.T) {
	for _, f := range []struct {
		path     string
		endpoint v1.Endpoint
		task     v1.Task
		output   string
	}{{"/v1/embeddings", "embeddings", "embedding", `{"data":[{"embedding":[0.1,0.2],"future":true}],"usage":{"prompt_tokens":3,"total_tokens":3}}`}, {"/v1/rerank", "jina-rerank", "rerank", `{"results":[{"index":1,"relevance_score":0.8}],"usage":{"total_tokens":7}}`}, {"/v1/images/generations", "image-generation", "image_generation", `{"data":[{"b64_json":"synthetic-image","future":true}]}`}} {
		t.Run(f.path, func(t *testing.T) {
			u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != f.path {
					t.Error("wrong native endpoint")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(f.output))
			}))
			defer u.Close()
			s, _ := setup(t, u)
			r := httptest.NewRequest("POST", f.path, strings.NewReader(`{"model":"public","input":"synthetic","future":{"opaque":"signature"}}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			serveBound(s, w, r, Request{Key: core.APIKey{ID: 1}, Model: model(f.endpoint, f.task)})
			if w.Code != 200 || w.Body.String() != f.output {
				t.Fatalf("native result changed %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMalformedUsageNeverBecomesFreeConfirmedUsage(t *testing.T) {
	p := newUsage("openai")
	p.event([]byte("data: {\"usage\":{\"prompt_tokens\":\"3\",\"completion_tokens\":4}}\n\n"))
	p.event([]byte("data: [DONE]\n\n"))
	if p.finish(true).Complete {
		t.Fatal("malformed upstream counts were converted to zero")
	}
	unknown := newUsage("openai")
	unknown.event([]byte("data: {\"usage\":{\"future\":{\"opaque\":true}}}\n\n"))
	unknown.event([]byte("data: [DONE]\n\n"))
	if unknown.finish(true).Complete {
		t.Fatal("unknown usage fields fabricated complete zero bill")
	}
}

func TestVerifiedNativeRejectionReleasesHold(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid synthetic credential","future":1}}`))
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	s.billing = billing.New(db)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","stream":true}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveBound(s, w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model("openai", "chat"), RequestID: "rejected"})
	var hold core.Reservation
	if e := db.First(&hold, "request_id = ?", "rejected").Error; e != nil {
		t.Fatal(e)
	}
	var user core.User
	db.First(&user, 1)
	if w.Code != 401 || hold.State != "released" || user.ReservedBalance != 0 || user.Balance != 100 {
		t.Fatalf("known rejection charged or held: %d %+v %+v", w.Code, hold, user)
	}
}

func TestPartialUsagePersistsOnlyMeterEvidence(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"private-not-stored\"}}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"future_usage\":1}}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	s.billing = billing.New(db)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","stream":true}`))
	r.Header.Set("Content-Type", "application/json")
	serveBound(s, httptest.NewRecorder(), r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model("openai", "chat"), RequestID: "partial"})
	var hold core.Reservation
	db.First(&hold, "request_id = ?", "partial")
	var usage core.Usage
	if json.Unmarshal([]byte(hold.UsageJSON), &usage) != nil {
		t.Fatal("missing partial usage evidence")
	}
	if hold.State != "reconciliation" || usage.Complete || usage.InputTokens != 2 || usage.OutputTokens != 3 || !strings.Contains(usage.RawJSON, "future_usage") || strings.Contains(hold.UsageJSON, "private-not-stored") {
		t.Fatalf("partial evidence fabricated or retained content %+v", hold)
	}
}

func TestGenerationAdmissionRequiresExplicitNativeCeilingAndUsage(t *testing.T) {
	var calls atomic.Int64
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer u.Close()
	s, db := setup(t, u)
	for _, f := range []struct {
		path, body string
		endpoint   v1.Endpoint
	}{{"/v1/chat/completions", `{"model":"public"}`, "openai"}, {"/v1/chat/completions", `{"model":"public","max_output_tokens":20}`, "openai"}, {"/v1/responses", `{"model":"public","max_tokens":20}`, "openai-response"}, {"/v1/messages", `{"model":"public","max_output_tokens":20}`, "anthropic"}, {"/v1beta/models/public:generateContent", `{"contents":[],"max_output_tokens":20}`, "gemini"}, {"/v1/chat/completions", `{"model":"public","max_tokens":20,"stream":true,"stream_options":{"include_usage":false}}`, "openai"}, {"/v1/chat/completions", `{"model":"public","max_tokens":20,"n":0}`, "openai"}, {"/v1/chat/completions", `{"model":"public","max_tokens":20,"n":17}`, "openai"}} {
		r := httptest.NewRequest("POST", f.path, strings.NewReader(f.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Serve(w, r, Request{Key: core.APIKey{ID: 1}, Model: model(f.endpoint, "chat")})
		if w.Code != 400 {
			t.Fatalf("unbounded or unmetered native admission %s: %d %s", f.body, w.Code, w.Body.String())
		}
	}
	var holds int64
	db.Model(&core.Reservation{}).Count(&holds)
	if calls.Load() != 0 || holds != 0 {
		t.Fatal("invalid admission dispatched or reserved before rejection")
	}
}

func TestChatMultipleCompletionLimitIsReservedAndPreserved(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		fields := object(raw)
		if integer(fields, "n") != 3 || integer(fields, "max_tokens") != 20 {
			t.Error("native completion count or limit altered")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":30}}`))
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	s.billing = billing.New(db)
	body := `{"model":"public","max_tokens":20,"n":3}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: model("openai", "chat"), RequestID: "multiple"})
	var hold core.Reservation
	db.First(&hold, "request_id = ?", "multiple")
	if w.Code != 200 || hold.ReservedTokens != int64(len(body))+60 || hold.State != "settled" {
		t.Fatalf("multiple completion output hold wrong %d %+v", w.Code, hold)
	}
}

func TestMultipartImageCountTenReservesAndSettlesTen(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
		}
		if r.FormValue("n") != "10" || r.FormValue("model") != "private" {
			t.Error("native multipart n/model changed")
		}
		data := make([]map[string]string, 10)
		for i := range data {
			data[i] = map[string]string{"b64_json": "synthetic"}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Create(&core.User{ID: 1, Balance: 100, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	s.billing = billing.New(db)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("model", "public")
	writer.WriteField("n", "10")
	file, _ := writer.CreateFormFile("image", "synthetic.png")
	file.Write([]byte{0, 255, 1})
	writer.Close()
	r := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	m := model("image-generation", "image_edit")
	m.Pricing = []v1.Price{{Task: "image_edit", Meter: "images", UnitSize: 1, Amount: 1, Currency: "USD"}}
	w := httptest.NewRecorder()
	s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: m, RequestID: "images-ten"})
	var hold core.Reservation
	db.First(&hold, "request_id = ?", "images-ten")
	var user core.User
	db.First(&user, 1)
	if w.Code != 200 || hold.ReservedAmount != 10 || hold.ActualAmount != 10 || user.Balance != 90 || hold.State != "settled" {
		t.Fatalf("multipart n=10 was under-reserved or charged %d %+v %+v", w.Code, hold, user)
	}
	for _, value := range []string{"0", "17", "2.5", "not-a-number"} {
		var input bytes.Buffer
		mw := multipart.NewWriter(&input)
		mw.WriteField("model", "public")
		mw.WriteField("n", value)
		mw.Close()
		request := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(input.Bytes()))
		request.Header.Set("Content-Type", mw.FormDataContentType())
		if _, e := InspectRequest(request, 1<<20); e == nil {
			t.Fatalf("invalid multipart image count %q accepted", value)
		}
	}
}

func TestAzureV1ResponsesHTTPNativeAuthentication(t *testing.T) {
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/openai/v1/responses" || r.URL.RawQuery != "" || r.Header.Get("api-key") != "synthetic-upstream-credential" || r.Header.Get("Authorization") != "" || !bytes.Contains(body, []byte(`"model":"private"`)) || !bytes.Contains(body, []byte(`"max_output_tokens":20`)) {
			t.Error("Azure v1 native request path, auth, model or limit is wrong")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"azure_response","output":[{"type":"reasoning","encrypted_content":"opaque+/="}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer u.Close()
	s, db := setup(t, u)
	db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": "azure", "base_url": u.URL + "/openai/v1"})
	w := execute(s, "/v1/responses", `{"model":"public","max_output_tokens":20}`, 1, "openai-response")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "opaque+/=") {
		t.Fatal("Azure native response changed")
	}
	var affinity core.ResponseAffinity
	if db.First(&affinity, "response_id = ?", "azure_response").Error != nil || affinity.APIKeyID != 1 {
		t.Fatal("Azure response binding missing")
	}
}

func TestResponsesContinuationReservesImplicitContextBeforeUpstream(t *testing.T) {
	var db *gorm.DB
	var calls atomic.Int64
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var pending core.Reservation
		if e := db.First(&pending, "request_id = ?", "chain-context").Error; e != nil {
			t.Error(e)
		}
		if pending.ReservedTokens != 1020 || pending.ReservedAmount != 1020 || pending.State != "reserved" {
			t.Errorf("implicit upstream context not reserved before dispatch: %+v", pending)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"continued","output":[],"usage":{"input_tokens":800,"output_tokens":10}}`))
	}))
	defer u.Close()
	s, opened := setup(t, u)
	db = opened
	s.billing = billing.New(db)
	db.Create(&core.User{ID: 1, Balance: 2000, Currency: "USD"})
	db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
	if e := s.Channels.BindResponse(context.Background(), 1, "prior", 1); e != nil {
		t.Fatal(e)
	}
	m := model("openai-response", "chat")
	contextWindow := int64(1000)
	m.ContextWindow = &contextWindow
	m.Pricing = []v1.Price{{Task: "chat", Meter: "input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}, {Task: "chat", Meter: "output_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}}
	invoke := func(id string, m v1.Model) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"public","max_output_tokens":20,"previous_response_id":"prior","input":"tiny follow-up"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: m, RequestID: id})
		return w
	}
	w := invoke("chain-context", m)
	var hold core.Reservation
	db.First(&hold, "request_id = ?", "chain-context")
	var user core.User
	db.First(&user, 1)
	if w.Code != 200 || calls.Load() != 1 || hold.State != "settled" || hold.ActualAmount != 810 || hold.ActualTokens != 810 || user.Balance != 1190 || user.ReservedBalance != 0 {
		t.Fatalf("context upper bound did not settle exactusage/release unused hold: %d %+v %+v", w.Code, hold, user)
	}
	quota := int64(900)
	db.Model(&core.APIKey{}).Where("id = ?", 1).Updates(map[string]any{"quota_limit": quota, "quota_used": 0})
	w = invoke("chain-quota-denied", m)
	if w.Code != 403 || calls.Load() != 1 {
		t.Fatalf("prior context bypassed key quota: %d %s", w.Code, w.Body.String())
	}
	db.Model(&core.APIKey{}).Where("id = ?", 1).Update("quota_limit", nil)
	db.Model(&core.User{}).Where("id = ?", 1).Update("balance", 900)
	w = invoke("chain-wallet-denied", m)
	if w.Code != 402 || calls.Load() != 1 {
		t.Fatalf("prior context bypassed wallet hold: %d %s", w.Code, w.Body.String())
	}
	m.ContextWindow = nil
	w = invoke("chain-missing-context", m)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "missing_context_bound") || calls.Load() != 1 {
		t.Fatal("missing model context admitted stored history")
	}
	var denied int64
	db.Model(&core.Reservation{}).Where("request_id IN ?", []string{"chain-quota-denied", "chain-wallet-denied", "chain-missing-context"}).Count(&denied)
	if denied != 0 {
		t.Fatal("denied continuation retained a reservation")
	}
}

func TestOpaqueInputClassificationIsContentScoped(t *testing.T) {
	for _, body := range []string{`{"messages":[{"role":"user","content":"plain text"}],"tools":[{"type":"function","function":{"name":"upload","parameters":{"type":"object","properties":{"image_url":{"type":"string"},"file_id":{"type":"string"}}}}}]}`, `{"input":[{"role":"user","content":[{"type":"input_text","text":"plain"}]}],"system":"plain"}`, `{"contents":[{"role":"user","parts":[{"text":"plain"}]}],"tools":[{"inlineData":"schema-only"}]}`, `{"system":[{"type":"text","text":"plain","cache_control":{"type":"ephemeral"}}]}`} {
		if hasOpaqueInput([]byte(body)) {
			t.Fatalf("plain text or tool schema falsely opaque: %s", body)
		}
	}
	for _, body := range []string{`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/a.png"}}]}]}`, `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"tiny-compressed-data"}}]}]}`, `{"input":[{"type":"input_file","file_id":"opaque"}]}`, `{"input":[{"type":"unknown_vendor_part","text":"could-be-opaque"}]}`, `{"input":[{"type":"text","text":"plain","encrypted_content":"opaque"}]}`, `{"contents":[{"parts":[{"text":"plain","thoughtSignature":"opaque"}]}]}`, `{"cachedContent":"cached/context"}`, `{"conversation":{"id":"stored-context"}}`, `{"state":"opaque"}`} {
		if !hasOpaqueInput([]byte(body)) {
			t.Fatalf("opaque content not bounded: %s", body)
		}
	}
	missing := model("openai", "chat")
	missing.ContextWindow = nil
	if _, e := EstimateUsage(NativeRequest{Endpoint: "openai", Task: "chat", Count: 1, MaxOutput: 20, Body: []byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image"}}]}]}`)}, missing); e == nil {
		t.Fatal("opaque content with unknown capacity admitted")
	}
}

func TestURLAndCompressedInlineImageRequireFullContextAdmission(t *testing.T) {
	for _, f := range []struct {
		name             string
		endpoint         v1.Endpoint
		kind, path, body string
	}{
		{"image-url", "openai", "openai", "/v1/chat/completions", `{"model":"public","max_tokens":20,"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"https://example.invalid/tiny-url.png"}}]}],"future" : { "signature" : "unchanged+/=" }}`},
		{"compressed-inline", "gemini", "gemini", "/v1beta/models/public:generateContent", `{"generationConfig":{"maxOutputTokens":20},"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}]}],"future" : { "signature" : "unchanged+/=" }}`},
	} {
		t.Run(f.name, func(t *testing.T) {
			var db *gorm.DB
			var calls atomic.Int64
			u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Contains(raw, []byte(`"future" : { "signature" : "unchanged+/=" }`)) {
					t.Error("opaque native request/unknown value changed")
				}
				var pending core.Reservation
				if db.Where("state = ?", "reserved").First(&pending).Error != nil || pending.ReservedTokens != 1020 || pending.ReservedAmount != 1020 {
					t.Errorf("opaque context not fully reserved before dispatch %+v", pending)
				}
				w.Header().Set("Content-Type", "application/json")
				if f.kind == "gemini" {
					w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":800,"candidatesTokenCount":10,"totalTokenCount":810}}`))
				} else {
					w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":800,"completion_tokens":10}}`))
				}
			}))
			defer u.Close()
			s, opened := setup(t, u)
			db = opened
			s.billing = billing.New(db)
			db.Model(&core.Channel{}).Where("id = ?", 1).Updates(map[string]any{"kind": f.kind, "endpoints_json": `["` + string(f.endpoint) + `"]`})
			db.Create(&core.User{ID: 1, Balance: 900, Currency: "USD"})
			db.Create(&core.APIKey{ID: 1, UserID: 1, ModelIDsJSON: "[]"})
			m := model(f.endpoint, "chat")
			bound := int64(1000)
			m.ContextWindow = &bound
			m.Pricing = []v1.Price{{Task: "chat", Meter: "input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}, {Task: "chat", Meter: "output_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}}
			invoke := func(id string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", f.path, strings.NewReader(f.body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				s.Serve(w, r, Request{Key: core.APIKey{ID: 1, UserID: 1}, Model: m, RequestID: id})
				return w
			}
			w := invoke("opaque-wallet-denied")
			if w.Code != 402 || calls.Load() != 0 {
				t.Fatalf("small opaque body bypassed fullcontext wallet: %d %s", w.Code, w.Body.String())
			}
			db.Model(&core.User{}).Where("id = ?", 1).Update("balance", 2000)
			db.Model(&core.APIKey{}).Where("id = ?", 1).Update("quota_limit", 900)
			w = invoke("opaque-quota-denied")
			if w.Code != 403 || calls.Load() != 0 {
				t.Fatalf("opaque body bypassed fullcontext keyquota: %d %s", w.Code, w.Body.String())
			}
			db.Model(&core.APIKey{}).Where("id = ?", 1).Update("quota_limit", nil)
			w = invoke("opaque-accepted")
			var hold core.Reservation
			db.First(&hold, "request_id = ?", "opaque-accepted")
			if w.Code != 200 || calls.Load() != 1 || hold.ReservedTokens != 1020 || hold.State != "settled" || hold.ActualTokens != 810 || hold.ActualAmount != 810 {
				t.Fatalf("opaque conservative hold/exactsettlement wrong: %d %+v", w.Code, hold)
			}
			var failedHolds int64
			db.Model(&core.Reservation{}).Where("request_id IN ?", []string{"opaque-wallet-denied", "opaque-quota-denied"}).Count(&failedHolds)
			if failedHolds != 0 {
				t.Fatal("denied opaque requests left financial holds")
			}
		})
	}
}
