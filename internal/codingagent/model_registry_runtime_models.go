package codingagent

import "github.com/MichaelKinsy/PiG/ai"

// RuntimeModels returns composed selection metadata without resolving request authentication or copying unused request capabilities.
func (r *ModelRegistry) RuntimeModels() []RuntimeModel {
	providerIDs := r.modelProviderIDs()
	// Size the result for the bundled catalogs, which are most of it, so it is not regrown model by model.
	builtins := 0
	for _, id := range providerIDs {
		builtins += len(builtinChatCatalog(id))
	}
	result := make([]RuntimeModel, 0, builtins)
	for _, id := range providerIDs {
		if r.hasModelDataOverlay(id) {
			for _, model := range r.GetProviderModelData(id) {
				result = append(result, RuntimeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)})
			}
			continue
		}
		// Unmodified built-ins already contain every field the model resolver reads. Preserve fresh header ownership without materializing the request-only fields of ai.Model.
		for _, model := range builtinChatCatalog(id) {
			result = append(result, RuntimeModel{Provider: model.Provider, ID: model.ID, Name: model.DisplayName, Reasoning: model.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.Headers)})
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
