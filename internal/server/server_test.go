// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	consoleassets "github.com/nomifun/nomifun-model-gateway/internal/console"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/payment"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
)

type fixture struct {
	app   *Server
	st    *store.Store
	admin string
	log   *bytes.Buffer
	calls *atomic.Int64
}

func setupServer(t *testing.T) fixture {
	t.Helper()
	st, err := store.Open(store.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gateway.db"), MasterKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err = st.BootstrapAdmin(ctx, "admin@example.test", "SyntheticAdministrator123!"); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.RegistrationEnabled = true
	purchase := "https://operator.example/purchase"
	settings.Operator.PurchaseURL = &purchase
	b, _ := json.Marshal(settings)
	if err = st.SetSetting(ctx, "instance", b); err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-upstream" {
			t.Error("instance API key leaked or native auth failed")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"model":"private"`)) {
			t.Error("native model alias was not rewritten")
		}
		if !bytes.Contains(body, []byte(`"future":{"opaque":"signature"}`)) {
			t.Error("native unknown content changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat_test","choices":[],"future":{"signature":"opaque"},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`))
	}))
	t.Cleanup(upstream.Close)
	_, err = st.CreateChannel(ctx, core.Channel{Name: "synthetic", Kind: "openai", BaseURL: upstream.URL, ModelsJSON: `{"public":"private","premium":"private"}`, EndpointsJSON: `["openai"]`, Weight: 1, Enabled: true}, "synthetic-upstream")
	if err != nil {
		t.Fatal(err)
	}
	maximum := int64(20)
	model := v1.Model{ID: "public", DisplayName: "Synthetic", Vendor: "openai", Tasks: []v1.Task{v1.Chat}, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}, MaxOutputTokens: &maximum, InputModalities: []string{"text"}, Traits: []string{}, Pricing: []v1.Price{{Task: v1.Chat, Meter: "requests", UnitSize: 1, Amount: 2, Currency: "USD"}}, Status: "available"}
	if err = st.SaveModelAccess(ctx, model, true, false); err != nil {
		t.Fatal(err)
	}
	premium := model
	premium.ID = "premium"
	if err = st.SaveModelAccess(ctx, premium, true, true); err != nil {
		t.Fatal(err)
	}
	bill := billing.New(st.DB())
	native := relay.New(st, bill, relay.Config{})
	t.Cleanup(native.Close)
	configs := payment.NewConfigRepository(st)
	pay := payment.New(st.DB(), bill, configs)
	logs := &bytes.Buffer{}
	app := New(st, bill, native, pay, configs, Options{Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	_, admin, err := st.Login(ctx, "admin@example.test", "SyntheticAdministrator123!")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{app, st, admin, logs, count}
}
func call(t *testing.T, app *Server, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:24680"
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Header().Get("x-request-id") == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("required response headers missing for %s", path)
	}
	return response
}
func document(t *testing.T, response *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not JSON: %d", response.Code)
	}
	return doc
}
func stringField(t *testing.T, doc map[string]json.RawMessage, field string) string {
	t.Helper()
	var value string
	if json.Unmarshal(doc[field], &value) != nil {
		t.Fatalf("missing string field %s", field)
	}
	return value
}

