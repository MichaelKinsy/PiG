package ai

// Ports packages/ai/src/providers/openrouter-api.ts (pi-mono commit ee409a85, #10672): OpenRouter's model listing shapes and the price conversion that the
// model generator (cmd/gen-models) and PersonalizeOpenRouterCatalog share.

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// OpenRouterRate is one of OpenRouter's per-token prices, a decimal string. A JSON number reads as its text, as String(value) does for
// Number.parseFloat; any other JSON value reads as the empty string, which parseFloat turns into NaN.
type OpenRouterRate string

func (r *OpenRouterRate) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		*r = OpenRouterRate(text)
	case len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9'):
		*r = OpenRouterRate(trimmed)
	default:
		*r = ""
	}
	return nil
}

// OpenRouterPricing is OpenRouter's per-token prices.
type OpenRouterPricing struct {
	Prompt          OpenRouterRate              `json:"prompt"`
	Completion      OpenRouterRate              `json:"completion"`
	InputCacheRead  OpenRouterRate              `json:"input_cache_read"`
	InputCacheWrite OpenRouterRate              `json:"input_cache_write"`
	Overrides       []OpenRouterPricingOverride `json:"overrides"`
}

// OpenRouterPricingOverride is a conditional price. MinPromptTokens selects prompt-length pricing; the utc_* members select time-of-day or weekday pricing and keep
// their raw JSON because only their presence matters. Missing rates keep the base price.
type OpenRouterPricingOverride struct {
	MinPromptTokens *float64        `json:"min_prompt_tokens"`
	UTCStart        json.RawMessage `json:"utc_start"`
	UTCEnd          json.RawMessage `json:"utc_end"`
	UTCDays         json.RawMessage `json:"utc_days"`
	Prompt          OpenRouterRate  `json:"prompt"`
	Completion      OpenRouterRate  `json:"completion"`
	InputCacheRead  OpenRouterRate  `json:"input_cache_read"`
	InputCacheWrite OpenRouterRate  `json:"input_cache_write"`
}

// OpenRouterTopProvider is the limits OpenRouter reports for the provider that serves a model. A limit that is absent, null or 0 is not reported.
type OpenRouterTopProvider struct {
	ContextLength       *float64 `json:"context_length"`
	MaxCompletionTokens *float64 `json:"max_completion_tokens"`
}

// OpenRouterModel is the fields pi reads from a model in OpenRouter's `GET /models` and `GET /models/user`.
type OpenRouterModel struct {
	ID            string                 `json:"id"`
	ContextLength *float64               `json:"context_length"`
	TopProvider   *OpenRouterTopProvider `json:"top_provider"`
	Pricing       *OpenRouterPricing     `json:"pricing"`
}

// orFirstLimit is JavaScript's `a || b || fallback` over reported limits: a limit that is absent, null, 0 or NaN falls through.
func orFirstLimit(fallback float64, limits ...*float64) float64 {
	for _, limit := range limits {
		if limit != nil && *limit != 0 && !math.IsNaN(*limit) {
			return *limit
		}
	}
	return fallback
}

// openRouterPerMillion converts one $/token price to $/1M tokens. 0 means free. A missing, invalid or negative price uses the fallback: OpenRouter
// reports -1 for router models such as `openrouter/auto`, whose price depends on the model they pick.
func openRouterPerMillion(value OpenRouterRate, fallback float64) float64 {
	perToken := jsParseFloat(string(value))
	if !(perToken >= 0) {
		return fallback
	}
	scaled := perToken * 1_000_000
	if math.IsInf(scaled, 0) {
		return scaled
	}
	// Number(x.toFixed(6)); toFixed switches to exponent text at 1e21, which ParseFloat reads back.
	//portlint:allow numbers toFixed's output is a JS number literal (decimal or 1e+21 form), which ParseFloat reads exactly as Number() does
	parsed, _ := strconv.ParseFloat(jsstring.ToFixed(scaled, 6), 64)
	return parsed
}

// OpenRouterCost converts OpenRouter's $/token prices to $/1M tokens. A missing, invalid or negative base price is 0. Prompt-length overrides become
// request-wide tiers; a rate a tier does not list keeps the base price. Time-of-day overrides are skipped because a ModelCost cannot express them.
func OpenRouterCost(pricing *OpenRouterPricing) ModelCost {
	if pricing == nil {
		pricing = &OpenRouterPricing{}
	}
	base := ModelCost{
		Input:      openRouterPerMillion(pricing.Prompt, 0),
		Output:     openRouterPerMillion(pricing.Completion, 0),
		CacheRead:  openRouterPerMillion(pricing.InputCacheRead, 0),
		CacheWrite: openRouterPerMillion(pricing.InputCacheWrite, 0),
	}
	for _, override := range pricing.Overrides {
		if override.MinPromptTokens == nil || override.UTCStart != nil || override.UTCEnd != nil || override.UTCDays != nil {
			continue
		}
		base.Tiers = append(base.Tiers, CostTier{
			InputTokensAbove:    *override.MinPromptTokens,
			InputCostPer1M:      openRouterPerMillion(override.Prompt, base.Input),
			OutputCostPer1M:     openRouterPerMillion(override.Completion, base.Output),
			CacheReadCostPer1M:  openRouterPerMillion(override.InputCacheRead, base.CacheRead),
			CacheWriteCostPer1M: openRouterPerMillion(override.InputCacheWrite, base.CacheWrite),
		})
	}
	return base
}
