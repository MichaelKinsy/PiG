package ai

// Ports packages/ai/src/api/openai-completions.ts (the compat objects of buildParams) and packages/coding-agent/src/core/provider-composer.ts mergeCompat.

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// compatObjectMembers are the compat members whose value is an object that a catalog entry, models.json or an extension supplies and that
// Pi hands on as it is: openRouterRouting is the request's provider member (openai-completions.ts:979-980), and buildChatTemplateValues
// enumerates chatTemplateKwargs and chatTemplateArgs (:1033). JSON.stringify writes an object's members in the order its source listed
// them, and a Go map has no order, so OpenAICompat keeps that order beside the maps (objectOrder), under the member's name as the path.
var compatObjectMembers = [...]string{"openRouterRouting", "chatTemplateKwargs", "chatTemplateArgs"}

// object is the value of one of compatObjectMembers.
func (compat *OpenAICompat) object(member string) map[string]any {
	switch member {
	case "openRouterRouting":
		return compat.OpenRouterRouting
	case "chatTemplateKwargs":
		return compat.ChatTemplateKwargs
	}
	return compat.ChatTemplateArgs
}

// readCompatObjectOrder is the member order of the compat objects among members, the members of a compat object's JSON text: nil when
// every object lists its members in the sorted order encoding/json writes.
func readCompatObjectOrder(members map[string]json.RawMessage) (schemaObjectOrder, error) {
	var order schemaObjectOrder
	for _, member := range compatObjectMembers {
		raw, ok := members[member]
		if !ok {
			continue
		}
		read, err := readSchemaObjectOrder(raw)
		if err != nil {
			return nil, err
		}
		for path, keys := range read {
			if order == nil {
				order = make(schemaObjectOrder, len(read))
			}
			order[schemaPath("", member)+path] = keys
		}
	}
	return order, nil
}

// records reports whether order holds the member order of the object at path or of an object inside it.
func (order schemaObjectOrder) records(path string) bool {
	for recorded := range order {
		if strings.HasPrefix(recorded, path) && (len(recorded) == len(path) || recorded[len(path)] == '/') {
			return true
		}
	}
	return false
}

// compatObject is one of the compat objects, or the chat-template values resolved from one, as a request member: its members are written in
// the recorded order, the order Pi's object has.
type compatObject struct {
	value map[string]any
	order schemaObjectOrder
	path  string
}

func (object compatObject) MarshalJSON() ([]byte, error) {
	return marshalSchemaWithOrder(object.value, object.order, object.path)
}

// requestObject is value, the compat object member or the values resolved from it, as the request carries it: the map itself when no order
// is recorded for it, which encoding/json writes sorted.
func (compat *OpenAICompat) requestObject(value map[string]any, member string) any {
	if path := schemaPath("", member); compat.objectOrder.records(path) {
		return compatObject{value: value, order: compat.objectOrder, path: path}
	}
	return value
}

// marshalObjectsInOrder is data, the compat's encoding with every map written sorted, with each compat object that has a recorded order
// written in it. A member keeps its place.
func (compat *OpenAICompat) marshalObjectsInOrder(data []byte) ([]byte, error) {
	object, err := orderedjson.Parse(data)
	if err != nil {
		return nil, err
	}
	for _, member := range compatObjectMembers {
		path := schemaPath("", member)
		if !object.Has(member) || !compat.objectOrder.records(path) {
			continue
		}
		ordered, err := marshalSchemaWithOrder(compat.object(member), compat.objectOrder, path)
		if err != nil {
			return nil, err
		}
		object.Set(member, ordered)
	}
	return object.MarshalJSON()
}

// oaiQwenChatTemplateKwargs is the chat_template_kwargs literal of the qwen-chat-template thinking format (openai-completions.ts:893-896).
type oaiQwenChatTemplateKwargs struct {
	EnableThinking   bool `json:"enable_thinking"`
	PreserveThinking bool `json:"preserve_thinking"`
}

