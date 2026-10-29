package modelgen

// Ports packages/ai/catalog/sources/models-dev.ts

import (
	"math"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The models.dev v2 catalog. The schema is published as experimental, so everything the catalog reads goes through
// NormalizeModelsDevModel.
const (
	ModelsDevURL = "https://models.dev/experimental/v2.0/api.json"
	// v2 has no provider-agnostic decision metadata endpoint; v1 still serves it.
	ModelsDevDecisionsURL = "https://models.dev/models.json?type=decision"
)

// ModelsDevCost is the prices as models.dev lists them. Missing prices stay nil.
type ModelsDevCost struct {
	Input, Output, CacheRead, CacheWrite *float64
	// Tiers are context-size tiers. Unlisted rates fall back to the base rates.
	Tiers []ModelsDevCostTier
}

// ModelsDevCostTier is one context-size tier.
type ModelsDevCostTier struct {
	Above                                float64
	Input, Output, CacheRead, CacheWrite *float64
}

// ModelsDevAPI is one API a model is served on, in the order models.dev lists them.
type ModelsDevAPI struct {
	Protocol, BaseURL, AISDK string
}

// ModelsDevModel is a models.dev model normalized for the catalog.
type ModelsDevModel struct {
	ID, Name, Type, Status string
	ToolCall               bool
	StructuredOutput       bool
	Reasoning              bool
	// ReasoningOptions is empty when the model does not reason.
	ReasoningOptions []ModelsDevReasoningOption
	ImageInput       bool
	TextInput        bool
	TextOutput       bool
	// ContextWindow and MaxTokens are 0 when models.dev lists none.
	ContextWindow, MaxTokens float64
	Cost                     ModelsDevCost
	APIs                     []ModelsDevAPI
	// Source is the full v2 entry, for fields not normalized above.
	Source *Obj
}

// HasEffortOption is `reasoningOptions?.some((option) => option.type === "effort")`.
func (m *ModelsDevModel) HasEffortOption() bool { return m.hasOption("effort") }

// HasToggleOption is `reasoningOptions?.some((option) => option.type === "toggle")`.
func (m *ModelsDevModel) HasToggleOption() bool { return m.hasOption("toggle") }

func (m *ModelsDevModel) hasOption(kind string) bool {
	for _, option := range m.ReasoningOptions {
		if option.Type == kind {
			return true
		}
	}
	return false
}

// ModelsDevDecisionMetadata is provider-agnostic decision model metadata from models.dev.
type ModelsDevDecisionMetadata struct {
	ID, Type, Name string
	// ContextLimit is limit.context, 0 when absent.
	ContextLimit float64
}

func numberPtr(value any) *float64 {
	if number, ok := value.(float64); ok {
		return &number
	}
	return nil
}

func objectOf(value any) *Obj {
	object, _ := value.(*Obj)
	return object
}

// firstPrice is `typeof write === "object" ? Object.values(write)[0] : write`.
func firstPrice(write any) *float64 {
	if object, ok := write.(*Obj); ok {
		keys := object.Keys()
		if len(keys) == 0 {
			return nil
		}
		return numberPtr(object.vals[keys[0]])
	}
	return numberPtr(write)
}

// cacheWritePrice: models.dev lists one cache write price per TTL; the first is the host's default.
func cacheWritePrice(cache *Obj) *float64 {
	if price := firstPrice(cache.member("write")); price != nil {
		return price
	}
	return firstPrice(objectOf(cache.member("explicit")).member("write"))
}

func contextThreshold(when *Obj) (float64, bool) {
	if when == nil {
		return 0, false
	}
	keys := when.Keys()
	if len(keys) != 1 || keys[0] != "context" {
		return 0, false
	}
	context := objectOf(when.vals["context"])
	size := context.member("gt")
	if size == nil {
		size = context.member("gte")
	}
	if number, ok := size.(float64); ok {
		return number, true
	}
	return 0, false
}

func normalizeCost(cost *Obj) ModelsDevCost {
	text := objectOf(cost.member("text"))
	input := objectOf(text.member("input"))
	output := objectOf(text.member("output"))
	perToken := func(entry *Obj) bool {
		unit := entry.member("unit")
		return unit == nil || unit == "token"
	}
	if input == nil || output == nil || !perToken(input) || !perToken(output) {
		return ModelsDevCost{}
	}

	// Only context-size overrides map onto pi's cost tiers. Region, service-tier, and time-based prices have no pi equivalent yet.
	var tiers []*ModelsDevCostTier
	tier := func(above float64) *ModelsDevCostTier {
		for _, entry := range tiers {
			if entry.Above == above {
				return entry
			}
		}
		entry := &ModelsDevCostTier{Above: above}
		tiers = append(tiers, entry)
		return entry
	}
	overrides := func(entry *Obj) []*Obj {
		var out []*Obj
		for _, item := range listOf(entry.member("overrides")) {
			if object, ok := item.(*Obj); ok {
				out = append(out, object)
			}
		}
		return out
	}
	for _, override := range overrides(input) {
		above, ok := contextThreshold(objectOf(override.member("when")))
		if !ok {
			continue
		}
		entry := tier(above)
		if price, present := override.Get("price"); present {
			entry.Input = numberPtr(price)
		}
		overrideCache := objectOf(override.member("cache"))
		if read, present := overrideCache.Get("read"); present {
			entry.CacheRead = numberPtr(read)
		}
		if write := cacheWritePrice(overrideCache); write != nil {
			entry.CacheWrite = write
		}
	}
	for _, override := range overrides(output) {
		above, ok := contextThreshold(objectOf(override.member("when")))
		if !ok {
			continue
		}
		if price, present := override.Get("price"); present {
			tier(above).Output = numberPtr(price)
		}
	}

	result := ModelsDevCost{
		Input:      numberPtr(input.member("price")),
		Output:     numberPtr(output.member("price")),
		CacheRead:  numberPtr(objectOf(input.member("cache")).member("read")),
		CacheWrite: cacheWritePrice(objectOf(input.member("cache"))),
	}
	for _, entry := range tiers {
		result.Tiers = append(result.Tiers, *entry)
	}
	return result
}

func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

func reasoningOptions(reasoning *Obj) []ModelsDevReasoningOption {
	options := []ModelsDevReasoningOption{}
	if truthy(reasoning.member("toggle")) {
		options = append(options, ModelsDevReasoningOption{Type: "toggle"})
	}
	if effort, present := reasoning.Get("effort"); present {
		option := ModelsDevReasoningOption{Type: "effort", Values: []*string{}}
		for _, value := range listOf(effort) {
			if text, ok := value.(string); ok {
				option.Values = append(option.Values, &text)
			} else {
				option.Values = append(option.Values, nil)
			}
		}
		options = append(options, option)
	}
	if budget, present := reasoning.Get("budget"); present {
		option := ModelsDevReasoningOption{Type: "budget_tokens"}
		if object, ok := budget.(*Obj); ok {
			option.Min = numberPtr(object.member("min"))
			option.Max = numberPtr(object.member("max"))
		}
		options = append(options, option)
	}
	return options
}

// truthy is JavaScript truthiness of a parsed JSON value.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	}
	return true
}

