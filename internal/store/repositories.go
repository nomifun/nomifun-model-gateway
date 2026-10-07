// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/secret"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListUsers(ctx context.Context) ([]core.User, error) {
	out := []core.User{}
	err := s.db.WithContext(ctx).Order("id desc").Find(&out).Error
	return out, err
}
func (s *Store) User(ctx context.Context, id int64) (core.User, error) {
	var u core.User
	err := s.db.WithContext(ctx).First(&u, id).Error
	return u, dbError(err)
}

// Account controls never overwrite wallet amounts, password hashes or identity.
func (s *Store) UpdateUserControls(ctx context.Context, id int64, name string, disabled bool) error {
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return errors.New("user name is required")
	}
	return s.Transaction(ctx, func(tx *gorm.DB) error {
		var target core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, id).Error; err != nil {
			return dbError(err)
		}
		if target.Admin && disabled {
			return errors.New("administrator access controls require an authorized peer")
		}
		return tx.Model(&core.User{}).Where("id = ?", id).Updates(map[string]any{"name": name, "disabled": disabled}).Error
	})
}

// UpdateUserAdmin serializes owner controls and keeps one usable administrator.
// Self-disable and self-demotion require another administrator's action.
func (s *Store) UpdateUserAdmin(ctx context.Context, actorID, id int64, disabled, admin *bool) error {
	return s.Transaction(ctx, func(tx *gorm.DB) error {
		var owners []core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("admin = ? AND disabled = ?", true, false).Order("id").Find(&owners).Error; err != nil {
			return err
		}
		actorAllowed := false
		for _, owner := range owners {
			if owner.ID == actorID {
				actorAllowed = true
			}
		}
		if !actorAllowed {
			return ErrUnauthorized
		}
		var target core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, id).Error; err != nil {
			return dbError(err)
		}
		desiredDisabled, desiredAdmin := target.Disabled, target.Admin
		if disabled != nil {
			desiredDisabled = *disabled
		}
		if admin != nil {
			desiredAdmin = *admin
		}
		if actorID == id && (desiredDisabled || !desiredAdmin) {
			return errors.New("administrator cannot disable or demote their own account")
		}
		if target.Admin && !target.Disabled && (desiredDisabled || !desiredAdmin) && len(owners) <= 1 {
			return errors.New("at least one active administrator is required")
		}
		return tx.Model(&core.User{}).Where("id = ?", id).Updates(map[string]any{"disabled": desiredDisabled, "admin": desiredAdmin}).Error
	})
}
func ValidatePlan(plan core.Plan) error {
	if strings.TrimSpace(plan.Name) == "" || len(plan.Name) > 200 || plan.Price < 0 || !validCurrency(plan.Currency) || plan.PeriodDays <= 0 || plan.PeriodDays > 3650 {
		return errors.New("invalid plan name, amount, currency or period")
	}
	if plan.TokenQuota != nil && *plan.TokenQuota < 0 {
		return errors.New("plan token quota cannot be negative")
	}
	var models []string
	if json.Unmarshal([]byte(plan.ModelIDsJSON), &models) != nil || models == nil {
		return errors.New("plan models must be an array")
	}
	return validateStringList(models)
}
func (s *Store) SavePlan(ctx context.Context, plan core.Plan) (core.Plan, error) {
	if err := ValidatePlan(plan); err != nil {
		return core.Plan{}, err
	}
	if plan.ID == 0 {
		err := s.db.WithContext(ctx).Create(&plan).Error
		return plan, dbError(err)
	}
	r := s.db.WithContext(ctx).Model(&core.Plan{}).Where("id = ?", plan.ID).Updates(map[string]any{"name": plan.Name, "price": plan.Price, "currency": plan.Currency, "period_days": plan.PeriodDays, "token_quota": plan.TokenQuota, "model_ids_json": plan.ModelIDsJSON, "enabled": plan.Enabled})
	if r.Error != nil {
		return core.Plan{}, r.Error
	}
	if r.RowsAffected == 0 {
		return core.Plan{}, ErrNotFound
	}
	var saved core.Plan
	err := s.db.WithContext(ctx).First(&saved, plan.ID).Error
	return saved, dbError(err)
}
func (s *Store) ListPlans(ctx context.Context, enabledOnly bool) ([]core.Plan, error) {
	out := []core.Plan{}
	q := s.db.WithContext(ctx).Order("id")
	if enabledOnly {
		q = q.Where("enabled = ?", true)
	}
	err := q.Find(&out).Error
	return out, err
}
func (s *Store) ListOrders(ctx context.Context, userID int64) ([]core.PaymentOrder, error) {
	out := []core.PaymentOrder{}
	q := s.db.WithContext(ctx).Order("created_at desc")
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Find(&out).Error
	return out, err
}
func (s *Store) ListSubscriptions(ctx context.Context, userID int64) ([]core.Subscription, error) {
	out := []core.Subscription{}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("period_end desc").Find(&out).Error
	return out, err
}
func (s *Store) ListUsage(ctx context.Context, userID int64, limit int) ([]core.Reservation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := []core.Reservation{}
	q := s.db.WithContext(ctx).Order("created_at desc").Limit(limit)
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Find(&out).Error
	return out, err
}
func (s *Store) ListLedger(ctx context.Context, userID int64, limit int) ([]core.LedgerEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := []core.LedgerEntry{}
	q := s.db.WithContext(ctx).Order("id desc").Limit(limit)
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Find(&out).Error
	return out, err
}

