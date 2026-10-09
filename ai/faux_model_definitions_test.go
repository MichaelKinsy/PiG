package ai

import (
	"slices"
	"testing"
)

// packages/ai/src/providers/faux.ts:463-488 maps each FauxModelDefinition to a model: name defaults to the id, reasoning to
// false, input to ["text","image"], cost to zeros, contextWindow to 128000, maxTokens to 16384, and inputLimits passes
// through; with no definitions one default model faux-1 / "Faux Model" is created. The Pi faux test only checks reasoning
// (faux-provider.test.ts:70-86).
func TestFauxModelDefinitionsApplyPiDefaultsAndOverrides(t *testing.T) {
	limits := &ModelInputLimits{MaxRequestBytes: 4096}
	provider := NewFauxProvider(FauxConfig{ProviderID: "faux-defs", API: "faux-defs-api", Models: []FauxModelDefinition{
		{ID: "bare"},
		{ID: "full", Name: "Full Model", Reasoning: true, Input: []string{"text"}, InputLimits: limits, Cost: &ModelCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}, ContextWindow: 2000, MaxTokens: 300},
	}})

	bare := provider.GetModel("bare")
	if bare == nil || bare.DisplayName != "bare" || bare.ProviderMeta.Reasoning || !slices.Equal(bare.Input, []string{"text", "image"}) ||
		bare.Capabilities.ContextWindow != 128000 || bare.Capabilities.MaxOutputTokens != 16384 || bare.InputLimits != nil ||
		bare.Capabilities.InputCostPer1M != 0 || bare.Capabilities.OutputCostPer1M != 0 || bare.Capabilities.CacheReadCostPer1M != 0 || bare.Capabilities.CacheWriteCostPer1M != 0 {
		t.Fatalf("bare model = %+v", bare)
	}
	full := provider.GetModel("full")
	if full == nil || full.DisplayName != "Full Model" || !full.ProviderMeta.Reasoning || !slices.Equal(full.Input, []string{"text"}) ||
		full.Capabilities.ContextWindow != 2000 || full.Capabilities.MaxOutputTokens != 300 || full.InputLimits != limits {
		t.Fatalf("full model = %+v", full)
	}
	if c := full.Capabilities; c.InputCostPer1M != 1 || c.OutputCostPer1M != 2 || c.CacheReadCostPer1M != 3 || c.CacheWriteCostPer1M != 4 {
		t.Fatalf("full cost = %+v", c)
	}
	for _, model := range []*Model{bare, full} {
		if model.ProviderMeta.ProviderID != "faux-defs" || model.ProviderMeta.API != "faux-defs-api" {
			t.Errorf("%s identity = %+v", model.ID, model.ProviderMeta)
		}
	}
	if got := provider.GetModel("missing"); got != nil {
		t.Errorf("GetModel(missing) = %+v, want nil", got)
	}
	if got := provider.GetModel(); got != bare {
		t.Errorf("GetModel() = %+v, want the first model", got)
	}
}

func TestFauxProviderWithoutModelsCreatesTheDefaultModel(t *testing.T) {
	model := NewFauxProvider(FauxConfig{}).GetModel()
	if model == nil || model.ID != "faux-1" || model.DisplayName != "Faux Model" || model.ProviderMeta.ProviderID != "faux" ||
		model.Capabilities.ContextWindow != 128000 || model.Capabilities.MaxOutputTokens != 16384 || !slices.Equal(model.Input, []string{"text", "image"}) {
		t.Fatalf("default model = %+v", model)
	}
}
