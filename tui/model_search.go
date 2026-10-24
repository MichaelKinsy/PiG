package tui

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Model search text builders shared by the /model selector, /scoped-models,
// and /model argument autocomplete.
//
// Upstream reference:
//   .upstream/current/packages/coding-agent/src/modes/interactive/model-search.ts

// ModelSearchItem is the provider, bare id, and optional display name a model
// search text is built from. Mirrors upstream ModelSearchItem; an empty Name
// is upstream's absent (or empty) name.
type ModelSearchItem struct {
	ID       string
	Provider string
	Name     string
}

// GetModelSearchText mirrors upstream getModelSearchText: the bare id leads,
// followed by the provider, the provider/id pair, provider and id again, and
// the name when present.
func GetModelSearchText(item ModelSearchItem) string {
	id, provider := item.ID, item.Provider
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return id + " " + provider + " " + provider + "/" + id + " " + provider + " " + id + name
}

// GetModelSelectorSearchText mirrors upstream getModelSelectorSearchText.
// The /model selector search should rank exact provider-prefixed queries before
// proxy-provider IDs like openrouter/openai/gpt-5, so the bare model ID stays
// out of the leading position.
func GetModelSelectorSearchText(item ModelSearchItem) string {
	id, provider := item.ID, item.Provider
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return provider + " " + provider + "/" + id + " " + provider + " " + id + name
}

// splitModelLabel is upstream splitModelLabel: the lower-cased value split at every run of characters that are neither letters nor
// numbers (/[^\p{L}\p{N}]+/u), without empty words.
func splitModelLabel(value string) []string {
	return strings.FieldsFunc(jsstring.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// GetDistinctModelName mirrors upstream getDistinctModelName: the model name when it has words the ID does not, such as a renamed override
// ("MiniMax M3 [rasmus@openrouter, parasail]" for "minimax/minimax-m3"), and "" (upstream's undefined) for a name that only restates the ID with
// different casing, separators, or a dropped namespace or version suffix. Only ID and Name of the item are read.
func GetDistinctModelName(item ModelSearchItem) string {
	name := jsstring.Trim(item.Name)
	if name == "" {
		return ""
	}
	idWords := splitModelLabel(item.ID)
	idTokens := make(map[string]struct{}, len(idWords))
	for _, word := range idWords {
		idTokens[word] = struct{}{}
	}
	compactID := strings.Join(idWords, "")
	for _, word := range splitModelLabel(name) {
		if _, ok := idTokens[word]; ok {
			continue
		}
		// Single-character words must match a whole ID token: "4" is not covered by "mistral-small-2603".
		if len(utf16.Encode([]rune(word))) > 1 && strings.Contains(compactID, word) {
			continue
		}
		return name
	}
	return ""
}
