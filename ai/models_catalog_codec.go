package ai

import (
	"encoding/json"
	"fmt"
)

// modelsCatalogRecord is the data-only Model<Api> stored by provider publication; bound backend objects never enter the catalog file.
type modelsCatalogRecord struct {
	Type             ModelType         `json:"type,omitempty"`
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
	// SamplingParamsByThinkingLevel is Model.samplingParamsByThinkingLevel.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string             `json:"headers,omitempty"`
	Compat                        *ModelCompat                  `json:"compat,omitempty"`
}

// imageModelRecord is the data-only ImageModel stored by provider publication.
type imageModelRecord struct {
	Type        ModelType         `json:"type"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	API         ImageAPI          `json:"api"`
	Provider    string            `json:"provider"`
	BaseURL     string            `json:"baseUrl"`
	Input       []string          `json:"input"`
	InputLimits *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost        ModelCost         `json:"cost"`
	Headers     map[string]string `json:"headers,omitempty"`
	Output      []string          `json:"output"`
}

// classifierModelRecord is the data-only ClassifierModel stored by provider publication.
type classifierModelRecord struct {
	Type          ModelType         `json:"type"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	API           ClassifierAPI     `json:"api"`
	Provider      string            `json:"provider"`
	BaseURL       string            `json:"baseUrl"`
	Input         []string          `json:"input"`
	InputLimits   *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost          ModelCost         `json:"cost"`
	Headers       map[string]string `json:"headers,omitempty"`
	ContextWindow int               `json:"contextWindow"`
}

func encodeModelsCatalog(models []AnyModel) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(models))
	for _, model := range models {
		var record any
		switch typed := model.(type) {
		case *Model:
			caps := typed.Capabilities
			record = modelsCatalogRecord{Type: typed.Type, ID: typed.ID, Name: typed.DisplayName, API: typed.ProviderMeta.API, Provider: typed.ProviderMeta.ProviderID, BaseURL: typed.ProviderMeta.BaseURL, Reasoning: typed.ProviderMeta.Reasoning, ThinkingLevelMap: typed.ThinkingLevelMap, Input: typed.Input, InputLimits: typed.InputLimits, Cost: ModelCost{Input: caps.InputCostPer1M, Output: caps.OutputCostPer1M, CacheRead: caps.CacheReadCostPer1M, CacheWrite: caps.CacheWriteCostPer1M, Tiers: caps.CostTiers}, PromptCache: typed.PromptCache, ContextWindow: caps.ContextWindow, MaxTokens: caps.MaxOutputTokens, SamplingParams: typed.SamplingParams, SamplingParamsByThinkingLevel: typed.SamplingParamsByThinkingLevel, Headers: typed.ProviderMeta.Headers, Compat: typed.ProviderMeta.Compat}
		case *ImageModel:
			record = imageModelRecord{Type: ModelTypeImage, ID: typed.ID, Name: typed.Name, API: typed.API, Provider: typed.Provider, BaseURL: typed.BaseURL, Input: typed.Input, InputLimits: typed.InputLimits, Cost: typed.Cost, Headers: typed.Headers, Output: typed.Output}
		case *ClassifierModel:
			record = classifierModelRecord{Type: ModelTypeClassifier, ID: typed.ID, Name: typed.Name, API: typed.API, Provider: typed.Provider, BaseURL: typed.BaseURL, Input: typed.Input, InputLimits: typed.InputLimits, Cost: typed.Cost, Headers: typed.Headers, ContextWindow: typed.ContextWindow}
		default:
			return nil, fmt.Errorf("encode model: unsupported model %T", model)
		}
		data, err := marshalStoreJSON(record)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

// DecodeModelsCatalog restores the stored models of providerID from their persisted JSON records. Records of types this version does not
// know, and records of another provider, are dropped.
func DecodeModelsCatalog(raw []json.RawMessage, providerID string) ([]AnyModel, error) {
	return decodeModelsCatalog(raw, providerID)
}

// decodeModelsCatalog restores the stored models of providerID. Records of types this version does not know are dropped.
func decodeModelsCatalog(raw []json.RawMessage, providerID string) ([]AnyModel, error) {
	models := []AnyModel{}
	for _, data := range raw {
		var kind struct {
			Type ModelType `json:"type"`
		}
		if err := json.Unmarshal(data, &kind); err != nil {
			return nil, fmt.Errorf("decode stored model: %w", err)
		}
		switch kind.Type {
		case "", ModelTypeChat:
			var record modelsCatalogRecord
			if err := json.Unmarshal(data, &record); err != nil {
				return nil, fmt.Errorf("decode stored model: %w", err)
			}
			if record.Provider == providerID {
				models = append(models, chatModelFromRecord(record))
			}
		case ModelTypeImage:
			var record imageModelRecord
			if err := json.Unmarshal(data, &record); err != nil {
				return nil, fmt.Errorf("decode stored model: %w", err)
			}
			if record.Provider == providerID {
				models = append(models, &ImageModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Output: record.Output, Cost: record.Cost})
			}
		case ModelTypeClassifier:
			var record classifierModelRecord
			if err := json.Unmarshal(data, &record); err != nil {
				return nil, fmt.Errorf("decode stored model: %w", err)
			}
			if record.Provider == providerID {
				models = append(models, &ClassifierModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Cost: record.Cost, ContextWindow: record.ContextWindow})
			}
		}
	}
	return models, nil
}

func chatModelFromRecord(record modelsCatalogRecord) *Model {
	return &Model{Type: record.Type, ID: record.ID, DisplayName: record.Name, ProviderMeta: ProviderMetadata{ProviderID: record.Provider, API: record.API, BaseURL: record.BaseURL, Headers: record.Headers, Compat: record.Compat, Reasoning: record.Reasoning}, Capabilities: ModelCapabilities{MaxThinking: thinkingMaxLevel(record.Reasoning, record.ThinkingLevelMap), ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxTokens, InputCostPer1M: record.Cost.Input, OutputCostPer1M: record.Cost.Output, CacheReadCostPer1M: record.Cost.CacheRead, CacheWriteCostPer1M: record.Cost.CacheWrite, CostTiers: record.Cost.Tiers}, ThinkingLevelMap: record.ThinkingLevelMap, Input: record.Input, InputLimits: record.InputLimits, PromptCache: record.PromptCache, SamplingParams: record.SamplingParams, SamplingParamsByThinkingLevel: record.SamplingParamsByThinkingLevel}
}

// withKnownModelTypes drops stored models whose type this version does not know.
func withKnownModelTypes(entry *ModelsStoreEntry) *ModelsStoreEntry {
	kept := make([]json.RawMessage, 0, len(entry.Models))
	for _, data := range entry.Models {
		var kind struct {
			Type ModelType `json:"type"`
		}
		if json.Unmarshal(data, &kind) != nil || kind.Type == "" || knownModelType(kind.Type) {
			kept = append(kept, data)
		}
	}
	entry.Models = kept
	return entry
}
