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

// GetImageModel returns a generated image model by provider and id.
func GetImageModel(provider string, modelID string) (ImageModel, bool) {
	providerModels := imageModelRegistry[provider]
	if providerModels == nil {
		return ImageModel{}, false
	}
	model, ok := providerModels[modelID]
	return model, ok
}

// GetImageProviders returns the generated image providers in stable order.
func GetImageProviders() []string {
	providers := make([]string, 0, len(imageModelRegistry))
	for provider := range imageModelRegistry {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	return providers
}

// GetImageModels returns generated image models for provider in stable ID order.
func GetImageModels(provider string) []ImageModel {
	providerModels := imageModelRegistry[provider]
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