func TestRealConsoleNativeBillingAndOwnership(t *testing.T) {
	f := setupServer(t)
	registration := call(t, f.app, "POST", "/api/console/v1/register", `{"email":"user@example.test","name":"Synthetic User","password":"SyntheticUserPassword123!"}`, "")
	if registration.Code != 201 {
		t.Fatalf("register: %d %s", registration.Code, registration.Body.String())
	}
	registered := document(t, registration)
	session := stringField(t, registered, "session_token")
	var owner core.User
	if json.Unmarshal(registered["user"], &owner) != nil {
		t.Fatal("missing user")
	}
	create := call(t, f.app, "POST", "/api/console/v1/keys", `{"name":"Development","quota_limit":null,"model_ids":["public","premium"],"allowed_ips":["127.0.0.1"],"requests_per_minute":100,"tokens_per_minute":null,"concurrent_requests":2,"expires_at":null}`, session)
	if create.Code != 201 {
		t.Fatalf("key create: %d %s", create.Code, create.Body.String())
	}
	apiKey := stringField(t, document(t, create), "api_key")
	keys := call(t, f.app, "GET", "/api/console/v1/keys", "", session)
	if strings.Contains(keys.Body.String(), apiKey) || strings.Contains(keys.Body.String(), "\"hash\"") {
		t.Fatal("plaintext/hash key disclosure")
	}
	// Exact JSON minor units beyond JavaScript Number precision survive credit,
	// billing, account and ledger without a float or a string amount concession.
	const amount = int64(9007199254740993)
	credit := call(t, f.app, "POST", "/api/console/v1/admin/users/"+strconv.FormatInt(owner.ID, 10)+"/credit", `{"amount":9007199254740993,"currency":"USD","reason":"synthetic test credit"}`, f.admin)
	if credit.Code != 200 {
		t.Fatalf("credit: %d %s", credit.Code, credit.Body.String())
	}
	for _, path := range []string{"/nomifun/v1/account", "/nomifun/v1/catalog", "/v1/models"} {
		response := call(t, f.app, "GET", path, "", apiKey)
		if response.Code != 200 {
			t.Fatalf("query %s: %d", path, response.Code)
		}
	}
	var before int64
	f.st.DB().Model(&core.Reservation{}).Count(&before)
	if before != 0 {
		t.Fatal("control queries charged inference")
	}
	response := call(t, f.app, "POST", "/v1/chat/completions", `{"model":"public","max_tokens":10,"messages":[],"future":{"opaque":"signature"}}`, apiKey)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"signature":"opaque"`) {
		t.Fatalf("native: %d %s", response.Code, response.Body.String())
	}
	u, err := f.st.User(context.Background(), owner.ID)
	if err != nil || u.Balance != amount-2 || u.ReservedBalance != 0 {
		t.Fatalf("wallet settled incorrectly: %+v, %v", u, err)
	}
	var usage core.Reservation
	if f.st.DB().First(&usage).Error != nil || usage.State != "settled" || usage.ActualTokens != 5 || usage.ActualAmount != 2 {
		t.Fatalf("settlement %+v", usage)
	}
	if f.calls.Load() != 1 {
		t.Fatal("unexpected native dispatch count")
	}
	premium := call(t, f.app, "POST", "/v1/chat/completions", `{"model":"premium","max_tokens":10,"messages":[],"future":{"opaque":"signature"}}`, apiKey)
	if premium.Code != 403 || !strings.Contains(premium.Body.String(), "subscription_expired") {
		t.Fatalf("premium admission business error: %d %s", premium.Code, premium.Body.String())
	}
	if f.calls.Load() != 1 {
		t.Fatal("subscription-denied request dispatched")
	}
	denied := call(t, f.app, "GET", "/api/console/v1/admin/users", "", session)
	if denied.Code != 403 {
		t.Fatal("ordinary user admin access")
	}
	var ownKeys []core.APIKey
	f.st.DB().Where("user_id = ?", owner.ID).Find(&ownKeys)
	otherKey, _, err := f.st.CreateAPIKey(context.Background(), 1, store.KeyInput{Name: "Admin key"})
	if err != nil {
		t.Fatal(err)
	}
	idor := call(t, f.app, "DELETE", "/api/console/v1/keys/"+strconvID(otherKey.ID), "", session)
	if idor.Code != 404 {
		t.Fatal("key ownership bypass")
	}
	wrongDomain := call(t, f.app, "GET", "/api/console/v1/me", "", apiKey)
	if wrongDomain.Code != 401 {
		t.Fatal("API key accepted as login session")
	}
	wrongAPI := call(t, f.app, "GET", "/nomifun/v1/account", "", session)
	if wrongAPI.Code != 401 {
		t.Fatal("login session accepted as API key")
	}
	logout := call(t, f.app, "POST", "/api/console/v1/logout", `{}`, session)
	if logout.Code != 200 {
		t.Fatal("logout failed")
	}
	revoked := call(t, f.app, "GET", "/api/console/v1/me", "", session)
	if revoked.Code != 401 {
		t.Fatal("logout did not revoke durable session")
	}
	if strings.Contains(f.log.String(), apiKey) || strings.Contains(f.log.String(), session) || strings.Contains(f.log.String(), "synthetic-upstream") || strings.Contains(f.log.String(), "SyntheticUserPassword") || strings.Contains(f.log.String(), "opaque") {
		t.Fatal("secret/native content leaked into telemetry")
	}
}

func TestAuthenticationQueryAndNativeErrorBoundaries(t *testing.T) {
	f := setupServer(t)
	_, apiKey, err := f.st.CreateAPIKey(context.Background(), 1, store.KeyInput{Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []http.Header{
		{"Authorization": []string{"Bearer " + apiKey, "Bearer " + apiKey}},
		{"Authorization": []string{"Bearer  " + apiKey}},
		{"Authorization": []string{"Bearer " + apiKey}, "X-Api-Key": []string{"different"}},
		{"X-Api-Key": []string{""}},
	} {
		r := httptest.NewRequest("GET", "/nomifun/v1/account", nil)
		r.Header = headers
		w := httptest.NewRecorder()
		f.app.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("ambiguous header accepted: %d", w.Code)
		}
	}
	for _, name := range []string{"Authorization", "x-api-key", "x-goog-api-key"} {
		r := httptest.NewRequest("GET", "/nomifun/v1/account", nil)
		value := apiKey
		if name == "Authorization" {
			value = "bEaReR " + apiKey
		}
		r.Header.Set(name, value)
		w := httptest.NewRecorder()
		f.app.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("supported key header rejected: %s %d", name, w.Code)
		}
	}
	meta := call(t, f.app, "GET", "/nomifun/v1/meta?key="+apiKey, "", "")
	if meta.Code != 401 || strings.Contains(f.log.String(), apiKey) {
		t.Fatal("anonymous meta query credential leaked/accepted")
	}
	native := call(t, f.app, "POST", "/v1beta/models/public:generateContent?key="+apiKey, `{"contents":[],"generationConfig":{"maxOutputTokens":10}}`, "")
	if native.Code != 400 || !strings.Contains(native.Body.String(), `"code":400`) || !strings.Contains(native.Body.String(), "UNSUPPORTED_ENDPOINT") {
		t.Fatalf("Gemini auth/native envelope: %d %s", native.Code, native.Body.String())
	}
	anthropic := call(t, f.app, "POST", "/v1/messages", `{"model":"public"}`, "invalid")
	if anthropic.Code != 401 || !strings.Contains(anthropic.Body.String(), `"type":"error"`) {
		t.Fatal("Anthropic envelope missing")
	}
	expired := time.Now().Add(-time.Hour)
	var key core.APIKey
	f.st.DB().Where("user_id = ?", 1).First(&key)
	f.st.DB().Model(&key).Update("expires_at", expired)
	expiredResult := call(t, f.app, "GET", "/nomifun/v1/account", "", apiKey)
	if expiredResult.Code != 401 || !strings.Contains(expiredResult.Body.String(), "key_expired") {
		t.Fatal("expired key business error lost")
	}
}

func TestConsoleIntegerSettingsRolesAndRateLimits(t *testing.T) {
	f := setupServer(t)
	float := call(t, f.app, "POST", "/api/console/v1/admin/users/1/credit", `{"amount":1.5,"currency":"USD","reason":"test"}`, f.admin)
	if float.Code != 400 {
		t.Fatal("float money accepted")
	}
	overflow := call(t, f.app, "POST", "/api/console/v1/admin/users/1/credit", `{"amount":9223372036854775808,"currency":"USD","reason":"test"}`, f.admin)
	if overflow.Code != 400 {
		t.Fatal("overflow money accepted")
	}
	role := call(t, f.app, "PATCH", "/api/console/v1/admin/users/1", `{"admin":false}`, f.admin)
	if role.Code != 400 {
		t.Fatal("self-demotion was accepted")
	}
	settings := defaultSettings()
	httpPurchase := "http://operator.example/purchase"
	settings.Operator.PurchaseURL = &httpPurchase
	b, _ := json.Marshal(settings)
	invalidSettings := call(t, f.app, "PUT", "/api/console/v1/admin/settings", string(b), f.admin)
	if invalidSettings.Code != 400 {
		t.Fatal("insecure operator link accepted")
	}
	r := httptest.NewRequest("POST", "/api/console/v1/login", strings.NewReader(`{"email":"admin@example.test","password":"SyntheticAdministrator123!"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin credential submission accepted")
	}
	zero := int64(0)
	_, zeroKey, err := f.st.CreateAPIKey(context.Background(), 1, store.KeyInput{Name: "Blocked", RequestsPerMinute: &zero})
	if err != nil {
		t.Fatal(err)
	}
	blocked := call(t, f.app, "POST", "/v1/chat/completions", `{"model":"public","max_tokens":10,"messages":[],"future":{"opaque":"signature"}}`, zeroKey)
	if blocked.Code != 429 || blocked.Header().Get("Retry-After") == "" || f.calls.Load() != 0 {
		t.Fatal("zero rate limit ignored")
	}
	account := call(t, f.app, "GET", "/nomifun/v1/account", "", zeroKey)
	if account.Code != 200 {
		t.Fatal("rate limit blocked uncharged account query")
	}
	metrics := call(t, f.app, "GET", "/metrics?diagnostic=private", "", "")
	if !strings.Contains(metrics.Body.String(), `route="/v1/chat/completions"`) || strings.Contains(metrics.Body.String(), "zeroKey") || strings.Contains(metrics.Body.String(), "private") {
		t.Fatal("unbounded metric labels")
	}
}