// NormalizeModelsDevModel reads the subset of a models.dev v2 model the catalog uses.
func NormalizeModelsDevModel(model *Obj) *ModelsDevModel {
	inputs := objectOf(model.member("modalities")).StringList("input")
	reasoning := objectOf(objectOf(model.member("capabilities")).member("reasoning"))
	normalized := &ModelsDevModel{
		ID:               stringOf(model.member("id")),
		Name:             stringOf(model.member("name")),
		Type:             stringOf(model.member("type")),
		Status:           stringOf(model.member("status")),
		ToolCall:         objectOf(objectOf(model.member("capabilities")).member("tools")).member("supported") == true,
		StructuredOutput: objectOf(model.member("capabilities")).member("structured_output") == true,
		Reasoning:        reasoning.member("supported") == true,
		TextInput:        contains(inputs, "text"),
		TextOutput:       contains(objectOf(model.member("modalities")).StringList("output"), "text"),
		Cost:             normalizeCost(objectOf(model.member("cost"))),
		Source:           model,
	}
	if normalized.Reasoning {
		normalized.ReasoningOptions = reasoningOptions(reasoning)
	}
	for _, modality := range inputs {
		if modality == "image" || strings.HasPrefix(modality, "image/") {
			normalized.ImageInput = true
		}
	}
	limit := objectOf(model.member("limit"))
	normalized.ContextWindow, _ = limit.member("context").(float64)
	normalized.MaxTokens, _ = limit.member("output").(float64)
	apis := objectOf(model.member("api"))
	if apis != nil {
		for _, protocol := range apis.Keys() {
			entry := objectOf(apis.vals[protocol])
			normalized.APIs = append(normalized.APIs, ModelsDevAPI{
				Protocol: protocol,
				BaseURL:  stringOf(entry.member("base_url")),
				AISDK:    stringOf(objectOf(entry.member("sdk")).member("aisdk")),
			})
		}
	}
	return normalized
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

// RoundCost is Number(value.toFixed(6)).
func RoundCost(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	return jsnumber.Parse(jsstring.ToFixed(value, 6))
}

func orZero(value *float64) float64 {
	if value == nil || math.IsNaN(*value) {
		return 0
	}
	return *value
}

func orElse(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

// ModelsDevToCost converts models.dev prices to pi's cost shape. Missing rates are free.
func ModelsDevToCost(cost ModelsDevCost) *Obj {
	base := [4]float64{orZero(cost.Input), orZero(cost.Output), orZero(cost.CacheRead), orZero(cost.CacheWrite)}
	result := O("input", base[0], "output", base[1], "cacheRead", base[2], "cacheWrite", base[3])
	if len(cost.Tiers) > 0 {
		tiers := make([]any, len(cost.Tiers))
		for i, tier := range cost.Tiers {
			tiers[i] = O(
				"inputTokensAbove", tier.Above,
				"input", orElse(tier.Input, base[0]),
				"output", orElse(tier.Output, base[1]),
				"cacheRead", orElse(tier.CacheRead, base[2]),
				"cacheWrite", orElse(tier.CacheWrite, base[3]),
			)
		}
		result.Set("tiers", tiers)
	}
	return result
}

// ModelsDevChatModels returns the chat models of one models.dev provider. Specialized types (image, speech, realtime, decision,
// ...) only appear in v2 and are left out here.
func ModelsDevChatModels(catalog *Obj, provider string) []*ModelsDevModel {
	models := objectOf(objectOf(catalog.member(provider)).member("models"))
	if models == nil {
		return nil
	}
	var out []*ModelsDevModel
	for _, key := range models.Keys() {
		model := objectOf(models.vals[key])
		// Unlike pi models, models.dev v2 always states the type.
		if model == nil || model.member("type") != "chat" {
			continue
		}
		out = append(out, NormalizeModelsDevModel(model))
	}
	return out
}
