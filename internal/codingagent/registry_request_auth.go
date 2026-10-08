package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts and packages/coding-agent/src/core/provider-composer.ts.
// Ports packages/coding-agent/src/core/model-registry.ts.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func (r *ModelRegistry) registryAuthConfig(id string) (ai.ProviderAuth, providerConfig, bool) {
	base, baseErr := ai.BuiltinProviderAuth(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if radius := r.radiusProviderLocked(id); radius != nil {
		base = ai.RadiusProviderAuth(radius)
		baseErr = nil
	}
	config, configured := r.effectiveProviderConfigLocked(id)
	if !configured {
		return base, config, baseErr == nil
	}
	// An extension registration's oauth is the provider's account login (`extension?.oauth ?? base.auth.oauth`); the bridge
	// publishes its flow in the OAuth provider registry, so the registration inherits that flow when the catalog has none.
	if base.OAuth == nil && config.OAuth != nil && config.OAuth.HasLogin {
		base.OAuth, _ = ai.OAuthProviderAuth(id)
	}
	return ai.ProviderAuth{APIKey: composeAPIKeyAuth(id, base, config), OAuth: composeOAuthAuth(id, base.OAuth, config)}, config, true
}

// effectiveProviderConfigLocked composes the models.json provider configuration
// with the extension registration that overrides it. Each field comes from the
// registration when that registration defines one, else from models.json, as
// upstream provider-composer.ts composes a provider's configuration and its
// extension registration (configuredApiKey, configuredHeaders,
// extension?.authHeader ?? config?.authHeader, extension?.oauth ??
// base.auth.oauth). A registration that defines neither an apiKey nor an oauth
// therefore leaves the models.json provider configuration in effect. The second
// result reports whether either layer names the provider. The caller holds r.mu.
func (r *ModelRegistry) effectiveProviderConfigLocked(id string) (providerConfig, bool) {
	config, configured := providerConfig{}, false
	if r.config != nil {
		config, configured = r.config.Providers[id]
	}
	dynamic, registered := r.dynamic[id]
	if !registered {
		return config, configured
	}
	if dynamic.APIKey != "" {
		config.APIKey = dynamic.APIKey
	}
	if dynamic.AuthHeader != nil {
		config.AuthHeader = dynamic.AuthHeader
	}
	if dynamic.OAuth != nil {
		config.OAuth = dynamic.OAuth
	}
	config.Headers = maps.Clone(config.Headers)
	if config.Headers == nil {
		config.Headers = map[string]*string{}
	}
	maps.Copy(config.Headers, dynamic.Headers)
	config.headerEntries = overlayHeaders(orderedHeaders(config.Headers, config.headerEntries), orderedHeaders(dynamic.Headers, dynamic.headerEntries))
	return config, true
}

// effectiveAPIKeyFromRegistration reports whether the extension registration, not
// models.json, supplies the effective API key. Upstream reports that value's
// source as "fallback" rather than "models_json_key" in the same case
// (provider-composer.ts:configuredRequestAuthStatus). The caller holds r.mu.
func (r *ModelRegistry) effectiveAPIKeyFromRegistration(id string) bool {
	return r.dynamic[id].APIKey != ""
}

// CheckRegistryAuth checks provider configuration without resolving command-backed fallback keys.
func (r *ModelRegistry) CheckRegistryAuth(ctx context.Context, id string) (*ai.AuthCheck, error) {
	if r.GetProvider(id) != nil {
		return r.NativeModels().CheckAuth(ctx, id)
	}
	if provider := r.NativeProvider(id); provider != nil {
		credential, err := (registryCredentials{r}).Read(ctx, id)
		if err != nil {
			return nil, err
		}
		return provider.CheckAuth(ctx, credential)
	}
	auth, _, exists := r.registryAuthConfig(id)
	if !exists {
		return nil, nil
	}
	return ai.CheckProviderAuth(ctx, id, auth, registryCredentials{r}, ai.DefaultProviderAuthContext())
}

// ResolveRegistryProviderAuth resolves provider credentials only at the request boundary.
func (r *ModelRegistry) ResolveRegistryProviderAuth(ctx context.Context, id string, overrides ...ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	if r.GetProvider(id) != nil {
		return r.NativeModels().GetAuth(ctx, id, overrides...)
	}
	var options ai.AuthResolutionOverrides
	if len(overrides) > 0 {
		options = overrides[0]
	}
	auth, _, exists := r.registryAuthConfig(id)
	if !exists {
		return nil, nil
	}
	return ai.ResolveProviderAuth(ctx, id, auth, registryCredentials{r}, ai.DefaultProviderAuthContext(), options)
}

