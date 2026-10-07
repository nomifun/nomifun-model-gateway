// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

type paymentRoundTrip func(*http.Request) (*http.Response, error)

func (f paymentRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func paymentResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func paymentFixture(t *testing.T, currency string) (*Service, *store.Store, core.User, string) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "payments.sqlite")
	st, err := store.Open(store.Config{Driver: "sqlite", DSN: dsn, MasterKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u := core.User{Email: "pay@example.invalid", Name: "Fixture", Currency: currency}
	if err = st.DB().Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewConfigRepository(st)
	c := Config{Provider: "stripe", Enabled: true, Sandbox: true, APIKey: "sk_test_synthetic", WebhookSecret: "whsec_synthetic", StripeAccountID: "acct_synthetic", ReturnURL: "https://operator.example/success", CancelURL: "https://operator.example/cancel"}
	b, _ := json.Marshal(c)
	if err = st.SaveSettingSecret(context.Background(), "payment.config.stripe", b); err != nil {
		t.Fatal(err)
	}
	s := New(st.DB(), billing.New(st.DB()), repo)
	s.transport = paymentRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.stripe.com" || r.Header.Get("Authorization") != "Bearer sk_test_synthetic" {
			t.Error("merchant origin or credentials changed")
		}
		if r.URL.Path == "/v1/account" {
			return paymentResponse(200, `{"id":"acct_synthetic"}`), nil
		}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			id := r.PostForm.Get("client_reference_id")
			if r.Header.Get("Idempotency-Key") != "nmg-order-"+id {
				t.Error("missing checkout idempotency")
			}
			return paymentResponse(200, fmt.Sprintf(`{"id":"cs_%s","url":"https://checkout.stripe.com/c/pay/%s","client_reference_id":"%s","amount_total":%s,"currency":%q,"livemode":false}`, id, id, id, r.PostForm.Get("line_items[0][price_data][unit_amount]"), r.PostForm.Get("line_items[0][price_data][currency]"))), nil
		}
		return nil, errors.New("unexpected request")
	})
	return s, st, u, dsn
}
func stripeBody(order core.PaymentOrder, id, state string, amount int64, currency string) []byte {
	obj := map[string]any{"id": order.ExternalID, "client_reference_id": order.ID, "amount_total": amount, "currency": strings.ToLower(currency), "payment_status": state, "status": "complete", "livemode": false}
	b, _ := json.Marshal(map[string]any{"id": id, "type": "checkout.session.completed", "created": time.Now().Unix(), "livemode": false, "data": map[string]any{"object": obj}})
	return b
}
func stripeHeaders(body []byte, stamp time.Time) http.Header {
	v := fmt.Sprint(stamp.Unix())
	mac := hmac.New(sha256.New, []byte("whsec_synthetic"))
	mac.Write([]byte(v + "."))
	mac.Write(body)
	h := http.Header{}
	h.Set("Stripe-Signature", "t="+v+",v1="+hex.EncodeToString(mac.Sum(nil)))
	return h
}
func walletOrder(t *testing.T, s *Service, u core.User) core.PaymentOrder {
	t.Helper()
	o, err := s.CreateOrder(context.Background(), u.ID, CreateOrderRequest{Provider: "stripe", Kind: "wallet", Amount: 12345, Currency: u.Currency})
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestPaymentWebhookAtomicReplayAndDurability(t *testing.T) {
	s, st, u, dsn := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	body := stripeBody(o, "evt_one", "paid", o.Amount, o.Currency)
	h := stripeHeaders(body, time.Now())
	for i := 0; i < 2; i++ {
		v, err := s.HandleWebhook(context.Background(), "stripe", h, body)
		if err != nil || v.State != "paid" {
			t.Fatalf("delivery %d state %s err %v", i, v.State, err)
		}
	}
	// A second unique provider event for the same payment still produces one credit.
	b2 := stripeBody(o, "evt_two", "paid", o.Amount, o.Currency)
	if _, err := s.HandleWebhook(context.Background(), "stripe", stripeHeaders(b2, time.Now()), b2); err != nil {
		t.Fatal(err)
	}
	var user core.User
	st.DB().First(&user, u.ID)
	if user.Balance != o.Amount {
		t.Fatalf("balance %d", user.Balance)
	}
	var ledgerCount int64
	st.DB().Model(&core.LedgerEntry{}).Count(&ledgerCount)
	if ledgerCount != 1 {
		t.Fatalf("ledger entries %d", ledgerCount)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(store.Config{Driver: "sqlite", DSN: dsn, MasterKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s2 := New(reopened.DB(), billing.New(reopened.DB()), NewConfigRepository(reopened))
	if _, err = s2.HandleWebhook(context.Background(), "stripe", h, body); err != nil {
		t.Fatal(err)
	}
	reopened.DB().First(&user, u.ID)
	if user.Balance != o.Amount {
		t.Fatal("reopened replay credited twice")
	}
}
func TestPaymentConcurrentWebhookNoDoubleCredit(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := stripeBody(o, fmt.Sprintf("evt_%d", i), "paid", o.Amount, o.Currency)
			_, err := s.HandleWebhook(context.Background(), "stripe", stripeHeaders(b, time.Now()), b)
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var user core.User
	st.DB().First(&user, u.ID)
	if user.Balance != o.Amount {
		t.Fatalf("balance %d", user.Balance)
	}
}

type rejectFulfillment struct{}

func (rejectFulfillment) ApplyPayment(*gorm.DB, core.PaymentOrder, string) error {
	return errors.New("synthetic fulfillment failure")
}
func TestPaymentFulfillmentRollbackIncludesEventAndOrder(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	s.billing = rejectFulfillment{}
	b := stripeBody(o, "evt_rollback", "paid", o.Amount, o.Currency)
	h := stripeHeaders(b, time.Now())
	if _, err := s.HandleWebhook(context.Background(), "stripe", h, b); err == nil {
		t.Fatal("expected fulfillment error")
	}
	var n int64
	st.DB().Model(&core.PaymentEvent{}).Count(&n)
	if n != 0 {
		t.Fatal("event escaped rollback")
	}
	v, err := s.GetOrder(context.Background(), u.ID, o.ID)
	if err != nil || v.State != "pending" {
		t.Fatal("order escaped rollback")
	}
	s.billing = billing.New(st.DB())
	if _, err = s.HandleWebhook(context.Background(), "stripe", h, b); err != nil {
		t.Fatal(err)
	}
}
func TestPaymentRejectsSignedWrongAmountCurrencyOrderAndSignature(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	tests := []struct {
		name   string
		body   []byte
		stamp  time.Time
		tamper bool
	}{
		{"amount", stripeBody(o, "evt_amount", "paid", o.Amount+1, o.Currency), time.Now(), false},
		{"currency", stripeBody(o, "evt_currency", "paid", o.Amount, "CNY"), time.Now(), false},
		{"stale", stripeBody(o, "evt_stale", "paid", o.Amount, o.Currency), time.Now().Add(-6 * time.Minute), false},
		{"future", stripeBody(o, "evt_future", "paid", o.Amount, o.Currency), time.Now().Add(6 * time.Minute), false},
		{"rawbody", stripeBody(o, "evt_tamper", "paid", o.Amount, o.Currency), time.Now(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := stripeHeaders(tt.body, tt.stamp)
			b := tt.body
			if tt.tamper {
				b = append(b, ' ')
			}
			if _, err := s.HandleWebhook(context.Background(), "stripe", h, b); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	wrong := o
	wrong.ExternalID = "cs_wrong"
	b := stripeBody(wrong, "evt_binding", "paid", o.Amount, o.Currency)
	if _, err := s.HandleWebhook(context.Background(), "stripe", stripeHeaders(b, time.Now()), b); err == nil {
		t.Fatal("wrong checkout accepted")
	}
	var user core.User
	st.DB().First(&user, u.ID)
	if user.Balance != 0 {
		t.Fatal("negative events changed balance")
	}
	var n int64
	st.DB().Model(&core.PaymentEvent{}).Count(&n)
	if n != 0 {
		t.Fatal("negative events persisted")
	}
}
func TestPaymentSubscriptionUsesServerPriceAndImmutableSnapshot(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	quota := int64(5000)
	plan := core.Plan{Name: "Starter", Price: 900, Currency: "USD", PeriodDays: 30, TokenQuota: &quota, ModelIDsJSON: `["model-a"]`, Enabled: true}
	if err := st.DB().Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	o, err := s.CreateOrder(context.Background(), u.ID, CreateOrderRequest{Provider: "stripe", Kind: "subscription", PlanID: &plan.ID, Amount: 1, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Amount != 900 || o.Currency != "USD" {
		t.Fatal("untrusted client price used")
	}
	st.DB().Model(&plan).Updates(map[string]any{"price": 2000, "period_days": 1, "name": "Changed", "token_quota": 1})
	b := stripeBody(o, "evt_plan", "paid", o.Amount, o.Currency)
	if _, err = s.HandleWebhook(context.Background(), "stripe", stripeHeaders(b, time.Now()), b); err != nil {
		t.Fatal(err)
	}
	var subscription core.Subscription
	if err = st.DB().Where("user_id = ?", u.ID).First(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	if subscription.PlanName != "Starter" || subscription.QuotaTotal == nil || *subscription.QuotaTotal != 5000 || subscription.PeriodEnd.Sub(subscription.PeriodStart) != 30*24*time.Hour {
		t.Fatal("mutable plan changed checkout")
	}
}
func TestPaymentConfigurationEncryptedMaskedFrozenAndDisabledCallbacks(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	var setting core.Setting
	st.DB().Where("name = ?", "payment.config.stripe").First(&setting)
	if strings.Contains(setting.ValueJSON, "sk_test_") || strings.Contains(setting.ValueJSON, "whsec_") {
		t.Fatal("plaintext credentials persisted")
	}
	statuses, err := s.configs.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(statuses)
	if strings.Contains(string(b), "synthetic") && strings.Contains(string(b), "sk_test_") {
		t.Fatal("credentials exposed")
	}
	newKey := "sk_test_new"
	if _, err = s.configs.Update(context.Background(), "stripe", ConfigPatch{APIKey: &newKey}); err == nil {
		t.Fatal("unresolved orders lost verification credentials")
	}
	disabled := false
	if _, err = s.configs.Update(context.Background(), "stripe", ConfigPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateOrder(context.Background(), u.ID, CreateOrderRequest{Provider: "stripe", Kind: "wallet", Amount: 100, Currency: "USD"}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatal("disabled created checkout")
	}
	body := stripeBody(o, "evt_disabled", "paid", o.Amount, o.Currency)
	if _, err = s.HandleWebhook(context.Background(), "stripe", stripeHeaders(body, time.Now()), body); err != nil {
		t.Fatal("disabled lost valid callback", err)
	}
	if _, err = s.configs.Update(context.Background(), "stripe", ConfigPatch{APIKey: &newKey}); err != nil {
		t.Fatal("paid order should permit key rotation", err)
	}
}
func TestPaymentMerchantRedirectRejectedAndOwnership(t *testing.T) {
	s, _, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	if _, err := s.GetOrder(context.Background(), u.ID+1, o.ID); !errors.Is(err, ErrOrderNotFound) {
		t.Fatal("foreign order exposed")
	}
	orders, err := s.ListOrders(context.Background(), u.ID, 10)
	if err != nil || len(orders) != 1 {
		t.Fatal("orders not listed", err)
	}
	requests := 0
	s.transport = paymentRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		v := paymentResponse(302, `{}`)
		v.Header.Set("Location", "https://attacker.invalid")
		return v, nil
	})
	if _, err = s.QueryReconcile(context.Background(), o.ID); err == nil {
		t.Fatal("redirect accepted")
	}
	if requests != 1 {
		t.Fatal("redirect followed")
	}
}

func TestPaymentReconciliationFindsLatePaidAndCannotDowngrade(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	// This is a local expiry, not proof that the merchant did not receive money.
	st.DB().Model(&core.PaymentOrder{}).Where("id = ?", o.ID).Updates(map[string]any{"state": "expired", "expires_at": time.Now().Add(-time.Minute)})
	s.transport = paymentRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/checkout/sessions/"+o.ExternalID {
			t.Fatal("wrong reconciliation resource")
		}
		return paymentResponse(200, fmt.Sprintf(`{"id":%q,"client_reference_id":%q,"amount_total":%d,"currency":"usd","payment_status":"paid","status":"complete","livemode":false}`, o.ExternalID, o.ID, o.Amount)), nil
	})
	results, err := s.Reconcile(context.Background(), 10)
	if err != nil || len(results) != 1 || results[0].State != "paid" || results[0].Error != "" {
		t.Fatalf("reconciliation %#v %v", results, err)
	}
	var user core.User
	st.DB().First(&user, u.ID)
	if user.Balance != o.Amount {
		t.Fatal("late paid not fulfilled")
	}
	late := stripeBody(o, "evt_late_failed", "unpaid", o.Amount, o.Currency)
	var envelope map[string]any
	json.Unmarshal(late, &envelope)
	envelope["type"] = "checkout.session.async_payment_failed"
	late, _ = json.Marshal(envelope)
	v, err := s.HandleWebhook(context.Background(), "stripe", stripeHeaders(late, time.Now()), late)
	if err != nil || v.State != "paid" {
		t.Fatal("paid downgraded", err)
	}
	if _, err = s.QueryReconcile(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	var n int64
	st.DB().Model(&core.LedgerEntry{}).Count(&n)
	if n != 1 {
		t.Fatal("reconciliation duplicate ledger")
	}
}

func TestPaymentEventIDConflictAndOrderSnapshotIdentity(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	o := walletOrder(t, s, u)
	e := Event{ID: "fixture_pending", OrderID: o.ID, ExternalID: o.ExternalID, MerchantID: o.MerchantID, State: "pending", Amount: o.Amount, Currency: o.Currency, OccurredAt: time.Now()}
	if _, err := s.apply(context.Background(), "stripe", e); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"merchant", "app", "amount", "currency", "event_state"} {
		t.Run(name, func(t *testing.T) {
			bad := e
			switch name {
			case "merchant":
				bad.MerchantID = "acct_other"
			case "app":
				bad.AppID = "other"
			case "amount":
				bad.Amount++
			case "currency":
				bad.Currency = "CNY"
			case "event_state":
				bad.State = "paid"
			}
			if _, err := s.apply(context.Background(), "stripe", bad); err == nil {
				t.Fatal("conflicting event or order binding accepted")
			}
		})
	}
	var user core.User
	st.DB().First(&user, u.ID)
	if user.Balance != 0 {
		t.Fatal("conflicting events credited")
	}
	// A 200-character native event ID stays exact in PaymentEvent while its
	// ledger idempotency key is fixed length and fits PostgreSQL varchar(200).
	e.ID = strings.Repeat("e", 200)
	e.State = "paid"
	if _, err := s.apply(context.Background(), "stripe", e); err != nil {
		t.Fatal(err)
	}
	var ledger core.LedgerEntry
	st.DB().First(&ledger)
	if len(ledger.IdempotencyKey) > 200 {
		t.Fatal("ledger key overflow")
	}
}

func TestPaymentRejectsUnconfiguredProviderAndInvalidOrder(t *testing.T) {
	s, st, u, _ := paymentFixture(t, "USD")
	for _, req := range []CreateOrderRequest{{Provider: "alipay", Kind: "wallet", Amount: 100, Currency: "CNY"}, {Provider: "stripe", Kind: "wallet", Amount: 0, Currency: "USD"}, {Provider: "stripe", Kind: "wallet", Amount: 1_000_000_001, Currency: "USD"}, {Provider: "stripe", Kind: "wallet", Amount: 100, Currency: "CNY"}, {Provider: "stripe", Kind: "subscription"}} {
		if _, err := s.CreateOrder(context.Background(), u.ID, req); err == nil {
			t.Fatal("invalid order accepted")
		}
	}
	var n int64
	st.DB().Model(&core.PaymentOrder{}).Count(&n)
	if n != 0 {
		t.Fatal("invalid order persisted")
	}
	providers, err := s.Providers(context.Background())
	if err != nil || len(providers) != 3 {
		t.Fatal(err)
	}
	if providers[1].Available || providers[2].Available {
		t.Fatal("fake merchant enabled")
	}
}
