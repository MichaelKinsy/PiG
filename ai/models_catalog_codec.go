package ai

import (
	"encoding/json"
	"fmt"
)

// modelsCatalogRecord is the data-only Model<Api> stored by provider publication; bound backend objects never enter the catalog file.
type modelsCatalogRecord struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	API              API               `json:"api"`
	Provider         string            `json:"provider"`
	BaseURL          string            `json:"baseUrl"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap ThinkingLevelMap  `json:"thinkingLevelMap,omitempty"`
	Input            []string          `json:"input"`
	InputLimits      *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost             ModelCost         `json:"cost"`
	PromptCache      ModelPromptCache  `json:"promptCache,omitempty"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	SamplingParams   map[string]any    `json:"samplingParams,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Compat           *ModelCompat      `json:"compat,omitempty"`
}

func encodeModelsCatalog(models []*Model) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(models))
	for _, model := range models {
		caps := model.Capabilities
		record := modelsCatalogRecord{ID: model.ID, Name: model.DisplayName, API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, BaseURL: model.ProviderMeta.BaseURL, Reasoning: model.ProviderMeta.Reasoning, ThinkingLevelMap: model.ThinkingLevelMap, Input: model.Input, InputLimits: model.InputLimits, Cost: ModelCost{Input: caps.InputCostPer1M, Output: caps.OutputCostPer1M, CacheRead: caps.CacheReadCostPer1M, CacheWrite: caps.CacheWriteCostPer1M, Tiers: caps.CostTiers}, PromptCache: model.PromptCache, ContextWindow: caps.ContextWindow, MaxTokens: caps.MaxOutputTokens, SamplingParams: model.SamplingParams, Headers: model.ProviderMeta.Headers, Compat: model.ProviderMeta.Compat}
		data, err := marshalStoreJSON(record)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

func decodeModelsCatalog(raw []json.RawMessage, providerID string) ([]*Model, error) {
	models := []*Model{}
	for _, data := range raw {
		var record modelsCatalogRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode stored model: %w", err)
		}
		if record.Provider != providerID {
			continue
		}
		models = append(models, &Model{ID: record.ID, DisplayName: record.Name, ProviderMeta: ProviderMetadata{ProviderID: record.Provider, API: record.API, BaseURL: record.BaseURL, Headers: record.Headers, Compat: record.Compat, Reasoning: record.Reasoning}, Capabilities: ModelCapabilities{MaxThinking: thinkingMaxLevel(record.Reasoning, record.ThinkingLevelMap), ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxTokens, InputCostPer1M: record.Cost.Input, OutputCostPer1M: record.Cost.Output, CacheReadCostPer1M: record.Cost.CacheRead, CacheWriteCostPer1M: record.Cost.CacheWrite, CostTiers: record.Cost.Tiers}, ThinkingLevelMap: record.ThinkingLevelMap, Input: record.Input, InputLimits: record.InputLimits, PromptCache: record.PromptCache, SamplingParams: record.SamplingParams})
	}
	return models, nil
}
