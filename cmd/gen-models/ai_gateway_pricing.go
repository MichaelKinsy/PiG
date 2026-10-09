package main

// Ports packages/ai/scripts/ai-gateway-pricing.ts.

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// aiGatewayPriceTier is a prompt-length bracket. min is inclusive and max exclusive, both in prompt tokens.
type aiGatewayPriceTier struct {
	Cost any      `json:"cost"`
	Min  *float64 `json:"min"`
	Max  *float64 `json:"max"`
}

// aiGatewayPricing is Vercel AI Gateway pricing in $/token; each rate is a string or a number.
type aiGatewayPricing struct {
	Input                any                  `json:"input"`
	Output               any                  `json:"output"`
	InputCacheRead       any                  `json:"input_cache_read"`
	InputCacheWrite      any                  `json:"input_cache_write"`
	InputTiers           []aiGatewayPriceTier `json:"input_tiers"`
	OutputTiers          []aiGatewayPriceTier `json:"output_tiers"`
	InputCacheReadTiers  []aiGatewayPriceTier `json:"input_cache_read_tiers"`
	InputCacheWriteTiers []aiGatewayPriceTier `json:"input_cache_write_tiers"`
}

// aiGatewayPerMillion is ai-gateway-pricing.ts perMillion: a number, or parseFloat of a string, in $/million tokens; an absent or non-finite rate is 0.
func aiGatewayPerMillion(value any) float64 {
	var parsed float64
	switch v := value.(type) {
	case float64:
		parsed = v
	case string:
		prefix := openRouterFloatPrefix.FindString(strings.TrimLeft(v, jsWhitespace))
		if prefix == "" {
			return 0
		}
		parsed, _ = strconv.ParseFloat(prefix, 64)
	default:
		return 0
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	return roundCost(parsed * 1_000_000)
}

// getAiGatewayCost converts AI Gateway pricing to $/million tokens. Each rate lists its own prompt-length brackets; every bracket start above zero
// becomes a request-wide tier with the rates in effect there.
func getAiGatewayCost(pricing *aiGatewayPricing) jsonCost {
	if pricing == nil {
		pricing = &aiGatewayPricing{}
	}
	base := jsonCost{Input: aiGatewayPerMillion(pricing.Input), Output: aiGatewayPerMillion(pricing.Output), CacheRead: aiGatewayPerMillion(pricing.InputCacheRead), CacheWrite: aiGatewayPerMillion(pricing.InputCacheWrite)}
	fields := []struct {
		brackets []aiGatewayPriceTier
		rate     func(*jsonCostTier) *float64
	}{
		{pricing.InputTiers, func(t *jsonCostTier) *float64 { return &t.Input }},
		{pricing.OutputTiers, func(t *jsonCostTier) *float64 { return &t.Output }},
		{pricing.InputCacheReadTiers, func(t *jsonCostTier) *float64 { return &t.CacheRead }},
		{pricing.InputCacheWriteTiers, func(t *jsonCostTier) *float64 { return &t.CacheWrite }},
	}
	var starts []float64
	for _, field := range fields {
		for _, bracket := range field.brackets {
			if bracket.Min != nil && *bracket.Min > 0 && !slices.Contains(starts, *bracket.Min) {
				starts = append(starts, *bracket.Min)
			}
		}
	}
	slices.Sort(starts)
	for _, start := range starts {
		tier := jsonCostTier{InputTokensAbove: int(start) - 1, Input: base.Input, Output: base.Output, CacheRead: base.CacheRead, CacheWrite: base.CacheWrite}
		for _, field := range fields {
			bracket := slices.IndexFunc(field.brackets, func(candidate aiGatewayPriceTier) bool {
				return (candidate.Min == nil || *candidate.Min <= start) && (candidate.Max == nil || start < *candidate.Max)
			})
			if bracket >= 0 && field.brackets[bracket].Cost != nil {
				*field.rate(&tier) = aiGatewayPerMillion(field.brackets[bracket].Cost)
			}
		}
		base.Tiers = append(base.Tiers, tier)
	}
	return base
}
