package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts.

import (
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// GetProviderModelData composes metadata without resolving keys, headers, or backend clients.
func (r *ModelRegistry) GetProviderModelData(id string) []*ai.Model {
	if r.GetProvider(id) != nil {
		return r.GetNativeModels(id)
	}
	generatedModels := ai.ListModels(id)
	var baseline []*ai.Model
	if len(generatedModels) > 0 {
		baseline = make([]*ai.Model, 0, len(generatedModels))
	}
	for _, generated := range generatedModels {
		baseline = append(baseline, generated.ToModel())
	}
	// The remote catalog overlay adds models to, and replaces same-id models of, the bundled catalog (remote-catalog-provider.ts:getModels).
	if overlay := r.remoteOverlayModels(id); len(overlay) > 0 {
		merged := mergeRemoteCatalogModels(ai.AnyModels(baseline), slices.DeleteFunc(overlay, func(model ai.AnyModel) bool { return !ai.IsModelType(model, ai.ModelTypeChat) }))
		baseline = baseline[:0:0]
		for _, model := range merged {
			baseline = append(baseline, model.(*ai.Model))
		}
	}
	r.mu.RLock()
	radius := r.radiusProviderLocked(id)
	r.mu.RUnlock()
	if radius != nil {
		baseline = nil
		for _, model := range radius.GetModels() {
			baseline = append(baseline, nativeModelFromEntry(radiusModelEntry(model)))
		}
	}
	r.mu.RLock()
	dynamic, registered := r.dynamic[id]
	r.mu.RUnlock()
	var input *ProviderConfigInput
	if registered {
		input = new(providerModelInput(id, dynamic))
	}
	provider, err := r.composeNativeProvider(&ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) { return baseline, nil }}, input)
	if err != nil {
		return nil
	}
	models, err := provider.GetModels()
	if err != nil {
		return nil
	}
	return models
}

// GetAllModelData returns the composed catalog in provider order without running credential configuration expressions.
func (r *ModelRegistry) GetAllModelData() []*ai.Model {
	var models []*ai.Model
	for _, id := range r.modelProviderIDs() {
		models = append(models, r.GetProviderModelData(id)...)
	}
	return models
}

// ReadNativeProviderModelData reads the catalog of every provider whose models come from provider code, in provider order, and discards the models. A caller that has no use for the composed models but must still run the provider callbacks Pi's getModels runs uses it. The catalogs of the other providers are static data whose read has no effect to observe.
func ReadNativeProviderModelData(r *ModelRegistry) {
	for _, id := range r.modelProviderIDs() {
		if r.GetProvider(id) != nil {
			_ = r.GetProviderModelData(id)
		}
	}
}

func (r *ModelRegistry) modelProviderIDs() []string {
	r.mu.RLock()
	var configured []string
	if r.config != nil {
		configured = slices.Sorted(maps.Keys(r.config.Providers))
	}
	dynamic := slices.Sorted(maps.Keys(r.dynamic))
	r.mu.RUnlock()
	order := ai.ListProviders()
	order = append(order, modelsJSONProviderOrder(r.modelConfigPath())...)
	order = append(order, configured...)
	order = append(order, dynamic...)
	order = append(order, r.GetRegisteredProviderIDs()...)
	seen := make(map[string]bool, len(order))
	ids := make([]string, 0, len(order))
	for _, id := range order {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}
