// SPDX-License-Identifier: Apache-2.0
// Package core holds durable gateway domain records shared by the services.
// Native model content is never a durable record here; only usage is retained.
package core

import "time"

type User struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	Email           string    `gorm:"uniqueIndex;size:254" json:"email"`
	Name            string    `json:"name"`
	PasswordHash    string    `json:"-"`
	Admin           bool      `json:"admin"`
	Disabled        bool      `json:"disabled"`
	Balance         int64     `json:"balance"`
	ReservedBalance int64     `json:"reserved_balance"`
	Currency        string    `gorm:"size:3" json:"currency"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type APIKey struct {
	ID                 int64      `gorm:"primaryKey" json:"id"`
	UserID             int64      `gorm:"index;not null" json:"user_id"`
	Name               string     `json:"name"`
	Prefix             string     `gorm:"index;size:24" json:"prefix"`
	Hash               string     `gorm:"uniqueIndex;size:64" json:"-"`
	ExpiresAt          *time.Time `json:"expires_at"`
	QuotaLimit         *int64     `json:"quota_limit"`
	QuotaUsed          int64      `json:"quota_used"`
	QuotaReserved      int64      `json:"quota_reserved"`
	ModelIDsJSON       string     `json:"model_ids_json"`
	AllowedIPsJSON     string     `json:"allowed_ips_json"`
	RequestsPerMinute  *int64     `json:"requests_per_minute"`
	TokensPerMinute    *int64     `json:"tokens_per_minute"`
	ConcurrentRequests *int64     `json:"concurrent_requests"`
	Revoked            bool       `json:"revoked"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
