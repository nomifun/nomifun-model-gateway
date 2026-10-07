// SPDX-License-Identifier: Apache-2.0
package billing

import (
	"fmt"
	"math"
	"math/big"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

// Quote calculates integer minor currency, rounding each published price line
// upward. Usage is canonical: input includes cache; output includes reasoning.
// The native usage remains in RawJSON, which this calculator never interprets.
func Quote(model v1.Model, task v1.Task, usage core.Usage) (int64, string, error) {
	if _, err := Tokens(usage); err != nil {
		return 0, "", err
	}
	prices := make(map[string]v1.Price)
	currency := ""
	for _, p := range model.Pricing {
		if p.Task != task {
			continue
		}
		if p.Amount < 0 || p.UnitSize <= 0 || !validCurrency(p.Currency) {
			return 0, "", fmt.Errorf("invalid price definition")
		}
		if currency != "" && currency != p.Currency {
			return 0, "", fmt.Errorf("mixed pricing currencies")
		}
		currency = p.Currency
		if _, ok := prices[p.Meter]; ok {
			return 0, "", fmt.Errorf("duplicate pricing meter")
		}
		switch p.Meter {
		case "requests", "input_tokens", "output_tokens", "cached_input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "reasoning_tokens", "images":
		default:
			return 0, "", fmt.Errorf("unsupported pricing meter")
		}
		prices[p.Meter] = p
	}
	if len(prices) == 0 {
		return 0, "", fmt.Errorf("task has no published pricing")
	}
	_, cachedAlias := prices["cached_input_tokens"]
	_, cacheRead := prices["cache_read_input_tokens"]
	if cachedAlias && cacheRead {
		return 0, "", fmt.Errorf("overlapping cache pricing aliases")
	}
	counts := map[string]int64{"requests": usage.Requests, "input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "cached_input_tokens": usage.CacheReadTokens, "cache_read_input_tokens": usage.CacheReadTokens, "cache_creation_input_tokens": usage.CacheCreationTokens, "reasoning_tokens": usage.ReasoningTokens, "images": usage.Images}
	if cachedAlias || cacheRead {
		counts["input_tokens"] -= usage.CacheReadTokens
	}
	if _, ok := prices["cache_creation_input_tokens"]; ok {
		counts["input_tokens"] -= usage.CacheCreationTokens
	}
	if _, ok := prices["reasoning_tokens"]; ok {
		counts["output_tokens"] -= usage.ReasoningTokens
	}
	// A published request-only or image-only price is an explicit flat tariff.
	flatRequest := len(prices) == 1 && hasPrice(prices, "requests")
	flatImage := len(prices) == 1 && hasPrice(prices, "images") && (task == v1.ImageGeneration || task == v1.ImageEdit)
	if !flatRequest && !flatImage {
		for _, meter := range []string{"input_tokens", "output_tokens", "images"} {
			if counts[meter] > 0 && !hasPrice(prices, meter) {
				return 0, "", fmt.Errorf("nonzero usage has no published %s price", meter)
			}
		}
	}
	total := big.NewInt(0)
	for meter, p := range prices {
		total.Add(total, priceLine(counts[meter], p))
		if !total.IsInt64() {
			return 0, "", fmt.Errorf("price exceeds int64")
		}
	}
	return total.Int64(), currency, nil
}

// QuoteUpperBound reserves against caller-supplied upper bounds on input,
// output, requests and images. Every input/cache tariff may consume the entire
// input bound; every output/reasoning tariff may consume the entire output
// bound. The sum is deliberately conservative. Each eventual nonnegative
// meter is bounded by its category, and ceiling is monotone, so this sum cannot
// be lower than an exact Quote whose category totals stay inside these bounds.
// Cache/reasoning classifications are unknown before provider completion;
// their premium rates must not be omitted merely because estimated subsets
// are zero. Actual settlement continues to remove overlapping subsets.
func QuoteUpperBound(model v1.Model, task v1.Task, bounds core.Usage) (int64, string, error) {
	if _, err := Tokens(bounds); err != nil {
		return 0, "", err
	}
	base := bounds
	base.CacheReadTokens = 0
	base.CacheCreationTokens = 0
	base.ReasoningTokens = 0
	_, currency, err := Quote(model, task, base)
	if err != nil {
		return 0, "", err
	}
	total := big.NewInt(0)
	for _, p := range model.Pricing {
		if p.Task != task {
			continue
		}
		var count int64
		switch p.Meter {
		case "requests":
			count = bounds.Requests
		case "images":
			count = bounds.Images
		case "input_tokens", "cached_input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens":
			count = bounds.InputTokens
		case "output_tokens", "reasoning_tokens":
			count = bounds.OutputTokens
		}
		total.Add(total, priceLine(count, p))
		if !total.IsInt64() {
			return 0, "", fmt.Errorf("reservation price exceeds int64")
		}
	}
	return total.Int64(), currency, nil
}

func priceLine(count int64, price v1.Price) *big.Int {
	product := new(big.Int).Mul(big.NewInt(count), big.NewInt(price.Amount))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, big.NewInt(price.UnitSize), remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func hasPrice(prices map[string]v1.Price, meter string) bool { _, ok := prices[meter]; return ok }

func validCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for i := range len(currency) {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}

// Tokens validates canonical counts and returns the quota meter. Empty total
// means unspecified; a provided total must equal input plus output exactly.
func Tokens(u core.Usage) (int64, error) {
	for _, value := range []int64{u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens, u.ReasoningTokens, u.Images, u.Requests, u.TotalTokens} {
		if value < 0 {
			return 0, fmt.Errorf("negative usage")
		}
	}
	cache, err := add(u.CacheReadTokens, u.CacheCreationTokens)
	if err != nil || cache > u.InputTokens || u.ReasoningTokens > u.OutputTokens {
		return 0, fmt.Errorf("usage subset exceeds total")
	}
	tokens, err := add(u.InputTokens, u.OutputTokens)
	if err != nil {
		return 0, err
	}
	if u.TotalTokens != 0 && u.TotalTokens != tokens {
		return 0, fmt.Errorf("inconsistent usage total")
	}
	return tokens, nil
}

func add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("integer overflow")
	}
	return a + b, nil
}
