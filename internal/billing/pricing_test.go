// SPDX-License-Identifier: Apache-2.0
package billing

import (
	"math"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func tokenModel() v1.Model {
	return v1.Model{ID: "test-model", Tasks: []v1.Task{v1.Chat}, Pricing: []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}}}
}
func TestQuoteSeparatesCanonicalSubsetsAndRounds(t *testing.T) {
	model := tokenModel()
	model.Pricing = []v1.Price{
		{Task: v1.Chat, Meter: "input_tokens", UnitSize: 10, Amount: 2, Currency: "USD"},
		{Task: v1.Chat, Meter: "output_tokens", UnitSize: 10, Amount: 3, Currency: "USD"},
		{Task: v1.Chat, Meter: "cache_read_input_tokens", UnitSize: 10, Amount: 1, Currency: "USD"},
		{Task: v1.Chat, Meter: "cache_creation_input_tokens", UnitSize: 10, Amount: 4, Currency: "USD"},
		{Task: v1.Chat, Meter: "reasoning_tokens", UnitSize: 10, Amount: 5, Currency: "USD"},
	}
	// 61 ordinary input => 13; 9 ordinary output => 3; 30 cache
	// read => 3; 10 cache creation => 4; 11 reasoning => 6. Total 29.
	amount, currency, err := Quote(model, v1.Chat, core.Usage{InputTokens: 101, OutputTokens: 20, CacheReadTokens: 30, CacheCreationTokens: 10, ReasoningTokens: 11, TotalTokens: 121})
	if err != nil || amount != 29 || currency != "USD" {
		t.Fatalf("quote = %d %s %v", amount, currency, err)
	}
	// A zero cache tariff still removes its subset from ordinary input.
	model.Pricing[2].Amount = 0
	amount, _, err = Quote(model, v1.Chat, core.Usage{InputTokens: 101, OutputTokens: 20, CacheReadTokens: 30, CacheCreationTokens: 10, ReasoningTokens: 11})
	if err != nil || amount != 26 {
		t.Fatalf("zero cached rate quote = %d %v", amount, err)
	}
}

func TestQuoteFailsClosedForUnknownMissingAndInvalidPricing(t *testing.T) {
	tests := []struct {
		name   string
		prices []v1.Price
		usage  core.Usage
	}{
		{"empty", nil, core.Usage{}},
		{"unknown", []v1.Price{{Task: v1.Chat, Meter: "future", UnitSize: 1, Currency: "USD"}}, core.Usage{}},
		{"missing-output", tokenModel().Pricing, core.Usage{InputTokens: 10, OutputTokens: 1}},
		{"aliases", []v1.Price{{Task: v1.Chat, Meter: "cached_input_tokens", UnitSize: 1, Currency: "USD"}, {Task: v1.Chat, Meter: "cache_read_input_tokens", UnitSize: 1, Currency: "USD"}}, core.Usage{InputTokens: 1, CacheReadTokens: 1}},
		{"cache-subset", tokenModel().Pricing, core.Usage{InputTokens: 1, CacheReadTokens: 2}},
		{"reasoning-subset", tokenModel().Pricing, core.Usage{OutputTokens: 1, ReasoningTokens: 2}},
		{"negative", tokenModel().Pricing, core.Usage{InputTokens: -1}},
		{"bad-total", tokenModel().Pricing, core.Usage{InputTokens: 1, TotalTokens: 2}},
		{"overflow-total", tokenModel().Pricing, core.Usage{InputTokens: math.MaxInt64, OutputTokens: 1}},
		{"overflow-price", []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Amount: 2, Currency: "USD"}}, core.Usage{InputTokens: math.MaxInt64}},
		{"zero-unit", []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 0, Currency: "USD"}}, core.Usage{}},
		{"negative-price", []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Amount: -1, Currency: "USD"}}, core.Usage{}},
		{"mixed-currency", []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Currency: "USD"}, {Task: v1.Chat, Meter: "output_tokens", UnitSize: 1, Currency: "CNY"}}, core.Usage{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := tokenModel()
			model.Pricing = test.prices
			if _, _, err := Quote(model, v1.Chat, test.usage); err == nil {
				t.Fatal("invalid quote accepted")
			}
		})
	}
}

func TestQuoteLargeProductDoesNotOverflowWhenResultFits(t *testing.T) {
	model := tokenModel()
	model.Pricing[0].Amount = math.MaxInt64
	model.Pricing[0].UnitSize = math.MaxInt64
	amount, _, err := Quote(model, v1.Chat, core.Usage{InputTokens: math.MaxInt64})
	if err != nil || amount != math.MaxInt64 {
		t.Fatalf("exact large quote = %d %v", amount, err)
	}
}

