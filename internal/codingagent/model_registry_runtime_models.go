package codingagent

import "github.com/MichaelKinsy/PiG/ai"

// RuntimeModels returns composed selection metadata without resolving request authentication or copying unused request capabilities.
func (r *ModelRegistry) RuntimeModels() []RuntimeModel {
	providerIDs := r.modelProviderIDs()
	// Size the result for the bundled catalogs, which are most of it, so it is not regrown model by model.
	overlays := make([]bool, len(providerIDs))
	builtins := 0
	for i, id := range providerIDs {
		if overlays[i] = r.hasModelDataOverlay(id); overlays[i] {
			builtins += len(builtinChatCatalog(id))
		} else {
			catalog, listing := builtinChatCatalogListing(id)
			builtins += max(len(catalog), len(listing))
		}
	}
	result := make([]RuntimeModel, 0, builtins)
	for i, id := range providerIDs {
		if overlays[i] {
			for _, model := range r.GetProviderModelData(id) {
				result = append(result, RuntimeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)})
			}
			continue
		}
		// Unmodified built-ins already contain every field the model resolver reads. Preserve fresh header ownership without materializing the request-only fields of ai.Model.
		catalog, listing := builtinChatCatalogListing(id)
		if listing == nil {
			for n := range catalog {
				model := &catalog[n]
				result = append(result, RuntimeModel{Provider: model.Provider, ID: model.ID, Name: model.DisplayName, Reasoning: model.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.Headers)})
			}
			continue
		}
		// pig divergence (D199): OpenRouter's routing variants follow their model, as they do in the composed catalog. The listing holds each one's id and name, and a variant repeats its model's provider, reasoning and headers, so no variant is built here.
		for n := range listing {
			listed := &listing[n]
			model := &catalog[listed.Base]
			result = append(result, RuntimeModel{Provider: model.Provider, ID: listed.ID, Name: listed.Name, Reasoning: model.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.Headers)})
		}
	}
	return result
}

func (r *ModelRegistry) hasModelDataOverlay(id string) bool {
	if r.GetProvider(id) != nil || len(r.remoteOverlayModels(id)) > 0 {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.radiusProviderLocked(id) != nil {
		return true
	}
	if _, exists := r.dynamic[id]; exists {
		return true
	}
	if r.config != nil {
		_, exists := r.config.Providers[id]
		return exists
	}
	return false
}
