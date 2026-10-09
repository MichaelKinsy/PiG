package coding

// Ports the provider, registration, credential and auth members of packages/coding-agent/src/core/model-runtime.ts (getProviders, getAllAvailable, getAuth, getCompatibilityRequestConfig, getRegisteredNativeProvider, getRegisteredProviderConfig, getRegisteredProviderIds, listCredentials, setRuntimeApiKey, removeRuntimeApiKey, unregisterProvider) and ModelRegistry.isUsingOAuth of model-registry.ts.

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// CompatibilityRequestConfig is the request configuration a model falls back to when its provider auth is unconfigured.
type CompatibilityRequestConfig = icodingagent.CompatibilityRequestConfig

// GetProviders lists every composed provider in provider order.
func (runtime *ModelRuntime) GetProviders() []*ai.ModelsProvider {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	return runtime.services.Registry().GetTypedProviders(runtime.builtinStream)
}

// GetAllAvailable lists the models of every type whose provider has working credentials, for one provider or all. Chat models are the same bound models GetAvailable returns.
func (runtime *ModelRuntime) GetAllAvailable(ctx context.Context, providerID ...string) ([]ai.AnyModel, error) {
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	id := ""
	if len(providerID) > 0 {
		id = providerID[0]
	}
	models, err := runtime.services.Registry().GetAvailableAllModelDataContext(ctx, id)
	if err != nil {
		return nil, err
	}
	models = slices.Clone(models)
	for i, model := range models {
		if chat, ok := model.(*ai.Model); ok {
			models[i] = runtime.bindCatalogModel(chat)
		}
	}
	return models, nil
}

// GetAuth resolves the auth of one provider. GetModelAuth is Pi's getAuth(model) overload.
func (runtime *ModelRuntime) GetAuth(ctx context.Context, providerID string, overrides ...ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	return runtime.services.Registry().ResolveRegistryProviderAuth(ctx, providerID, overrides...)
}

// GetCompatibilityRequestConfig resolves the configured headers and authHeader flag of a model. A header that cannot be resolved is an error.
func (runtime *ModelRuntime) GetCompatibilityRequestConfig(model *ai.Model) (CompatibilityRequestConfig, error) {
	if runtime == nil || runtime.services == nil {
		return CompatibilityRequestConfig{}, ErrNoServices
	}
	return runtime.services.Registry().GetCompatibilityRequestConfig(model)
}

// GetRegisteredNativeProvider returns the original native registration of a provider, or nil.
func (runtime *ModelRuntime) GetRegisteredNativeProvider(providerID string) *ai.ModelsProvider {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	return runtime.services.Registry().GetRegisteredNativeProvider(providerID)
}

// GetRegisteredProviderConfig returns the active provider registration input, or nil.
func (runtime *ModelRuntime) GetRegisteredProviderConfig(providerID string) *ProviderConfigInput {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	return runtime.services.Registry().GetRegisteredProviderConfig(providerID)
}

// GetRegisteredProviderIds lists the IDs of the providers registered through RegisterProvider or RegisterNativeProvider.
func (runtime *ModelRuntime) GetRegisteredProviderIds() []string {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	return runtime.services.Registry().GetRegisteredProviderIDs()
}

// ListCredentials enumerates the runtime overlay and shared credential storage.
func (runtime *ModelRuntime) ListCredentials(ctx context.Context) ([]ai.CredentialInfo, error) {
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	return runtime.services.Registry().ListCredentials(ctx)
}

// SetRuntimeApiKey installs a non-persistent API key for a provider, serialized with the provider's other credential operations and followed by local synchronization. A synchronization failure returns CredentialSynchronizationError.
func (runtime *ModelRuntime) SetRuntimeApiKey(ctx context.Context, providerID, apiKey string) error {
	if runtime == nil || runtime.services == nil {
		return ErrNoServices
	}
	return runtime.services.Registry().SetRuntimeAPIKeyOperation(ctx, providerID, apiKey)
}

// RemoveRuntimeApiKey drops the non-persistent API key of a provider, serialized with the provider's other credential operations and followed by local synchronization.
func (runtime *ModelRuntime) RemoveRuntimeApiKey(ctx context.Context, providerID string) error {
	if runtime == nil || runtime.services == nil {
		return ErrNoServices
	}
	return runtime.services.Registry().RemoveRuntimeAPIKeyOperation(ctx, providerID)
}

// UnregisterProvider removes a provider registration, native or legacy, and queues the local refresh. An unknown ID is not an error.
func (runtime *ModelRuntime) UnregisterProvider(providerID string) {
	if runtime == nil || runtime.services == nil {
		return
	}
	runtime.services.Registry().UnregisterProvider(providerID)
}
