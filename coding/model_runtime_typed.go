package coding

// Ports the typed model accessors and image generation of packages/coding-agent/src/core/model-runtime.ts (getModelsOfType, getModelOfType, getAllModels, getAvailableOfType, getProvider, getAuth(model), generateImages).

import (
	"context"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// GetProvider returns the composed provider with the given ID: a registered native or composed provider, else the built-in provider composed with models.json and legacy registrations.
func (runtime *ModelRuntime) GetProvider(id string) *ai.ModelsProvider {
	if runtime == nil || runtime.services == nil || id == "" {
		return nil
	}
	return runtime.services.Registry().GetTypedProvider(id, runtime.builtinStream(id))
}

// GetAllModels lists the last-known models of every type from one provider or all providers. Chat models are the same bound models GetModel returns.
func (runtime *ModelRuntime) GetAllModels(providerID ...string) []ai.AnyModel {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	registry := runtime.services.Registry()
	var models []ai.AnyModel
	if len(providerID) > 0 {
		models = registry.GetProviderAllModelData(providerID[0])
	} else {
		models = registry.GetAllProviderModelData()
	}
	// A provider may return its own list; bind into a copy.
	models = slices.Clone(models)
	for i, model := range models {
		if chat, ok := model.(*ai.Model); ok {
			models[i] = runtime.bindCatalogModel(chat)
		}
	}
	return models
}

// GetModelsOfType lists the last-known models of one type from one provider or all providers.
func (runtime *ModelRuntime) GetModelsOfType(modelType ai.ModelType, providerID ...string) []ai.AnyModel {
	all := runtime.GetAllModels(providerID...)
	models := all[:0:0]
	for _, model := range all {
		if ai.IsModelType(model, modelType) {
			models = append(models, model)
		}
	}
	return models
}

// GetModelOfType looks up one model of the given type, or returns nil.
func (runtime *ModelRuntime) GetModelOfType(modelType ai.ModelType, providerID, modelID string) ai.AnyModel {
	for _, model := range runtime.GetModelsOfType(modelType, providerID) {
		if model.ModelID() == modelID {
			return model
		}
	}
	return nil
}

// GetAvailableOfType lists the models of one type whose provider has working credentials. Chat models follow the provider's chat availability, so the chat list matches GetAvailable; every other model type is kept (models.ts getAvailableOfType, getAllAvailable).
func (runtime *ModelRuntime) GetAvailableOfType(ctx context.Context, modelType ai.ModelType, providerID ...string) ([]ai.AnyModel, error) {
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	id := ""
	if len(providerID) > 0 {
		id = providerID[0]
	}
	registry := runtime.services.Registry()
	registry.StartRegistrationRefresh(ctx)
	all, err := registry.GetAvailableAllModelDataContext(ctx, id)
	if err != nil {
		return nil, err
	}
	models := []ai.AnyModel{}
	for _, model := range all {
		if !ai.IsModelType(model, modelType) {
			continue
		}
		if chat, ok := model.(*ai.Model); ok {
			model = runtime.bindCatalogModel(chat)
		}
		models = append(models, model)
	}
	return models, nil
}

// GetModelAuth resolves request auth for a model of any type. Auth is provider-scoped, so one credential serves chat, image and classifier models.
func (runtime *ModelRuntime) GetModelAuth(ctx context.Context, model ai.AnyModel, overrides ...ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	registry := runtime.services.Registry()
	if chat, ok := model.(*ai.Model); ok {
		return registry.ResolveRegistryModelAuth(ctx, chat, overrides...)
	}
	return registry.ResolveRegistryTypedModelAuth(ctx, model, overrides...)
}

// GenerateImages generates images through the owning provider with auth resolved like a chat request. It never returns an error: an unknown provider, unconfigured auth, cancellation and a provider without image generation become an error or aborted result.
func (runtime *ModelRuntime) GenerateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages {
	var opts ai.ModelsImagesOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := runtime.generateImages(ctx, model, request, opts)
	if err != nil {
		return ai.ImageErrorResult(model, err, ctx.Err() != nil)
	}
	return result
}

func (runtime *ModelRuntime) generateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ModelsImagesOptions) (ai.AssistantImages, error) {
	if err := context.Cause(ctx); err != nil {
		return ai.AssistantImages{}, err
	}
	provider := runtime.GetProvider(model.Provider)
	if provider == nil {
		return ai.AssistantImages{}, ai.NewModelsError(ai.ModelsErrorProvider, "Unknown provider: "+model.Provider, nil)
	}
	overrides := ai.AuthResolutionOverrides{Env: options.Env}
	if options.APIKey != "" || options.APIKeySet {
		overrides.APIKey = new(options.APIKey)
	}
	resolution, err := runtime.GetModelAuth(ctx, model, overrides)
	if err != nil {
		return ai.AssistantImages{}, err
	}
	if resolution == nil {
		return ai.AssistantImages{}, ai.NewModelsError(ai.ModelsErrorAuth, "Provider is not configured: "+model.Provider, nil)
	}
	if provider.GenerateImages == nil {
		return ai.AssistantImages{}, ai.NewModelsError(ai.ModelsErrorProvider, "Provider "+model.Provider+" does not support image generation", nil)
	}
	headers := ai.MergeProviderHeaders(resolution.Auth.Headers, options.Headers)
	if options.TransformHeaders != nil {
		transformed, err := options.TransformHeaders(ctx, headers)
		if err != nil {
			return ai.AssistantImages{}, err
		}
		headers = transformed
	}
	requestModel := model
	if resolution.Auth.BaseURL != "" {
		requestModel = new(*model)
		requestModel.BaseURL = resolution.Auth.BaseURL
	}
	requestOptions := options.ImagesOptions
	requestOptions.APIKey, requestOptions.Headers = options.APIKey, headers
	if options.APIKey == "" && !options.APIKeySet {
		requestOptions.APIKey = resolution.Auth.APIKey
	}
	if len(resolution.Env) > 0 || len(options.Env) > 0 {
		requestOptions.Env = make(map[string]string, len(resolution.Env)+len(options.Env))
		maps.Copy(requestOptions.Env, resolution.Env)
		maps.Copy(requestOptions.Env, options.Env)
	}
	return provider.GenerateImages(ctx, requestModel, request, requestOptions)
}

// assertChat rejects a model that is not a chat model before any provider lookup.
func assertChat(model *ai.Model) error {
	if model == nil {
		return nil
	}
	return ai.AssertChatModel(model)
}

// failedStream returns a stream that ends with an error result, as a lazy stream does when its setup rejects (model-runtime.ts stream: assertChatModel inside lazyStream). lazy.ts reports every setup rejection with reason "error", even under a cancelled signal.
func (runtime *ModelRuntime) failedStream(model *ai.Model, err error) *ai.AssistantMessageEventStream {
	outer := ai.NewAssistantMessageEventStream()
	// upstream: packages/ai/src/api/lazy.ts:lazyStream pushes reason "error" for a setup rejection.
	runtime.fail(context.Background(), outer, model, err)
	return outer
}

// builtinStream is the stream function of a built-in or legacy-registered provider: it builds the model's API provider from its catalog entry.
func (runtime *ModelRuntime) builtinStream(id string) ai.ModelsStreamFunction {
	return func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		entry := icodingagent.NativeModelEntry(model)
		provider, err := buildProviderForEntry(id, model.ID, model.ProviderMeta.API, entry, runtime.services, options.APIKey, true)
		if err != nil {
			return nil, err
		}
		options.IsReasoning = model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != ""
		return provider.Stream(ctx, transcript, options)
	}
}
