// SPDX-License-Identifier: Apache-2.0
// Uses stripe-go webhook verification without modifying SDK source.
// Source: https://github.com/stripe/stripe-go v83.2.1
// Commit: 84e4bccf468c855c651c76cf4a796940596ddcbb; MIT.
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	stripeSDK "github.com/stripe/stripe-go/v83"
	"github.com/stripe/stripe-go/v83/webhook"
)

type stripeProvider struct {
	cfg    Config
	client *http.Client
}

func newStripe(c Config, client *http.Client) (Provider, error) {
	if c.Provider != "stripe" || !configured(c) {
		return nil, ErrProviderUnavailable
	}
	if err := validateConfig(c); err != nil {
		return nil, err
	}
	return &stripeProvider{cfg: c, client: client}, nil
}
func (*stripeProvider) Name() string { return "stripe" }
func (s *stripeProvider) request(ctx context.Context, method, path string, form url.Values, idempotency string, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.stripe.com"+path, body)
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Stripe-Version", stripeSDK.APIVersion)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUpstream
	}
	if json.Unmarshal(b, out) != nil {
		return ErrUpstream
	}
	return nil
}

type stripeSession struct {
	ID                string `json:"id"`
	URL               string `json:"url"`
	ClientReferenceID string `json:"client_reference_id"`
	AmountTotal       int64  `json:"amount_total"`
	Currency          string `json:"currency"`
	PaymentStatus     string `json:"payment_status"`
	Status            string `json:"status"`
	Livemode          bool   `json:"livemode"`
}

func (s *stripeProvider) CreateCheckout(ctx context.Context, o core.PaymentOrder) (Checkout, error) {
	if o.Currency != "USD" && o.Currency != "CNY" || o.Amount <= 0 {
		return Checkout{}, ErrInvalidOrder
	}
	var acct struct {
		ID string `json:"id"`
	}
	if err := s.request(ctx, http.MethodGet, "/v1/account", nil, "", &acct); err != nil {
		return Checkout{}, err
	}
	if acct.ID != s.cfg.StripeAccountID {
		return Checkout{}, ErrInvalidEvent
	}
	form := url.Values{"mode": {"payment"}, "client_reference_id": {o.ID}, "success_url": {s.cfg.ReturnURL}, "cancel_url": {s.cfg.CancelURL}, "expires_at": {strconv.FormatInt(o.ExpiresAt.Unix(), 10)}, "line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {strings.ToLower(o.Currency)}, "line_items[0][price_data][unit_amount]": {strconv.FormatInt(o.Amount, 10)}, "line_items[0][price_data][product_data][name]": {"Gateway " + o.Kind}, "metadata[gateway_order_id]": {o.ID}}
	var session stripeSession
	if err := s.request(ctx, http.MethodPost, "/v1/checkout/sessions", form, "nmg-order-"+o.ID, &session); err != nil {
		return Checkout{}, err
	}
	if session.ID == "" || session.ClientReferenceID != o.ID || session.AmountTotal != o.Amount || strings.ToUpper(session.Currency) != o.Currency || session.Livemode == s.cfg.Sandbox {
		return Checkout{}, ErrInvalidEvent
	}
	u, err := url.Parse(session.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() != "checkout.stripe.com" || u.User != nil {
		return Checkout{}, ErrInvalidEvent
	}
	return Checkout{ExternalID: session.ID, URL: session.URL}, nil
}
func (s *stripeProvider) VerifyWebhook(_ context.Context, h http.Header, raw []byte) (Event, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return Event{}, ErrInvalidEvent
	}
	sig := h.Get("Stripe-Signature")
	if len(h.Values("Stripe-Signature")) != 1 || strings.TrimSpace(sig) != sig || strings.ContainsAny(sig, "\r\n") {
		return Event{}, ErrInvalidSignature
	}
	if webhook.ValidatePayload(raw, sig, s.cfg.WebhookSecret) != nil {
		return Event{}, ErrInvalidSignature
	}
	var timestamp int64
	timestampCount := 0
	for _, v := range strings.Split(sig, ",") {
		if strings.HasPrefix(v, "t=") {
			timestampCount++
			var err error
			timestamp, err = strconv.ParseInt(strings.TrimPrefix(v, "t="), 10, 64)
			if err != nil {
				return Event{}, ErrInvalidSignature
			}
		}
	}
	if timestampCount != 1 || !fresh(time.Unix(timestamp, 0), time.Now()) {
		return Event{}, ErrInvalidSignature
	}
	var envelope struct {
		ID, Type, Account string
		Created           int64
		Livemode          bool
		Data              struct{ Object json.RawMessage }
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ID == "" || len(envelope.ID) > 200 || envelope.Livemode == s.cfg.Sandbox || envelope.Account != "" && envelope.Account != s.cfg.StripeAccountID {
		return Event{}, ErrInvalidEvent
	}
	switch envelope.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "checkout.session.expired":
	default:
		return Event{}, ErrIgnoredEvent
	}
	var session stripeSession
	if json.Unmarshal(envelope.Data.Object, &session) != nil {
		return Event{}, ErrInvalidEvent
	}
	e, err := s.event(session)
	if err != nil {
		return Event{}, err
	}
	e.ID = envelope.ID
	e.OccurredAt = time.Unix(envelope.Created, 0)
	if envelope.Type == "checkout.session.async_payment_failed" {
		e.State = "failed"
	}
	if envelope.Type == "checkout.session.expired" {
		e.State = "cancelled"
	}
	return e, nil
}
func (s *stripeProvider) event(v stripeSession) (Event, error) {
	if !strings.HasPrefix(v.ID, "cs_") || v.ClientReferenceID == "" || v.AmountTotal <= 0 || v.Livemode == s.cfg.Sandbox {
		return Event{}, ErrInvalidEvent
	}
	state := "pending"
	if v.PaymentStatus == "paid" {
		state = "paid"
	} else if v.Status == "expired" {
		state = "cancelled"
	}
	return Event{OrderID: v.ClientReferenceID, ExternalID: v.ID, MerchantID: s.cfg.StripeAccountID, State: state, Amount: v.AmountTotal, Currency: strings.ToUpper(v.Currency)}, nil
}
func (s *stripeProvider) Query(ctx context.Context, o core.PaymentOrder) (Event, error) {
	if !strings.HasPrefix(o.ExternalID, "cs_") {
		return Event{}, ErrInvalidOrder
	}
	var v stripeSession
	if err := s.request(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(o.ExternalID), nil, "", &v); err != nil {
		return Event{}, err
	}
	e, err := s.event(v)
	if err != nil {
		return Event{}, err
	}
	e.ID = fmt.Sprintf("query:%s:%s", e.ExternalID, e.State)
	e.OccurredAt = time.Now()
	return e, nil
}