// MergeOpenAICompat merges two compats, a setting of override replacing base's, and returns nil when both are nil. It is Pi's mergeCompat
// (provider-composer.ts:124-146): `{ ...base, ...override }`, with openRouterRouting, vercelGatewayRouting, chatTemplateKwargs and
// chatTemplateArgs merged member by member (`{ ...base.x, ...override.x }`). A spread keeps base's members in base's order, an overridden
// member in its place, and puts override's new members after them in override's order; the merged compat records that order with the values.
func MergeOpenAICompat(base, override *OpenAICompat) *OpenAICompat {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		merged := *override
		return &merged
	}
	if override == nil {
		merged := *base
		return &merged
	}
	merged := *base
	ovr := override
	merged.objectOrder = nil
	if ovr.SupportsDeveloperRole != nil {
		merged.SupportsDeveloperRole = ovr.SupportsDeveloperRole
	}
	if ovr.SupportsReasoningEffort != nil {
		merged.SupportsReasoningEffort = ovr.SupportsReasoningEffort
	}
	if ovr.SupportsStore != nil {
		merged.SupportsStore = ovr.SupportsStore
	}
	if ovr.SupportsUsageInStreaming != nil {
		merged.SupportsUsageInStreaming = ovr.SupportsUsageInStreaming
	}
	if ovr.SupportsFinishReason != nil {
		merged.SupportsFinishReason = ovr.SupportsFinishReason
	}
	if ovr.MaxTokensField != "" {
		merged.MaxTokensField = ovr.MaxTokensField
	}
	if ovr.RequiresToolResultName != nil {
		merged.RequiresToolResultName = ovr.RequiresToolResultName
	}
	if ovr.RequiresAssistantAfterToolResult != nil {
		merged.RequiresAssistantAfterToolResult = ovr.RequiresAssistantAfterToolResult
	}
	if ovr.RequiresNonNullAssistantContent != nil {
		merged.RequiresNonNullAssistantContent = ovr.RequiresNonNullAssistantContent
	}
	if ovr.RequiresToolsWithToolHistory != nil {
		merged.RequiresToolsWithToolHistory = ovr.RequiresToolsWithToolHistory
	}
	if ovr.RequiresThinkingAsText != nil {
		merged.RequiresThinkingAsText = ovr.RequiresThinkingAsText
	}
	if ovr.RequiresReasoningContentOnAssistantMessages != nil {
		merged.RequiresReasoningContentOnAssistantMessages = ovr.RequiresReasoningContentOnAssistantMessages
	}
	if ovr.SupportsReasoningReplay != nil {
		merged.SupportsReasoningReplay = ovr.SupportsReasoningReplay
	}
	if ovr.SupportsOpenAIGrammarTools != nil {
		merged.SupportsOpenAIGrammarTools = ovr.SupportsOpenAIGrammarTools
	}
	if ovr.SupportsExplicitPromptCacheMode != nil {
		merged.SupportsExplicitPromptCacheMode = ovr.SupportsExplicitPromptCacheMode
	}
	if ovr.SupportsStrictTools != nil {
		merged.SupportsStrictTools = ovr.SupportsStrictTools
	}
	if ovr.ThinkingFormat != "" {
		merged.ThinkingFormat = ovr.ThinkingFormat
	}
	if ovr.CacheControlFormat != "" {
		merged.CacheControlFormat = ovr.CacheControlFormat
	}
	if ovr.SendSessionAffinityHeaders != nil {
		merged.SendSessionAffinityHeaders = ovr.SendSessionAffinityHeaders
	}
	if ovr.SupportsStrictMode != nil {
		merged.SupportsStrictMode = ovr.SupportsStrictMode
	}
	merged.OpenRouterRouting = mergeCompatObject(&merged.objectOrder, "openRouterRouting", base, ovr)
	merged.ChatTemplateKwargs = mergeCompatObject(&merged.objectOrder, "chatTemplateKwargs", base, ovr)
	if ovr.VLLMPriority != nil {
		merged.VLLMPriority = ovr.VLLMPriority
	}
	merged.VercelGatewayRouting = mergeVercelGatewayRouting(base.VercelGatewayRouting, ovr.VercelGatewayRouting)
	if ovr.SupportsLongCacheRetention != nil {
		merged.SupportsLongCacheRetention = ovr.SupportsLongCacheRetention
	}
	if ovr.SendSessionIdHeader != nil {
		merged.SendSessionIdHeader = ovr.SendSessionIdHeader
	}
	if ovr.SupportsEagerToolInputStreaming != nil {
		merged.SupportsEagerToolInputStreaming = ovr.SupportsEagerToolInputStreaming
	}
	if ovr.SupportsCacheControlOnTools != nil {
		merged.SupportsCacheControlOnTools = ovr.SupportsCacheControlOnTools
	}
	if ovr.ForceAdaptiveThinking != nil {
		merged.ForceAdaptiveThinking = ovr.ForceAdaptiveThinking
	}
	if ovr.AllowEmptySignature != nil {
		merged.AllowEmptySignature = ovr.AllowEmptySignature
	}
	if ovr.SupportsTemperature != nil {
		merged.SupportsTemperature = ovr.SupportsTemperature
	}
	if ovr.ZaiToolStream != nil {
		merged.ZaiToolStream = ovr.ZaiToolStream
	}
	if ovr.DeferredToolsMode != "" {
		merged.DeferredToolsMode = ovr.DeferredToolsMode
	}
	if ovr.SessionAffinityFormat != "" {
		merged.SessionAffinityFormat = ovr.SessionAffinityFormat
	}
	if ovr.PromptCacheKeyMode != "" {
		merged.PromptCacheKeyMode = ovr.PromptCacheKeyMode
	}
	if ovr.SupportsToolSearch != nil {
		merged.SupportsToolSearch = ovr.SupportsToolSearch
	}
	if ovr.SupportsMaxOutputTokens != nil {
		merged.SupportsMaxOutputTokens = ovr.SupportsMaxOutputTokens
	}
	merged.ChatTemplateArgs = mergeCompatObject(&merged.objectOrder, "chatTemplateArgs", base, ovr)
	if ovr.SupportsThinkingTokenBudget != nil {
		merged.SupportsThinkingTokenBudget = ovr.SupportsThinkingTokenBudget
	}
	if ovr.ThinkingTokenBudgetField != "" {
		merged.ThinkingTokenBudgetField = ovr.ThinkingTokenBudgetField
	}
	if ovr.SupportsAdditionalTools != nil {
		merged.SupportsAdditionalTools = ovr.SupportsAdditionalTools
	}
	if ovr.SupportsMidConvoEffort != nil {
		merged.SupportsMidConvoEffort = ovr.SupportsMidConvoEffort
	}
	if ovr.SupportsMidConvoSystemMessages != nil {
		merged.SupportsMidConvoSystemMessages = ovr.SupportsMidConvoSystemMessages
	}
	if ovr.SupportsMidConvoToolAdditions != nil {
		merged.SupportsMidConvoToolAdditions = ovr.SupportsMidConvoToolAdditions
	}
	if ovr.SupportsMidConvoToolChanges != nil {
		merged.SupportsMidConvoToolChanges = ovr.SupportsMidConvoToolChanges
	}
	if ovr.SupportsMessageThreads != nil {
		merged.SupportsMessageThreads = ovr.SupportsMessageThreads
	}
	if base.Extra != nil || ovr.Extra != nil {
		merged.Extra = maps.Clone(base.Extra)
		if merged.Extra == nil {
			merged.Extra = make(map[string]json.RawMessage, len(ovr.Extra))
		}
		maps.Copy(merged.Extra, ovr.Extra)
	}
	if ovr.AllowedFallbackModels != nil {
		merged.AllowedFallbackModels = slices.Clone(ovr.AllowedFallbackModels)
		for i := range merged.AllowedFallbackModels {
			merged.AllowedFallbackModels[i].Cost.Tiers = slices.Clone(merged.AllowedFallbackModels[i].Cost.Tiers)
		}
	}
	return &merged
}

