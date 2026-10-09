package ai

import (
	"testing"
)

// Upstream: .upstream/v0.99.2/packages/ai/src/models.ts:1193 calculateCost(model: AnyModel, usage). Pi has no
// test that prices an image or classifier model through calculateCost; these cases reuse the inputs and expected
// values of models-runtime.test.ts "applies request-wide pricing tiers above the configured input threshold"
// (.upstream/v0.99.2/packages/ai/test/models-runtime.test.ts:126-160) for every model type, so one price table
// must give one answer whichever of the three models carries it.
func TestCalculateCostAcceptsAnyModelType(t *testing.T) {
	cost := ModelCost{
		Input: 5, Output: 30, CacheRead: .5, CacheWrite: 6.25,
		Tiers: []CostTier{{InputTokensAbove: 272000, InputCostPer1M: 10, OutputCostPer1M: 45, CacheReadCostPer1M: 1, CacheWriteCostPer1M: 12.5}},
	}
	chat := &Model{Capabilities: ModelCapabilities{
		InputCostPer1M: cost.Input, OutputCostPer1M: cost.Output, CacheReadCostPer1M: cost.CacheRead, CacheWriteCostPer1M: cost.CacheWrite,
		CostTiers: cost.Tiers,
	}}
	models := map[string]AnyModel{
		"chat":       chat,
		"image":      &ImageModel{ID: "image", Provider: "p", Cost: cost},
		"classifier": &ClassifierModel{ID: "classifier", Provider: "p", Cost: cost},
	}
	for name, model := range models {
		t.Run(name, func(t *testing.T) {
			usage := Usage{Input: 200000, Output: 100000, CacheRead: 72000, TotalTokens: 372000}
			short := CalculateCost(model, &usage)
			if short.Input != 1 || short.Output != 3 || short.CacheRead != .036 || short.CacheWrite != 0 {
				t.Fatalf("short=%+v", short)
			}
			if usage.Cost != short {
				t.Fatalf("usage.Cost=%+v, want the returned %+v", usage.Cost, short)
			}
			usage = Usage{Input: 200000, Output: 100000, CacheRead: 72000, CacheWrite: 1, TotalTokens: 372001}
			long := CalculateCost(model, &usage)
			if long.Input != 2 || long.Output != 4.5 || long.CacheRead != .072 || long.CacheWrite != .0000125 {
				t.Fatalf("long=%+v", long)
			}
			// models.ts:1204-1210: 1h cache writes bill at 2x the base input rate ; the 1M-token prompt is above the 272000 threshold, so tier rates apply: 600k*12.5/1M + 2*10*400k/1M.
			usage = Usage{CacheWrite: 1_000_000, CacheWrite1h: new(400_000)}
			got := CalculateCost(model, &usage)
			if want := 7.5 + 8.0; got.CacheWrite < want-1e-9 || got.CacheWrite > want+1e-9 || got.Total != got.CacheWrite {
				t.Fatalf("mixed short/long write = %+v, want cacheWrite %v", got, want)
			}
		})
	}
}

// A nil model of any type has no price and a nil usage prices nothing, as the *Model-only function already did.
func TestCalculateCostNilModelsAndUsage(t *testing.T) {
	for name, model := range map[string]AnyModel{
		"nil interface":        nil,
		"nil chat":             (*Model)(nil),
		"nil image":            (*ImageModel)(nil),
		"nil classifier":       (*ClassifierModel)(nil),
		"image without prices": &ImageModel{},
	} {
		t.Run(name, func(t *testing.T) {
			usage := Usage{Input: 1_000_000, Output: 1_000_000}
			if got := CalculateCost(model, &usage); got != (UsageCost{}) {
				t.Fatalf("CalculateCost = %+v, want zero", got)
			}
		})
	}
	if got := CalculateCost(&ImageModel{Cost: ModelCost{Input: 1}}, nil); got != (UsageCost{}) {
		t.Fatalf("CalculateCost(nil usage) = %+v", got)
	}
}

// system-one-shared.ts:143 prices classifier usage with calculateCost(model, usage), so the model's cost tiers apply.
func TestSystemOneUsageAppliesClassifierCostTiers(t *testing.T) {
	model := ClassifierModel{Cost: ModelCost{Input: 1, Output: 2, Tiers: []CostTier{{InputTokensAbove: 1000, InputCostPer1M: 10, OutputCostPer1M: 20}}}}
	usage := ParseClassifierUsage([]byte(`{"input_tokens":2000,"output_tokens":1000}`), model)
	if usage == nil || usage.Cost.Input != .02 || usage.Cost.Output != .02 || usage.Cost.Total != .04 {
		t.Fatalf("usage=%+v, want tier-priced cost", usage)
	}
}
