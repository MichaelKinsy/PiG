package codingagent

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// RuntimeModels returns the composed model identities in upstream
// ModelRuntime.getModels order: built-in providers first, then models.json
// providers in file order, then extension-registered providers. Within a
// provider, the catalog models come first and models.json or extension
// definitions replace a catalog model of the same id or follow in definition
// order. An extension provider that declares models replaces the catalog.
func (r *ModelRegistry) RuntimeModels() []RuntimeModel {
	configured := append(r.getAllConfigured(), r.radiusEntries(false)...)
	byProvider := map[string][]ModelEntry{}
	for _, entry := range configured {
		byProvider[entry.ProviderID] = append(byProvider[entry.ProviderID], entry)
	}
	order := ai.ListProviders()
	order = append(order, modelsJSONProviderOrder(filepath.Join(r.agentDir, "models.json"))...)
	order = append(order, slices.Sorted(maps.Keys(byProvider))...)
	seen := make(map[string]bool, len(order))
	var models []RuntimeModel
	for _, providerID := range order {
		if seen[providerID] {
			continue
		}
		seen[providerID] = true
		var providerModels []RuntimeModel
		for _, generated := range ai.ListModels(providerID) {
			if r.HasGeneratedModel(providerID, generated.ID) {
				providerModels = append(providerModels, RuntimeModel{Provider: providerID, ID: generated.ID, Name: generated.DisplayName})
			}
		}
		for _, entry := range byProvider[providerID] {
			model := RuntimeModel{Provider: providerID, ID: entry.ModelID, Name: firstModelValue(entry.DisplayName, entry.ModelID)}
			if index := slices.IndexFunc(providerModels, func(existing RuntimeModel) bool { return existing.ID == entry.ModelID }); index >= 0 {
				providerModels[index] = model
			} else {
				providerModels = append(providerModels, model)
			}
		}
		models = append(models, providerModels...)
	}
	return models
}
