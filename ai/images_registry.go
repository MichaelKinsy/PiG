package ai

import "slices"

var imageModelRegistry = func() map[string]map[string]ImageModel {
	out := make(map[string]map[string]ImageModel)
	for _, model := range GeneratedImageModels {
		providerModels := out[model.Provider]
		if providerModels == nil {
			providerModels = make(map[string]ImageModel)
			out[model.Provider] = providerModels
		}
		providerModels[model.ID] = model
	}
	return out
}()

// BuiltinImageProvider is a built-in provider with at least one image model in the generated catalog.
// upstream: image-models.ts:13 BuiltinImageProvider
type BuiltinImageProvider string

// GetImageModel returns a generated image model by provider and id.
func GetImageModel(provider BuiltinImageProvider, modelID string) (ImageModel, bool) {
	providerModels := imageModelRegistry[string(provider)]
	if providerModels == nil {
		return ImageModel{}, false
	}
	model, ok := providerModels[modelID]
	return model, ok
}

// GetImageProviders returns the generated image providers in stable order.
func GetImageProviders() []BuiltinImageProvider {
	providers := make([]BuiltinImageProvider, 0, len(imageModelRegistry))
	for provider := range imageModelRegistry {
		providers = append(providers, BuiltinImageProvider(provider))
	}
	slices.Sort(providers)
	return providers
}

// GetImageModels returns generated image models for provider in stable ID order.
func GetImageModels(provider BuiltinImageProvider) []ImageModel {
	providerModels := imageModelRegistry[string(provider)]
	if providerModels == nil {
		return nil
	}
	ids := make([]string, 0, len(providerModels))
	for id := range providerModels {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	models := make([]ImageModel, 0, len(ids))
	for _, id := range ids {
		models = append(models, providerModels[id])
	}
	return models
}