func TestLimiterWindowConcurrencyOverflowAndBound(t *testing.T) {
	l := newLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	one := int64(1)
	ten := int64(10)
	release, _, ok := l.acquire("one", 5, &one, &ten, &one)
	if !ok {
		t.Fatal("first allowed request denied")
	}
	if _, _, ok = l.acquire("one", 1, nil, nil, &one); ok {
		t.Fatal("concurrent limit ignored")
	}
	release()
	release()
	if _, _, ok = l.acquire("one", 1, &one, nil, nil); ok {
		t.Fatal("RPM limit ignored")
	}
	now = now.Add(time.Minute)
	release, _, ok = l.acquire("one", 10, &one, &ten, &one)
	if !ok {
		t.Fatal("window did not reset")
	}
	release()
	maximum := int64(9223372036854775807)
	release, _, ok = l.acquire("max", maximum, nil, nil, nil)
	if !ok {
		t.Fatal("int64 maximum should be representable")
	}
	release()
	if _, _, ok = l.acquire("max", 1, nil, nil, nil); ok {
		t.Fatal("token counter overflow")
	}
	for i := 0; i < 10000; i++ {
		l.buckets["synthetic:"+strconv.Itoa(i)] = &limitBucket{last: now, active: 1}
	}
	if _, _, ok = l.acquire("new", 1, nil, nil, nil); ok {
		t.Fatal("bucket bound failed open")
	}
}