func (r *ModelRegistry) configuredModelHeaders(model *ai.Model) []orderedHeaderEntry {
	input := r.GetRegisteredProviderConfig(model.ProviderMeta.ProviderID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var configured, dynamic providerConfig
	if r.config != nil {
		configured = r.config.Providers[model.ProviderMeta.ProviderID]
	}
	dynamic = r.dynamic[model.ProviderMeta.ProviderID]
	override := configured.ModelOverrides[model.ID]
	headers := orderedHeaders(override.Headers, override.headerEntries)
	if definition, ok := findModelDefinition(configured.Models, model.ID); ok {
		headers = overlayHeaders(headers, orderedHeaders(definition.Headers, definition.headerEntries))
	}
	if definition, ok := findModelDefinition(dynamic.Models, model.ID); ok {
		headers = overlayHeaders(headers, orderedHeaders(definition.Headers, definition.headerEntries))
	}
	if input != nil {
		for _, entry := range input.Models {
			if definition, ok := entry.(*ai.Model); ok && ai.IsModelType(definition, ai.ModelTypeChat) && definition.ID == model.ID {
				values := make(map[string]*string, len(definition.ProviderMeta.Headers))
				for name, value := range definition.ProviderMeta.Headers {
					values[name] = new(value)
				}
				headers = overlayHeaders(headers, orderedHeaders(values, nil))
				break
			}
		}
	}
	return headers
}

// ResolveRegistryModelAuth preserves nullable provider headers and resolves model headers after authentication.
func (r *ModelRegistry) ResolveRegistryModelAuth(ctx context.Context, model *ai.Model, overrides ...ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	result, err := r.ResolveRegistryProviderAuth(ctx, model.ProviderMeta.ProviderID, overrides...)
	if err != nil || result == nil {
		return result, err
	}
	copy := new(*result)
	headerEnv := maps.Clone(result.Env)
	if len(overrides) > 0 && len(overrides[0].Env) > 0 {
		if headerEnv == nil {
			headerEnv = make(map[string]string)
		}
		maps.Copy(headerEnv, overrides[0].Env)
	}
	configuredHeaders := r.configuredModelHeaders(model)
	headers, err := resolveHeadersOrError(configuredHeaders, fmt.Sprintf(`model "%s/%s"`, model.ProviderMeta.ProviderID, model.ID), headerEnv)
	if err != nil {
		return nil, err
	}
	copy.Auth.Headers = ai.MergeProviderHeaders(copy.Auth.Headers, ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers))
	copy.Auth.Headers = mergeConfiguredHeaders(copy.Auth.Headers, configuredHeaders, headers)
	return copy, nil
}

// ResolveCompatibilityModelAuth returns request credentials or unconfigured compatibility headers, preserving nullable header suppression and the facade's error messages.
func (r *ModelRegistry) ResolveCompatibilityModelAuth(ctx context.Context, model *ai.Model) (*ai.AuthResult, error) {
	resolution, err := r.ResolveRegistryModelAuth(ctx, model)
	if err == nil && resolution == nil {
		headers, authHeader, configErr := r.CompatibilityRequestHeaders(model)
		switch {
		case configErr != nil:
			err = configErr
		case authHeader:
			err = fmt.Errorf("authHeader requires a resolved API key")
		default:
			return &ai.AuthResult{Auth: ai.ModelAuth{Headers: headers}}, nil
		}
	}
	if err != nil {
		if cause := errors.Unwrap(err); cause != nil {
			err = cause
		}
		if err.Error() == "authHeader requires a resolved API key" {
			err = fmt.Errorf(`No API key found for "%s"`, model.ProviderMeta.ProviderID)
		}
		return nil, err
	}
	return resolution, nil
}

// ProviderInsecure returns the configured transport flag without resolving credentials or headers.
func (r *ModelRegistry) ProviderInsecure(providerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if dynamic, ok := r.dynamic[providerID]; ok {
		return dynamic.Insecure
	}
	if r.config != nil {
		return r.config.Providers[providerID].Insecure
	}
	return false
}

// RequestAuthHeaders folds configured HTTP header names in declaration order while compatibility auth results retain their original keys and null values.
func (r *ModelRegistry) RequestAuthHeaders(model *ai.Model, headers ai.ProviderHeaders) ai.ProviderHeaders {
	_, config, _ := r.registryAuthConfig(model.ProviderMeta.ProviderID)
	order := orderedHeaders(config.Headers, config.headerEntries)
	order = append(order, r.configuredModelHeaders(model)...)
	return mergeConfiguredHeaders(headers, order, headers)
}

func mergeConfiguredHeaders(base ai.ProviderHeaders, order []orderedHeaderEntry, values ai.ProviderHeaders) ai.ProviderHeaders {
	if base == nil && len(values) == 0 {
		return nil
	}
	result := maps.Clone(base)
	if result == nil {
		result = make(ai.ProviderHeaders, len(values))
	}
	for _, entry := range order {
		value, exists := values[entry.Name]
		if !exists {
			continue
		}
		for name := range result {
			if strings.EqualFold(name, entry.Name) {
				delete(result, name)
			}
		}
		result[entry.Name] = value
	}
	return result
}

// CompatibilityRequestHeaders resolves configured headers even when no API key is configured.
func (r *ModelRegistry) CompatibilityRequestHeaders(model *ai.Model) (ai.ProviderHeaders, bool, error) {
	_, config, _ := r.registryAuthConfig(model.ProviderMeta.ProviderID)
	headers := overlayHeaders(orderedHeaders(config.Headers, config.headerEntries), r.configuredModelHeaders(model))
	resolved, err := resolveHeadersOrError(headers, fmt.Sprintf(`model "%s/%s"`, model.ProviderMeta.ProviderID, model.ID), nil)
	return mergeConfiguredHeaders(ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers), headers, resolved), authHeaderEnabled(config), err
}
