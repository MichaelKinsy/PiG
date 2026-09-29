package coding

// Ports packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// nativeModelProvider keeps the selected model identity while the registration
// owns its connection and callbacks. Close does not close another model's owner.
type nativeModelProvider struct {
	native   *extension.NativeProvider
	model    *ai.Model
	registry *icodingagent.ModelRegistry
	prepared bool
	simple   bool
}

func (p *nativeModelProvider) ID() string   { return p.native.ID }
func (p *nativeModelProvider) Close() error { return nil }
func (p *nativeModelProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if !p.prepared {
		current := p.registry.NativeProvider(p.native.ID)
		if current == nil {
			return nil, fmt.Errorf("native provider %s is no longer registered", p.native.ID)
		}
		_, provider, prepared, err := prepareNativeRequest(ctx, p.model, options, current, p.registry)
		if err != nil {
			return nil, err
		}
		provider.(*nativeModelProvider).simple = p.simple
		return provider.Stream(ctx, transcript, prepared)
	}
	return p.native.Stream(ctx, p.model, transcript, options, p.simple)
}

func (runtime *ModelRuntime) prepareNativeRequest(ctx context.Context, model *ai.Model, options ai.StreamOptions, p *extension.NativeProvider) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
	return prepareNativeRequest(ctx, model, options, p, runtime.services.Registry().ModelRegistry)
}

// BuildNativeModel binds a registered model to the same authenticated native
// callback path used by ModelRuntime. The CLI's model selection uses this too.
func BuildNativeModel(registry *icodingagent.ModelRegistry, providerID, modelID string) *ai.Model {
	native := registry.NativeProvider(providerID)
	if native == nil {
		return nil
	}
	entry, ok := registry.Resolve(providerID, modelID)
	if !ok {
		return nil
	}
	model := modelFromEntry(entry, nil)
	model.ProviderMeta.Headers = maps.Clone(entry.Headers)
	model.Provider = &nativeModelProvider{native: native, model: model, registry: registry, simple: true}
	return model
}

func prepareNativeRequest(ctx context.Context, model *ai.Model, options ai.StreamOptions, p *extension.NativeProvider, registry *icodingagent.ModelRegistry) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
	overrides := ai.AuthResolutionOverrides{Env: options.Env}
	if options.APIKey != "" {
		overrides.APIKey = &options.APIKey
	}
	resolution, err := registry.NativeProviderAuth(ctx, p.ID, overrides)
	if err != nil {
		return nil, nil, options, err
	}
	if resolution == nil {
		return nil, nil, options, fmt.Errorf("provider is not configured: %s", p.ID)
	}
	requestModel := *model
	requestModel.ProviderMeta = model.ProviderMeta
	if resolution.Auth.BaseURL != "" {
		requestModel.ProviderMeta.BaseURL = resolution.Auth.BaseURL
	}
	prepared := options
	prepared.APIKey = resolution.Auth.APIKey
	prepared.ModelCost = model.CostRates()
	prepared.Headers = ai.MergeProviderHeaders(resolution.Auth.Headers, options.Headers)
	prepared.Env = maps.Clone(resolution.Env)
	if prepared.Env == nil && options.Env != nil {
		prepared.Env = ai.ProviderEnv{}
	}
	maps.Copy(prepared.Env, options.Env)
	if options.TransformHeaders != nil {
		prepared.Headers, err = options.TransformHeaders(ctx, prepared.Headers)
		if err != nil {
			return nil, nil, options, err
		}
	}
	prepared.TransformHeaders = nil
	provider := &nativeModelProvider{native: p, model: &requestModel, prepared: true, registry: registry}
	requestModel.Provider = provider
	return &requestModel, provider, prepared, nil
}
