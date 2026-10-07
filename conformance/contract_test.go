// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

func TestContractRejectsUnsafeOrMalformedMetadata(t *testing.T) {
	meta, _ := json.Marshal(v1.MockMeta())
	cat, _ := json.Marshal(v1.MockCatalog(false))
	account, _ := json.Marshal(v1.MockAccount())
	for _, tc := range []struct {
		name     string
		data     []byte
		validate func([]byte) error
	}{{"meta", meta, validateMeta}, {"catalog", cat, validateCatalog}, {"account", account, validateAccount}} {
		if e := tc.validate(tc.data); e != nil {
			t.Fatalf("valid %s rejected: %v", tc.name, e)
		}
	}
	tests := []struct {
		name string
		body []byte
		f    func([]byte) error
	}{
		{"purchase-http", []byte(strings.Replace(string(meta), "https://operator.example/purchase", "http://operator.example/purchase", 1)), validateMeta},
		{"fractional-money", []byte(strings.Replace(string(account), `"amount":12345`, `"amount":1.5`, 1)), validateAccount},
		{"out-of-range-money", []byte(strings.Replace(string(account), `"amount":12345`, `"amount":9223372036854775808`, 1)), validateAccount},
		{"null-money", []byte(strings.Replace(string(account), `"amount":12345`, `"amount":null`, 1)), validateAccount},
		{"non-UTC-period", []byte(strings.Replace(string(account), "2026-10-01T00:00:00Z", "2026-10-01T08:00:00+08:00", 1)), validateAccount},
		{"unknown-preferred", []byte(strings.Replace(string(cat), `"preferred_endpoint":"anthropic"`, `"preferred_endpoint":"unknown"`, 1)), validateCatalog},
		{"missing-anthropic-limit", []byte(strings.Replace(string(cat), `"max_output_tokens":4096`, `"max_output_tokens":null`, 1)), validateCatalog},
		{"not-in-endpoints", []byte(strings.Replace(string(cat), `"preferred_endpoint":"anthropic"`, `"preferred_endpoint":"openai"`, 1)), validateCatalog},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if e := tc.f(tc.body); e == nil {
				t.Fatal("invalid contract data accepted")
			}
		})
	}
	negative := []byte(strings.Replace(string(account), `"amount":12345`, `"amount":-12345`, 1))
	if e := validateAccount(negative); e != nil {
		t.Fatalf("signed balance rejected: %v", e)
	}
}
func TestFixtureRejectsUnknownOrMultipleJSON(t *testing.T) {
	for _, input := range []string{`{"models":{"unknown":"model"}}`, `{"models":{"openai":""}}`, `{"models":{"openai":"m"},"api_key":"secret"}`, `{"models":{"openai":"m"}} {}`} {
		if _, e := LoadFixture(strings.NewReader(input)); e == nil {
			t.Fatal("invalid fixture accepted")
		}
	}
}
func TestForcedChatToolCannotPassWithTextOnly(t *testing.T) {
	body := []byte(`{"id":"chat_1","object":"chat.completion","model":"test","choices":[{"message":{"role":"assistant","content":"text"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	if e := validateNative(probe{endpoint: "openai"}, body, true); e == nil {
		t.Fatal("text-only response passed forced tool assertion")
	}
}

func TestContractAcceptsExplicitZeroLimitsOverageAndExtensionModality(t *testing.T) {
	a := v1.MockAccount()
	zero := int64(0)
	a.RateLimits.RequestsPerMinute = &zero
	a.RateLimits.TokensPerMinute = &zero
	a.RateLimits.ConcurrentRequests = &zero
	a.Plan.PeriodEnd = a.Plan.PeriodStart
	a.Plan.Quota.Used = *a.Plan.Quota.Total + 1
	data, _ := json.Marshal(a)
	if e := validateAccount(data); e != nil {
		t.Fatalf("valid snapshot rejected: %v", e)
	}
	c := v1.MockCatalog(false)
	c.Models[0].InputModalities = append(c.Models[0].InputModalities, "future_modality")
	data, _ = json.Marshal(c)
	if e := validateCatalog(data); e != nil {
		t.Fatalf("extensible modality rejected: %v", e)
	}
	c.Models[0].InputModalities = append(c.Models[0].InputModalities, "future_modality")
	data, _ = json.Marshal(c)
	if e := validateCatalog(data); e == nil {
		t.Fatal("duplicate input modality accepted")
	}
}

func TestSyntheticReasoningAssertionsRejectFieldLoss(t *testing.T) {
	for _, ep := range []string{"openai-response", "anthropic", "gemini"} {
		if e := validateSyntheticEvidence(probe{endpoint: ep}, []byte(`{"text":"text survives but native reasoning was removed"}`), false); e == nil {
			t.Fatalf("synthetic %s field loss passed", ep)
		}
	}
}