type LoginSession struct {
	Hash      string    `gorm:"primaryKey;size:64" json:"-"`
	UserID    int64     `gorm:"index;not null" json:"user_id"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
type Model struct {
	ID               string    `gorm:"primaryKey;size:200" json:"id"`
	DefinitionJSON   string    `json:"definition_json"`
	Enabled          bool      `json:"enabled"`
	SubscriptionOnly bool      `json:"subscription_only"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// One channel is one immutable upstream account identity. Rotating to a
// different upstream account creates a new channel; affinity is never rebound.
type Channel struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"` // openai, anthropic, gemini, compatible, azure
	BaseURL       string     `json:"base_url"`
	EncryptedKey  string     `json:"-"`
	ModelsJSON    string     `json:"models_json"`    // {public_model_id: upstream_model_id}
	EndpointsJSON string     `json:"endpoints_json"` // endpoint type list
	Priority      int        `json:"priority"`
	Weight        int        `json:"weight"`
	Enabled       bool       `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until"`
	Failures      int64      `json:"failures"`
	APIVersion    string     `json:"api_version"` // Azure version or Anthropic default
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
type ResponseAffinity struct {
	APIKeyID   int64     `gorm:"primaryKey"`
	ResponseID string    `gorm:"primaryKey;size:300"`
	ChannelID  int64     `gorm:"index;not null"`
	ExpiresAt  time.Time `gorm:"index"`
	CreatedAt  time.Time
}
type SessionAffinity struct {
	APIKeyID   int64  `gorm:"primaryKey"`
	SessionID  string `gorm:"primaryKey;size:256"`
	ChannelID  int64  `gorm:"index;not null"`
	LastUsedAt time.Time
	Expired    bool // durable tombstone prevents silently reusing an expired ID
	CreatedAt  time.Time
}
type Plan struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Name         string    `json:"name"`
	Price        int64     `json:"price"`
	Currency     string    `gorm:"size:3" json:"currency"`
	PeriodDays   int64     `json:"period_days"`
	TokenQuota   *int64    `json:"token_quota"`
	ModelIDsJSON string    `json:"model_ids_json"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type Subscription struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	UserID        int64     `gorm:"index;not null" json:"user_id"`
	PlanID        int64     `gorm:"index;not null" json:"plan_id"`
	PlanName      string    `json:"plan_name"`
	PeriodStart   time.Time `json:"period_start"`
	PeriodEnd     time.Time `gorm:"index" json:"period_end"`
	QuotaTotal    *int64    `json:"quota_total"`
	QuotaUsed     int64     `json:"quota_used"`
	QuotaReserved int64     `json:"quota_reserved"`
	ModelIDsJSON  string    `json:"model_ids_json"`
	CreatedAt     time.Time `json:"created_at"`
}
type Reservation struct {
	RequestID        string    `gorm:"primaryKey;size:100" json:"request_id"`
	UserID           int64     `gorm:"index;not null" json:"user_id"`
	APIKeyID         int64     `gorm:"index;not null" json:"api_key_id"`
	SubscriptionID   *int64    `json:"subscription_id"`
	ModelID          string    `json:"model_id"`
	Endpoint         string    `json:"endpoint"`
	Currency         string    `gorm:"size:3" json:"currency"`
	ReservedAmount   int64     `json:"reserved_amount"`
	ReservedTokens   int64     `json:"reserved_tokens"`
	ActualAmount     int64     `json:"actual_amount"`
	ActualTokens     int64     `json:"actual_tokens"`
	State            string    `gorm:"index" json:"state"` // reserved, settled, released, reconciliation
	UpstreamAccepted bool      `json:"upstream_accepted"`
	PricingJSON      string    `json:"pricing_json"` // immutable price snapshot for settlement
	UsageJSON        string    `json:"usage_json"`
	Reason           string    `json:"reason"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type LedgerEntry struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	IdempotencyKey string    `gorm:"uniqueIndex;size:200" json:"idempotency_key"`
	UserID         int64     `gorm:"index;not null" json:"user_id"`
	RequestID      string    `gorm:"index;size:100" json:"request_id"`
	Kind           string    `json:"kind"`
	Amount         int64     `json:"amount"` // signed wallet change, integer minor currency
	Currency       string    `gorm:"size:3" json:"currency"`
	BalanceAfter   int64     `json:"balance_after"`
	CreatedAt      time.Time `json:"created_at"`
}
type PaymentOrder struct {
	ID               string     `gorm:"primaryKey;size:100" json:"id"`
	UserID           int64      `gorm:"index;not null" json:"user_id"`
	Provider         string     `json:"provider"`
	Kind             string     `json:"kind"` // wallet or subscription
	PlanID           *int64     `json:"plan_id"`
	Amount           int64      `json:"amount"`
	Currency         string     `gorm:"size:3" json:"currency"`
	State            string     `gorm:"index" json:"state"` // pending, paid, cancelled, failed
	ExternalID       string     `gorm:"index" json:"external_id"`
	MerchantID       string     `json:"merchant_id"`
	AppID            string     `json:"app_id"`
	PlanSnapshotJSON string     `json:"plan_snapshot_json"`
	CheckoutURL      string     `json:"checkout_url"`
	ExpiresAt        time.Time  `json:"expires_at"`
	PaidAt           *time.Time `json:"paid_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type PaymentEvent struct {
	Provider   string    `gorm:"primaryKey;size:40" json:"provider"`
	EventID    string    `gorm:"primaryKey;size:200" json:"event_id"`
	OrderID    string    `gorm:"index;size:100" json:"order_id"`
	ExternalID string    `json:"external_id"`
	State      string    `json:"state"`
	Amount     int64     `json:"amount"`
	Currency   string    `gorm:"size:3" json:"currency"`
	CreatedAt  time.Time `json:"created_at"`
}
type RedeemCode struct {
	ID         int64      `gorm:"primaryKey" json:"id"`
	Hash       string     `gorm:"uniqueIndex;size:64" json:"-"`
	Prefix     string     `json:"prefix"`
	Amount     int64      `json:"amount"`
	Currency   string     `gorm:"size:3" json:"currency"`
	PlanID     *int64     `json:"plan_id"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RedeemedBy *int64     `json:"redeemed_by"`
	RedeemedAt *time.Time `json:"redeemed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}
type Setting struct {
	Name      string    `gorm:"primaryKey;size:100" json:"name"`
	ValueJSON string    `json:"value_json"`
	UpdatedAt time.Time `json:"updated_at"`
}
type AuditLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int64     `gorm:"index" json:"user_id"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	CreatedAt time.Time `json:"created_at"`
}

// Usage uses totals that include input cache and output reasoning subsets.
// Anthropic's non-cache input is combined with its cache counts; Gemini output
// combines candidate and thought counts. The unmodified native usage object is
// retained in RawJSON, so normalized billing never replaces upstream evidence.
type Usage struct {
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	Images              int64  `json:"images"`
	Requests            int64  `json:"requests"`
	TotalTokens         int64  `json:"total_tokens"`
	RawJSON             string `json:"raw_json"`
	Complete            bool   `json:"complete"`
}
