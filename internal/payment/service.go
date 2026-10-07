// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Fulfiller interface {
	ApplyPayment(*gorm.DB, core.PaymentOrder, string) error
}
type Service struct {
	db        *gorm.DB
	billing   Fulfiller
	configs   *ConfigRepository
	transport http.RoundTripper
}

func New(db *gorm.DB, billing Fulfiller, configs *ConfigRepository) *Service {
	configs.db = db
	return &Service{db: db, billing: billing, configs: configs}
}

type CreateOrderRequest struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	PlanID   *int64 `json:"plan_id,omitempty"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}
type ProviderStatus struct {
	Provider   string   `json:"provider"`
	Available  bool     `json:"available"`
	Currencies []string `json:"currencies"`
}
type ReconcileResult struct {
	OrderID string `json:"order_id"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) provider(ctx context.Context, name string, create bool) (Provider, Config, error) {
	c, err := s.configs.Load(ctx, name)
	if err != nil {
		return nil, c, err
	}
	if !configured(c) || create && !c.Enabled {
		return nil, c, ErrProviderUnavailable
	}
	client := newHTTPClient(s.transport)
	var p Provider
	switch name {
	case "stripe":
		p, err = newStripe(c, client)
	case "alipay":
		p, err = newAlipay(c, client)
	case "wechat":
		p, err = newWechat(c, client)
	default:
		err = ErrProviderUnavailable
	}
	return p, c, err
}
func (s *Service) Providers(ctx context.Context) ([]ProviderStatus, error) {
	out := make([]ProviderStatus, 0, 3)
	for _, name := range []string{"stripe", "alipay", "wechat"} {
		c, err := s.configs.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		currencies := []string{"CNY"}
		if name == "stripe" {
			currencies = []string{"USD", "CNY"}
		}
		out = append(out, ProviderStatus{Provider: name, Available: c.Enabled && configured(c), Currencies: currencies})
	}
	return out, nil
}
func (s *Service) CreateOrder(ctx context.Context, userID int64, r CreateOrderRequest) (core.PaymentOrder, error) {
	s.configs.mu.Lock()
	defer s.configs.mu.Unlock()
	if userID <= 0 || r.Kind != "wallet" && r.Kind != "subscription" {
		return core.PaymentOrder{}, ErrInvalidOrder
	}
	p, c, err := s.provider(ctx, r.Provider, true)
	if err != nil {
		return core.PaymentOrder{}, err
	}
	var user core.User
	if err = s.db.WithContext(ctx).Where("id = ? AND disabled = ?", userID, false).First(&user).Error; err != nil {
		return core.PaymentOrder{}, ErrInvalidOrder
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return core.PaymentOrder{}, err
	}
	now := time.Now().UTC()
	o := core.PaymentOrder{ID: hex.EncodeToString(id[:]), UserID: userID, Provider: r.Provider, Kind: r.Kind, State: "creating", Amount: r.Amount, Currency: r.Currency, MerchantID: c.MerchantID, AppID: c.AppID, ExpiresAt: now.Add(31 * time.Minute)}
	if r.Provider == "stripe" {
		o.MerchantID = c.StripeAccountID
	}
	if r.Kind == "subscription" {
		if r.PlanID == nil || *r.PlanID <= 0 {
			return core.PaymentOrder{}, ErrInvalidOrder
		}
		var plan core.Plan
		if err = s.db.WithContext(ctx).Where("id = ? AND enabled = ?", *r.PlanID, true).First(&plan).Error; err != nil {
			return core.PaymentOrder{}, ErrInvalidOrder
		}
		b, err := json.Marshal(plan)
		if err != nil {
			return core.PaymentOrder{}, err
		}
		o.PlanID = r.PlanID
		o.PlanSnapshotJSON = string(b)
		o.Amount = plan.Price
		o.Currency = plan.Currency
	} else if r.PlanID != nil {
		return core.PaymentOrder{}, ErrInvalidOrder
	}
	if o.Amount <= 0 || o.Amount > 1_000_000_000 || o.Currency != user.Currency || o.Currency != "USD" && o.Currency != "CNY" || r.Provider != "stripe" && o.Currency != "CNY" {
		return core.PaymentOrder{}, ErrInvalidOrder
	}
	if err = s.db.WithContext(ctx).Create(&o).Error; err != nil {
		return core.PaymentOrder{}, err
	}
	checkout, err := p.CreateCheckout(ctx, o)
	if err != nil {
		s.db.WithContext(ctx).Model(&core.PaymentOrder{}).Where("id = ? AND state = ?", o.ID, "creating").Update("state", "failed")
		o.State = "failed"
		return o, err
	}
	updated := s.db.WithContext(ctx).Model(&core.PaymentOrder{}).Where("id = ? AND state = ?", o.ID, "creating").Updates(map[string]any{"state": "pending", "external_id": checkout.ExternalID, "checkout_url": checkout.URL})
	if updated.Error != nil {
		return o, updated.Error
	}
	if updated.RowsAffected == 0 {
		// A callback can race the checkout response. Never report or overwrite
		// a paid order as pending after its transaction has already settled.
		if err = s.db.WithContext(ctx).Where("id = ?", o.ID).First(&o).Error; err != nil {
			return o, err
		}
		return o, nil
	}
	o.State = "pending"
	o.ExternalID = checkout.ExternalID
	o.CheckoutURL = checkout.URL
	return o, nil
}
func (s *Service) GetOrder(ctx context.Context, userID int64, id string) (core.PaymentOrder, error) {
	var o core.PaymentOrder
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return o, ErrOrderNotFound
	}
	return o, err
}
func (s *Service) ListOrders(ctx context.Context, userID int64, limit int) ([]core.PaymentOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	out := []core.PaymentOrder{}
	q := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit)
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Find(&out).Error
	return out, err
}
func (s *Service) HandleWebhook(ctx context.Context, provider string, h http.Header, raw []byte) (core.PaymentOrder, error) {
	p, _, err := s.provider(ctx, provider, false)
	if err != nil {
		return core.PaymentOrder{}, err
	}
	e, err := p.VerifyWebhook(ctx, h, raw)
	if err != nil {
		return core.PaymentOrder{}, err
	}
	return s.apply(ctx, provider, e)
}
func (s *Service) apply(ctx context.Context, provider string, e Event) (core.PaymentOrder, error) {
	if !validProvider(provider) || e.ID == "" || len(e.ID) > 200 || e.OrderID == "" || e.Amount <= 0 || e.Currency != "USD" && e.Currency != "CNY" || e.State != "paid" && e.State != "pending" && e.State != "cancelled" && e.State != "failed" {
		return core.PaymentOrder{}, ErrInvalidEvent
	}
	var o core.PaymentOrder
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", e.OrderID).First(&o).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if o.Provider != provider || o.Amount != e.Amount || o.Currency != e.Currency || o.MerchantID != e.MerchantID || o.AppID != e.AppID || e.ExternalID == "" {
			return ErrInvalidEvent
		}
		// Stripe's Checkout ID is available at creation. AliPay and WeChat
		// return the transaction ID only once the merchant order is paid.
		if provider == "stripe" && o.ExternalID != "" && o.ExternalID != e.ExternalID || provider != "stripe" && o.PaidAt != nil && o.ExternalID != e.ExternalID {
			return ErrInvalidEvent
		}
		var prior core.PaymentEvent
		err := tx.Where("provider = ? AND event_id = ?", provider, e.ID).First(&prior).Error
		if err == nil {
			if prior.OrderID != o.ID || prior.Amount != e.Amount || prior.Currency != e.Currency || prior.ExternalID != e.ExternalID || prior.State != e.State {
				return ErrInvalidEvent
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		record := core.PaymentEvent{Provider: provider, EventID: e.ID, OrderID: o.ID, ExternalID: e.ExternalID, State: e.State, Amount: e.Amount, Currency: e.Currency}
		if err = tx.Create(&record).Error; err != nil {
			return err
		}
		if o.State == "paid" {
			return nil
		} // late failed/expired notifications never undo settled credit
		if e.State == "paid" {
			// Bound ledger keys even when the merchant uses a long event ID.
			// The durable PaymentEvent retains the exact provider ID separately.
			digest := sha256.Sum256([]byte(provider + ":" + e.ID))
			if err = s.billing.ApplyPayment(tx, o, "event:"+hex.EncodeToString(digest[:])); err != nil {
				return err
			}
			paid := e.OccurredAt
			if paid.IsZero() {
				paid = time.Now().UTC()
			}
			o.State = "paid"
			o.PaidAt = &paid
			o.ExternalID = e.ExternalID
			return tx.Model(&core.PaymentOrder{}).Where("id = ?", o.ID).Updates(map[string]any{"state": "paid", "paid_at": paid, "external_id": e.ExternalID}).Error
		}
		if o.State == "cancelled" {
			return nil
		}
		if e.State == "pending" && time.Now().After(o.ExpiresAt) {
			o.State = "expired"
		} else {
			o.State = e.State
		}
		return tx.Model(&core.PaymentOrder{}).Where("id = ?", o.ID).Update("state", o.State).Error
	})
	return o, err
}
func (s *Service) QueryReconcile(ctx context.Context, id string) (core.PaymentOrder, error) {
	var o core.PaymentOrder
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return o, ErrOrderNotFound
		}
		return o, err
	}
	if o.State == "paid" || o.State == "cancelled" {
		return o, nil
	}
	p, _, err := s.provider(ctx, o.Provider, false)
	if err != nil {
		return o, err
	}
	// A Stripe timeout might have created a Checkout before its response was
	// received. Reuse the same immutable form and idempotency key to recover it.
	if o.Provider == "stripe" && o.ExternalID == "" {
		checkout, err := p.CreateCheckout(ctx, o)
		if err != nil {
			return o, err
		}
		if err = s.db.WithContext(ctx).Model(&core.PaymentOrder{}).Where("id = ?", o.ID).Updates(map[string]any{"external_id": checkout.ExternalID, "checkout_url": checkout.URL}).Error; err != nil {
			return o, err
		}
		o.ExternalID = checkout.ExternalID
		o.CheckoutURL = checkout.URL
	}
	e, err := p.Query(ctx, o)
	if err != nil {
		return o, err
	}
	if e.OrderID != o.ID {
		return o, ErrInvalidEvent
	}
	return s.apply(ctx, o.Provider, e)
}
func (s *Service) Reconcile(ctx context.Context, limit int) ([]ReconcileResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var orders []core.PaymentOrder
	if err := s.db.WithContext(ctx).Where("state IN ?", []string{"creating", "pending", "expired", "failed"}).Order("updated_at ASC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	out := make([]ReconcileResult, 0, len(orders))
	for _, o := range orders {
		v, err := s.QueryReconcile(ctx, o.ID)
		r := ReconcileResult{OrderID: o.ID, State: v.State}
		if err != nil {
			r.Error = "merchant reconciliation failed"
		}
		out = append(out, r)
	}
	return out, nil
}
