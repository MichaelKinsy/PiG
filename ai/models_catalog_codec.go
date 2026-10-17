package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
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
	var recordJSON []byte
	if typed, ok := model.(*Model); ok && *shape == nil && typed.catalogRecord != "" {
		// A built-in model nobody changed encodes as its record, and it is unchanged exactly when its typed record encodes as the typed record of the
		// decoded catalog entry: every member is then at its decoded value, which is what the member-by-member merge below tests. Comparing the two
		// encodings decides that without decoding the record into ordered members.
		baseline, err := catalogRecordBaseline(typed.catalogRecord)
		if err != nil {
			return nil, err
		}
		if recordJSON, err = marshalStoreJSON(record); err != nil {
			return nil, err
		}
		if bytes.Equal(recordJSON, baseline) {
			return json.RawMessage(typed.catalogRecord), nil
		}
	}
	built, err := modelCatalogShape(model, *shape)
	if err != nil {
		return nil, err
	}
	shape = &built
	var pairs []catalogPair
	if recordJSON != nil {
		pairs, err = orderedCatalogPairs(recordJSON)
	} else {
		pairs, err = catalogRecordJSON(record)
	}
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
	// unchanged stays true while every member is the record's own: the encoding is then the record itself.
	unchanged := true
	for _, key := range (*shape).order {
		value, present := current[key]
		original := (*shape).original[key]
		baseline, hadBaseline := (*shape).baseline[key]
		switch {
		case present && hadBaseline && bytes.Equal(value, baseline), !present && !hadBaseline:
			// The typed model left this field as decoded (or has no member for it): keep its original bytes.
			merged = append(merged, catalogPair{key, original})
		case present:
			unchanged = false
			merged = append(merged, catalogPair{key, spreadMerged(original, value)})
		default:
			// The typed model cleared a field it decoded: it is no longer persisted.
			unchanged = false
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
		unchanged = false
		merged = append(merged, pair)
	}
	if typed, ok := model.(*Model); ok && unchanged && typed.catalogRecord != "" {
		return json.RawMessage(typed.catalogRecord), nil
	}
	return encodeCatalogPairs(merged)
}

// modelCatalogShape is the shape of the record model came from: the one it was decoded with, or, for a built-in model,
// the shape of its catalog entry, built here so the model itself is never written while it is encoded. A changed built-in
// model is rare (an unchanged one never reaches here), so the shape is decoded for each encoding and not retained.
func modelCatalogShape(model AnyModel, decoded *catalogShape) (*catalogShape, error) {
	typed, ok := model.(*Model)
	if decoded != nil || !ok || typed.catalogRecord == "" {
		return decoded, nil
	}
	entry, _, err := decodeCatalogModel([]byte(typed.catalogRecord))
	if err != nil {
		return nil, err
	}
	return entry.(*Model).catalog, nil
}

// catalogRecordBaseline is the typed record encoding of the model decoded from record, a catalog record: what the model
// encodes as while every member is at its decoded value. A built-in record's baseline is kept, one small encoding per
// record, so the cache is bounded by the catalog.
func catalogRecordBaseline(record string) ([]byte, error) {
	if cached, found := catalogRecordBaselines.Load(record); found {
		return cached.([]byte), nil
	}
	entry, known, err := decodeCatalogTyped([]byte(record))
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, fmt.Errorf("decode stored model: unknown model type in %q", record)
	}
	typedRecord, _, err := anyModelRecord(entry)
	if err != nil {
		return nil, err
	}
	baseline, err := marshalStoreJSON(typedRecord)
	if err != nil {
		return nil, err
	}
	if _, builtin := builtinCatalogRecords()[record]; builtin {
		catalogRecordBaselines.Store(record, baseline)
	}
	return baseline, nil
}

// CatalogRecordOf is the catalog record model was made from, its built-in catalog entry or the models store record it was
// decoded from, as a JSON object in the record's member order, or "" for a model of neither. A model built from it again
// (GeneratedModel.Record) encodes with CatalogModelJSON in that order.
func CatalogRecordOf(model *Model) string {
	switch {
	case model == nil:
		return ""
	case model.catalogRecord != "":
		return model.catalogRecord
	case model.catalog == nil:
		return ""
	}
	pairs := make([]catalogPair, 0, len(model.catalog.order))
	for _, key := range model.catalog.order {
		if value, ok := model.catalog.original[key]; ok {
			pairs = append(pairs, catalogPair{key, value})
		}
	}
	data, err := encodeCatalogPairs(pairs)
	if err != nil {
		return ""
	}
	return string(data)
}

// ExtensionModelRecord is the catalog record of the chat model an extension registers from definition, a model definition as the
// extension wrote it: Pi's `{ ...definition, api, provider, baseUrl, headers: undefined }` (provider-composer.ts
// extensionModelFromDefinition). The definition's members keep the extension's order; api, provider and baseUrl take their
// place when the definition has them and follow in that order when it does not; headers is undefined, so it is no member of
// the object. A model built from the record (GeneratedModel.Record) encodes with CatalogModelJSON as Pi serializes it. A
// definition that is not a JSON object has no record ("").
func ExtensionModelRecord(definition json.RawMessage, api API, providerID, baseURL string) string {
	pairs, err := orderedCatalogPairs(definition)
	if err != nil {
		return ""
	}
	for _, member := range [...]struct{ key, value string }{{"api", string(api)}, {"provider", providerID}, {"baseUrl", baseURL}} {
		encoded, err := marshalStoreJSON(member.value)
		if err != nil {
			return ""
		}
		pairs = append(pairs, catalogPair{member.key, encoded})
	}
	// A JavaScript object keeps a key at its first position and takes its last value.
	merged := make([]catalogPair, 0, len(pairs))
	index := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		if position, seen := index[pair.key]; seen {
			merged[position].value = pair.value
			continue
		}
		index[pair.key] = len(merged)
		merged = append(merged, pair)
	}
	if position, ok := index["headers"]; ok {
		merged = slices.Delete(merged, position, position+1)
	}
	encoded, err := encodeCatalogPairs(merged)
	if err != nil {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		return ""
	}
	return compact.String()
}