// mergeCompatObject is `{ ...base, ...override }` for one of the compat objects of two compats: the merged map, with its member order
// recorded in order when it is not the sorted one. Each member's value, a nested object's order with it, is the override's when it has
// the member and base's otherwise.
func mergeCompatObject(order *schemaObjectOrder, member string, base, override *OpenAICompat) map[string]any {
	baseObject, overrideObject := base.object(member), override.object(member)
	if baseObject == nil && overrideObject == nil {
		return nil
	}
	merged := maps.Clone(baseObject)
	if merged == nil {
		merged = make(map[string]any, len(overrideObject))
	}
	maps.Copy(merged, overrideObject)
	path := schemaPath("", member)
	keys := orderedSchemaKeys(baseObject, base.objectOrder, path)
	for _, key := range orderedSchemaKeys(overrideObject, override.objectOrder, path) {
		if _, inBase := baseObject[key]; !inBase {
			keys = append(keys, key)
		}
	}
	record := func(recorded string, keys []string) {
		if *order == nil {
			*order = make(schemaObjectOrder)
		}
		(*order)[recorded] = keys
	}
	if enumerated := unsortedKeyOrder(keys); enumerated != nil {
		record(path, enumerated)
	}
	for recorded, keys := range override.objectOrder {
		if _, nested := compatObjectMemberOf(recorded, path); nested {
			record(recorded, keys)
		}
	}
	for recorded, keys := range base.objectOrder {
		if key, nested := compatObjectMemberOf(recorded, path); nested {
			if _, overridden := overrideObject[key]; !overridden {
				record(recorded, keys)
			}
		}
	}
	return merged
}

// compatObjectMemberOf is the member of the object at path that recorded, a path below it, lies in.
func compatObjectMemberOf(recorded, path string) (string, bool) {
	if len(recorded) <= len(path) || !strings.HasPrefix(recorded, path) || recorded[len(path)] != '/' {
		return "", false
	}
	segment, _, _ := strings.Cut(recorded[len(path)+1:], "/")
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~"), true
}

// mergeVercelGatewayRouting merges key by key, as mergeCompatObject does: a list the override sets replaces the base list, and an absent list keeps it.
func mergeVercelGatewayRouting(base, override *VercelGatewayRouting) *VercelGatewayRouting {
	if base == nil && override == nil {
		return nil
	}
	merged := VercelGatewayRouting{}
	if base != nil {
		merged = VercelGatewayRouting{Only: slices.Clone(base.Only), Order: slices.Clone(base.Order)}
	}
	if override != nil {
		if override.Only != nil {
			merged.Only = slices.Clone(override.Only)
		}
		if override.Order != nil {
			merged.Order = slices.Clone(override.Order)
		}
	}
	return &merged
}
