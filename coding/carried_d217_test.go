package coding

import "slices"

// Pig divergence D217: Pi 2e0f175b (#11368) removes the realtime and image rows that models.dev does not type as chat models from the chat catalog. The pinned Pi
// that these oracles run still lists them, so a Pi answer is read with those rows taken out of its model lists, and a probe whose answer is one of the rows is
// skipped. Every other member of the answer is still compared with Pi.
var d217CarriedDroppedChatRefs = []string{
	"azure/gpt-realtime-2.1",
	"openai/gpt-realtime-2.1",
	"google/gemini-3.1-flash-live-preview",
	"google/gemini-3.1-flash-lite-image",
	"google-vertex/gemini-3.1-flash-lite-image",
}

// d217PiWithoutDroppedChatRows returns value, a decoded Pi oracle answer, with the carried-out rows removed from every list of model references.
func d217PiWithoutDroppedChatRows(value any) any {
	switch v := value.(type) {
	case []any:
		kept := make([]any, 0, len(v))
		for _, element := range v {
			if d217IsDroppedChatRow(element) {
				continue
			}
			kept = append(kept, d217PiWithoutDroppedChatRows(element))
		}
		return kept
	case map[string]any:
		kept := make(map[string]any, len(v))
		provider, _ := v["id"].(string)
		for key, element := range v {
			if models, ok := element.([]any); ok && key == "models" && provider != "" {
				// The `provider` op lists a provider's model ids without the provider prefix.
				element = slices.DeleteFunc(slices.Clone(models), func(model any) bool {
					id, _ := model.(string)
					return slices.Contains(d217CarriedDroppedChatRefs, provider+"/"+id)
				})
			}
			kept[key] = d217PiWithoutDroppedChatRows(element)
		}
		return kept
	}
	return value
}

// d217PiMentionsDroppedChatRow reports whether a decoded Pi answer names one of the carried-out rows anywhere. A partial-match probe (`gpt`) that Pi resolves to one
// of them resolves to another model in PiG, so such a probe is compared only when the two answers agree.
func d217PiMentionsDroppedChatRow(value any) bool {
	switch v := value.(type) {
	case string:
		return slices.Contains(d217CarriedDroppedChatRefs, v)
	case []any:
		return slices.ContainsFunc(v, d217PiMentionsDroppedChatRow)
	case map[string]any:
		for _, element := range v {
			if d217PiMentionsDroppedChatRow(element) {
				return true
			}
		}
	}
	return false
}

// d217IsDroppedChatRow reports whether a list element is a carried-out model reference, alone or as the first member of a [reference, thinking level] pair.
func d217IsDroppedChatRow(element any) bool {
	if pair, ok := element.([]any); ok && len(pair) == 2 {
		element = pair[0]
	}
	ref, ok := element.(string)
	return ok && slices.Contains(d217CarriedDroppedChatRefs, ref)
}