// catalogRecordBaselines holds the baseline ([]byte, see catalogRecordBaseline) of each built-in catalog record
// (GeneratedModel.Record) that has been encoded, keyed by the record. A baseline is read-only once computed. Only records
// compiled into the binary are cached, so the cache is bounded by the catalog: a models store record (a Radius gateway's
// catalog, CatalogRecordOf) changes with each refresh and is decoded each time.
var catalogRecordBaselines sync.Map

// builtinCatalogRecords is the set of GeneratedModel.Record values.
var builtinCatalogRecords = sync.OnceValue(func() map[string]struct{} {
	records := make(map[string]struct{}, len(GeneratedModels))
	for i := range GeneratedModels {
		if record := GeneratedModels[i].Record; record != "" {
			records[record] = struct{}{}
		}
	}
	return records
})

// CatalogModelJSON is the JSON object of a model that came from a catalog record, the built-in catalog or a models
// store, as Pi serializes that model object: the record's members in the record's order, with the record's own bytes for
// every member the model still holds at its catalog value. ok is false for a model without a record.
func CatalogModelJSON(model AnyModel) (data json.RawMessage, ok bool, err error) {
	_, shape, err := anyModelRecord(model)
	if err != nil {
		return nil, false, err
	}
	if typed, isChat := model.(*Model); *shape == nil && (!isChat || typed.catalogRecord == "") {
		return nil, false, nil
	}
	data, err = marshalCatalogModel(model)
	return data, err == nil, err
}

// spreadMerged is value, a changed member, in the member order Pi's `{ ...original, ...override }` merges give it: the
// members original has keep their place, each with original's bytes when its value did not change, and the members
// value adds follow in value's order. A value that is not an object on both sides is value.
func spreadMerged(original, value json.RawMessage) json.RawMessage {
	originalPairs, errOriginal := orderedCatalogPairs(original)
	valuePairs, errValue := orderedCatalogPairs(value)
	if errOriginal != nil || errValue != nil {
		return value
	}
	current := make(map[string]json.RawMessage, len(valuePairs))
	for _, pair := range valuePairs {
		current[pair.key] = pair.value
	}
	merged := make([]catalogPair, 0, len(valuePairs))
	written := make(map[string]bool, len(valuePairs))
	for _, pair := range originalPairs {
		next, present := current[pair.key]
		if !present || written[pair.key] {
			continue
		}
		written[pair.key] = true
		if sameJSONValue(pair.value, next) {
			merged = append(merged, pair)
		} else {
			merged = append(merged, catalogPair{pair.key, spreadMerged(pair.value, next)})
		}
	}
	for _, pair := range valuePairs {
		if !written[pair.key] {
			written[pair.key] = true
			merged = append(merged, pair)
		}
	}
	encoded, err := encodeCatalogPairs(merged)
	if err != nil {
		return value
	}
	return encoded
}

// sameJSONValue reports whether two JSON texts hold the same value, whatever their member order or number spelling.
func sameJSONValue(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
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

// decodeCatalogTyped is the typed model of one persisted record, without its shape. known is false for a model type this version does not know; such a record has no typed form.
func decodeCatalogTyped(data []byte) (model AnyModel, known bool, err error) {
	// The typed fields read the record as JSON.parse does (128000.0 is the integer 128000); the stored record keeps its own bytes.
	typed := jsjson.NormalizeIntegers(data)
	var kind struct {
		Type ModelType `json:"type"`
	}
	if err := json.Unmarshal(typed, &kind); err != nil {
		return nil, false, fmt.Errorf("decode stored model: %w", err)
	}
	switch kind.Type {
	case "", ModelTypeChat:
		var record modelsCatalogRecord
		if err := json.Unmarshal(typed, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = chatModelFromRecord(record)
	case ModelTypeImage:
		var record imageModelRecord
		if err := json.Unmarshal(typed, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = &ImageModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Output: record.Output, Cost: record.Cost}
	case ModelTypeClassifier:
		var record classifierModelRecord
		if err := json.Unmarshal(typed, &record); err != nil {
			return nil, false, fmt.Errorf("decode stored model: %w", err)
		}
		model = &ClassifierModel{ID: record.ID, Name: record.Name, API: record.API, Provider: record.Provider, BaseURL: record.BaseURL, Headers: record.Headers, Input: record.Input, InputLimits: record.InputLimits, Cost: record.Cost, ContextWindow: record.ContextWindow}
	default:
		return nil, false, nil
	}
	return model, true, nil
}

// decodeCatalogModel restores one persisted model. known is false for a model type this version does not know; such a record has no typed form.
func decodeCatalogModel(data []byte) (model AnyModel, known bool, err error) {
	model, known, err = decodeCatalogTyped(data)
	if err != nil || !known {
		return nil, false, err
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
