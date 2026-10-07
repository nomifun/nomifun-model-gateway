// SPDX-License-Identifier: Apache-2.0
package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
)

func (s *Service) Credit(ctx context.Context, userID int64, key string, amount int64, currency string) (core.LedgerEntry, error) {
	var result core.LedgerEntry
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.CreditTx(tx, userID, key, amount, currency)
		return err
	})
	return result, err
}

// CreditTx belongs inside the caller's transaction, allowing payment events,
// order state and wallet effects to commit together.
func (s *Service) CreditTx(tx *gorm.DB, userID int64, key string, amount int64, currency string) (core.LedgerEntry, error) {
	var result core.LedgerEntry
	if key == "" || len(key) > 200 || amount <= 0 || !validCurrency(currency) {
		return result, business("invalid_credit", 400, "Invalid wallet credit")
	}
	var user core.User
	if err := locked(tx).First(&user, userID).Error; err != nil {
		return result, err
	}
	if err := tx.Where("idempotency_key = ?", key).First(&result).Error; err == nil {
		if result.UserID != userID || result.Amount != amount || result.Currency != currency || result.Kind != "credit" {
			return result, business("idempotency_conflict", 409, "Credit key already has different parameters")
		}
		return result, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if user.Currency != currency {
		return result, business("invalid_credit", 400, "Wallet currency does not match credit")
	}
	balance, err := add(user.Balance, amount)
	if err != nil {
		return result, err
	}
	if err := tx.Model(&user).Update("balance", balance).Error; err != nil {
		return result, err
	}
	result = core.LedgerEntry{IdempotencyKey: key, UserID: userID, Kind: "credit", Amount: amount, Currency: currency, BalanceAfter: balance, CreatedAt: s.Now()}
	return result, tx.Create(&result).Error
}

func (s *Service) ActivateSubscription(ctx context.Context, userID, planID int64, key string) (core.Subscription, error) {
	var result core.Subscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan core.Plan
		if err := tx.First(&plan, planID).Error; err != nil {
			return err
		}
		if !plan.Enabled {
			return business("invalid_plan", 400, "Plan is unavailable")
		}
		var err error
		result, err = s.ActivateSubscriptionTx(tx, userID, plan, key)
		return err
	})
	return result, err
}

