// SPDX-License-Identifier: Apache-2.0
// Package billing owns atomic wallet and quota holds, terminal settlement and
// auditable reconciliation. Content and native transcripts are never stored.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Error struct {
	Code    string
	Status  int
	Message string
}

func (e *Error) Error() string                               { return e.Message }
func business(code string, status int, message string) error { return &Error{code, status, message} }

type Service struct {
	db  *gorm.DB
	Now func() time.Time
}

func New(db *gorm.DB) *Service {
	return &Service{db: db, Now: func() time.Time { return time.Now().UTC() }}
}

type Request struct {
	RequestID           string
	UserID, APIKeyID    int64
	Model               v1.Model
	Task                v1.Task
	Endpoint            string
	EstimatedUsage      core.Usage
	RequireSubscription bool
}
type tariff struct {
	Model               v1.Model `json:"model"`
	Task                v1.Task  `json:"task"`
	RequireSubscription bool     `json:"require_subscription"`
}

func (s *Service) Reserve(ctx context.Context, req Request) (core.Reservation, error) {
	var result core.Reservation
	if req.RequestID == "" || len(req.RequestID) > 100 || req.UserID <= 0 || req.APIKeyID <= 0 || req.Model.ID == "" {
		return result, business("invalid_request_error", 400, "Invalid billing request")
	}
	tokens, err := Tokens(req.EstimatedUsage)
	if err != nil {
		return result, business("invalid_request_error", 400, "Invalid estimated usage")
	}
	amount, currency, priceErr := QuoteUpperBound(req.Model, req.Task, req.EstimatedUsage)
	pricing, _ := json.Marshal(tariff{Model: req.Model, Task: req.Task, RequireSubscription: req.RequireSubscription})
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("request_id = ?", req.RequestID).First(&result).Error; err == nil {
			if result.UserID != req.UserID || result.APIKeyID != req.APIKeyID || result.ModelID != req.Model.ID || result.Endpoint != req.Endpoint || result.PricingJSON != string(pricing) || (result.SubscriptionID == nil && result.ReservedAmount != amount) || result.ReservedTokens != tokens {
				return business("idempotency_conflict", 409, "Request ID already has different billing parameters")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var user core.User
		if err := locked(tx).First(&user, req.UserID).Error; err != nil {
			return err
		}
		// Concurrent replays may both observe an absent reservation before the
		// wallet row lock. Re-read after the lock serializes their insertion.
		if err := tx.Where("request_id = ?", req.RequestID).First(&result).Error; err == nil {
			if result.UserID != req.UserID || result.APIKeyID != req.APIKeyID || result.ModelID != req.Model.ID || result.Endpoint != req.Endpoint || result.PricingJSON != string(pricing) || (result.SubscriptionID == nil && result.ReservedAmount != amount) || result.ReservedTokens != tokens {
				return business("idempotency_conflict", 409, "Request ID already has different billing parameters")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var key core.APIKey
		if err := locked(tx).First(&key, req.APIKeyID).Error; err != nil {
			return err
		}
		now := s.Now()
		if user.Disabled || key.Revoked || key.UserID != user.ID {
			return business("invalid_api_key", 401, "API key is unavailable")
		}
		if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
			return business("key_expired", 401, "API key has expired")
		}
		if !allowed(key.ModelIDsJSON, req.Model.ID) {
			return business("model_not_in_plan", 403, "Model is not allowed by this API key")
		}
		if key.QuotaLimit != nil && !room(*key.QuotaLimit, key.QuotaUsed, key.QuotaReserved, tokens) {
			return business("quota_exceeded", 403, "API key quota is exhausted")
		}
		result = core.Reservation{RequestID: req.RequestID, UserID: user.ID, APIKeyID: key.ID, ModelID: req.Model.ID, Endpoint: req.Endpoint, Currency: user.Currency, ReservedAmount: amount, ReservedTokens: tokens, State: "reserved", PricingJSON: string(pricing), CreatedAt: now, UpdatedAt: now}
		// The latest activation is authoritative. Its expiry cannot silently
		// revive an older overlapping plan. Wire IncludedInPlan is informative;
		// actual entitlement comes from this locked durable subscription.
		var sub core.Subscription
		subErr := locked(tx).Where("user_id = ?", user.ID).Order("id DESC").First(&sub).Error
		if subErr != nil && !errors.Is(subErr, gorm.ErrRecordNotFound) {
			return subErr
		}
		active := subErr == nil && !sub.PeriodStart.After(now) && sub.PeriodEnd.After(now)
		if active && !validModelIDs(sub.ModelIDsJSON) {
			return business("billing_unavailable", 503, "Subscription model permissions are invalid")
		}
		covered := active && allowed(sub.ModelIDsJSON, req.Model.ID)
		if req.RequireSubscription && !covered {
			if !active {
				return business("subscription_expired", 403, "An active subscription is required")
			}
			return business("model_not_in_plan", 403, "Model is not included in this subscription")
		}
		if covered {
			if sub.QuotaTotal != nil && !room(*sub.QuotaTotal, sub.QuotaUsed, sub.QuotaReserved, tokens) {
				return business("insufficient_balance", 402, "Subscription quota is exhausted")
			}
			newReserved, err := add(sub.QuotaReserved, tokens)
			if err != nil {
				return err
			}
			if err := tx.Model(&sub).Update("quota_reserved", newReserved).Error; err != nil {
				return err
			}
			result.SubscriptionID = &sub.ID
			result.ReservedAmount = 0
		} else {
			if priceErr != nil {
				return business("pricing_unavailable", 503, priceErr.Error())
			}
			if currency != user.Currency {
				return business("pricing_unavailable", 503, "Wallet and model currency differ")
			}
			if !room(user.Balance, 0, user.ReservedBalance, amount) {
				return business("insufficient_balance", http.StatusPaymentRequired, "Insufficient wallet balance")
			}
			reserved, err := add(user.ReservedBalance, amount)
			if err != nil {
				return err
			}
			if err := tx.Model(&user).Update("reserved_balance", reserved).Error; err != nil {
				return err
			}
		}
		reserved, err := add(key.QuotaReserved, tokens)
		if err != nil {
			return err
		}
		if err := tx.Model(&key).Update("quota_reserved", reserved).Error; err != nil {
			return err
		}
		return tx.Create(&result).Error
	})
	return result, err
}

func locked(tx *gorm.DB) *gorm.DB { return tx.Clauses(clause.Locking{Strength: "UPDATE"}) }
func room(total, used, reserved, extra int64) bool {
	if total < 0 || used < 0 || reserved < 0 || extra < 0 || used > total {
		return false
	}
	remaining := total - used
	return reserved <= remaining && extra <= remaining-reserved
}
func allowed(raw, id string) bool {
	var ids []string
	if !validModelIDs(raw) || json.Unmarshal([]byte(raw), &ids) != nil {
		return false
	}
	if len(ids) == 0 {
		return true
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func validModelIDs(raw string) bool {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil || ids == nil {
		return false
	}
	for _, id := range ids {
		if id == "" {
			return false
		}
	}
	return true
}

// MarkAccepted is conservative: call before dispatching upstream. A process
// crash after dispatch cannot safely infer whether a provider charged usage.
func (s *Service) MarkAccepted(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Model(&core.Reservation{}).Where("request_id = ? AND state = ?", id, "reserved").Updates(map[string]any{"upstream_accepted": true, "updated_at": s.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return business("invalid_reservation", 409, "Reservation is not pending")
	}
	return nil
}

func (s *Service) Settle(ctx context.Context, id string, usage core.Usage) (core.Reservation, error) {
	var result core.Reservation
	if !usage.Complete {
		return result, business("incomplete_usage", 409, "Terminal provider usage is required")
	}
	tokens, err := Tokens(usage)
	if err != nil {
		return result, err
	}
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return result, err
	}
	var pending error
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := locked(tx).Where("request_id = ?", id).First(&result).Error; err != nil {
			return err
		}
		if result.State == "settled" {
			if result.UsageJSON != string(usageJSON) {
				return business("idempotency_conflict", 409, "Reservation already settled with different usage")
			}
			return nil
		}
		if result.State == "released" {
			return business("invalid_reservation", 409, "Reservation was released")
		}
		amount, currency := int64(0), result.Currency
		if result.SubscriptionID == nil {
			var prices tariff
			if err := json.Unmarshal([]byte(result.PricingJSON), &prices); err != nil {
				return err
			}
			var err error
			amount, currency, err = Quote(prices.Model, prices.Task, usage)
			if err != nil {
				result.State = "reconciliation"
				result.ActualTokens = tokens
				result.UsageJSON = string(usageJSON)
				result.Reason = "Confirmed usage could not be priced: " + err.Error()
				result.UpdatedAt = s.Now()
				pending = business("pricing_unavailable", 503, result.Reason)
				return tx.Save(&result).Error
			}
		}
		if currency != result.Currency {
			return fmt.Errorf("currency changed")
		}
		var user core.User
		if err := locked(tx).First(&user, result.UserID).Error; err != nil {
			return err
		}
		var key core.APIKey
		if err := locked(tx).First(&key, result.APIKeyID).Error; err != nil {
			return err
		}
		if user.ReservedBalance < result.ReservedAmount || key.QuotaReserved < result.ReservedTokens {
			return fmt.Errorf("hold counters are inconsistent")
		}
		if !room(user.Balance, 0, user.ReservedBalance-result.ReservedAmount, amount) {
			pending = business("insufficient_balance", 402, "Confirmed usage exceeds available wallet balance")
		}
		if key.QuotaLimit != nil && !room(*key.QuotaLimit, key.QuotaUsed, key.QuotaReserved-result.ReservedTokens, tokens) {
			pending = business("quota_exceeded", 403, "Confirmed usage exceeds API key quota")
		}
		var sub core.Subscription
		if result.SubscriptionID != nil {
			if err := locked(tx).First(&sub, *result.SubscriptionID).Error; err != nil {
				return err
			}
			if sub.QuotaReserved < result.ReservedTokens {
				return fmt.Errorf("subscription hold is inconsistent")
			}
			if sub.QuotaTotal != nil && !room(*sub.QuotaTotal, sub.QuotaUsed, sub.QuotaReserved-result.ReservedTokens, tokens) {
				pending = business("insufficient_balance", 402, "Confirmed usage exceeds subscription quota")
			}
		}
		result.ActualAmount = amount
		result.ActualTokens = tokens
		result.UsageJSON = string(usageJSON)
		result.UpdatedAt = s.Now()
		if pending != nil {
			result.State = "reconciliation"
			result.Reason = pending.Error()
			return tx.Save(&result).Error
		}
		keyUsed, err := add(key.QuotaUsed, tokens)
		if err != nil {
			return err
		}
		if err := tx.Model(&key).Updates(map[string]any{"quota_used": keyUsed, "quota_reserved": key.QuotaReserved - result.ReservedTokens}).Error; err != nil {
			return err
		}
		if result.SubscriptionID != nil {
			used, err := add(sub.QuotaUsed, tokens)
			if err != nil {
				return err
			}
			if err := tx.Model(&sub).Updates(map[string]any{"quota_used": used, "quota_reserved": sub.QuotaReserved - result.ReservedTokens}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&user).Updates(map[string]any{"balance": user.Balance - amount, "reserved_balance": user.ReservedBalance - result.ReservedAmount}).Error; err != nil {
			return err
		}
		entry := core.LedgerEntry{IdempotencyKey: "usage:" + id, UserID: user.ID, RequestID: id, Kind: "usage", Amount: -amount, Currency: currency, BalanceAfter: user.Balance - amount, CreatedAt: s.Now()}
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		result.State = "settled"
		result.Reason = ""
		return tx.Save(&result).Error
	})
	if err == nil && pending != nil {
		err = pending
	}
	return result, err
}

// Release is for a verified pre-acceptance failure. Accepted/incomplete work
// retains its hold until explicit operator reconciliation.
func (s *Service) Release(ctx context.Context, id, reason string) (core.Reservation, error) {
	return s.release(ctx, id, reason, false)
}

// ConfirmRejected releases a conservatively marked dispatch only after the
// relay verified every attempt rejected the request before provider acceptance.
// It is not a cancellation/timeout escape hatch; uncertain attempts stay held.
func (s *Service) ConfirmRejected(ctx context.Context, id, reason string) (core.Reservation, error) {
	return s.release(ctx, id, reason, true)
}
func (s *Service) release(ctx context.Context, id, reason string, operator bool) (core.Reservation, error) {
	var result core.Reservation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := locked(tx).Where("request_id = ?", id).First(&result).Error; err != nil {
			return err
		}
		if result.State == "released" {
			return nil
		}
		if result.State == "settled" {
			return business("invalid_reservation", 409, "Settled usage cannot be released")
		}
		if result.UpstreamAccepted && !operator {
			return business("reconciliation_required", 409, "Accepted usage requires reconciliation")
		}
		var user core.User
		if err := locked(tx).First(&user, result.UserID).Error; err != nil {
			return err
		}
		var key core.APIKey
		if err := locked(tx).First(&key, result.APIKeyID).Error; err != nil {
			return err
		}
		if user.ReservedBalance < result.ReservedAmount || key.QuotaReserved < result.ReservedTokens {
			return fmt.Errorf("hold counters are inconsistent")
		}
		if err := tx.Model(&user).Update("reserved_balance", user.ReservedBalance-result.ReservedAmount).Error; err != nil {
			return err
		}
		if err := tx.Model(&key).Update("quota_reserved", key.QuotaReserved-result.ReservedTokens).Error; err != nil {
			return err
		}
		if result.SubscriptionID != nil {
			var sub core.Subscription
			if err := locked(tx).First(&sub, *result.SubscriptionID).Error; err != nil {
				return err
			}
			if sub.QuotaReserved < result.ReservedTokens {
				return fmt.Errorf("subscription hold is inconsistent")
			}
			if err := tx.Model(&sub).Update("quota_reserved", sub.QuotaReserved-result.ReservedTokens).Error; err != nil {
				return err
			}
		}
		result.State = "released"
		result.Reason = reason
		result.UpdatedAt = s.Now()
		return tx.Save(&result).Error
	})
	return result, err
}

