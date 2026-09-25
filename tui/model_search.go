package tui

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
