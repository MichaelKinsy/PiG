package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts.

import (
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// GetProviderModelData composes metadata without resolving keys, headers, or backend clients. Like the catalog and the native collection it composes from, it omits the models of a stripped API, including models.json and extension definitions on one (ai.OfferedModels).
func (r *ModelRegistry) GetProviderModelData(id string) []*ai.Model {
	if plain, ok := r.plainGeneratedModels(id); ok {
		return plainModelData(plain)
	}
	if r.GetProvider(id) != nil {
		return r.GetNativeModels(id)
	}
	generatedModels := builtinChatCatalog(id)
	var baseline []*ai.Model
	if len(generatedModels) > 0 {
		baseline = make([]*ai.Model, 0, len(generatedModels))
	}
	for _, generated := range generatedModels {
		baseline = append(baseline, generated.ToModel())
	}
	// The remote catalog overlay adds models to, and replaces same-id models of, the bundled catalog (remote-catalog-provider.ts:getModels).
	// A personalized catalog (the user's own list) replaces the bundled one instead (remote-catalog-provider.ts:withPersonalizedCatalog getModels).
	if overlay := r.remoteOverlayModels(id); len(overlay) > 0 {
		chatOverlay := slices.DeleteFunc(overlay, func(model ai.AnyModel) bool { return !ai.IsModelType(model, ai.ModelTypeChat) })
		merged := mergeRemoteCatalogModels(ai.AnyModels(baseline), chatOverlay)
		if r.remoteOverlayReplaces(id) {
			merged = chatOverlay
		}
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
	return ai.OfferedModels(models)
}

// plainGeneratedModels returns the bundled catalog of provider id when nothing changes it: no provider code, remote catalog overlay, Radius provider, extension registration or models.json entry. GetProviderModelData's models for such a provider are exactly plainModelData of it, and building them has no effect to observe.
func (r *ModelRegistry) plainGeneratedModels(id string) ([]ai.GeneratedModel, bool) {
	if r.GetProvider(id) != nil {
		return nil, false
	}
	if len(r.remoteOverlayModels(id)) > 0 {
		return nil, false
	}
	r.mu.RLock()
	radius := r.radiusProviderLocked(id)
	_, registered := r.dynamic[id]
	configured := false
	if r.config != nil {
		_, configured = r.config.Providers[id]
	}
	r.mu.RUnlock()
	if radius != nil || registered || configured {
		return nil, false
	}
	return builtinChatCatalog(id), true
}

// plainModelData builds the models of a plain bundled catalog in catalog order.
func plainModelData(generated []ai.GeneratedModel) []*ai.Model {
	if len(generated) == 0 {
		return nil
	}
	models := make([]*ai.Model, 0, len(generated))
	for i := range generated {
		models = append(models, generated[i].ToModel())
	}
	return ai.OfferedModels(models)
}

// GetProviderModelDataByID is the first model of GetProviderModelData(id) whose ID is modelID, or nil. A plain bundled catalog is searched
// by entry and only the match is built, where GetProviderModelData builds every model of the provider (about 8 allocations and 1 KB each).
func (r *ModelRegistry) GetProviderModelDataByID(id, modelID string) *ai.Model {
	if plain, ok := r.plainGeneratedModels(id); ok {
		for _, generated := range ai.OfferedGeneratedModels(plain) {
			if generated.ID == modelID {
				return generated.ToModel()
			}
		}
		return nil
	}
	for _, model := range r.GetProviderModelData(id) {
		if model.ID == modelID {
			return model
		}
	}
	return nil
}

// GetAllModelData returns the composed catalog in provider order without running credential configuration expressions.
func (r *ModelRegistry) GetAllModelData() []*ai.Model {
	var models []*ai.Model
	for _, id := range r.modelProviderIDs() {
		models = append(models, r.GetProviderModelData(id)...)
	}
	return models
}

// ModelDataSource is one model of GetAllModelData before it is built. A plain bundled catalog's model is its catalog entry (Generated), whose
// ToModel is the model GetAllModelData lists; any other provider's model is that model (Model).
type ModelDataSource struct {
	Generated *ai.GeneratedModel
	Model     *ai.Model
}

// GetAllModelDataSources is GetAllModelData for a caller that reads every model's identity and builds few models: it lists the same models,
// in the same order, without building a plain bundled catalog's models.
func (r *ModelRegistry) GetAllModelDataSources() []ModelDataSource {
	var sources []ModelDataSource
	for _, id := range r.modelProviderIDs() {
		if plain, ok := r.plainGeneratedModels(id); ok {
			for _, generated := range ai.OfferedGeneratedModels(plain) {
				sources = append(sources, ModelDataSource{Generated: generated})
			}
			continue
		}
		for _, model := range r.GetProviderModelData(id) {
			sources = append(sources, ModelDataSource{Model: model})
		}
	}
	return sources
}

// ReadNativeProviderModelData reads the catalog of every provider whose models come from provider code, in provider order, and discards the models. A caller that has no use for the composed models but must still run the provider callbacks Pi's getModels runs uses it. The catalogs of the other providers are static data whose read has no effect to observe.
func ReadNativeProviderModelData(r *ModelRegistry) {
	for _, id := range r.modelProviderIDs() {
		if r.GetProvider(id) != nil {
			_ = r.GetProviderModelData(id)
		}
	}
}

// nativeCollectionProviderIDs lists the providers of the native Models collection in its order.
func (r *ModelRegistry) nativeCollectionProviderIDs() []string {
	r.nativeMu.Lock()
	models := r.nativeModels
	r.nativeMu.Unlock()
	if models == nil {
		return nil
	}
	providers := models.GetProviders()
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return ids
}

func (r *ModelRegistry) modelProviderIDs() []string {
	r.mu.RLock()
	var configured []string
	if r.config != nil {
		configured = slices.Sorted(maps.Keys(r.config.Providers))
	}
	// Pi's ModelRuntime composes an extension provider into its Models map when the extension registers it, so the
	// map, and getAll, list registered providers in registration order (model-runtime.ts:921-933, models.ts setProvider).
	dynamic := make([]string, 0, len(r.dynamic))
	for _, id := range r.dynamicOrder {
		if _, ok := r.dynamic[id]; ok {
			dynamic = append(dynamic, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(r.dynamic)) {
		if !slices.Contains(dynamic, id) {
			dynamic = append(dynamic, id)
		}
	}
	r.mu.RUnlock()
	order := ai.ListProviders()
	order = append(order, modelsJSONProviderOrder(r.modelConfigPath())...)
	order = append(order, configured...)
	order = append(order, dynamic...)
	order = append(order, r.nativeCollectionProviderIDs()...)
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