func (s *Service) MarkReconciliation(ctx context.Context, id, reason string) (core.Reservation, error) {
	return s.markReconciliation(ctx, id, reason, nil)
}

// MarkReconciliationWithUsage retains provider usage observed before an
// interruption. Incomplete counts are evidence, never an estimated charge.
func (s *Service) MarkReconciliationWithUsage(ctx context.Context, id, reason string, usage core.Usage) (core.Reservation, error) {
	if _, err := Tokens(usage); err != nil {
		return core.Reservation{}, err
	}
	return s.markReconciliation(ctx, id, reason, &usage)
}

func (s *Service) markReconciliation(ctx context.Context, id, reason string, usage *core.Usage) (core.Reservation, error) {
	var result core.Reservation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := locked(tx).Where("request_id = ?", id).First(&result).Error; err != nil {
			return err
		}
		if result.State == "settled" || result.State == "released" {
			return nil
		}
		// Once confirmed complete usage has been captured, a later cancellation
		// cannot replace it with a partial snapshot or erase its failure reason.
		var existing core.Usage
		if result.UsageJSON != "" && json.Unmarshal([]byte(result.UsageJSON), &existing) == nil && existing.Complete {
			return nil
		}
		if usage != nil {
			encoded, err := json.Marshal(usage)
			if err != nil {
				return err
			}
			result.UsageJSON = string(encoded)
			result.ActualTokens, _ = Tokens(*usage)
		}
		result.State = "reconciliation"
		result.Reason = reason
		result.UpdatedAt = s.Now()
		return tx.Save(&result).Error
	})
	return result, err
}

