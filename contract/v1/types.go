// SPDX-License-Identifier: Apache-2.0
// Package v1 defines the frozen NomiFun Model Gateway control-plane wire types.
// Native inference bodies remain native; they are not converted to these types.
package v1

const Version = "1.0"

type Endpoint string
type Task string

const (
	OpenAI          Endpoint = "openai"
	Responses       Endpoint = "openai-response"
	Anthropic       Endpoint = "anthropic"
	Gemini          Endpoint = "gemini"
	Images          Endpoint = "image-generation"
	Embeddings      Endpoint = "embeddings"
	Rerank          Endpoint = "jina-rerank"
	Chat            Task     = "chat"
	ImageGeneration Task     = "image_generation"
	ImageEdit       Task     = "image_edit"
	Embedding       Task     = "embedding"
	Reranking       Task     = "rerank"
)

// Operator URLs are absolute HTTPS URLs. Local HTTP applies only to the gateway
// API base URL, never to purchase links. Optional links are explicitly null.
type Operator struct {
	Name        string  `json:"name"`
	HomepageURL *string `json:"homepage_url"`
	ConsoleURL  *string `json:"console_url"`
	PurchaseURL *string `json:"purchase_url"`
	TermsURL    *string `json:"terms_url"`
	PrivacyURL  *string `json:"privacy_url"`
}
type Meta struct {
	ContractVersion   string     `json:"contract_version"`
	Operator          Operator   `json:"operator"`
	Capabilities      []Endpoint `json:"capabilities"`
	OptionalEndpoints []string   `json:"optional_endpoints"`
}
type TaskEndpoints struct {
	Endpoints         []Endpoint `json:"endpoints"`
	PreferredEndpoint Endpoint   `json:"preferred_endpoint"`
}

// Price is integer minor currency units per UnitSize of Meter for a task.
// Missing meters are unpriced, not free. A zero amount explicitly means free.
type Price struct {
	Task     Task   `json:"task"`
	Meter    string `json:"meter"`
	UnitSize int64  `json:"unit_size"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}
type Model struct {
	ID              string                 `json:"id"`
	DisplayName     string                 `json:"display_name"`
	Vendor          string                 `json:"vendor"`
	Tasks           []Task                 `json:"tasks"`
	TaskEndpoints   map[Task]TaskEndpoints `json:"task_endpoints"`
	ContextWindow   *int64                 `json:"context_window"`
	MaxOutputTokens *int64                 `json:"max_output_tokens"`
	InputModalities []string               `json:"input_modalities"`
	Traits          []string               `json:"traits"`
	Pricing         []Price                `json:"pricing"`
	IncludedInPlan  bool                   `json:"included_in_plan"`
	Status          string                 `json:"status"`
}
type Catalog struct {
	ContractVersion string  `json:"contract_version"`
	Models          []Model `json:"models"`
}
type Quota struct {
	Unit  string `json:"unit"`
	Total *int64 `json:"total"`
	Used  int64  `json:"used"`
}
type Plan struct {
	Name        string  `json:"name"`
	PeriodStart *string `json:"period_start"`
	PeriodEnd   *string `json:"period_end"`
	Quota       Quota   `json:"quota"`
}
type Balance struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}
type Key struct {
	Name           string  `json:"name"`
	ExpiresAt      *string `json:"expires_at"`
	QuotaUnit      string  `json:"quota_unit"`
	RemainingQuota *int64  `json:"remaining_quota"`
}
type RateLimits struct {
	RequestsPerMinute  *int64 `json:"requests_per_minute"`
	TokensPerMinute    *int64 `json:"tokens_per_minute"`
	ConcurrentRequests *int64 `json:"concurrent_requests"`
}
type Account struct {
	ContractVersion string     `json:"contract_version"`
	Plan            *Plan      `json:"plan"`
	Balance         Balance    `json:"balance"`
	Key             Key        `json:"key"`
	RateLimits      RateLimits `json:"rate_limits"`
}
