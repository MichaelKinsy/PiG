package ai

import "slices"

var imageModelRegistry = func() map[ImagesProvider]map[string]ImagesModel {
	out := make(map[ImagesProvider]map[string]ImagesModel)
	for _, model := range GeneratedImageModels {
		providerModels := out[model.Provider]
		if providerModels == nil {
			providerModels = make(map[string]ImagesModel)
			out[model.Provider] = providerModels
		}
		providerModels[model.ID] = model
	}
	return out
}()

// GetImageModel returns a generated image model by provider and id.
func GetImageModel(provider ImagesProvider, modelID string) (ImagesModel, bool) {
	providerModels := imageModelRegistry[provider]
	if providerModels == nil {
		return ImagesModel{}, false
	}
	model, ok := providerModels[modelID]
	return model, ok
}

// GetImageProviders returns the generated image providers in stable order.
func GetImageProviders() []ImagesProvider {
	providers := make([]ImagesProvider, 0, len(imageModelRegistry))
	for provider := range imageModelRegistry {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	return providers
}

// GetImageModels returns generated image models for provider in stable ID order.
func GetImageModels(provider ImagesProvider) []ImagesModel {
	providerModels := imageModelRegistry[provider]
	if providerModels == nil {
		return nil
	}
	ids := make([]string, 0, len(providerModels))
	for id := range providerModels {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	models := make([]ImagesModel, 0, len(ids))
	for _, id := range ids {
		models = append(models, providerModels[id])
	}
	return models
}
