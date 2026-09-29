package codingagent

// Ports packages/coding-agent/src/core/provider-composer.ts.

import (
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

func modelDefinitionEntry(providerID string, prov providerConfig, md modelDefinition) ModelEntry {
	// Model-level overrides provider-level.
	baseURL := md.BaseURL
	if baseURL == "" {
		baseURL = prov.BaseURL
	}
	api := md.API
	if api == "" {
		api = prov.API
	}
	// Merge compat: provider → model (model wins per-field).
	compat := mergeCompat(prov.Compat, md.Compat)

	name := md.Name
	if name == "" {
		name = md.ID
	}

	reasoning := false
	if md.Reasoning != nil {
		reasoning = *md.Reasoning
	}

	input := []string{"text"}
	if md.Input != nil {
		input = append([]string{}, (*md.Input)...)
	}

	ctxWindow := 128000
	if md.ContextWindow != nil {
		ctxWindow = *md.ContextWindow
	}

	maxTokens := 16384
	if md.MaxTokens != nil {
		maxTokens = *md.MaxTokens
	}
	var inputCost, outputCost, cacheReadCost, cacheWriteCost float64
	var costTiers []ai.CostTier
	if md.Cost != nil {
		inputCost = md.Cost.Input
		outputCost = md.Cost.Output
		cacheReadCost = md.Cost.CacheRead
		cacheWriteCost = md.Cost.CacheWrite
		costTiers = append([]ai.CostTier(nil), md.Cost.Tiers...)
	}

	return ModelEntry{
		ProviderID:       providerID,
		ModelID:          md.ID,
		BaseURL:          baseURL,
		DisplayName:      name,
		API:              api,
		Compat:           compat,
		Reasoning:        reasoning,
		ThinkingLevelMap: cloneThinkingLevelMap(md.ThinkingLevelMap),
		SamplingParams:   maps.Clone(md.SamplingParams),
		Input:            input,
		InputLimits:      md.InputLimits.Clone(),
		ContextWindow:    ctxWindow,
		MaxTokens:        maxTokens,
		InputCost:        inputCost,
		OutputCost:       outputCost,
		CacheReadCost:    cacheReadCost,
		CacheWriteCost:   cacheWriteCost,
		CostTiers:        costTiers,
		PromptCache:      maps.Clone(md.PromptCache),
		Insecure:         prov.Insecure,
	}
}

func applyModelOverride(e *ModelEntry, ovr modelOverrideJSON) {
	if ovr.Name != "" {
		e.DisplayName = ovr.Name
	}
	if ovr.Reasoning != nil {
		e.Reasoning = *ovr.Reasoning
	}
	if ovr.ThinkingLevelMap != nil {
		e.ThinkingLevelMap = mergeThinkingLevelMaps(e.ThinkingLevelMap, ovr.ThinkingLevelMap)
	}
	if ovr.SamplingParams != nil {
		if e.SamplingParams == nil {
			e.SamplingParams = make(map[string]any, len(ovr.SamplingParams))
		}
		maps.Copy(e.SamplingParams, ovr.SamplingParams)
	}
	e.InputLimits = mergeModelInputLimits(e.InputLimits, ovr.InputLimits)
	if ovr.Input != nil {
		e.Input = slices.Clone(*ovr.Input)
	}
	if ovr.ContextWindow != nil {
		e.ContextWindow = *ovr.ContextWindow
	}
	if ovr.MaxTokens != nil {
		e.MaxTokens = *ovr.MaxTokens
	}
	if ovr.Cost != nil {
		if ovr.Cost.Input != nil {
			e.InputCost = *ovr.Cost.Input
		}
		if ovr.Cost.Output != nil {
			e.OutputCost = *ovr.Cost.Output
		}
		if ovr.Cost.CacheRead != nil {
			e.CacheReadCost = *ovr.Cost.CacheRead
		}
		if ovr.Cost.CacheWrite != nil {
			e.CacheWriteCost = *ovr.Cost.CacheWrite
		}
		if ovr.Cost.Tiers != nil {
			e.CostTiers = append([]ai.CostTier(nil), (*ovr.Cost.Tiers)...)
		}
	}
	if ovr.PromptCache != nil {
		if e.PromptCache == nil {
			e.PromptCache = make(ai.ModelPromptCache, len(ovr.PromptCache))
		}
		maps.Copy(e.PromptCache, ovr.PromptCache)
	}
	e.Compat = mergeCompat((*providerCompat)(e.Compat), ovr.Compat)
}
