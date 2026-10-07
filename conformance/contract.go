// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

func object(data []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, errors.New("invalid JSON object")
	}
	return obj, nil
}
func required(obj map[string]json.RawMessage, fields ...string) error {
	for _, f := range fields {
		if _, ok := obj[f]; !ok {
			return errors.New("required contract field missing")
		}
	}
	return nil
}
func nested(obj map[string]json.RawMessage, name string, fields ...string) error {
	o, e := object(obj[name])
	if e != nil {
		return errors.New("invalid nested contract object")
	}
	return required(o, fields...)
}
func currency(s string) bool { return regexp.MustCompile(`^[A-Z]{3}$`).MatchString(s) }
func https(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
func utc(s *string) bool {
	if s == nil {
		return true
	}
	_, e := time.Parse(time.RFC3339Nano, *s)
	return e == nil && strings.HasSuffix(*s, "Z")
}
func positive(p *int64) bool    { return p == nil || *p > 0 }
func nonnegative(p *int64) bool { return p == nil || *p >= 0 }
func rawInt64(o map[string]json.RawMessage, key string) bool {
	var value *int64
	return json.Unmarshal(o[key], &value) == nil && value != nil
}
func contains[T comparable](values []T, value T) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func unique[T comparable](values []T) bool {
	seen := map[T]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func validateMeta(data []byte) error {
	o, e := object(data)
	if e != nil {
		return e
	}
	if e = required(o, "contract_version", "operator", "capabilities", "optional_endpoints"); e != nil {
		return e
	}
	if e = nested(o, "operator", "name", "homepage_url", "console_url", "purchase_url", "terms_url", "privacy_url"); e != nil {
		return e
	}
	var m v1.Meta
	if json.Unmarshal(data, &m) != nil {
		return errors.New("invalid meta field types")
	}
	if m.ContractVersion != v1.Version || strings.TrimSpace(m.Operator.Name) == "" || m.Capabilities == nil || m.OptionalEndpoints == nil || !unique(m.Capabilities) || !unique(m.OptionalEndpoints) {
		return errors.New("invalid meta version, operator or capabilities")
	}
	for _, ep := range m.Capabilities {
		if !knownEndpoint(string(ep)) {
			return errors.New("unknown capability endpoint")
		}
	}
	for _, s := range []*string{m.Operator.HomepageURL, m.Operator.ConsoleURL, m.Operator.PurchaseURL, m.Operator.TermsURL, m.Operator.PrivacyURL} {
		if s != nil && !https(*s) {
			return errors.New("operator link must be an absolute HTTPS URL")
		}
	}
	for _, p := range m.OptionalEndpoints {
		if p != "/v1/messages/count_tokens" {
			return errors.New("unsupported optional endpoint")
		}
	}
	return nil
}

func validateCatalog(data []byte) error {
	o, e := object(data)
	if e != nil {
		return e
	}
	if e = required(o, "contract_version", "models"); e != nil {
		return e
	}
	var c v1.Catalog
	if json.Unmarshal(data, &c) != nil {
		return errors.New("catalog field types invalid (integer fields require signed int64)")
	}
	if c.ContractVersion != v1.Version || c.Models == nil {
		return errors.New("invalid catalog version or models")
	}
	var rawModels []map[string]json.RawMessage
	if json.Unmarshal(o["models"], &rawModels) != nil {
		return errors.New("invalid catalog model list")
	}
	ids := map[string]bool{}
	for i, m := range c.Models {
		raw := rawModels[i]
		if e = required(raw, "id", "display_name", "vendor", "tasks", "task_endpoints", "context_window", "max_output_tokens", "input_modalities", "traits", "pricing", "included_in_plan", "status"); e != nil {
			return e
		}
		if m.ID == "" || ids[m.ID] || strings.TrimSpace(m.DisplayName) == "" || strings.TrimSpace(m.Vendor) == "" {
			return errors.New("model identity must be nonempty and unique")
		}
		ids[m.ID] = true
		if len(m.Tasks) == 0 || !unique(m.Tasks) || m.TaskEndpoints == nil || len(m.TaskEndpoints) != len(m.Tasks) || m.InputModalities == nil || !unique(m.InputModalities) || m.Traits == nil || m.Pricing == nil {
			return errors.New("invalid model collections")
		}
		if !positive(m.ContextWindow) || !positive(m.MaxOutputTokens) {
			return errors.New("model token limits must be positive integers or null")
		}
		if m.Status != "available" && m.Status != "unavailable" && m.Status != "degraded" {
			return errors.New("invalid model status")
		}
		for _, mod := range m.InputModalities {
			if strings.TrimSpace(mod) == "" {
				return errors.New("input modality must be nonempty")
			}
		}
		var caps map[string]map[string]json.RawMessage
		if json.Unmarshal(raw["task_endpoints"], &caps) != nil {
			return errors.New("invalid task endpoint mappings")
		}
		for _, task := range m.Tasks {
			if !contains([]v1.Task{v1.Chat, v1.ImageGeneration, v1.ImageEdit, v1.Embedding, v1.Reranking}, task) {
				return errors.New("unrecognized task")
			}
			cap, ok := m.TaskEndpoints[task]
			if !ok {
				return errors.New("task endpoint mapping missing")
			}
			if e = required(caps[string(task)], "endpoints", "preferred_endpoint"); e != nil {
				return e
			}
			if len(cap.Endpoints) == 0 || !unique(cap.Endpoints) || !knownEndpoint(string(cap.PreferredEndpoint)) || !contains(cap.Endpoints, cap.PreferredEndpoint) {
				return errors.New("preferred endpoint must be recognized and belong to task endpoints")
			}
			for _, ep := range cap.Endpoints {
				if !knownEndpoint(string(ep)) {
					return errors.New("unrecognized task endpoint")
				}
				valid := false
				switch task {
				case v1.Chat:
					valid = contains([]v1.Endpoint{v1.OpenAI, v1.Responses, v1.Anthropic, v1.Gemini}, ep)
				case v1.ImageGeneration, v1.ImageEdit:
					valid = ep == v1.Images
				case v1.Embedding:
					valid = ep == v1.Embeddings
				case v1.Reranking:
					valid = ep == v1.Rerank
				}
				if !valid {
					return errors.New("endpoint is incompatible with task")
				}
			}
			if cap.PreferredEndpoint == v1.Anthropic && m.MaxOutputTokens == nil {
				return errors.New("Anthropic preferred endpoint requires max_output_tokens")
			}
		}
		var included *bool
		if json.Unmarshal(raw["included_in_plan"], &included) != nil || included == nil {
			return errors.New("included_in_plan must be a boolean")
		}
		var prices []map[string]json.RawMessage
		if json.Unmarshal(raw["pricing"], &prices) != nil {
			return errors.New("invalid pricing array")
		}
		priceIDs := map[string]bool{}
		for j, p := range m.Pricing {
			if e = required(prices[j], "task", "meter", "unit_size", "amount", "currency"); e != nil {
				return e
			}
			identity := string(p.Task) + "\x00" + p.Meter + "\x00" + p.Currency
			if !rawInt64(prices[j], "amount") || !rawInt64(prices[j], "unit_size") || priceIDs[identity] || !contains(m.Tasks, p.Task) || p.Meter == "" || p.UnitSize <= 0 || p.Amount < 0 || !currency(p.Currency) {
				return errors.New("invalid or duplicate price task, integer amount, meter or currency")
			}
			priceIDs[identity] = true
		}
	}
	return nil
}

func validateAccount(data []byte) error {
	o, e := object(data)
	if e != nil {
		return e
	}
	if e = required(o, "contract_version", "plan", "balance", "key", "rate_limits"); e != nil {
		return e
	}
	if e = nested(o, "balance", "amount", "currency"); e != nil {
		return e
	}
	if e = nested(o, "key", "name", "expires_at", "quota_unit", "remaining_quota"); e != nil {
		return e
	}
	if e = nested(o, "rate_limits", "requests_per_minute", "tokens_per_minute", "concurrent_requests"); e != nil {
		return e
	}
	var a v1.Account
	if json.Unmarshal(data, &a) != nil {
		return errors.New("account field types invalid (money and quota require signed int64)")
	}
	if a.ContractVersion != v1.Version || !currency(a.Balance.Currency) || strings.TrimSpace(a.Key.Name) == "" || a.Key.QuotaUnit == "" || !utc(a.Key.ExpiresAt) || !nonnegative(a.Key.RemainingQuota) {
		return errors.New("invalid account version, balance, key or UTC expiration")
	}
	bal, _ := object(o["balance"])
	if !rawInt64(bal, "amount") {
		return errors.New("balance amount requires signed int64")
	}
	if !nonnegative(a.RateLimits.RequestsPerMinute) || !nonnegative(a.RateLimits.TokensPerMinute) || !nonnegative(a.RateLimits.ConcurrentRequests) {
		return errors.New("rate limits must be nonnegative integers or null")
	}
	if a.Plan != nil {
		p := a.Plan
		if e = nested(o, "plan", "name", "period_start", "period_end", "quota"); e != nil {
			return e
		}
		po, _ := object(o["plan"])
		if e = nested(po, "quota", "unit", "total", "used"); e != nil {
			return e
		}
		quota, _ := object(po["quota"])
		if !rawInt64(quota, "used") || strings.TrimSpace(p.Name) == "" || !utc(p.PeriodStart) || !utc(p.PeriodEnd) || p.Quota.Unit == "" || !nonnegative(p.Quota.Total) || p.Quota.Used < 0 {
			return errors.New("invalid plan, UTC periods or quota")
		}
		if p.PeriodStart != nil && p.PeriodEnd != nil {
			start, _ := time.Parse(time.RFC3339Nano, *p.PeriodStart)
			end, _ := time.Parse(time.RFC3339Nano, *p.PeriodEnd)
			if end.Before(start) {
				return errors.New("plan period end must not precede start")
			}
		}
	}
	return nil
}

func validateNativeError(endpoint string, data []byte, code string) error {
	var o map[string]json.RawMessage
	if json.Unmarshal(data, &o) != nil || o == nil {
		return errors.New("invalid native error JSON")
	}
	var e map[string]json.RawMessage
	if json.Unmarshal(o["error"], &e) != nil || e == nil {
		return errors.New("native error object missing")
	}
	var message string
	if json.Unmarshal(e["message"], &message) != nil || message == "" {
		return errors.New("native error message missing")
	}
	var actual string
	var purchase *string
	if endpoint == "gemini" {
		var status string
		var n int
		_ = json.Unmarshal(e["status"], &status)
		if json.Unmarshal(e["code"], &n) != nil || n < 400 || n > 599 || status == "" {
			return errors.New("invalid Gemini numeric error status envelope")
		}
		var details []struct {
			Type     string            `json:"@type"`
			Reason   string            `json:"reason"`
			Domain   string            `json:"domain"`
			Metadata map[string]string `json:"metadata"`
		}
		if json.Unmarshal(e["details"], &details) != nil {
			return errors.New("Gemini ErrorInfo details missing")
		}
		for _, d := range details {
			if d.Type == "type.googleapis.com/google.rpc.ErrorInfo" && d.Domain == "nomifun-model-gateway" {
				actual = d.Metadata["nomifun_code"]
				if d.Reason != strings.ToUpper(actual) {
					return errors.New("Gemini ErrorInfo reason differs from business code")
				}
				if s, ok := d.Metadata["purchase_url"]; ok {
					purchase = &s
				}
			}
		}
	} else {
		_ = json.Unmarshal(e["code"], &actual)
		if endpoint == "anthropic" {
			var typ, et string
			_ = json.Unmarshal(o["type"], &typ)
			_ = json.Unmarshal(e["type"], &et)
			if typ != "error" || et == "" {
				return errors.New("invalid Anthropic error envelope")
			}
		} else {
			var typ string
			_ = json.Unmarshal(e["type"], &typ)
			if typ == "" {
				return errors.New("OpenAI error type missing")
			}
		}
		if contains(billingCodes, code) {
			if _, ok := e["purchase_url"]; !ok {
				return errors.New("billing purchase_url field missing")
			}
			if json.Unmarshal(e["purchase_url"], &purchase) != nil {
				return errors.New("invalid purchase_url field")
			}
		}
	}
	if code != "" && actual != code {
		return errors.New("native error code differs from expected billing or affinity code")
	}
	if purchase != nil && !https(*purchase) {
		return errors.New("billing purchase URL must be absolute HTTPS")
	}
	return nil
}