// ActivateSubscriptionTx consumes a verified immutable checkout snapshot. It
// grants service; it does not charge a wallet because the payment was external.
func (s *Service) ActivateSubscriptionTx(tx *gorm.DB, userID int64, plan core.Plan, key string) (core.Subscription, error) {
	var result core.Subscription
	if key == "" || len(key) > 200 || plan.ID <= 0 || plan.PeriodDays <= 0 || plan.PeriodDays > 36500 || plan.Price < 0 || !validCurrency(plan.Currency) || (plan.TokenQuota != nil && *plan.TokenQuota < 0) {
		return result, business("invalid_plan", 400, "Invalid subscription plan")
	}
	var user core.User
	if err := locked(tx).First(&user, userID).Error; err != nil {
		return result, err
	}
	if user.Currency != plan.Currency {
		return result, business("invalid_plan", 400, "Wallet and plan currencies differ")
	}
	var existing core.LedgerEntry
	if err := tx.Where("idempotency_key = ?", key).First(&existing).Error; err == nil {
		if existing.UserID != userID || existing.Kind != "subscription" || existing.Currency != plan.Currency {
			return result, business("idempotency_conflict", 409, "Subscription key already has different parameters")
		}
		id, err := strconv.ParseInt(existing.RequestID, 10, 64)
		if err != nil {
			return result, err
		}
		if err := tx.First(&result, id).Error; err != nil {
			return result, err
		}
		if result.PlanID != plan.ID || result.PlanName != plan.Name || !equalQuota(result.QuotaTotal, plan.TokenQuota) || result.ModelIDsJSON != plan.ModelIDsJSON || result.PeriodEnd.Sub(result.PeriodStart) != time.Duration(plan.PeriodDays)*24*time.Hour {
			return result, business("idempotency_conflict", 409, "Subscription key already has different plan parameters")
		}
		return result, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if !validModelIDs(plan.ModelIDsJSON) {
		return result, business("invalid_plan", 400, "Invalid plan model list")
	}
	now := s.Now()
	end := now.Add(time.Duration(plan.PeriodDays) * 24 * time.Hour)
	result = core.Subscription{UserID: userID, PlanID: plan.ID, PlanName: plan.Name, PeriodStart: now, PeriodEnd: end, QuotaTotal: plan.TokenQuota, ModelIDsJSON: plan.ModelIDsJSON, CreatedAt: now}
	if err := tx.Create(&result).Error; err != nil {
		return result, err
	}
	entry := core.LedgerEntry{IdempotencyKey: key, UserID: userID, RequestID: strconv.FormatInt(result.ID, 10), Kind: "subscription", Currency: plan.Currency, BalanceAfter: user.Balance, CreatedAt: now}
	return result, tx.Create(&entry).Error
}
func equalQuota(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (s *Service) ApplyPayment(tx *gorm.DB, order core.PaymentOrder, eventKey string) error {
	key := "payment:" + eventKey
	if order.Kind == "wallet" {
		_, err := s.CreditTx(tx, order.UserID, key, order.Amount, order.Currency)
		return err
	}
	if order.Kind != "subscription" || order.PlanID == nil {
		return business("invalid_order", 400, "Unsupported payment order")
	}
	var plan core.Plan
	if err := json.Unmarshal([]byte(order.PlanSnapshotJSON), &plan); err != nil {
		return business("invalid_order", 400, "Payment has no valid plan snapshot")
	}
	if plan.ID != *order.PlanID || plan.Price != order.Amount || plan.Currency != order.Currency {
		return business("invalid_order", 400, "Payment plan snapshot differs from paid order")
	}
	_, err := s.ActivateSubscriptionTx(tx, order.UserID, plan, key)
	return err
}

func CodeHash(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}
func (s *Service) Redeem(ctx context.Context, userID int64, code string) (core.RedeemCode, error) {
	var result core.RedeemCode
	if code == "" || len(code) > 512 {
		return result, business("invalid_redeem_code", 400, "Invalid redeem code")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// User first follows the wallet/payment lock order and prevents credits
		// for the same wallet racing with redemption effects.
		var user core.User
		if err := locked(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if err := locked(tx).Where("hash = ?", CodeHash(code)).First(&result).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return business("invalid_redeem_code", 400, "Invalid redeem code")
			}
			return err
		}
		if result.RedeemedBy != nil {
			if *result.RedeemedBy == userID {
				return nil
			}
			return business("invalid_redeem_code", 400, "Redeem code has already been used")
		}
		now := s.Now()
		if result.ExpiresAt != nil && !result.ExpiresAt.After(now) {
			return business("invalid_redeem_code", 400, "Redeem code has expired")
		}
		key := "redeem:" + strconv.FormatInt(result.ID, 10)
		if result.PlanID != nil {
			var plan core.Plan
			if err := tx.First(&plan, *result.PlanID).Error; err != nil {
				return err
			}
			if !plan.Enabled {
				return business("invalid_plan", 400, "Redeem plan is unavailable")
			}
			if _, err := s.ActivateSubscriptionTx(tx, userID, plan, key); err != nil {
				return err
			}
		} else {
			if _, err := s.CreditTx(tx, userID, key, result.Amount, result.Currency); err != nil {
				return err
			}
		}
		result.RedeemedBy = &userID
		result.RedeemedAt = &now
		return tx.Save(&result).Error
	})
	return result, err
}

// Reconcile requires an operator's explanation and records the action in the
// same transaction as its wallet/quota effect. No estimated charge is allowed.
func (s *Service) Reconcile(ctx context.Context, id, action, reason string, actorID int64, usage core.Usage) (core.Reservation, error) {
	var result core.Reservation
	if strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return result, business("invalid_reconciliation", 400, "An operator explanation is required")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var actor core.User
		if err := tx.First(&actor, actorID).Error; err != nil {
			return err
		}
		if !actor.Admin || actor.Disabled {
			return business("forbidden", 403, "Administrator access is required")
		}
		inner := *s
		inner.db = tx
		var err error
		switch action {
		case "settle":
			result, err = inner.Settle(ctx, id, usage)
		case "release":
			result, err = inner.release(ctx, id, reason, true)
		default:
			return business("invalid_reconciliation", 400, "Unsupported reconciliation action")
		}
		if err != nil {
			return err
		}
		resource, _ := json.Marshal(map[string]string{"request_id": id, "action": action, "reason": reason})
		return tx.Create(&core.AuditLog{UserID: actorID, Action: "billing.reconcile", Resource: string(resource), CreatedAt: s.Now()}).Error
	})
	return result, err
}
