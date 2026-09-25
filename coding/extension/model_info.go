package extension

import (
	"encoding/json"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelInfo projects one composed model into the complete extension-facing Pi Model shape.
func ModelInfo(model *ai.Model) map[string]any {
	if model == nil {
		return nil
	}
	providerID := model.ProviderMeta.ProviderID
	if providerID == "" && model.Provider != nil {
		providerID = model.Provider.ID()
	}
	var input []string
	if model.Input != nil {
		input = append([]string{}, model.Input...)
	} else {
		input = []string{"text"}
		if model.Capabilities.SupportsImages {
			input = append(input, "image")
		}
	}
	costTiers := append([]ai.CostTier(nil), model.Capabilities.CostTiers...)
	var headers any
	if model.ProviderMeta.Headers != nil {
		headers = maps.Clone(model.ProviderMeta.Headers)
	}
	projected := map[string]any{
		"id":                  model.ID,
		"modelId":             model.ID,
		"name":                model.DisplayName,
		"displayName":         model.DisplayName,
		"provider":            providerID,
		"api":                 model.ProviderMeta.API,
		"baseUrl":             model.ProviderMeta.BaseURL,
		"reasoning":           model.ProviderMeta.Reasoning,
		"thinkingLevelMap":    cloneThinkingLevelMap(model.ThinkingLevelMap),
		"input":               input,
		"cost":                map[string]any{"input": model.Capabilities.InputCostPer1M, "output": model.Capabilities.OutputCostPer1M, "cacheRead": model.Capabilities.CacheReadCostPer1M, "cacheWrite": model.Capabilities.CacheWriteCostPer1M, "tiers": costTiers},
		"promptCache":         maps.Clone(model.PromptCache),
		"contextWindow":       model.Capabilities.ContextWindow,
		"maxTokens":           model.Capabilities.MaxOutputTokens,
		"samplingParams":      cloneJSONMap(model.SamplingParams),
		"headers":             headers,
		"compat":              cloneCompat(model.ProviderMeta.Compat),
		"maxOutputTokens":     model.Capabilities.MaxOutputTokens,
		"inputCostPer1M":      model.Capabilities.InputCostPer1M,
		"outputCostPer1M":     model.Capabilities.OutputCostPer1M,
		"cacheReadCostPer1M":  model.Capabilities.CacheReadCostPer1M,
		"cacheWriteCostPer1M": model.Capabilities.CacheWriteCostPer1M,
	}
	if model.InputLimits != nil {
		projected["inputLimits"] = model.InputLimits.Clone()
	}
	return projected
}

func cloneCompat(value *ai.ModelCompat) *ai.ModelCompat {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var cloned ai.ModelCompat
	if json.Unmarshal(data, &cloned) != nil {
		return nil
	}
	return &cloned
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = cloneJSONValue(item)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneJSONValue(item)
		}
		return cloned
	default:
		return value
	}
}

func cloneThinkingLevelMap(values ai.ThinkingLevelMap) ai.ThinkingLevelMap {
	if values == nil {
		return nil
	}
	cloned := make(ai.ThinkingLevelMap, len(values))
	for level, value := range values {
		if value == nil {
			cloned[level] = nil
			continue
		}
		copied := *value
		cloned[level] = &copied
	}
	return cloned
}
