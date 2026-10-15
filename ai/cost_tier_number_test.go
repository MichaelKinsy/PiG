package ai

import (
	"encoding/json"
	"testing"
)

// model-schema.ts:34 types ModelCostTier.inputTokensAbove as Type.Number, so a models.json tier with a fractional threshold passes the schema and must
// decode; the usage cost then compares the request's input tokens with it as numbers (calculateUsageCost).
func TestCostTierInputTokensAboveIsANumber(t *testing.T) {
	var tier CostTier
	if err := json.Unmarshal([]byte(`{"inputTokensAbove":1000.5,"input":2,"output":4,"cacheRead":1,"cacheWrite":3}`), &tier); err != nil {
		t.Fatalf("a fractional inputTokensAbove must decode: %v", err)
	}
	if tier.InputTokensAbove != 1000.5 {
		t.Fatalf("InputTokensAbove = %v, want 1000.5", tier.InputTokensAbove)
	}
	rates := ModelCost{Input: 1, Output: 1, Tiers: []CostTier{tier}}
	if got := calculateUsageCost(rates, &Usage{Input: 1000}).Input; got != 0.001 {
		t.Errorf("1000 input tokens do not exceed 1000.5, cost %v, want the base rate 0.001", got)
	}
	if got := calculateUsageCost(rates, &Usage{Input: 1001}).Input; got != 0.002002 {
		t.Errorf("1001 input tokens exceed 1000.5, cost %v, want the tier rate 0.002002", got)
	}
	// Two thresholds with the same integer part: 1001 tokens exceed both, and models.ts:1205 keeps the higher threshold, 1000.7.
	two := ModelCost{Input: 1, Output: 1, Tiers: []CostTier{{InputTokensAbove: 1000.2, InputCostPer1M: 2}, {InputTokensAbove: 1000.7, InputCostPer1M: 3}}}
	if got := calculateUsageCost(two, &Usage{Input: 1001}).Input; got != 0.003003 {
		t.Errorf("1001 input tokens over tiers 1000.2 and 1000.7 cost %v, want the 1000.7 tier rate 0.003003", got)
	}
}
