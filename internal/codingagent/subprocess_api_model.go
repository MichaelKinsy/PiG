package codingagent

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// subprocessAPIModel decodes Pi's already-resolved Model at the API leaf.
// Resolving its id through the parent catalog would discard child-only models,
// per-instance base URLs and metadata (packages/ai/src/compat/stream.ts).
func subprocessAPIModel(info map[string]any) (*ai.Model, error) {
	var definition extension.ProviderModelConfig
	if err := subprocessDecodeJSON(info, &definition, "API model"); err != nil {
		return nil, err
	}
	provider, err := subprocessOptionalString(info, "provider", "API model")
	if err != nil {
		return nil, err
	}
	if provider == "" || definition.ID == "" || definition.API == "" {
		return nil, fmt.Errorf("API model requires provider, id and api")
	}
	var cost ai.ModelCost
	if err := subprocessDecodeJSON(info["cost"], &cost, "API model.cost"); err != nil {
		return nil, err
	}
	var compat *ai.ModelCompat
	if definition.Compat != nil {
		if err := subprocessDecodeJSON(definition.Compat, &compat, "API model.compat"); err != nil {
			return nil, err
		}
	}
	var thinking ai.ThinkingLevel
	if definition.Reasoning {
		thinking = ai.ThinkingHigh
	}
	return &ai.Model{
		ID: definition.ID, DisplayName: definition.Name,
		Input: definition.Input, InputLimits: definition.InputLimits,
		ThinkingLevelMap: definition.ThinkingLevelMap, SamplingParams: definition.SamplingParams, PromptCache: definition.PromptCache,
		ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: definition.API, BaseURL: definition.BaseURL, Headers: definition.Headers, Compat: compat, Reasoning: definition.Reasoning},
		Capabilities: ai.ModelCapabilities{
			MaxThinking: thinking, SupportsImages: slices.Contains(definition.Input, "image"), SupportsToolUse: true,
			ContextWindow: definition.ContextWindow, MaxOutputTokens: definition.MaxTokens,
			InputCostPer1M: definition.Cost.Input, OutputCostPer1M: definition.Cost.Output,
			CacheReadCostPer1M: definition.Cost.CacheRead, CacheWriteCostPer1M: definition.Cost.CacheWrite, CostTiers: cost.Tiers,
		},
	}, nil
}
