package coding

// Ports packages/coding-agent/src/core/model-registry.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// NewModelRegistry is the synchronous facade over runtime's registry (model-registry.ts constructor(runtime)). Services builds the one facade of its runtime this way.
func NewModelRegistry(runtime *ModelRuntime) *ModelRegistry {
	return &ModelRegistry{ModelRegistry: runtime.services.registry.ModelRegistry, runtime: runtime}
}

// The Session's registry is what an extension reaches through ctx.modelRegistry.
var _ extension.ModelRegistry = (*ModelRegistry)(nil)

// Refresh reloads models.json and the provider catalogs and returns the outcome (model-registry.ts:56 refresh, which hands it to runtime.refresh). It hides the embedded registry's Refresh(), which reloads synchronously without options.
func (registry *ModelRegistry) Refresh(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return registry.runtime.Refresh(ctx, options)
}

// GetAll returns the full model catalog without resolving request credentials (model-registry.ts getAll). Each model carries its cost through Capabilities (ai.Model.CostRates).
func (registry *ModelRegistry) GetAll() []*ai.Model { return registry.runtime.GetModels() }

// GetAvailable returns the models of providers with configured auth from the last published availability, without credential or catalog I/O (model-registry.ts getAvailable).
func (registry *ModelRegistry) GetAvailable() []*ai.Model {
	return registry.runtime.GetAvailableSnapshot()
}

// HasConfiguredAuth reports whether the model's provider has configured auth (model-registry.ts hasConfiguredAuth(model)).
func (registry *ModelRegistry) HasConfiguredAuth(model *ai.Model) bool {
	return registry.runtime.HasConfiguredAuth(model.ProviderID())
}

// GetError returns the current model configuration diagnostic, or an empty string.
func (registry *ModelRegistry) GetError() string { return registry.LoadError() }

// RegisterProvider registers a provider by name with its core configuration (model-registry.ts registerProvider(providerName, config)). ModelRuntime.RegisterNativeProvider is its Provider overload, and RegisterExtensionProvider (promoted from the internal registry) takes the extension host's distinct Go registration payload.
func (registry *ModelRegistry) RegisterProvider(name string, config ProviderConfigInput) error {
	return registry.runtime.RegisterProvider(name, config)
}

// RegisterProviderObject registers a Provider object (model-registry.ts:210 registerProvider(provider: Provider), which hands it to runtime.registerNativeProvider at :218). Go names the overload apart from RegisterProvider(name, config); the promoted RegisterNativeProvider takes the extension host's payload.
func (registry *ModelRegistry) RegisterProviderObject(provider *ai.ModelsProvider) error {
	return registry.runtime.RegisterNativeProvider(provider)
}

// ResolvedRequestAuth is the compatibility result for a model's request credentials (model-registry.ts:33).
type ResolvedRequestAuth = extension.ResolvedRequestAuth

// GetAPIKeyAndHeaders returns compatibility headers even for an unconfigured provider.
func (registry *ModelRegistry) GetAPIKeyAndHeaders(ctx context.Context, model *ai.Model) ResolvedRequestAuth {
	resolution, err := registry.ResolveCompatibilityModelAuth(ctx, model)
	if err != nil {
		return ResolvedRequestAuth{Error: err.Error()}
	}
	result := ResolvedRequestAuth{OK: true, Headers: resolution.Auth.Headers, BaseURL: resolution.Auth.BaseURL, Env: resolution.Env}
	if resolution.Auth.APIKey != "" {
		result.APIKey = new(resolution.Auth.APIKey)
	}
	return result
}

// IsUsingOAuth reports whether the model's provider is authenticated with OAuth in the latest published availability snapshot (model-registry.ts isUsingOAuth).
func (registry *ModelRegistry) IsUsingOAuth(model *ai.Model) bool {
	return registry.runtime.IsUsingOAuth(model.ProviderID())
}

// GetProviderAuth resolves current provider authentication without caching configuration commands.
func (registry *ModelRegistry) GetProviderAuth(ctx context.Context, id string) (*ai.AuthResult, error) {
	return registry.ResolveRegistryProviderAuth(ctx, id)
}

// GetAPIKeyForProvider returns nil when authentication is absent or fails, as the compatibility facade specifies.
func (registry *ModelRegistry) GetAPIKeyForProvider(ctx context.Context, id string) *string {
	auth, err := registry.GetProviderAuth(ctx, id)
	if err != nil || auth == nil || auth.Auth.APIKey == "" {
		return nil
	}
	return new(auth.Auth.APIKey)
}

// FindOfType finds a model of a non-chat type, for example FindOfType(ai.ModelTypeClassifier, "typesafe", "jev-latest") (model-registry.ts findOfType).
func (registry *ModelRegistry) FindOfType(modelType ai.ModelType, provider, modelID string) ai.AnyModel {
	return registry.runtime.GetModelOfType(modelType, provider, modelID)
}

// GetModelsOfType lists every known model of a type, optionally for one provider (model-registry.ts getModelsOfType).
func (registry *ModelRegistry) GetModelsOfType(modelType ai.ModelType, provider ...string) []ai.AnyModel {
	return registry.runtime.GetModelsOfType(modelType, provider...)
}

// GetAvailableOfType lists the models of a type whose provider has working credentials (model-registry.ts getAvailableOfType).
func (registry *ModelRegistry) GetAvailableOfType(ctx context.Context, modelType ai.ModelType, provider ...string) ([]ai.AnyModel, error) {
	return registry.runtime.GetAvailableOfType(ctx, modelType, provider...)
}

// GetModelOfType looks up a model of a type by provider and ID (model-registry.ts getModelOfType).
func (registry *ModelRegistry) GetModelOfType(modelType ai.ModelType, provider, modelID string) ai.AnyModel {
	return registry.runtime.GetModelOfType(modelType, provider, modelID)
}
