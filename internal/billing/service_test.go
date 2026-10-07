// SPDX-License-Identifier: Apache-2.0
package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

func fixture(t *testing.T, balance int64) (*Service, *store.Store, core.User, core.APIKey) {
	t.Helper()
	cfg := store.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "billing.db"), MasterKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	st, err := store.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user := core.User{Email: "billing@example.test", Currency: "USD", Balance: balance}
	if err := st.DB().Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	key := core.APIKey{UserID: user.ID, Name: "test", Hash: "billing-synthetic-key", ModelIDsJSON: "[]"}
	if err := st.DB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	s := New(st.DB())
	s.Now = func() time.Time { return time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC) }
	return s, st, user, key
}
func request(user core.User, key core.APIKey, id string, tokens int64) Request {
	return Request{RequestID: id, UserID: user.ID, APIKeyID: key.ID, Model: tokenModel(), Task: v1.Chat, Endpoint: string(v1.OpenAI), EstimatedUsage: core.Usage{InputTokens: tokens, Requests: 1}}
}
func finalUsage(tokens int64) core.Usage {
	return core.Usage{InputTokens: tokens, TotalTokens: tokens, Requests: 1, Complete: true}
}
func balances(t *testing.T, db *gorm.DB, userID, keyID int64) (core.User, core.APIKey) {
	t.Helper()
	var user core.User
	var key core.APIKey
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&key, keyID).Error; err != nil {
		t.Fatal(err)
	}
	return user, key
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var businessError *Error
	if !errors.As(err, &businessError) || businessError.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestConcurrentHundredReservationsAndSettlements(t *testing.T) {
	s, st, user, key := fixture(t, 10000)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reserve(ctx, request(user, key, fmt.Sprintf("request-%03d", i), 10))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 10000 || u.ReservedBalance != 1000 || k.QuotaReserved != 1000 {
		t.Fatalf("holds = %#v %#v", u, k)
	}
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Settle(ctx, fmt.Sprintf("request-%03d", i), finalUsage(7))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	u, k = balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 9300 || u.ReservedBalance != 0 || k.QuotaReserved != 0 || k.QuotaUsed != 700 {
		t.Fatalf("settlement = %#v %#v", u, k)
	}
	var count int64
	st.DB().Model(&core.LedgerEntry{}).Count(&count)
	if count != 100 {
		t.Fatalf("ledger count = %d", count)
	}
}

