package codingagent

// Ports packages/coding-agent/src/core/provider-composer.ts.

import (
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// anyModelFromDefinition materializes a provider model definition of any type: an image or classifier definition keeps its own api, headers and output, and a chat definition is the composed chat model (types.ts:1953-1976 ProviderModelConfig).
func anyModelFromDefinition(providerID string, prov providerConfig, md modelDefinition) ai.AnyModel {
	name := firstModelValue(md.Name, md.ID)
	var input []string
	if md.Input != nil {
		input = slices.Clone(*md.Input)
	}
	var cost ai.ModelCost
	if md.Cost != nil {
		cost = ai.ModelCost{Input: md.Cost.Input, Output: md.Cost.Output, CacheRead: md.Cost.CacheRead, CacheWrite: md.Cost.CacheWrite, Tiers: slices.Clone(md.Cost.Tiers)}
	}
	var headers map[string]string
	if md.Headers != nil {
		headers = make(map[string]string, len(md.Headers))
		for key, value := range md.Headers {
			if value != nil {
				headers[key] = *value
			}
		}
	}
	switch md.Type {
	case ai.ModelTypeImage:
		return &ai.ImageModel{ID: md.ID, Name: name, API: ai.ImageAPI(md.API), Provider: providerID, BaseURL: md.BaseURL, Headers: headers, Input: input, InputLimits: md.InputLimits.Clone(), Output: slices.Clone(md.Output), Cost: cost}
	case ai.ModelTypeClassifier:
		model := &ai.ClassifierModel{ID: md.ID, Name: name, API: ai.ClassifierAPI(md.API), Provider: providerID, BaseURL: md.BaseURL, Headers: headers, Input: input, InputLimits: md.InputLimits.Clone(), Cost: cost}
		if md.ContextWindow != nil {
			model.ContextWindow = *md.ContextWindow
		}
		return model
	}
	return nativeModelFromEntry(modelDefinitionEntry(providerID, prov, md))
}

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
		ProviderID:                    providerID,
		ModelID:                       md.ID,
		BaseURL:                       baseURL,
		DisplayName:                   name,
		API:                           api,
		Compat:                        compat,
		Reasoning:                     reasoning,
		ThinkingLevelMap:              cloneThinkingLevelMap(md.ThinkingLevelMap),
		SamplingParams:                maps.Clone(md.SamplingParams),
		SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(md.SamplingParamsByThinkingLevel),
		Input:                         input,
		InputLimits:                   md.InputLimits.Clone(),
		ContextWindow:                 ctxWindow,
		MaxTokens:                     maxTokens,
		InputCost:                     inputCost,
		OutputCost:                    outputCost,
		CacheReadCost:                 cacheReadCost,
		CacheWriteCost:                cacheWriteCost,
		CostTiers:                     costTiers,
		PromptCache:                   maps.Clone(md.PromptCache),
		Insecure:                      prov.Insecure,
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
	e.SamplingParamsByThinkingLevel = mergeSamplingParamsByThinkingLevel(e.SamplingParamsByThinkingLevel, ovr.SamplingParamsByThinkingLevel)
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
