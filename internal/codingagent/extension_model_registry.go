package codingagent

// Ports packages/coding-agent/src/core/model-registry.ts
// Ports packages/coding-agent/src/core/model-runtime.ts

// The state and operations behind an extension's ctx.modelRegistry. Upstream
// hands extensions its ModelRegistry facade (packages/coding-agent/src/core/
// model-registry.ts) over the session's ModelRuntime (model-runtime.ts). PiG's
// extensions run beside the host, so the host sends the registry snapshot
// the facade's synchronous reads answer from, and answers the asynchronous
// ones on request.

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

// testFauxProviderID is the scripted provider the parity harness registers
// from an extension under Pi (test/parity/testdata/test-faux-provider.ts, apiKey
// "unused") and PiG builds in under PIG_TEST_FAUX.
const testFauxProviderID = "test-faux"

func testFauxProvider(providerID string) bool {
	return providerID == testFauxProviderID && os.Getenv("PIG_TEST_FAUX") == "1"
}

// ExtensionModelRegistryState returns the registry snapshot for extension
// processes: the catalog under "models", each provider's display name, base
// URL, auth status, configured auth and OAuth use under "providers", and the
// registry error under "error". Upstream ModelRuntime keeps the same facts in
// its snapshot (all models, configured providers, auth checks) and answers
// getAll, getAvailable, hasConfiguredAuth, getProviderAuthStatus, getProvider,
// getProviderDisplayName, isUsingOAuth and getError from them.
func ExtensionModelRegistryState(registry *ModelRegistry, catalog []*ai.Model) map[string]any {
	models := make([]map[string]any, 0, len(catalog))
	var providerIDs []string
	for _, model := range catalog {
		info := extension.ModelInfo(model)
		if info == nil {
			continue
		}
		models = append(models, info)
		providerID, _ := info["provider"].(string)
		providerIDs = append(providerIDs, providerID)
	}
	if registry != nil {
		registry.orderExtensionModels(models)
	}
	return extensionRegistryState(registry, models, providerIDs)
}

func extensionRegistryState(registry *ModelRegistry, models any, catalogProviders []string) map[string]any {
	state := map[string]any{"models": models}
	if registry == nil {
		state["providers"] = map[string]any{}
		return state
	}
	providerIDs := []string{}
	seen := map[string]bool{}
	for _, providerID := range append(catalogProviders, registry.extensionProviderIDs()...) {
		if providerID != "" && !seen[providerID] {
			seen[providerID] = true
			providerIDs = append(providerIDs, providerID)
		}
	}
	providers := make(map[string]any, len(providerIDs))
	for _, providerID := range providerIDs {
		providers[providerID] = registry.extensionProviderState(providerID)
	}
	state["providers"] = providers
	if loadError := registry.LoadError(); loadError != "" {
		state["error"] = loadError
	}
	return state
}

// orderExtensionModels puts the models of providers only an extension
// registered after every other model, in registration order and each
// provider's definition order, as upstream ModelRuntime composes providers:
// built-ins, models.json, then extension registrations.
func (r *ModelRegistry) orderExtensionModels(models []map[string]any) {
	compare := r.extensionModelOrder()
	if compare == nil {
		return
	}
	slices.SortStableFunc(models, func(a, b map[string]any) int {
		aProvider, _ := a["provider"].(string)
		aID, _ := a["id"].(string)
		bProvider, _ := b["provider"].(string)
		bID, _ := b["id"].(string)
		return compare(aProvider, aID, bProvider, bID)
	})
}

func (r *ModelRegistry) extensionModelOrder() func(string, string, string, string) int {
	r.mu.RLock()
	rank := map[string]int{}
	definition := map[string]map[string]int{}
	for index, providerID := range r.dynamicOrder {
		if r.config != nil {
			if _, configured := r.config.Providers[providerID]; configured {
				continue
			}
		}
		if len(ai.ListModels(providerID)) > 0 {
			continue
		}
		rank[providerID] = index + 1
		definition[providerID] = map[string]int{}
		for position, model := range r.dynamic[providerID].Models {
			definition[providerID][model.ID] = position
		}
	}
	r.mu.RUnlock()
	if len(rank) == 0 {
		return nil
	}
	return func(aProvider, aID, bProvider, bID string) int {
		aRank, bRank := rank[aProvider], rank[bProvider]
		if aRank != bRank {
			return aRank - bRank
		}
		return definition[aProvider][aID] - definition[bProvider][bID]
	}
}

