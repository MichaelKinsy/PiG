package ai

import (
	"bytes"
	"encoding/json"
	"errors"
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

// catalogShape remembers the persisted record a model was decoded from, so that re-encoding it is lossless: fields the typed model has no member for
// keep their raw bytes, every field keeps its position, and a field the typed model did not change keeps its original bytes (number formatting, an
// empty object or array an omitempty member would drop).
type catalogShape struct {
	order    []string
	original map[string]json.RawMessage
	// baseline is the record JSON of the typed model as decoded, field by field.
	baseline map[string]json.RawMessage
}

type catalogPair struct {
	key   string
	value json.RawMessage
}

// orderedCatalogPairs reads a JSON object's members in document order.
func orderedCatalogPairs(data []byte) ([]catalogPair, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	var pairs []catalogPair
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		pairs = append(pairs, catalogPair{key: keyToken.(string), value: bytes.Clone(value)})
	}
	return pairs, nil
}

func catalogRecordJSON(record any) ([]catalogPair, error) {
	data, err := marshalStoreJSON(record)
	if err != nil {
		return nil, err
	}
	return orderedCatalogPairs(data)
}

// anyModelRecord is the data-only persisted record of a model, and the shape that records its extra fields.
func anyModelRecord(model AnyModel) (record any, shape **catalogShape, err error) {
	switch typed := model.(type) {
	case *Model:
		caps := typed.Capabilities
		return modelsCatalogRecord{Type: typed.Type, ID: typed.ID, Name: typed.DisplayName, API: typed.ProviderMeta.API, Provider: typed.ProviderMeta.ProviderID, BaseURL: typed.ProviderMeta.BaseURL, Reasoning: typed.ProviderMeta.Reasoning, ThinkingLevelMap: typed.ThinkingLevelMap, Input: typed.Input, InputLimits: typed.InputLimits, Cost: ModelCost{Input: caps.InputCostPer1M, Output: caps.OutputCostPer1M, CacheRead: caps.CacheReadCostPer1M, CacheWrite: caps.CacheWriteCostPer1M, Tiers: caps.CostTiers}, PromptCache: typed.PromptCache, ContextWindow: caps.ContextWindow, MaxTokens: caps.MaxOutputTokens, SamplingParams: typed.SamplingParams, SamplingParamsByThinkingLevel: typed.SamplingParamsByThinkingLevel, Headers: typed.ProviderMeta.Headers, Compat: typed.ProviderMeta.Compat}, &typed.catalog, nil
	case *ImageModel:
		return imageModelRecord{Type: ModelTypeImage, ID: typed.ID, Name: typed.Name, API: typed.API, Provider: typed.Provider, BaseURL: typed.BaseURL, Input: typed.Input, InputLimits: typed.InputLimits, Cost: typed.Cost, Headers: typed.Headers, Output: typed.Output}, &typed.catalog, nil
	case *ClassifierModel:
		return classifierModelRecord{Type: ModelTypeClassifier, ID: typed.ID, Name: typed.Name, API: typed.API, Provider: typed.Provider, BaseURL: typed.BaseURL, Input: typed.Input, InputLimits: typed.InputLimits, Cost: typed.Cost, Headers: typed.Headers, ContextWindow: typed.ContextWindow}, &typed.catalog, nil
	}
	return nil, nil, fmt.Errorf("encode model: unsupported model %T", model)
}

// marshalCatalogModel is the persisted JSON of one model. A model decoded from a catalog re-encodes with its unknown fields and its field order.
func marshalCatalogModel(model AnyModel) (json.RawMessage, error) {
	record, shape, err := anyModelRecord(model)
	if err != nil {
		return nil, err
	}
	pairs, err := catalogRecordJSON(record)
	if err != nil {
		return nil, err
	}
	if *shape == nil {
		return encodeCatalogPairs(pairs)
	}
	current := make(map[string]json.RawMessage, len(pairs))
	for _, pair := range pairs {
		current[pair.key] = pair.value
	}
	var merged []catalogPair
	written := make(map[string]bool, len(pairs))
	for _, key := range (*shape).order {
		value, present := current[key]
		original := (*shape).original[key]
		baseline, hadBaseline := (*shape).baseline[key]
		switch {
		case present && hadBaseline && bytes.Equal(value, baseline), !present && !hadBaseline:
			// The typed model left this field as decoded (or has no member for it): keep its original bytes.
			merged = append(merged, catalogPair{key, original})
		case present:
			merged = append(merged, catalogPair{key, value})
		default:
			// The typed model cleared a field it decoded: it is no longer persisted.
		}
		written[key] = true
	}
	for _, pair := range pairs {
		if written[pair.key] {
			continue
		}
		// A field the record never persisted and the typed model still holds at its decoded value (a zero member) stays unpersisted.
		if baseline, hadBaseline := (*shape).baseline[pair.key]; hadBaseline && bytes.Equal(baseline, pair.value) {
			continue
		}
		merged = append(merged, pair)
	}
	return encodeCatalogPairs(merged)
}

