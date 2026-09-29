package extension

import (
	"encoding/json"
	"maps"
	"reflect"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/scalarjson"
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
	cost := map[string]any{"input": model.Capabilities.InputCostPer1M, "output": model.Capabilities.OutputCostPer1M, "cacheRead": model.Capabilities.CacheReadCostPer1M, "cacheWrite": model.Capabilities.CacheWriteCostPer1M}
	// Upstream's cost.tiers is absent unless the model defines tiers.
	if len(model.Capabilities.CostTiers) > 0 {
		cost["tiers"] = append([]ai.CostTier(nil), model.Capabilities.CostTiers...)
	}
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
		"cost":                cost,
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
	scalar := *value
	if scalarjson.CloneFields(reflect.ValueOf(&scalar).Elem()) {
		return &scalar
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
	return cloneJSONValue(value, make(map[any]any)).(map[string]any)
}

// Active ancestors preserve cycles for the JSON encoder to reject. Completed subgraphs leave the table so repeated acyclic input keeps the existing independent-copy semantics.
func cloneJSONValue(value any, ancestors map[any]any) any {
	switch typed := value.(type) {
	case map[string]any:
		if typed == nil {
			return map[string]any(nil)
		}
		identity := reflect.ValueOf(typed)
		if previous, ok := ancestors[identity]; ok {
			return previous
		}
		cloned := make(map[string]any, len(typed))
		ancestors[identity] = cloned
		defer delete(ancestors, identity)
		for key, item := range typed {
			cloned[key] = cloneJSONValue(item, ancestors)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		if len(typed) == 0 {
			return cloned
		}
		identity := struct {
			first  *any
			length int
		}{&typed[0], len(typed)}
		if previous, ok := ancestors[identity]; ok {
			return previous
		}
		ancestors[identity] = cloned
		defer delete(ancestors, identity)
		for i, item := range typed {
			cloned[i] = cloneJSONValue(item, ancestors)
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