func TestConcurrentWalletCannotOverdraw(t *testing.T) {
	s, st, user, key := fixture(t, 40)
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reserve(context.Background(), request(user, key, fmt.Sprint(i), 2))
			if err == nil {
				succeeded.Add(1)
			} else {
				var e *Error
				if !errors.As(err, &e) || e.Code != "insufficient_balance" {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if succeeded.Load() != 20 || u.ReservedBalance != 40 || u.Balance != 40 || k.QuotaReserved != 40 {
		t.Fatalf("success=%d, balance=%d reserved=%d quota=%d", succeeded.Load(), u.Balance, u.ReservedBalance, k.QuotaReserved)
	}
}

func TestReplayIdsExactlyOnceAndConflict(t *testing.T) {
	s, st, user, key := fixture(t, 1000)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reserve(ctx, request(user, key, "same", 10))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.ReservedBalance != 10 || k.QuotaReserved != 10 {
		t.Fatal("duplicate reserve")
	}
	_, err := s.Reserve(ctx, request(user, key, "same", 11))
	requireCode(t, err, "idempotency_conflict")
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Settle(ctx, "same", finalUsage(7))
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	u, k = balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 993 || u.ReservedBalance != 0 || k.QuotaUsed != 7 || k.QuotaReserved != 0 {
		t.Fatal("duplicate settlement")
	}
	var count int64
	st.DB().Model(&core.LedgerEntry{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate ledger")
	}
	_, err = s.Settle(ctx, "same", finalUsage(8))
	requireCode(t, err, "idempotency_conflict")
}

func TestConfirmedUsageBeyondReservationNeedsReconciliationThenCredit(t *testing.T) {
	s, st, user, key := fixture(t, 12)
	ctx := context.Background()
	if _, err := s.Reserve(ctx, request(user, key, "more", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccepted(ctx, "more"); err != nil {
		t.Fatal(err)
	}
	hold, err := s.Settle(ctx, "more", finalUsage(15))
	requireCode(t, err, "insufficient_balance")
	if hold.State != "reconciliation" || hold.ActualAmount != 15 {
		t.Fatalf("hold = %#v", hold)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 12 || u.ReservedBalance != 10 || k.QuotaUsed != 0 || k.QuotaReserved != 10 {
		t.Fatal("insufficient settlement changed wallet or quota")
	}
	if _, err := s.Credit(ctx, user.ID, "topup", 3, "USD"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settle(ctx, "more", finalUsage(15)); err != nil {
		t.Fatal(err)
	}
	u, k = balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 0 || u.ReservedBalance != 0 || k.QuotaUsed != 15 {
		t.Fatal("credited settlement incorrect")
	}
}

func TestReleaseAcceptedUnknownAndAuditedOperatorReconciliation(t *testing.T) {
	s, st, user, key := fixture(t, 100)
	ctx := context.Background()
	if _, err := s.Reserve(ctx, request(user, key, "unknown", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccepted(ctx, "unknown"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Release(ctx, "unknown", "cancelled")
	requireCode(t, err, "reconciliation_required")
	_, err = s.MarkReconciliation(ctx, "unknown", "missing terminal usage")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Reconcile(ctx, "unknown", "release", "Provider confirms rejected", user.ID, core.Usage{})
	requireCode(t, err, "forbidden")
	if err := st.DB().Model(&user).Update("admin", true).Error; err != nil {
		t.Fatal(err)
	}
	_, err = s.Reconcile(ctx, "unknown", "release", "Provider confirms rejected", user.ID, core.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.ReservedBalance != 0 || u.Balance != 100 || k.QuotaReserved != 0 {
		t.Fatal("operator release incorrect")
	}
	var audit core.AuditLog
	if err := st.DB().First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.Action != "billing.reconcile" {
		t.Fatal("reconciliation audit missing")
	}
	if _, err := s.Reserve(ctx, request(user, key, "reject", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccepted(ctx, "reject"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmRejected(ctx, "reject", "All upstream attempts returned explicit rejection"); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionAndKeyQuotaModelExpiry(t *testing.T) {
	s, st, user, key := fixture(t, 1000)
	ctx := context.Background()
	quota := int64(15)
	plan := core.Plan{Name: "Included", Currency: "USD", PeriodDays: 30, TokenQuota: &quota, ModelIDsJSON: `["test-model"]`, Enabled: true}
	if err := st.DB().Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	sub, err := s.ActivateSubscription(ctx, user.ID, plan.ID, "sub-payment")
	if err != nil {
		t.Fatal(err)
	}
	req := request(user, key, "included", 10)
	req.Model.IncludedInPlan = true
	req.RequireSubscription = true
	hold, err := s.Reserve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hold.ReservedAmount != 0 || hold.SubscriptionID == nil {
		t.Fatal("included request charged wallet")
	}
	if _, err := s.Reserve(ctx, req); err != nil {
		t.Fatalf("subscription replay: %v", err)
	}
	_, err = s.Settle(ctx, "included", finalUsage(16))
	requireCode(t, err, "insufficient_balance")
	if _, err := s.Settle(ctx, "included", finalUsage(14)); err != nil {
		t.Fatal(err)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 1000 || k.QuotaUsed != 14 {
		t.Fatal("included quota incorrect")
	}
	var got core.Subscription
	st.DB().First(&got, sub.ID)
	if got.QuotaUsed != 14 || got.QuotaReserved != 0 {
		t.Fatal("subscription quota incorrect")
	}
	req.RequestID = "exhausted"
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "insufficient_balance")
	st.DB().Model(&sub).Update("period_end", s.Now().Add(-time.Second))
	req.RequestID = "expired"
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "subscription_expired")
	req.Model.IncludedInPlan = false
	req.RequireSubscription = false
	limit := int64(15)
	st.DB().Model(&key).Update("quota_limit", &limit)
	req.RequestID = "key-exhausted"
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "quota_exceeded")
	st.DB().Model(&key).Updates(map[string]any{"quota_limit": nil, "model_ids_json": `["other"]`})
	req.RequestID = "notallowed"
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "model_not_in_plan")
}

func TestCreditIdempotencyOverflowAndPaymentSnapshot(t *testing.T) {
	s, st, user, _ := fixture(t, 0)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Credit(ctx, user.ID, "paid-event", 100, "USD")
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var got core.User
	st.DB().First(&got, user.ID)
	if got.Balance != 100 {
		t.Fatalf("balance = %d", got.Balance)
	}
	_, err := s.Credit(ctx, user.ID, "paid-event", 101, "USD")
	requireCode(t, err, "idempotency_conflict")
	_, err = s.Credit(ctx, user.ID, "overflow", math.MaxInt64, "USD")
	if err == nil {
		t.Fatal("overflow accepted")
	}
	plan := core.Plan{ID: 42, Name: "Snapshot", Currency: "USD", Price: 50, PeriodDays: 30, Enabled: true, ModelIDsJSON: "[]"}
	snapshot, _ := json.Marshal(plan)
	order := core.PaymentOrder{ID: "plan-order", UserID: user.ID, Kind: "subscription", PlanID: &plan.ID, Amount: 50, Currency: "USD", PlanSnapshotJSON: string(snapshot)}
	if err := st.DB().Transaction(func(tx *gorm.DB) error { return s.ApplyPayment(tx, order, "provider:event") }); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().Transaction(func(tx *gorm.DB) error { return s.ApplyPayment(tx, order, "provider:event") }); err != nil {
		t.Fatal(err)
	}
	var count int64
	st.DB().Model(&core.Subscription{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate plan grant")
	}
	order.Amount = 51
	err = st.DB().Transaction(func(tx *gorm.DB) error { return s.ApplyPayment(tx, order, "provider:event2") })
	requireCode(t, err, "invalid_order")
}

func TestRedeemExactlyOnceAndAccountSnapshot(t *testing.T) {
	s, st, user, key := fixture(t, 0)
	ctx := context.Background()
	code := core.RedeemCode{Hash: CodeHash("synthetic-code"), Amount: 25, Currency: "USD"}
	if err := st.DB().Create(&code).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(ctx, user.ID, "synthetic-code"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(ctx, user.ID, "synthetic-code"); err != nil {
		t.Fatal(err)
	}
	other := core.User{Email: "other@example.test", Currency: "USD"}
	st.DB().Create(&other)
	_, err := s.Redeem(ctx, other.ID, "synthetic-code")
	requireCode(t, err, "invalid_redeem_code")
	limit := int64(100)
	st.DB().Model(&key).Update("quota_limit", &limit)
	if _, err := s.Reserve(ctx, request(user, key, "snapshot", 10)); err != nil {
		t.Fatal(err)
	}
	account, err := s.Account(ctx, user.ID, key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.ContractVersion != v1.Version || account.Balance.Amount != 15 || account.Key.RemainingQuota == nil || *account.Key.RemainingQuota != 90 || account.Plan != nil {
		t.Fatalf("account = %#v", account)
	}
	console, err := s.Account(ctx, user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if console.Key.Name != "Console session" || console.Key.RemainingQuota != nil || console.RateLimits.RequestsPerMinute != nil || console.Key.ExpiresAt != nil || console.Balance.Amount != 15 {
		t.Fatalf("console account = %#v", console)
	}
}

func TestPricingFailurePreservesConfirmedUsageForReconciliation(t *testing.T) {
	s, st, user, key := fixture(t, 100)
	ctx := context.Background()
	if _, err := s.Reserve(ctx, request(user, key, "unpriced-output", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccepted(ctx, "unpriced-output"); err != nil {
		t.Fatal(err)
	}
	usage := finalUsage(7)
	usage.OutputTokens = 1
	usage.TotalTokens = 8
	hold, err := s.Settle(ctx, "unpriced-output", usage)
	requireCode(t, err, "pricing_unavailable")
	if hold.State != "reconciliation" || hold.ActualTokens != 8 || hold.UsageJSON == "" {
		t.Fatalf("hold = %#v", hold)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 100 || u.ReservedBalance != 10 || k.QuotaReserved != 10 || k.QuotaUsed != 0 {
		t.Fatal("unpriceable confirmed usage changed accounting")
	}
}

func TestSubscriptionEntitlementUsesDurableLatestPlanNotWireFlag(t *testing.T) {
	s, st, user, key := fixture(t, 100)
	ctx := context.Background()
	req := request(user, key, "wire-flag", 10)
	req.Model.IncludedInPlan = true
	hold, err := s.Reserve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hold.SubscriptionID != nil || hold.ReservedAmount != 10 {
		t.Fatal("wire inclusion flag created an entitlement")
	}
	if _, err := s.Release(ctx, "wire-flag", "fixture"); err != nil {
		t.Fatal(err)
	}
	quota := int64(100)
	plan := core.Plan{ID: 1, Name: "Actual", Currency: "USD", PeriodDays: 30, TokenQuota: &quota, ModelIDsJSON: `["test-model"]`}
	if err := st.DB().Transaction(func(tx *gorm.DB) error {
		_, err := s.ActivateSubscriptionTx(tx, user.ID, plan, "actual-plan")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req = request(user, key, "durable-inclusion", 10)
	req.Model.Pricing = nil
	hold, err = s.Reserve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hold.SubscriptionID == nil || hold.ReservedAmount != 0 {
		t.Fatal("active inclusion ignored or required wallet pricing")
	}
	if _, err := s.Settle(ctx, req.RequestID, finalUsage(9)); err != nil {
		t.Fatal(err)
	}
	// A newer short period supersedes this plan, including after its expiry.
	newPlan := plan
	newPlan.ID = 2
	newPlan.PeriodDays = 1
	newSub, err := s.ActivateSubscription(ctx, user.ID, newPlan.ID, "new-plan")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing configured plan error = %v", err)
	}
	err = st.DB().Transaction(func(tx *gorm.DB) error {
		var e error
		newSub, e = s.ActivateSubscriptionTx(tx, user.ID, newPlan, "new-plan")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DB().Model(&newSub).Update("period_end", s.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	req = request(user, key, "expired-latest", 10)
	req.RequireSubscription = true
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "subscription_expired")
	account, err := s.Account(ctx, user.ID, key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Plan == nil || account.Plan.PeriodEnd == nil || *account.Plan.PeriodEnd != s.Now().Add(-time.Second).Format(time.RFC3339) {
		t.Fatal("account silently revived prior plan")
	}
}

func TestExcludedSubscriptionModelWalletOrSubscriptionOnly(t *testing.T) {
	s, st, user, key := fixture(t, 100)
	ctx := context.Background()
	plan := core.Plan{ID: 1, Name: "Other models", Currency: "USD", PeriodDays: 30, ModelIDsJSON: `["other"]`}
	if err := st.DB().Transaction(func(tx *gorm.DB) error {
		_, err := s.ActivateSubscriptionTx(tx, user.ID, plan, "other-plan")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req := request(user, key, "paygo", 10)
	req.Model.IncludedInPlan = true
	hold, err := s.Reserve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hold.SubscriptionID != nil || hold.ReservedAmount != 10 {
		t.Fatal("excluded model consumed subscription")
	}
	req.RequestID = "subscription-only"
	req.RequireSubscription = true
	_, err = s.Reserve(ctx, req)
	requireCode(t, err, "model_not_in_plan")
}

func TestPartialUsageIsEvidenceAndDoesNotBillOrReplaceConfirmedUsage(t *testing.T) {
	s, st, user, key := fixture(t, 100)
	ctx := context.Background()
	if _, err := s.Reserve(ctx, request(user, key, "partial", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccepted(ctx, "partial"); err != nil {
		t.Fatal(err)
	}
	usage := core.Usage{InputTokens: 4, Requests: 1, RawJSON: `{"input_tokens":4}`}
	hold, err := s.MarkReconciliationWithUsage(ctx, "partial", "Interrupted before terminal event", usage)
	if err != nil {
		t.Fatal(err)
	}
	if hold.State != "reconciliation" || hold.ActualTokens != 4 || hold.UsageJSON == "" || hold.ActualAmount != 0 {
		t.Fatal("partial usage not retained")
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 100 || u.ReservedBalance != 10 || k.QuotaUsed != 0 || k.QuotaReserved != 10 {
		t.Fatal("partial evidence billed usage")
	}
	if _, err := s.Settle(ctx, "partial", usage); err == nil {
		t.Fatal("partial usage allowed settlement")
	}
	confirmed := finalUsage(101)
	_, err = s.Settle(ctx, "partial", confirmed)
	requireCode(t, err, "insufficient_balance")
	hold, err = s.MarkReconciliationWithUsage(ctx, "partial", "Later cancellation", usage)
	if err != nil {
		t.Fatal(err)
	}
	var captured core.Usage
	if json.Unmarshal([]byte(hold.UsageJSON), &captured) != nil || !captured.Complete || captured.InputTokens != 101 {
		t.Fatal("partial replaced confirmed usage")
	}
}

func TestReservationRejectsInsufficientPremiumCacheAndReasoningMoney(t *testing.T) {
	tests := []struct {
		name, meter, base string
		usage             core.Usage
	}{
		{"cache-write", "cache_creation_input_tokens", "input_tokens", core.Usage{InputTokens: 4, Requests: 1}},
		{"reasoning", "reasoning_tokens", "output_tokens", core.Usage{OutputTokens: 4, Requests: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, st, user, key := fixture(t, 39)
			ctx := context.Background()
			req := request(user, key, "premium", 0)
			req.EstimatedUsage = test.usage
			req.Model.Pricing = []v1.Price{{Task: v1.Chat, Meter: test.base, UnitSize: 1, Amount: 0, Currency: "USD"}, {Task: v1.Chat, Meter: test.meter, UnitSize: 1, Amount: 10, Currency: "USD"}}
			_, err := s.Reserve(ctx, req)
			requireCode(t, err, "insufficient_balance")
			var count int64
			if err := st.DB().Model(&core.Reservation{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("insufficient premium money admitted a request")
			}
			u, k := balances(t, st.DB(), user.ID, key.ID)
			if u.ReservedBalance != 0 || k.QuotaReserved != 0 {
				t.Fatal("rejected admission retained counters")
			}
			if _, err := s.Credit(ctx, user.ID, "cover-premium", 1, "USD"); err != nil {
				t.Fatal(err)
			}
			hold, err := s.Reserve(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if hold.ReservedAmount != 40 {
				t.Fatalf("premium reservation = %d", hold.ReservedAmount)
			}
			actual := test.usage
			actual.Complete = true
			if test.meter == "reasoning_tokens" {
				actual.ReasoningTokens = 3
			} else {
				actual.CacheCreationTokens = 3
			}
			hold, err = s.Settle(ctx, req.RequestID, actual)
			if err != nil {
				t.Fatal(err)
			}
			if hold.ActualAmount != 30 {
				t.Fatalf("premium exact settlement = %d", hold.ActualAmount)
			}
			u, k = balances(t, st.DB(), user.ID, key.ID)
			if u.Balance != 10 || u.ReservedBalance != 0 || k.QuotaReserved != 0 {
				t.Fatal("premium bound leaked into actual charge")
			}
		})
	}
}

func TestSQLiteRestartPreservesHoldsAndRecoveryPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.db")
	cfg := store.Config{Driver: "sqlite", DSN: path, MasterKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	st, err := store.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	user := core.User{Email: "restart@example.test", Currency: "USD", Balance: 100}
	st.DB().Create(&user)
	key := core.APIKey{UserID: user.ID, Hash: "restart-hash", ModelIDsJSON: "[]"}
	st.DB().Create(&key)
	s := New(st.DB())
	ctx := context.Background()
	for _, id := range []string{"not-dispatched", "possibly-accepted"} {
		if _, err := s.Reserve(ctx, request(user, key, id, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkAccepted(ctx, "possibly-accepted"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s = New(st.DB())
	if err := s.RecoverPending(ctx); err != nil {
		t.Fatal(err)
	}
	var holds []core.Reservation
	st.DB().Order("request_id").Find(&holds)
	if len(holds) != 2 || holds[0].State != "released" || holds[1].State != "reconciliation" {
		t.Fatalf("recovered holds = %#v", holds)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 100 || u.ReservedBalance != 10 || k.QuotaReserved != 10 {
		t.Fatal("restart lost or charged unknown hold")
	}
}