// RecoverPending is called once after exclusive startup, or after all request
// workers are stopped. It must never run concurrently with another instance's
// live requests; operators use explicit reconciliation for shared deployments.
func (s *Service) RecoverPending(ctx context.Context) error {
	var holds []core.Reservation
	if err := s.db.WithContext(ctx).Where("state = ?", "reserved").Find(&holds).Error; err != nil {
		return err
	}
	for _, hold := range holds {
		var err error
		if hold.UpstreamAccepted {
			_, err = s.MarkReconciliation(ctx, hold.RequestID, "Interrupted request: provider acceptance is possible")
		} else {
			_, err = s.Release(ctx, hold.RequestID, "Interrupted before provider dispatch")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Account(ctx context.Context, userID, keyID int64) (v1.Account, error) {
	var result v1.Account
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := locked(tx).First(&user, userID).Error; err != nil {
			return err
		}
		key := core.APIKey{Name: "Console session"}
		if keyID != 0 {
			if err := locked(tx).Where("id = ? AND user_id = ?", keyID, userID).First(&key).Error; err != nil {
				return err
			}
		}
		if user.Balance < user.ReservedBalance {
			return business("billing_unavailable", 503, "Wallet counters are inconsistent")
		}
		result = v1.Account{ContractVersion: v1.Version, Balance: v1.Balance{Amount: user.Balance - user.ReservedBalance, Currency: user.Currency}, Key: v1.Key{Name: key.Name, QuotaUnit: "tokens"}, RateLimits: v1.RateLimits{RequestsPerMinute: key.RequestsPerMinute, TokensPerMinute: key.TokensPerMinute, ConcurrentRequests: key.ConcurrentRequests}}
		if key.ExpiresAt != nil {
			v := key.ExpiresAt.UTC().Format(time.RFC3339)
			result.Key.ExpiresAt = &v
		}
		if key.QuotaLimit != nil {
			remaining := *key.QuotaLimit - key.QuotaUsed - key.QuotaReserved
			if remaining < 0 {
				remaining = 0
			}
			result.Key.RemainingQuota = &remaining
		}
		var sub core.Subscription
		if err := tx.Where("user_id = ?", userID).Order("id DESC").First(&sub).Error; err == nil {
			start, end := sub.PeriodStart.UTC().Format(time.RFC3339), sub.PeriodEnd.UTC().Format(time.RFC3339)
			result.Plan = &v1.Plan{Name: sub.PlanName, PeriodStart: &start, PeriodEnd: &end, Quota: v1.Quota{Unit: "tokens", Total: sub.QuotaTotal, Used: sub.QuotaUsed}}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return nil
	})
	return result, err
}
