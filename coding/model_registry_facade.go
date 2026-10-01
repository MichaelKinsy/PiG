package coding

// Ports packages/coding-agent/src/core/model-registry.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// GetAll returns the full model catalog without resolving request credentials.
func (registry *ModelRegistry) GetAll() []icodingagent.ModelEntry {
	var entries []icodingagent.ModelEntry
	for _, model := range registry.GetAllModelData() {
		entries = append(entries, icodingagent.NativeModelEntry(model))
	}
	return entries
}

// GetError returns the current model configuration diagnostic, or an empty string.
func (registry *ModelRegistry) GetError() string { return registry.LoadError() }

// RegisterProviderConfig registers the core provider input; RegisterProvider retains the extension host's distinct Go registration payload.
func (registry *ModelRegistry) RegisterProviderConfig(id string, config ProviderConfigInput) error {
	return registry.runtime.RegisterProvider(id, config)
}

// ResolvedRequestAuth is the compatibility result for a model's request credentials.
type ResolvedRequestAuth struct {
	OK      bool               `json:"ok"`
	APIKey  *string            `json:"apiKey,omitempty"`
	Headers ai.ProviderHeaders `json:"headers,omitempty"`
	BaseURL string             `json:"baseUrl,omitempty"`
	Env     map[string]string  `json:"env,omitempty"`
	Error   string             `json:"error,omitempty"`
}

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