func TestEmbeddedConsoleIntegratedStatusAndFallbackBoundary(t *testing.T) {
	f := setupServer(t)
	app := New(f.st, f.app.Billing, f.app.Relay, f.app.Payment, f.app.PaymentConfig, Options{Console: consoleassets.Handler()})
	for _, path := range []string{"/console/", "/console/index.html", "/console/account"} {
		response := call(t, app, http.MethodGet, path, "", "")
		if response.Code != http.StatusOK || !strings.Contains(strings.ToLower(response.Header().Get("Content-Type")), "text/html") || !strings.Contains(response.Body.String(), "<html") {
			t.Fatalf("embedded console %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	redirect := call(t, app, http.MethodGet, "/console", "", "")
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/console/" {
		t.Fatal("console canonical redirect changed")
	}
	root := call(t, app, http.MethodGet, "/", "", "")
	if root.Code != http.StatusTemporaryRedirect || root.Header().Get("Location") != "/console/" {
		t.Fatal("default index redirect missing")
	}
	head := call(t, app, http.MethodHead, "/console/", "", "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatal("console HEAD status or body incorrect")
	}
	for _, path := range []string{"/console/assets/absent-synthetic.js", "/console/absent-synthetic.png", "/api/console/v1/absent-synthetic", "/unrelated-synthetic"} {
		response := call(t, app, http.MethodGet, path, "", "")
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "<html") {
			t.Fatalf("fallback swallowed missing asset/API: %s %d", path, response.Code)
		}
	}
}

func TestResponsesRetainedContextRateBoundAndPrivateBindingErrors(t *testing.T) {
	f := setupServer(t)
	ctx := context.Background()
	model, err := f.st.Model(ctx, "public")
	if err != nil {
		t.Fatal(err)
	}
	contextLimit := int64(1000)
	model.ContextWindow = &contextLimit
	model.TaskEndpoints[v1.Chat] = v1.TaskEndpoints{Endpoints: []v1.Endpoint{v1.OpenAI, v1.Responses}, PreferredEndpoint: v1.Responses}
	if err = f.st.SaveModelAccess(ctx, model, true, false); err != nil {
		t.Fatal(err)
	}
	var nativeChannel core.Channel
	if err = f.st.DB().First(&nativeChannel).Error; err != nil {
		t.Fatal(err)
	}
	if err = f.st.DB().Model(&nativeChannel).Update("endpoints_json", `["openai","openai-response"]`).Error; err != nil {
		t.Fatal(err)
	}
	limit := int64(900)
	ownerKey, ownerPlain, err := f.st.CreateAPIKey(ctx, 1, store.KeyInput{Name: "Retained Context", TokensPerMinute: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.DB().Create(&core.ResponseAffinity{APIKeyID: ownerKey.ID, ResponseID: "resp_existing_synthetic", ChannelID: nativeChannel.ID, ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	blocked := call(t, f.app, "POST", "/v1/responses", `{"model":"public","previous_response_id":"resp_existing_synthetic","input":"tiny","max_output_tokens":10}`, ownerPlain)
	if blocked.Code != 429 || !strings.Contains(blocked.Body.String(), "rate_limited") || f.calls.Load() != 0 {
		t.Fatalf("retained context bypassed TPM: %d %s", blocked.Code, blocked.Body.String())
	}
	var reservations int64
	f.st.DB().Model(&core.Reservation{}).Count(&reservations)
	if reservations != 0 {
		t.Fatal("rate-rejected continuation created wallet hold")
	}
	// Missing context must not turn cross-key versus unknown IDs into different
	// errors. The known owner's missing-context failure is a separate diagnostic.
	model.ContextWindow = nil
	if err = f.st.SaveModelAccess(ctx, model, true, false); err != nil {
		t.Fatal(err)
	}
	_, otherPlain, err := f.st.CreateAPIKey(ctx, 1, store.KeyInput{Name: "Other Identity", TokensPerMinute: &limit})
	if err != nil {
		t.Fatal(err)
	}
	cross := call(t, f.app, "POST", "/v1/responses", `{"model":"public","previous_response_id":"resp_existing_synthetic","input":"tiny","max_output_tokens":10}`, otherPlain)
	unknown := call(t, f.app, "POST", "/v1/responses", `{"model":"public","previous_response_id":"resp_unknown_synthetic","input":"tiny","max_output_tokens":10}`, otherPlain)
	if cross.Code != 400 || unknown.Code != 400 || cross.Body.String() != unknown.Body.String() || !strings.Contains(cross.Body.String(), "invalid_previous_response_id") {
		t.Fatal("private binding identity error changed or disclosed existence")
	}
	known := call(t, f.app, "POST", "/v1/responses", `{"model":"public","previous_response_id":"resp_existing_synthetic","input":"tiny","max_output_tokens":10}`, ownerPlain)
	if known.Code != 400 || !strings.Contains(known.Body.String(), "missing_context_bound") || f.calls.Load() != 0 {
		t.Fatalf("known parent estimate failure: %d %s", known.Code, known.Body.String())
	}
}

type merchantTransport func(*http.Request) (*http.Response, error)

func (transport merchantTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSignedMerchantPaymentSubscriptionRedeemAndNativeClosedLoop(t *testing.T) {
	f := setupServer(t)
	registration := call(t, f.app, "POST", "/api/console/v1/register", `{"email":"buyer@example.test","name":"Synthetic Buyer","password":"SyntheticBuyerPassword123!"}`, "")
	if registration.Code != 201 {
		t.Fatal("buyer registration failed")
	}
	doc := document(t, registration)
	session := stringField(t, doc, "session_token")
	var buyer core.User
	_ = json.Unmarshal(doc["user"], &buyer)
	config := call(t, f.app, "PUT", "/api/console/v1/admin/paymentconfig/stripe", `{"enabled":true,"sandbox":true,"stripe_account_id":"acct_synthetic","api_key":"sk_test_synthetic_local_only","webhook_secret":"whsec_synthetic_local_only","return_url":"https://operator.example/success","cancel_url":"https://operator.example/cancel"}`, f.admin)
	if config.Code != 200 {
		t.Fatalf("merchant config: %d %s", config.Code, config.Body.String())
	}
	if strings.Contains(config.Body.String(), "whsec_synthetic_local_only") || strings.Contains(config.Body.String(), "sk_test_synthetic_local_only") {
		t.Fatal("merchant credentials serialized")
	}
	// The merchant URLs and authentication remain official; a local transport
	// supplies deterministic responses without an account, spend or network.
	originalTransport := http.DefaultTransport
	http.DefaultTransport = merchantTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.stripe.com" || request.Header.Get("Authorization") != "Bearer sk_test_synthetic_local_only" {
			t.Error("merchant destination/authentication boundary")
		}
		payload := `{"id":"acct_synthetic"}`
		if request.URL.Path == "/v1/checkout/sessions" {
			raw, _ := io.ReadAll(request.Body)
			form, _ := url.ParseQuery(string(raw))
			payload = string(mustJSON(t, map[string]any{"id": "cs_" + form.Get("client_reference_id"), "url": "https://checkout.stripe.com/c/pay/synthetic", "client_reference_id": form.Get("client_reference_id"), "amount_total": json.Number(form.Get("line_items[0][price_data][unit_amount]")), "currency": form.Get("line_items[0][price_data][currency]"), "livemode": false}))
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
	})
	defer func() { http.DefaultTransport = originalTransport }()
	paid := func(order core.PaymentOrder, eventID string) *httptest.ResponseRecorder {
		t.Helper()
		now := time.Now().Unix()
		body := mustJSON(t, map[string]any{"id": eventID, "type": "checkout.session.completed", "created": now, "livemode": false, "account": "acct_synthetic", "data": map[string]any{"object": map[string]any{"id": order.ExternalID, "client_reference_id": order.ID, "amount_total": order.Amount, "currency": strings.ToLower(order.Currency), "payment_status": "paid", "status": "complete", "livemode": false}}})
		timestamp := strconv.FormatInt(now, 10)
		mac := hmac.New(sha256.New, []byte("whsec_synthetic_local_only"))
		_, _ = mac.Write([]byte(timestamp + "."))
		_, _ = mac.Write(body)
		request := httptest.NewRequest("POST", "/api/payments/v1/stripe/webhook", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Stripe-Signature", "t="+timestamp+",v1="+hex.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		f.app.ServeHTTP(response, request)
		return response
	}
	walletResponse := call(t, f.app, "POST", "/api/console/v1/orders", `{"provider":"stripe","kind":"wallet","amount":100,"currency":"USD"}`, session)
	if walletResponse.Code != 201 {
		t.Fatalf("checkout order: %d %s", walletResponse.Code, walletResponse.Body.String())
	}
	var wallet core.PaymentOrder
	_ = json.Unmarshal(document(t, walletResponse)["order"], &wallet)
	if wallet.State != "pending" || !strings.HasPrefix(wallet.CheckoutURL, "https://checkout.stripe.com/") {
		t.Fatal("checkout missing")
	}
	for i := 0; i < 2; i++ {
		response := paid(wallet, "evt_wallet_synthetic")
		if response.Code != 200 {
			t.Fatalf("signed wallet event: %d %s", response.Code, response.Body.String())
		}
	}
	user, err := f.st.User(context.Background(), buyer.ID)
	if err != nil || user.Balance != 100 {
		t.Fatal("duplicate wallet callback credited incorrectly")
	}
	var events int64
	f.st.DB().Model(&core.PaymentEvent{}).Count(&events)
	if events != 1 {
		t.Fatal("duplicate payment event was not idempotent")
	}
	idor := call(t, f.app, "GET", "/api/console/v1/orders/"+wallet.ID, "", f.admin)
	if idor.Code != 404 {
		t.Fatal("order ownership bypass")
	}
	bad := call(t, f.app, "POST", "/api/payments/v1/stripe/webhook", `{"fake":"paid"}`, "")
	if bad.Code != 400 {
		t.Fatal("unsigned merchant event accepted")
	}
	planResponse := call(t, f.app, "POST", "/api/console/v1/admin/plans", `{"name":"Synthetic Subscription","price":10,"currency":"USD","period_days":30,"token_quota":1000,"model_ids":["premium"],"enabled":true}`, f.admin)
	if planResponse.Code != 200 {
		t.Fatalf("plan: %d %s", planResponse.Code, planResponse.Body.String())
	}
	var plan core.Plan
	_ = json.Unmarshal(document(t, planResponse)["plan"], &plan)
	purchase := call(t, f.app, "POST", "/api/console/v1/orders", string(mustJSON(t, map[string]any{"provider": "stripe", "kind": "subscription", "plan_id": plan.ID})), session)
	if purchase.Code != 201 {
		t.Fatalf("subscription checkout: %d %s", purchase.Code, purchase.Body.String())
	}
	var subscriptionOrder core.PaymentOrder
	_ = json.Unmarshal(document(t, purchase)["order"], &subscriptionOrder)
	if paid(subscriptionOrder, "evt_subscription_synthetic").Code != 200 {
		t.Fatal("subscription event failed")
	}
	keyResponse := call(t, f.app, "POST", "/api/console/v1/keys", `{"name":"Purchased Access","model_ids":[],"allowed_ips":[]}`, session)
	if keyResponse.Code != 201 {
		t.Fatal("key create failed")
	}
	apiKey := stringField(t, document(t, keyResponse), "api_key")
	catalog := call(t, f.app, "GET", "/nomifun/v1/catalog", "", apiKey)
	var models v1.Catalog
	_ = json.Unmarshal(catalog.Body.Bytes(), &models)
	included := false
	for _, model := range models.Models {
		if model.ID == "premium" {
			included = model.IncludedInPlan
		}
	}
	if !included {
		t.Fatal("paid subscription was not reflected in catalog")
	}
	native := call(t, f.app, "POST", "/v1/chat/completions", `{"model":"premium","max_tokens":10,"messages":[],"future":{"opaque":"signature"}}`, apiKey)
	if native.Code != 200 {
		t.Fatalf("paid plan native: %d %s", native.Code, native.Body.String())
	}
	var sub core.Subscription
	f.st.DB().Where("user_id = ?", buyer.ID).First(&sub)
	if sub.QuotaUsed != 5 || sub.QuotaReserved != 0 {
		t.Fatal("subscription usage did not settle")
	}
	user, _ = f.st.User(context.Background(), buyer.ID)
	if user.Balance != 100 || user.ReservedBalance != 0 {
		t.Fatal("subscription request charged wallet")
	}
	codesResponse := call(t, f.app, "POST", "/api/console/v1/admin/redeemcodes", `{"amount":50,"currency":"USD","plan_id":null,"count":2,"expires_at":null}`, f.admin)
	if codesResponse.Code != 201 {
		t.Fatalf("codes: %d %s", codesResponse.Code, codesResponse.Body.String())
	}
	var codes []string
	_ = json.Unmarshal(document(t, codesResponse)["codes"], &codes)
	if len(codes) != 2 {
		t.Fatal("batch codes missing")
	}
	redeem := call(t, f.app, "POST", "/api/console/v1/redeem", string(mustJSON(t, map[string]string{"code": codes[0]})), session)
	if redeem.Code != 200 {
		t.Fatal("redeem failed")
	}
	again := call(t, f.app, "POST", "/api/console/v1/redeem", string(mustJSON(t, map[string]string{"code": codes[0]})), session)
	if again.Code != 200 {
		t.Fatal("same-user redemption replay should be idempotent")
	}
	user, _ = f.st.User(context.Background(), buyer.ID)
	if user.Balance != 150 {
		t.Fatal("redeem credit incorrect")
	}
	if strings.Contains(f.log.String(), "sk_test_synthetic_local_only") || strings.Contains(f.log.String(), "whsec_synthetic_local_only") || strings.Contains(f.log.String(), apiKey) {
		t.Fatal("merchant/API credentials in telemetry")
	}
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