func encodeCatalogPairs(pairs []catalogPair) (json.RawMessage, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, pair := range pairs {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, err := marshalStoreJSON(pair.key)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(pair.value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// decodeCatalogModel restores one persisted model. known is false for a model type this version does not know; such a record has no typed form.
func decodeCatalogModel(data []byte) (model AnyModel, known bool, err error) {
	var kind struct {
		Type ModelType `json:"type"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return nil, false, fmt.Errorf("decode stored model: %w", err)
	}
	switch kind.Type {
	case "", ModelTypeChat:
		var record modelsCatalogRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = chatModelFromRecord(record)
	case ModelTypeImage:
		var record imageModelRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = &ImageModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Output: record.Output, Cost: record.Cost}
	case ModelTypeClassifier:
		var record classifierModelRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = &ClassifierModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Cost: record.Cost, ContextWindow: record.ContextWindow}
	default:
		return nil, false, nil
	}
	original, err := orderedCatalogPairs(data)
	if err != nil {
		return nil, false, fmt.Errorf("decode stored model: %w", err)
	}
	record, shape, err := anyModelRecord(model)
	if err != nil {
		return nil, false, err
	}
	baseline, err := catalogRecordJSON(record)
	if err != nil {
		return nil, false, err
	}
	built := &catalogShape{original: map[string]json.RawMessage{}, baseline: map[string]json.RawMessage{}}
	for _, pair := range original {
		if _, seen := built.original[pair.key]; !seen {
			built.order = append(built.order, pair.key)
		}
		built.original[pair.key] = pair.value
	}
	for _, pair := range baseline {
		built.baseline[pair.key] = pair.value
	}
	*shape = built
	return model, true, nil
}

// decodeModelsCatalog keeps the models of providerID.
func decodeModelsCatalog(models []AnyModel, providerID string) ([]AnyModel, error) {
	kept := []AnyModel{}
	for _, model := range models {
		if model != nil && model.ProviderID() == providerID {
			kept = append(kept, model)
		}
	}
	return kept, nil
}

// DecodeModelsCatalog keeps the stored models of providerID. Records of types this version does not know never reach it: ModelsStoreEntry decoding sets them aside.
func DecodeModelsCatalog(models []AnyModel, providerID string) ([]AnyModel, error) {
	return decodeModelsCatalog(models, providerID)
}

func chatModelFromRecord(record modelsCatalogRecord) *Model {
	return &Model{Type: record.Type, ID: record.ID, DisplayName: record.Name, ProviderMeta: ProviderMetadata{ProviderID: record.Provider, API: record.API, BaseURL: record.BaseURL, Headers: record.Headers, Compat: record.Compat, Reasoning: record.Reasoning}, Capabilities: ModelCapabilities{MaxThinking: thinkingMaxLevel(record.Reasoning, record.ThinkingLevelMap), ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxTokens, InputCostPer1M: record.Cost.Input, OutputCostPer1M: record.Cost.Output, CacheReadCostPer1M: record.Cost.CacheRead, CacheWriteCostPer1M: record.Cost.CacheWrite, CostTiers: record.Cost.Tiers}, ThinkingLevelMap: record.ThinkingLevelMap, Input: record.Input, InputLimits: record.InputLimits, PromptCache: record.PromptCache, SamplingParams: record.SamplingParams, SamplingParamsByThinkingLevel: record.SamplingParamsByThinkingLevel}
}

// DecodeStoredModel restores one persisted catalog record as a typed model that keeps the fields it has no member for. known is false, with a nil
// model, for a model type this version does not know.
func DecodeStoredModel(data []byte) (model AnyModel, known bool, err error) {
	return decodeCatalogModel(data)
}

// EncodeStoredModel is the persisted catalog record of a model; a model that was decoded from a record re-encodes with its unknown fields and field order.
func EncodeStoredModel(model AnyModel) (json.RawMessage, error) {
	return marshalCatalogModel(model)
}

// DecodeStoredModels restores persisted catalog records in order; records of a model type this version does not know are dropped.
func DecodeStoredModels(records []json.RawMessage) ([]AnyModel, error) {
	models := make([]AnyModel, 0, len(records))
	for _, record := range records {
		model, known, err := decodeCatalogModel(record)
		if err != nil {
			return nil, err
		}
		if known {
			models = append(models, model)
		}
	}
	return models, nil
}

// EncodeModelsCatalog writes models as their persisted JSON records, the Pi model shapes a provider object receives and returns.
func EncodeModelsCatalog(models []AnyModel) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(models))
	for _, model := range models {
		data, err := marshalCatalogModel(model)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}