// extensionProviderIDs lists the providers configured outside the built-in
// catalog: extension registrations, models.json providers and stored
// credentials, sorted.
func (r *ModelRegistry) extensionProviderIDs() []string {
	r.mu.RLock()
	var ids []string
	for providerID := range r.dynamic {
		ids = append(ids, providerID)
	}
	if r.config != nil {
		for providerID := range r.config.Providers {
			ids = append(ids, providerID)
		}
	}
	for providerID := range r.radius {
		ids = append(ids, providerID)
	}
	storage := r.credentialStore
	r.mu.RUnlock()
	if storage != nil {
		if stored, err := storage.List(context.Background()); err == nil {
			for _, entry := range stored {
				ids = append(ids, entry.ProviderID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (r *ModelRegistry) extensionProviderState(providerID string) map[string]any {
	status := r.ExtensionProviderAuthStatus(providerID)
	authStatus := map[string]any{"configured": status.Configured}
	if status.Source != "" {
		authStatus["source"] = string(status.Source)
	}
	if status.Label != "" {
		authStatus["label"] = status.Label
	}
	configured := r.HasConfiguredAuth(providerID) || testFauxProvider(providerID)
	state := map[string]any{
		"name":       r.GetProviderDisplayName(providerID),
		"authStatus": authStatus,
		"configured": configured,
		"usingOAuth": configured && r.usingOAuth(providerID),
	}
	r.mu.RLock()
	if native, ok := r.native[providerID]; ok {
		state["availableModelIds"] = append([]string{}, native.available...)
	}
	dynamic, hasDynamic := r.dynamic[providerID]
	var configProvider providerConfig
	hasConfig := false
	if r.config != nil {
		configProvider, hasConfig = r.config.Providers[providerID]
	}
	r.mu.RUnlock()
	// Upstream composes a provider from its built-in definition, models.json
	// and an extension registration; only an uncomposed built-in is the
	// built-in provider object itself.
	state["composed"] = hasDynamic || hasConfig
	if hasConfig {
		state["modelsConfig"] = configProvider
	}
	if hasDynamic {
		state["extensionConfig"] = dynamic
	}
	switch {
	case hasDynamic && dynamic.BaseURL != "":
		state["baseUrl"] = dynamic.BaseURL
	case hasConfig && configProvider.BaseURL != "":
		state["baseUrl"] = configProvider.BaseURL
	}
	return state
}

// ExtensionProviderAuthStatus mirrors upstream
// ModelRuntime.getProviderAuthStatus: a runtime API key, then a stored
// credential, then the API key an extension registration or models.json
// configures, then the provider's ambient credentials.
func (r *ModelRegistry) ExtensionProviderAuthStatus(providerID string) ai.AuthStatus {
	r.mu.RLock()
	native, isNative := r.native[providerID]
	r.mu.RUnlock()
	if isNative {
		if native.check == nil {
			return ai.AuthStatus{}
		}
		source := ai.AuthSourceEnvironment
		if _, ok := r.RuntimeAPIKey(providerID); ok {
			source = ai.AuthSourceRuntime
		} else if r.hasStoredCredential(providerID) {
			source = ai.AuthSourceStored
		}
		status := ai.AuthStatus{Configured: true, Source: source}
		if source == ai.AuthSourceEnvironment {
			status.Label = native.check.Source
		}
		return status
	}
	if _, ok := r.RuntimeAPIKey(providerID); ok {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceRuntime}
	}
	if r.hasStoredCredential(providerID) {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored}
	}
	if testFauxProvider(providerID) {
		// The harness registration's literal apiKey is upstream's "fallback".
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceFallback}
	}
	r.mu.RLock()
	value, fromExtension := "", false
	if dynamic, ok := r.dynamic[providerID]; ok && dynamic.APIKey != "" {
		value, fromExtension = dynamic.APIKey, true
	} else if r.config != nil {
		value = r.config.Providers[providerID].APIKey
	}
	r.mu.RUnlock()
	if value != "" {
		return configuredRequestAuthStatus(value, fromExtension)
	}
	if keys := ai.FindEnvKeys(providerID, nil); len(keys) > 0 {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: keys[0]}
	}
	return ai.AuthStatus{}
}

// configuredRequestAuthStatus mirrors upstream provider-composer.ts
// configuredRequestAuthStatus for a configured API key value. It evaluates
// "$VAR" references against the process environment only.
func configuredRequestAuthStatus(value string, fromExtension bool) ai.AuthStatus {
	if configvalue.IsCommandConfigValue(value) {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONCommand}
	}
	if names := configvalue.GetConfigValueEnvVarNames(value); len(names) > 0 {
		if configvalue.IsConfigValueConfigured(value, nil) {
			return ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: strings.Join(names, ", ")}
		}
		return ai.AuthStatus{}
	}
	if fromExtension {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceFallback}
	}
	return ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONKey}
}