func TestExplicitFlatRates(t *testing.T) {
	model := tokenModel()
	model.Pricing = []v1.Price{{Task: v1.Chat, Meter: "requests", UnitSize: 1, Amount: 3, Currency: "USD"}}
	amount, _, err := Quote(model, v1.Chat, core.Usage{InputTokens: 1000, OutputTokens: 1000, Requests: 1})
	if err != nil || amount != 3 {
		t.Fatalf("request quote = %d %v", amount, err)
	}
	model.Pricing = []v1.Price{{Task: v1.ImageGeneration, Meter: "images", UnitSize: 1, Amount: 11, Currency: "USD"}}
	amount, _, err = Quote(model, v1.ImageGeneration, core.Usage{InputTokens: 1000, OutputTokens: 1000, Images: 2, Requests: 1})
	if err != nil || amount != 22 {
		t.Fatalf("image quote = %d %v", amount, err)
	}
}

func TestReservationQuoteIncludesPremiumCacheAndReasoningBounds(t *testing.T) {
	model := tokenModel()
	model.Pricing = []v1.Price{
		{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Amount: 0, Currency: "USD"},
		{Task: v1.Chat, Meter: "output_tokens", UnitSize: 1, Amount: 0, Currency: "USD"},
		{Task: v1.Chat, Meter: "cache_creation_input_tokens", UnitSize: 2, Amount: 3, Currency: "USD"},
		{Task: v1.Chat, Meter: "cache_read_input_tokens", UnitSize: 2, Amount: 1, Currency: "USD"},
		{Task: v1.Chat, Meter: "reasoning_tokens", UnitSize: 3, Amount: 5, Currency: "USD"},
	}
	plain, _, err := Quote(model, v1.Chat, core.Usage{InputTokens: 7, OutputTokens: 5, Requests: 1})
	if err != nil || plain != 0 {
		t.Fatalf("plain estimate = %d %v", plain, err)
	}
	bound, _, err := QuoteUpperBound(model, v1.Chat, core.Usage{InputTokens: 7, OutputTokens: 5, Requests: 1})
	if err != nil || bound != 24 {
		t.Fatalf("premium bound = %d %v", bound, err)
	}
	for input := int64(0); input <= 7; input++ {
		for cacheRead := int64(0); cacheRead <= input; cacheRead++ {
			for cacheCreation := int64(0); cacheCreation <= input-cacheRead; cacheCreation++ {
				for output := int64(0); output <= 5; output++ {
					for reasoning := int64(0); reasoning <= output; reasoning++ {
						actual, _, err := Quote(model, v1.Chat, core.Usage{InputTokens: input, OutputTokens: output, CacheReadTokens: cacheRead, CacheCreationTokens: cacheCreation, ReasoningTokens: reasoning, Requests: 1})
						if err != nil || actual > bound {
							t.Fatalf("actual %d exceeds bound %d: %v", actual, bound, err)
						}
					}
				}
			}
		}
	}
}

func TestReservationQuoteFlatModesAndOverflow(t *testing.T) {
	model := tokenModel()
	model.Pricing = []v1.Price{{Task: v1.Chat, Meter: "requests", UnitSize: 1, Amount: 3, Currency: "USD"}}
	bound, _, err := QuoteUpperBound(model, v1.Chat, core.Usage{InputTokens: 1000, OutputTokens: 1000, Requests: 1})
	if err != nil || bound != 3 {
		t.Fatalf("flat request bound = %d %v", bound, err)
	}
	model.Pricing = []v1.Price{{Task: v1.ImageGeneration, Meter: "images", UnitSize: 1, Amount: 11, Currency: "USD"}}
	bound, _, err = QuoteUpperBound(model, v1.ImageGeneration, core.Usage{InputTokens: 1000, OutputTokens: 1000, Requests: 1, Images: 2})
	if err != nil || bound != 22 {
		t.Fatalf("flat image bound = %d %v", bound, err)
	}
	model.Pricing = []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1, Amount: 0, Currency: "USD"}, {Task: v1.Chat, Meter: "cache_creation_input_tokens", UnitSize: 1, Amount: math.MaxInt64, Currency: "USD"}}
	if _, _, err := QuoteUpperBound(model, v1.Chat, core.Usage{InputTokens: 2}); err == nil {
		t.Fatal("premium bound overflow accepted")
	}
}
