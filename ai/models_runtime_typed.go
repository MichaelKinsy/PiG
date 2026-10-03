package ai

import (
	"context"
	"slices"
)

// ModelsImagesOptions is ImagesOptions plus the per-request header transform Models applies.
type ModelsImagesOptions struct {
	ImagesOptions
	// TransformHeaders runs once after configured, auth, and request headers are merged.
	TransformHeaders func(context.Context, ProviderHeaders) (ProviderHeaders, error)
}

// GetAllModels reads the last-known models of every type from one provider or all providers. A provider whose
// listing fails contributes no models.
func (m *Models) GetAllModels(provider ...string) []AnyModel {
	var providers []*ModelsProvider
	if len(provider) > 0 {
		providers = []*ModelsProvider{m.GetProvider(provider[0])}
	} else {
		providers = m.GetProviders()
	}
	out := []AnyModel{}
	for _, entry := range providers {
		if entry == nil {
			continue
		}
		models, err := providerAllModels(entry)
		if err == nil {
			out = append(out, models...)
		}
	}
	return out
}

// GetModelsOfType reads the last-known models of one type from one provider or all providers.
func (m *Models) GetModelsOfType(modelType ModelType, provider ...string) []AnyModel {
	return slices.DeleteFunc(m.GetAllModels(provider...), func(model AnyModel) bool { return !IsModelType(model, modelType) })
}

// GetModelOfType looks up one model of the given type in the last-known lists, or returns nil.
func (m *Models) GetModelOfType(modelType ModelType, provider, id string) AnyModel {
	for _, model := range m.GetModelsOfType(modelType, provider) {
		if model.ModelID() == id {
			return model
		}
	}
	return nil
}

// GenerateImages generates images through the owning provider with auth resolved like Stream. It never returns an error:
// unknown providers, unconfigured auth, cancellation and providers without image generation become an error or
// aborted AssistantImages.
func (m *Models) GenerateImages(ctx context.Context, model *ImageModel, request ImagesContext, options ...ModelsImagesOptions) AssistantImages {
	var opts ModelsImagesOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := m.generateImages(ctx, model, request, opts)
	if err != nil {
		return imageErrorResult(model, err, ctx.Err() != nil)
	}
	return result
}

func (m *Models) generateImages(ctx context.Context, model *ImageModel, request ImagesContext, options ModelsImagesOptions) (AssistantImages, error) {
	provider, err := m.requireProvider(model)
	if err != nil {
		return AssistantImages{}, err
	}
	if provider.GenerateImages == nil {
		return AssistantImages{}, NewModelsError(ModelsErrorProvider, "Provider "+model.Provider+" does not support image generation", nil)
	}
	auth, err := m.resolveRequestAuth(ctx, model, options.APIKey, options.APIKeySet, options.Headers, options.Env, options.TransformHeaders)
	if err != nil {
		return AssistantImages{}, err
	}
	requestModel := model
	if auth.baseURL != "" {
		requestModel = new(*model)
		requestModel.BaseURL = auth.baseURL
	}
	requestOptions := options.ImagesOptions
	requestOptions.APIKey, requestOptions.Headers, requestOptions.Env = auth.apiKey, auth.headers, auth.env
	return provider.GenerateImages(ctx, requestModel, request, requestOptions)
}
