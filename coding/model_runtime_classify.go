package coding

// Ports the classifier operation of packages/coding-agent/src/core/model-runtime.ts (classify) and packages/coding-agent/src/core/model-registry.ts (classify).

import (
	"context"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
)

// Classify classifies structured state through the owning provider with auth resolved like a chat request. It never returns an error: a model that is not a classifier, an unknown provider, unconfigured auth, cancellation and a provider without classification become an error or aborted result.
func (runtime *ModelRuntime) Classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ...ai.ModelsClassifierOptions) ai.ClassifierResult {
	var opts ai.ModelsClassifierOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := runtime.classify(ctx, model, request, opts)
	if err != nil {
		return ai.ClassifierErrorResult(model, err, ctx.Err() != nil)
	}
	return result
}

func (runtime *ModelRuntime) classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ModelsClassifierOptions) (ai.ClassifierResult, error) {
	if err := ai.AssertClassifierModel(model); err != nil {
		return ai.ClassifierResult{}, err
	}
	if err := ai.AssertClassifierInputSupported(model, request); err != nil {
		return ai.ClassifierResult{}, err
	}
	if err := context.Cause(ctx); err != nil {
		return ai.ClassifierResult{}, err
	}
	provider := runtime.GetProvider(model.Provider)
	if provider == nil {
		return ai.ClassifierResult{}, ai.NewModelsError(ai.ModelsErrorProvider, "Unknown provider: "+model.Provider, nil)
	}
	overrides := ai.AuthResolutionOverrides{Env: options.Env}
	if options.APIKey != "" || options.APIKeySet {
		overrides.APIKey = new(options.APIKey)
	}
	resolution, err := runtime.GetModelAuth(ctx, model, overrides)
	if err != nil {
		return ai.ClassifierResult{}, err
	}
	if resolution == nil {
		return ai.ClassifierResult{}, ai.NewModelsError(ai.ModelsErrorAuth, "Provider is not configured: "+model.Provider, nil)
	}
	if provider.Classify == nil {
		return ai.ClassifierResult{}, ai.NewModelsError(ai.ModelsErrorProvider, "Provider "+model.Provider+" does not support classification", nil)
	}
	headers := ai.MergeProviderHeaders(resolution.Auth.Headers, options.Headers)
	if options.TransformHeaders != nil {
		transformed, err := options.TransformHeaders(ctx, headers)
		if err != nil {
			return ai.ClassifierResult{}, err
		}
		headers = transformed
	}
	requestModel := model
	if resolution.Auth.BaseURL != "" {
		requestModel = new(*model)
		requestModel.BaseURL = resolution.Auth.BaseURL
	}
	requestOptions := options.ClassifierOptions
	requestOptions.APIKey, requestOptions.Headers = options.APIKey, headers
	if options.APIKey == "" && !options.APIKeySet {
		requestOptions.APIKey = resolution.Auth.APIKey
	}
	if len(resolution.Env) > 0 || len(options.Env) > 0 {
		requestOptions.Env = make(ai.ProviderEnv, len(resolution.Env)+len(options.Env))
		maps.Copy(requestOptions.Env, resolution.Env)
		maps.Copy(requestOptions.Env, options.Env)
	}
	return provider.Classify(ctx, requestModel, request, requestOptions)
}

// Classify classifies structured state with request-time authentication. It never fails: failures are error or aborted results (model-registry.ts classify).
func (registry *ModelRegistry) Classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ...ai.ModelsClassifierOptions) ai.ClassifierResult {
	return registry.runtime.Classify(ctx, model, request, options...)
}

// GenerateImages generates images with request-time authentication. It never fails: failures are error or aborted results (model-registry.ts generateImages).
func (registry *ModelRegistry) GenerateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages {
	return registry.runtime.GenerateImages(ctx, model, request, options...)
}
