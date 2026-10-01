package ai

// Ports packages/ai/src/providers/all.ts (built-in model accessors and provider assembly), providers/typesafe.ts,
// providers/cloudflare-workers-ai.ts and the classifier registrations of providers/{opencode,openrouter,
// vercel-ai-gateway}.ts.

import (
	"context"
	"slices"
)

// GetBuiltinClassifierModels lists the built-in classifier models of one provider, in catalog order. The models are
// copies: changing one does not change the catalog.
func GetBuiltinClassifierModels(provider string) []*ClassifierModel {
	var models []*ClassifierModel
	for i := range GeneratedClassifierModels {
		if GeneratedClassifierModels[i].Provider == provider {
			models = append(models, cloneClassifierModel(&GeneratedClassifierModels[i]))
		}
	}
	return models
}

// GetBuiltinClassifierModel reads one built-in classifier model, or nil.
func GetBuiltinClassifierModel(provider, id string) *ClassifierModel {
	for i := range GeneratedClassifierModels {
		if model := &GeneratedClassifierModels[i]; model.Provider == provider && model.ID == id {
			return cloneClassifierModel(model)
		}
	}
	return nil
}

func cloneClassifierModel(model *ClassifierModel) *ClassifierModel {
	clone := *model
	clone.Headers = cloneStringMap(model.Headers)
	clone.Input = slices.Clone(model.Input)
	return &clone
}

// builtinChatModels lists the built-in chat models of one provider.
func builtinChatModels(provider string) []*Model {
	var models []*Model
	for _, generated := range ListModels(provider) {
		models = append(models, generated.ToModel())
	}
	return models
}

// GetAllBuiltinModels lists the built-in chat, image and classifier models of one provider.
func GetAllBuiltinModels(provider string) []AnyModel {
	var models []AnyModel
	for _, model := range builtinChatModels(provider) {
		models = append(models, model)
	}
	for _, image := range GetImageModels(provider) {
		models = append(models, &image)
	}
	for _, classifier := range GetBuiltinClassifierModels(provider) {
		models = append(models, classifier)
	}
	return models
}

// builtinClassifiers are the classifier implementations of the providers that serve classifier models.
func builtinClassifiers(provider string) ProviderClassifierMap {
	switch provider {
	case "typesafe", "opencode", "openrouter", "vercel-ai-gateway":
		return ProviderClassifierMap{ClassifierAPITypesafeSystemOne: TypesafeSystemOneAPI()}
	case "cloudflare-workers-ai":
		return ProviderClassifierMap{ClassifierAPICloudflareWorkersAISystemOne: CloudflareClassifier(CloudflareWorkersAISystemOneAPI())}
	}
	return nil
}

// builtinProvider assembles one built-in provider: its chat models routed through the registered API implementations,
// its image models and its classifier models.
func builtinProvider(id string) *ModelsProvider {
	auth, err := BuiltinProviderAuth(id)
	if err != nil {
		panic(err)
	}
	options := CreateProviderOptions{ID: id, Name: new(ProviderDisplayName(id)), Auth: auth, Models: GetAllBuiltinModels(id), Classifiers: builtinClassifiers(id)}
	if len(builtinChatModels(id)) > 0 {
		options.API = &ProviderStreams{Stream: StreamSimple, StreamSimple: StreamSimple}
	}
	if len(GetImageModels(id)) > 0 {
		options.Images = ProviderImageAPIMap{APIImagesOpenRouter: {GenerateImages: func(ctx context.Context, model *ImageModel, request ImagesContext, imageOptions ImagesOptions) (AssistantImages, error) {
			return GenerateImagesOpenRouter(ctx, *model, request, imageOptions), nil
		}}}
	}
	return CreateProvider(options)
}

// TypesafeProvider is the TypeSafe provider: classifier models only.
func TypesafeProvider() *ModelsProvider { return builtinProvider("typesafe") }

// CloudflareWorkersAIProvider is the Cloudflare Workers AI provider.
func CloudflareWorkersAIProvider() *ModelsProvider { return builtinProvider("cloudflare-workers-ai") }

// BuiltinProviders returns every built-in provider, freshly constructed.
func BuiltinProviders() []*ModelsProvider {
	ids := ListProviders()
	providers := make([]*ModelsProvider, len(ids))
	for i, id := range ids {
		providers[i] = builtinProvider(id)
	}
	return providers
}

// BuiltinModels returns a Models collection with every built-in provider registered.
func BuiltinModels(options ...CreateModelsOptions) *Models {
	models := CreateModels(options...)
	for _, provider := range BuiltinProviders() {
		models.SetProvider(provider)
	}
	return models
}
