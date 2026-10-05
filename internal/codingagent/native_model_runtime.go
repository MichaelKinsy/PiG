package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts.

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

type registryModelsStore struct{ registry *ModelRegistry }

func (store registryModelsStore) Read(ctx context.Context, id string) (*ai.ModelsStoreEntry, error) {
	return store.registry.catalogStore().Read(ctx, id)
}
func (store registryModelsStore) Write(ctx context.Context, id string, entry ai.ModelsStoreEntry) error {
	return store.registry.catalogStore().Write(ctx, id, entry)
}
func (store registryModelsStore) Delete(ctx context.Context, id string) error {
	return store.registry.catalogStore().Delete(ctx, id)
}

type registryCredentials struct{ registry *ModelRegistry }

func (store registryCredentials) current() *ai.RuntimeCredentials {
	store.registry.mu.Lock()
	defer store.registry.mu.Unlock()
	return store.registry.runtimeCredentialsLocked()
}
func (store registryCredentials) Read(ctx context.Context, id string) (*ai.Credential, error) {
	return store.current().Read(ctx, id)
}
func (store registryCredentials) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	return store.current().List(ctx)
}
func (store registryCredentials) Modify(ctx context.Context, id string, fn func(*ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	return store.current().Modify(ctx, id, fn)
}
func (store registryCredentials) Delete(ctx context.Context, id string) error {
	return store.current().Delete(ctx, id)
}

// NativeModels returns the provider-owned collection sharing this registry's credentials and ModelsStore.
func (r *ModelRegistry) NativeModels() *ai.Models {
	r.nativeMu.Lock()
	defer r.nativeMu.Unlock()
	if r.nativeModels == nil {
		r.nativeModels = ai.CreateModelsWithModelAuth(func(ctx context.Context, model *ai.Model, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
			// ResolveRegistryModelAuth resolves provider auth with GetAuth(providerID); it must never call GetModelAuth.
			return r.ResolveRegistryModelAuth(ctx, model, overrides)
		}, ai.CreateModelsOptions{Credentials: registryCredentials{r}, ModelsStore: registryModelsStore{r}})
		r.nativeOriginal = map[string]*ai.ModelsProvider{}
		r.nativeBase = map[string]*ai.ModelsProvider{}
		r.nativeInputs = map[string]*ProviderConfigInput{}
		r.nativeAvailable = map[string]bool{}
	}
	return r.nativeModels
}

// GetProvider returns a native or composed provider by exact ID.
func (r *ModelRegistry) GetProvider(id string) *ai.ModelsProvider {
	r.nativeMu.Lock()
	models := r.nativeModels
	r.nativeMu.Unlock()
	if models == nil {
		return nil
	}
	return models.GetProvider(id)
}

// GetRegisteredNativeProvider returns the caller's original native registration.
func (r *ModelRegistry) GetRegisteredNativeProvider(id string) *ai.ModelsProvider {
	r.nativeMu.Lock()
	defer r.nativeMu.Unlock()
	return r.nativeOriginal[id]
}

// GetRegisteredProviderConfig returns the active core provider-registration input.
func (r *ModelRegistry) GetRegisteredProviderConfig(id string) *ProviderConfigInput {
	r.nativeMu.Lock()
	defer r.nativeMu.Unlock()
	return r.nativeInputs[id]
}

// GetRegisteredProviderIDs preserves native registration order.
func (r *ModelRegistry) GetRegisteredProviderIDs() []string {
	r.nativeMu.Lock()
	models := r.nativeModels
	r.nativeMu.Unlock()
	var ids []string
	if models != nil {
		for _, provider := range models.GetProviders() {
			ids = append(ids, provider.ID)
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range slices.Sorted(maps.Keys(r.dynamic)) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *ModelRegistry) publishNativeChange() {
	r.mu.RLock()
	listener := r.onChange
	observers := slices.Clone(r.observers)
	r.mu.RUnlock()
	for _, observer := range observers {
		observer.publish()
	}
	listener.publish()
}

// RegisterNativeModelsProvider replaces legacy registration state with the native catalog/auth/API callbacks, while retaining models.json overlays. It returns without authenticating or waiting for availability: the bound projection republishes the catalog, and the local refresh is queued for the caller's next yield.
func (r *ModelRegistry) RegisterNativeModelsProvider(provider *ai.ModelsProvider) error {
	if provider == nil || provider.ID == "" || provider.GetModels == nil {
		return fmt.Errorf("native provider requires an id and getModels")
	}
	collection := r.NativeModels()
	composed, err := r.composeNativeProvider(provider, nil)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.nativeMu.Lock()
	delete(r.dynamic, provider.ID)
	delete(r.native, provider.ID)
	r.dynamicOrder = slices.DeleteFunc(r.dynamicOrder, func(id string) bool { return id == provider.ID })
	r.nativeOriginal[provider.ID] = provider
	delete(r.nativeInputs, provider.ID)
	r.nativeBase[provider.ID] = provider
	collection.SetProvider(composed)
	r.nativeMu.Unlock()
	r.mu.Unlock()
	// upstream: packages/coding-agent/src/core/model-runtime.ts:885-917 (markProvisionallyConfigured)
	provisional := &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"}
	if provider.Auth.OAuth != nil && provider.Auth.APIKey == nil {
		provisional.Type = ai.CredentialOAuth
	}
	r.syncRegistration(provider.ID, provisional)
	r.publishNativeChange()
	return nil
}

func mergeProviderConfigInput(previous *ProviderConfigInput, input ProviderConfigInput) ProviderConfigInput {
	if previous == nil {
		return input
	}
	merged := *previous
	if input.Name != "" {
		merged.Name = input.Name
	}
	if input.BaseURL != "" {
		merged.BaseURL = input.BaseURL
	}
	if input.APIKey != "" {
		merged.APIKey = input.APIKey
	}
	if input.API != "" {
		merged.API = input.API
	}
	if input.Headers != nil {
		merged.Headers = input.Headers
	}
	if input.AuthHeader != nil {
		merged.AuthHeader = input.AuthHeader
	}
	if input.Models != nil {
		merged.Models = input.Models
	}
	if input.Images != nil {
		merged.Images = input.Images
	}
	if input.Classifiers != nil {
		merged.Classifiers = input.Classifiers
	}
	if input.OAuth != nil {
		merged.OAuth = input.OAuth
	}
	if input.StreamSimple != nil {
		merged.StreamSimple = input.StreamSimple
	}
	if input.RefreshModels != nil {
		merged.RefreshModels = input.RefreshModels
	}
	return merged
}

// RegisterProviderInput composes legacy model and OAuth callbacks over built-in/configured metadata. Both legacy entry points share merge state, while a prior native registration is replaced. A configured or stored-credential registration is provisionally available until its queued local refresh runs.
func (r *ModelRegistry) RegisterProviderInput(id string, input ProviderConfigInput, fallback ai.ModelsStreamFunction) error {
	if input.StreamSimple != nil && input.API == "" {
		return fmt.Errorf(`Provider %s: "api" is required when registering streamSimple.`, id)
	}
	collection := r.NativeModels()
	base := r.builtinBase(id, fallback)
	if base == nil {
		auth, _ := ai.BuiltinProviderAuth(id)
		base = &ai.ModelsProvider{ID: id, Name: catalogProviderName(id), Auth: auth, GetModels: func() ([]*ai.Model, error) { return nil, nil }, Stream: fallback, StreamSimple: fallback}
	}
	if _, err := r.composeNativeProvider(base, &input); err != nil {
		return err
	}
	r.mu.RLock()
	r.nativeMu.Lock()
	previous := r.nativeInputs[id]
	legacy, hasLegacy := r.dynamic[id]
	native := r.nativeOriginal[id] != nil || r.native[id].provider != nil
	r.nativeMu.Unlock()
	r.mu.RUnlock()
	if previous == nil && hasLegacy && !native {
		converted := legacyProviderInput(id, legacy)
		previous = &converted
	}
	input = mergeProviderConfigInput(previous, input)
	composed, err := r.composeNativeProvider(base, &input)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.nativeMu.Lock()
	delete(r.dynamic, id)
	delete(r.native, id)
	r.dynamicOrder = slices.DeleteFunc(r.dynamicOrder, func(existing string) bool { return existing == id })
	r.nativeInputs[id] = &input
	delete(r.nativeOriginal, id)
	r.nativeBase[id] = base
	collection.SetProvider(composed)
	r.nativeMu.Unlock()
	r.mu.Unlock()
	r.syncRegistration(id, registrationAuth(input.OAuth != nil, input.APIKey))
	r.publishNativeChange()
	return nil
}

func (r *ModelRegistry) unregisterNativeProvider(id string) {
	r.nativeMu.Lock()
	if r.nativeModels != nil {
		r.nativeModels.DeleteProvider(id)
	}
	delete(r.nativeOriginal, id)
	delete(r.nativeBase, id)
	delete(r.nativeInputs, id)
	delete(r.nativeAvailable, id)
	r.nativeMu.Unlock()
}

// GetNativeModels returns provider-owned model data without building backend clients.
func (r *ModelRegistry) GetNativeModels(id ...string) []*ai.Model {
	r.nativeMu.Lock()
	collection := r.nativeModels
	r.nativeMu.Unlock()
	if collection == nil {
		return nil
	}
	return collection.GetModels(id...)
}

// RefreshNativeProviders awaits native catalog publication and the bound availability snapshot before returning to credential synchronization.
func (r *ModelRegistry) RefreshNativeProviders(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	result := r.reconcileAvailability(ctx, options.Providers, r.refreshNativeModels(ctx, options))
	if !result.Aborted {
		r.publishNativeChange()
	}
	return result
}

func (r *ModelRegistry) refreshNativeModels(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	r.nativeMu.Lock()
	collection := r.nativeModels
	r.nativeMu.Unlock()
	if collection == nil {
		return ai.ModelsRefreshResult{Errors: map[string]error{}}
	}
	if len(options.Providers) > 0 {
		selected := slices.DeleteFunc(slices.Clone(options.Providers), func(id string) bool { return collection.GetProvider(id) == nil })
		if len(selected) == 0 {
			return ai.ModelsRefreshResult{Errors: map[string]error{}}
		}
		options.Providers = selected
	}
	result := collection.Refresh(ctx, options)
	if !result.Aborted {
		for _, provider := range collection.GetProviders() {
			if len(options.Providers) > 0 && !slices.Contains(options.Providers, provider.ID) {
				continue
			}
			check, err := collection.CheckAuth(ctx, provider.ID)
			if ctx.Err() != nil {
				result.Aborted = true
				break
			}
			if err != nil {
				if _, exists := result.Errors[provider.ID]; !exists {
					result.ErrorOrder = append(result.ErrorOrder, provider.ID)
				}
				result.Errors[provider.ID] = err
				continue
			}
			r.nativeMu.Lock()
			r.nativeAvailable[provider.ID] = check != nil
			r.nativeMu.Unlock()
		}
	}
	return result
}

// recomposeNativeProviders recomposes every native provider, or only the selected ones. Each SetProvider supersedes that provider's in-flight refresh, as Pi's rebuildProviders and recomposeProvider do (model-runtime.ts:270-275,708-712).
func (r *ModelRegistry) recomposeNativeProviders(selected []string) {
	r.nativeMu.Lock()
	collection := r.nativeModels
	r.nativeMu.Unlock()
	if collection == nil {
		return
	}
	for _, provider := range collection.GetProviders() {
		if selected != nil && !slices.Contains(selected, provider.ID) {
			continue
		}
		r.nativeMu.Lock()
		base, input := r.nativeBase[provider.ID], r.nativeInputs[provider.ID]
		r.nativeMu.Unlock()
		if base == nil {
			continue
		}
		composed, err := r.composeNativeProvider(base, input)
		if err != nil {
			r.mu.Lock()
			r.loadError = err.Error()
			r.mu.Unlock()
			continue
		}
		collection.SetProvider(composed)
	}
}

// RefreshModelRuntime reloads configuration and refreshes every selected provider through the registry's shared coordinator. A refresh over every provider first yields to the queued registration refresh, and a provider-scoped refresh covers the queued registrations of its own providers (model-runtime.ts:750,788,796).
func (r *ModelRegistry) RefreshModelRuntime(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	covered := r.yieldForRefresh(ctx, options.Providers)
	result := r.refreshModelRuntime(ctx, options)
	if result.Aborted {
		r.uncoverRegistrationRefresh(covered)
	}
	return result
}

func (r *ModelRegistry) refreshModelRuntime(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	if r.refreshContext(ctx, options.Providers).Aborted {
		return ai.ModelsRefreshResult{Aborted: true, Errors: map[string]error{}}
	}
	allow := options.AllowNetwork == nil || *options.AllowNetwork
	result := r.RefreshCatalogs(ctx, CatalogRefreshOptions{AllowNetwork: allow, Providers: options.Providers, Force: options.Force})
	return ai.ModelsRefreshResult{Aborted: result.Aborted, Errors: result.Errors, ErrorOrder: slices.Clone(result.errorOrder)}
}

// GetProviderAuth resolves native provider auth against the shared credential store.
func (r *ModelRegistry) GetProviderAuth(ctx context.Context, id string) (*ai.AuthResult, error) {
	return r.NativeModels().GetAuth(ctx, id)
}

// LoginNativeProvider serializes same-provider login and logout through persistence and local synchronization. It logs in the composed provider GetTypedProvider returns for the ID, a built-in or models.json provider included, and passes the optional LoginOptions to the provider's login. A synchronization failure returns the committed credential with CredentialSynchronizationError.
func (r *ModelRegistry) LoginNativeProvider(ctx context.Context, id string, kind ai.AuthType, interaction ai.AuthInteraction, options ...ai.LoginOptions) (ai.Credential, error) {
	synced := false
	defer r.coverCredentialOperation(ctx, id, &synced)()
	release, err := r.beginCredentialOperation(ctx, id)
	if err != nil {
		return ai.Credential{}, err
	}
	defer release()
	if ctx.Err() != nil {
		return ai.Credential{}, context.Cause(ctx)
	}
	// Login reads only the composed auth methods, which do not depend on the stream fallback.
	provider := r.GetTypedProvider(id, nil)
	if provider == nil {
		return ai.Credential{}, ai.NewModelsError(ai.ModelsErrorProvider, "Unknown provider: "+id, nil)
	}
	credential, err := r.NativeModels().LoginProvider(ctx, provider, kind, interaction, options...)
	if err != nil {
		return credential, err
	}
	synced = true
	return credential, r.synchronizeCredentialState(ctx, id, CredentialSynchronizationLogin, &credential)
}

// LogoutNativeProvider serializes deletion and local synchronization with other credential operations for this provider. A synchronization failure reports that deletion committed.
func (r *ModelRegistry) LogoutNativeProvider(ctx context.Context, id string) error {
	synced := false
	defer r.coverCredentialOperation(ctx, id, &synced)()
	release, err := r.beginCredentialOperation(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	if err := r.NativeModels().Logout(ctx, id); err != nil {
		return err
	}
	synced = true
	return r.synchronizeCredentialState(ctx, id, CredentialSynchronizationLogout, nil)
}

// coverCredentialOperation covers the provider's queued registration refresh for a login or logout and starts the refresh of the other queued providers without waiting for it. The operation's local synchronization recomposes and refreshes that provider (model-runtime.ts:515-535), and a pass started now would begin that provider's refresh after the operation's own and supersede it (models.ts beginProviderRefresh), dropping its synchronization error, where Pi's earlier-queued registration refresh begins first. The returned function returns the covered provider to the queue and starts it when the operation never reached synchronization or its context ended.
func (r *ModelRegistry) coverCredentialOperation(ctx context.Context, id string, synced *bool) func() {
	covered := r.coverRegistrationRefresh([]string{id})
	r.StartRegistrationRefresh(ctx)
	return func() {
		if !*synced || ctx.Err() != nil {
			r.uncoverRegistrationRefresh(covered)
		}
	}
}