func (s *Store) CreateRedeemCode(ctx context.Context, code core.RedeemCode) (core.RedeemCode, string, error) {
	records, plaintexts, err := s.CreateRedeemCodes(ctx, 0, code, 1)
	if err != nil {
		return core.RedeemCode{}, "", err
	}
	return records[0], plaintexts[0], nil
}

func (s *Store) CreateRedeemCodes(ctx context.Context, actorID int64, code core.RedeemCode, count int) ([]core.RedeemCode, []string, error) {
	if count < 1 || count > 100 {
		return nil, nil, errors.New("redeem code count must be between 1 and 100")
	}
	if code.ID != 0 || code.RedeemedBy != nil || code.RedeemedAt != nil {
		return nil, nil, errors.New("new redeem code cannot have id or redemption")
	}
	if code.Amount < 0 || !validCurrency(code.Currency) || (code.PlanID == nil && code.Amount == 0) {
		return nil, nil, errors.New("redeem code requires a positive amount or plan")
	}
	if code.ExpiresAt != nil && !code.ExpiresAt.After(time.Now().UTC()) {
		return nil, nil, errors.New("redeem expiration must be in the future")
	}
	if code.PlanID != nil {
		var plan core.Plan
		if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ?", *code.PlanID, true).First(&plan).Error; err != nil {
			return nil, nil, errors.New("redeem plan is unavailable")
		}
		if plan.Currency != code.Currency {
			return nil, nil, errors.New("redeem plan currency mismatch")
		}
	}
	records := make([]core.RedeemCode, count)
	plaintexts := make([]string, count)
	for i := range count {
		plain, err := secret.Token("nmr_")
		if err != nil {
			return nil, nil, err
		}
		records[i] = code
		records[i].Hash = secret.Hash(plain)
		records[i].Prefix = plain[:12]
		plaintexts[i] = plain
	}
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		for i := range records {
			if err := tx.Create(&records[i]).Error; err != nil {
				return dbError(err)
			}
		}
		if actorID > 0 {
			return tx.Create(&core.AuditLog{UserID: actorID, Action: "redeem.create", Resource: "batch:" + strconv.Itoa(count)}).Error
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return records, plaintexts, nil
}
func (s *Store) ListRedeemCodes(ctx context.Context) ([]core.RedeemCode, error) {
	out := []core.RedeemCode{}
	err := s.db.WithContext(ctx).Order("id desc").Find(&out).Error
	return out, err
}
func (s *Store) DeleteRedeemCode(ctx context.Context, id int64) error {
	r := s.db.WithContext(ctx).Where("id = ? AND redeemed_by IS NULL", id).Delete(&core.RedeemCode{})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Audit(ctx context.Context, userID int64, action, resource string) error {
	if action == "" || strings.ContainsAny(action+resource, "\r\n") {
		return errors.New("invalid audit identity")
	}
	return s.db.WithContext(ctx).Create(&core.AuditLog{UserID: userID, Action: action, Resource: resource}).Error
}
func (s *Store) ListAudit(ctx context.Context, limit int) ([]core.AuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := []core.AuditLog{}
	err := s.db.WithContext(ctx).Order("id desc").Limit(limit).Find(&out).Error
	return out, err
}
