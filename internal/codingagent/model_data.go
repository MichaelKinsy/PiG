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
		if md.Compat != nil {
			compat := overlaySystemOneCompat(nil, md.Compat)
			model.Compat = &compat
		}
		return model
	}
	return nativeModelFromEntry(modelDefinitionEntry(providerID, prov, md))
}

// modelDefinitionEntry resolves a provider's model definition. A registered definition (one an extension registered) is Pi's spread
// of the definition (provider-composer.ts extensionModelFromDefinition): the entry carries that object as its record, and its input
// stays as the extension wrote it, where modelFromJson defaults a models.json model's to ["text"].
func modelDefinitionEntry(providerID string, prov providerConfig, md modelDefinition) ModelEntry {
	registered := md.registration != nil
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

	var input []string
	if md.Input != nil {
		input = append([]string{}, (*md.Input)...)
	} else if !registered {
		input = []string{"text"}
	}

	ctxWindow := 128000
	if md.ContextWindow != nil {
		ctxWindow = *md.ContextWindow
	}

	maxTokens := 16384
	if md.MaxTokens != nil {
		maxTokens = *md.MaxTokens
	}
	var record string
	if registered && (md.Type == "" || md.Type == ai.ModelTypeChat) {
		record = ai.ExtensionModelRecord(md.registration, ai.API(api), providerID, baseURL)
	}
	var inputCost, outputCost, cacheReadCost, cacheWriteCost float64
	var costTiers []ai.CostTier
	if md.Cost != nil {
		inputCost = md.Cost.Input
		outputCost = md.Cost.Output
		cacheReadCost = md.Cost.CacheRead
		cacheWriteCost = md.Cost.CacheWrite
		costTiers = slices.Clone(md.Cost.Tiers)
	}

	return ModelEntry{
		Type:                          md.Type,
		CatalogRecord:                 record,
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
			e.CostTiers = slices.Clone(*ovr.Cost.Tiers)
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
