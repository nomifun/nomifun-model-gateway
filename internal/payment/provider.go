// SPDX-License-Identifier: Apache-2.0
// Package payment implements official merchant payments. Payment credentials
// remain encrypted at rest; merchant adapters never accept caller base URLs.
package payment

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

var (
	ErrProviderUnavailable = errors.New("payment provider is not configured or enabled")
	ErrInvalidSignature    = errors.New("invalid payment signature")
	ErrInvalidEvent        = errors.New("invalid or mismatched payment event")
	ErrInvalidOrder        = errors.New("invalid payment order")
	ErrOrderNotFound       = errors.New("payment order not found")
	ErrUpstream            = errors.New("payment merchant request failed")
	ErrIgnoredEvent        = errors.New("payment event type is not handled")
)

type Checkout struct {
	ExternalID string
	URL        string
}
type Event struct {
	ID, OrderID, ExternalID, MerchantID, AppID, State, Currency string
	Amount                                                      int64
	OccurredAt                                                  time.Time
}
type Provider interface {
	Name() string
	CreateCheckout(context.Context, core.PaymentOrder) (Checkout, error)
	VerifyWebhook(context.Context, http.Header, []byte) (Event, error)
	Query(context.Context, core.PaymentOrder) (Event, error)
}

// The fixed merchant origins prevent an administrator from turning credential
// bearing merchant requests into a general URL fetcher. A custom transport is
// available to package tests, while the URL and Host remain the official ones.
func newHTTPClient(transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func fresh(t time.Time, now time.Time) bool {
	d := now.Sub(t)
	return d >= -5*time.Minute && d <= 5*time.Minute
}