// usingOAuth mirrors upstream ModelRuntime.isUsingOAuth: the provider's
// configured auth is a stored OAuth credential.
func (r *ModelRegistry) usingOAuth(providerID string) bool {
	r.mu.RLock()
	native, ok := r.native[providerID]
	r.mu.RUnlock()
	if ok {
		return native.check != nil && native.check.Type == ai.CredentialOAuth
	}
	if _, ok := r.RuntimeAPIKey(providerID); ok {
		return false
	}
	credential, ok := r.storedCredential(providerID)
	return ok && credential.Type == ai.CredentialOAuth
}

// ExtensionProviderAuth mirrors upstream ModelRuntime.getAuth(provider): the
// provider's request auth, or nil when it has none.
func (r *ModelRegistry) ExtensionProviderAuth(ctx context.Context, providerID string) (*ai.AuthResult, error) {
	if r.NativeProvider(providerID) != nil {
		return r.NativeProviderAuth(ctx, providerID, ai.AuthResolutionOverrides{})
	}
	if testFauxProvider(providerID) {
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "unused"}, Source: "configured API key"}, nil
	}
	r.mu.Lock()
	credentials := r.runtimeCredentialsLocked()
	value := ""
	if dynamic, ok := r.dynamic[providerID]; ok && dynamic.APIKey != "" {
		value = dynamic.APIKey
	} else if r.config != nil {
		value = r.config.Providers[providerID].APIKey
	}
	r.mu.Unlock()
	auth := ai.ProviderAuth{}
	if builtin, err := ai.BuiltinProviderAuth(providerID); err == nil {
		auth = builtin
	}
	if oauth, ok := ai.OAuthProviderAuth(providerID); ok && auth.OAuth == nil {
		auth.OAuth = oauth
	}
	if value != "" {
		// A configured key replaces the provider's ambient resolution, as
		// upstream composeApiKeyAuth resolves rawKey ahead of the inherited
		// method when nothing is stored.
		env := r.providerEnv(providerID)
		auth.APIKey = &ai.APIKeyAuth{Name: "API key", Resolve: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			if input.Credential != nil && input.Credential.Key != "" {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Env: input.Credential.Env, Source: "stored credential"}, nil
			}
			key := configvalue.Resolve(value, env)
			if key == "" {
				return nil, nil
			}
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key}, Source: "configured API key"}, nil
		}}
	} else if auth.APIKey == nil {
		auth.APIKey = ai.EnvAPIKeyAuth("API key")
	}
	return ai.ResolveProviderAuth(ctx, providerID, auth, credentials, ai.DefaultProviderAuthContext(), ai.AuthResolutionOverrides{})
}

// ExtensionRefresh mirrors upstream ModelRuntime.refresh: reload models.json,
// then refresh the provider catalogs, from the network only when allowed.
func (r *ModelRegistry) ExtensionRefresh(ctx context.Context, allowNetwork *bool, providers []string, force *bool) CatalogRefreshResult {
	covered := r.yieldForRefresh(ctx, providers)
	if r.refreshContext(ctx, providers).Aborted {
		r.uncoverRegistrationRefresh(covered)
		return CatalogRefreshResult{Aborted: true, Errors: map[string]error{}}
	}
	network := ModelNetworkEnabled()
	if allowNetwork != nil {
		network = *allowNetwork
	}
	result := r.RefreshCatalogs(ctx, CatalogRefreshOptions{AllowNetwork: network, Providers: providers, Force: force})
	if result.Aborted {
		r.uncoverRegistrationRefresh(covered)
	}
	return result
}

// AuthResultJSON is upstream's AuthResult object: auth with apiKey, headers
// and baseUrl when set, then env and source.
func AuthResultJSON(result *ai.AuthResult) map[string]any {
	if result == nil {
		return nil
	}
	auth := map[string]any{}
	if result.Auth.APIKey != "" {
		auth["apiKey"] = result.Auth.APIKey
	}
	if len(result.Auth.Headers) > 0 {
		auth["headers"] = result.Auth.Headers
	}
	if result.Auth.BaseURL != "" {
		auth["baseUrl"] = result.Auth.BaseURL
	}
	out := map[string]any{"auth": auth}
	if result.Source != "" || result.SourcePresent {
		out["source"] = result.Source
	}
	if len(result.Env) > 0 {
		out["env"] = result.Env
	}
	return out
}
