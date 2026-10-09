package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts (getProviders, getCompatibilityRequestConfig, setRuntimeApiKey, removeRuntimeApiKey) over this registry.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// CompatibilityRequestConfig is the request configuration a model falls back to when its provider auth is unconfigured (provider-composer.ts CompatibilityRequestConfig).
type CompatibilityRequestConfig struct {
	// Headers is nil when neither the model nor the provider configuration defines a header.
	Headers ai.ProviderHeaders
	// AuthHeader reports that the provider requires a resolved API key in an Authorization header.
	AuthHeader bool
}

// GetCompatibilityRequestConfig resolves the configured request headers and the authHeader flag of a model. A header value that cannot be resolved is an error (resolveHeadersOrThrow).
func (r *ModelRegistry) GetCompatibilityRequestConfig(model *ai.Model) (CompatibilityRequestConfig, error) {
	headers, authHeader, err := r.CompatibilityRequestHeaders(model)
	return CompatibilityRequestConfig{Headers: headers, AuthHeader: authHeader}, err
}

// GetTypedProviders lists every composed provider in provider order, each streaming through the fallback of its ID (models.ts getProviders). A provider that fails to compose is omitted.
func (r *ModelRegistry) GetTypedProviders(fallback func(id string) ai.ModelsStreamFunction) []*ai.ModelsProvider {
	var providers []*ai.ModelsProvider
	for _, id := range r.modelProviderIDs() {
		var stream ai.ModelsStreamFunction
		if fallback != nil {
			stream = fallback(id)
		}
		if provider := r.GetTypedProvider(id, stream); provider != nil {
			providers = append(providers, provider)
		}
	}
	return providers
}

// SetRuntimeAPIKeyOperation installs the non-persistent key like SetRuntimeAPIKey, serialized with the other credential operations of the provider and followed by local synchronization (model-runtime.ts setRuntimeApiKey). A synchronization failure returns CredentialSynchronizationError after the key is installed.
func (r *ModelRegistry) SetRuntimeAPIKeyOperation(ctx context.Context, providerID, apiKey string) error {
	synced := false
	defer r.coverCredentialOperation(ctx, providerID, &synced)()
	release, err := r.beginCredentialOperation(ctx, providerID)
	if err != nil {
		return err
	}
	defer release()
	r.SetRuntimeAPIKey(providerID, apiKey)
	synced = true
	return r.synchronizeCredentialState(ctx, providerID, CredentialSynchronizationSetRuntimeAPIKey, &ai.Credential{Type: ai.CredentialAPIKey, Key: apiKey})
}

// RemoveRuntimeAPIKeyOperation drops the non-persistent key like RemoveRuntimeAPIKey, serialized with the other credential operations of the provider and followed by local synchronization (model-runtime.ts removeRuntimeApiKey).
func (r *ModelRegistry) RemoveRuntimeAPIKeyOperation(ctx context.Context, providerID string) error {
	synced := false
	defer r.coverCredentialOperation(ctx, providerID, &synced)()
	release, err := r.beginCredentialOperation(ctx, providerID)
	if err != nil {
		return err
	}
	defer release()
	r.RemoveRuntimeAPIKey(providerID)
	synced = true
	return r.synchronizeCredentialState(ctx, providerID, CredentialSynchronizationRemoveRuntimeAPIKey, nil)
}
