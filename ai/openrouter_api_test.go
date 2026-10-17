package ai

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// openrouter-api.ts openRouterCost (pi-mono ee409a85): $/token prices become $/1M tokens rounded with Number(x.toFixed(6)); a missing, invalid or negative price
// takes the fallback (0 for a base price, the base rate for a tier rate). OpenRouter reports -1 for router models such as openrouter/auto.
func TestOpenRouterCostConvertsPricesAndFallsBackOnMissingInvalidAndNegativeRates(t *testing.T) {
	decode := func(t *testing.T, text string) *OpenRouterPricing {
		t.Helper()
		var pricing OpenRouterPricing
		if err := json.Unmarshal([]byte(text), &pricing); err != nil {
			t.Fatal(err)
		}
		return &pricing
	}
	for _, c := range []struct {
		name    string
		pricing *OpenRouterPricing
		want    ModelCost
	}{
		{"absent pricing", nil, ModelCost{}},
		{"plain rates", decode(t, `{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.0000003","input_cache_write":"0.00000375"}`), ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
		{"router model reports -1", decode(t, `{"prompt":"-1","completion":"-1"}`), ModelCost{}},
		{"zero is free", decode(t, `{"prompt":"0","completion":"0"}`), ModelCost{}},
		{"invalid and empty rates are 0", decode(t, `{"prompt":"abc","completion":"","input_cache_read":null,"input_cache_write":true}`), ModelCost{}},
		{"parseFloat reads a numeric prefix", decode(t, `{"prompt":"0.000002xyz","completion":" 0.000004"}`), ModelCost{Input: 2, Output: 4}},
		{"a JSON number is read as its text", decode(t, `{"prompt":0.000001,"completion":2e-6}`), ModelCost{Input: 1, Output: 2}},
		{"toFixed(6) rounds to six places", decode(t, `{"prompt":"0.00000012345678"}`), ModelCost{Input: 0.123457}},
		{"a tier keeps the base rate it does not list and a negative tier rate falls back to it", decode(t, `{"prompt":"0.000002","completion":"0.000012","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"-1"}]}`),
			ModelCost{Input: 2, Output: 12, Tiers: []CostTier{{InputTokensAbove: 200000, InputCostPer1M: 4, OutputCostPer1M: 12}}}},
		{"time-of-day overrides are skipped", decode(t, `{"prompt":"0.000002","overrides":[{"min_prompt_tokens":1,"utc_start":1,"prompt":"0.000009"},{"utc_days":["mon"],"prompt":"0.000009"},{"prompt":"0.000009"}]}`), ModelCost{Input: 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := OpenRouterCost(c.pricing); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("OpenRouterCost = %+v, want %+v", got, c.want)
			}
		})
	}
	if got := OpenRouterCost(&OpenRouterPricing{Prompt: "Infinity"}); !math.IsInf(got.Input, 1) {
		t.Fatalf("Infinity rate = %v, want +Inf (Number((Infinity).toFixed(6)))", got.Input)
	}
}
