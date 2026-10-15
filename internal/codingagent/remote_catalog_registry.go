package codingagent

// Wires the remote catalog overlay into the built-in providers of one ModelRegistry.
// upstream: packages/coding-agent/src/core/model-runtime.ts:create (withRemoteCatalog over every built-in provider except radius)

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// SetCatalogBaseURL selects the remote catalog endpoint; empty selects DefaultCatalogBaseURL.
// upstream: model-runtime.ts:CreateModelRuntimeOptions.catalogBaseUrl
func (r *ModelRegistry) SetCatalogBaseURL(baseURL string) {
	r.remoteMu.Lock()
	r.catalogBaseURL = baseURL
	r.remoteMu.Unlock()
}

func (r *ModelRegistry) remoteOverlay(providerID string) *remoteCatalogOverlay {
	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if r.remoteOverlays == nil {
		r.remoteOverlays = map[string]*remoteCatalogOverlay{}
	}
	overlay := r.remoteOverlays[providerID]
	if overlay == nil {
		overlay = &remoteCatalogOverlay{replaces: providerID == openRouterProviderID}
		r.remoteOverlays[providerID] = overlay
	}
	return overlay
}

// remoteOverlayModels returns the models the remote catalog added to a built-in provider, without creating state for providers that have none.
func (r *ModelRegistry) remoteOverlayModels(providerID string) []ai.AnyModel {
	r.remoteMu.Lock()
	overlay := r.remoteOverlays[providerID]
	r.remoteMu.Unlock()
	if overlay == nil {
		return nil
	}
	return overlay.snapshot()
}

// remoteOverlayReplaces reports whether the provider's overlay is its whole catalog (a personalized one) rather than additions to the bundled catalog.
func (r *ModelRegistry) remoteOverlayReplaces(providerID string) bool {
	r.remoteMu.Lock()
	overlay := r.remoteOverlays[providerID]
	r.remoteMu.Unlock()
	return overlay != nil && overlay.replaces
}

// builtinBase is the built-in provider with its remote catalog overlay, or nil for a provider with no built-in model of any type.
func (r *ModelRegistry) builtinBase(id string, fallback ai.ModelsStreamFunction) *ai.ModelsProvider {
	base := builtinTypedBase(id, fallback)
	if base == nil || id == RadiusProviderID {
		return base
	}
	r.remoteMu.Lock()
	baseURL := r.catalogBaseURL
	r.remoteMu.Unlock()
	overlay := r.remoteOverlay(id)
	// OpenRouter narrows the catalog to the models, limits and prices of the user's key.
	// upstream: model-runtime.ts:create (provider.id === "openrouter" selects withPersonalizedCatalog and personalizeOpenRouterCatalog)
	if id == openRouterProviderID {
		return withPersonalizedCatalogOverlay(base, ai.PersonalizeOpenRouterCatalog, baseURL, builtinModelDataGeneratedAt(), overlay)
	}
	return withRemoteCatalogOverlay(base, baseURL, builtinModelDataGeneratedAt(), overlay)
}

// openRouterProviderID is the built-in provider whose catalog is personalized with the user's key (model-runtime.ts:create).
const openRouterProviderID = "openrouter"

// providerBaseURLOverride is the base URL an extension registration or models.json sets for a provider, or "" when neither does; an extension's wins, as in
// resolveGeneratedModelLocked and the dynamic provider resolution.
func (r *ModelRegistry) providerBaseURLOverride(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if dynamic, ok := r.dynamic[id]; ok && dynamic.Models == nil && dynamic.BaseURL != "" {
		return dynamic.BaseURL
	}
	if r.config != nil {
		if provider, ok := r.config.Providers[id]; ok {
			return provider.BaseURL
		}
	}
	return ""
}

// remoteCatalogProviderIDs are the built-in providers whose catalogs the overlay refreshes: not Radius, which has its own catalog, and not
// a provider an extension replaced, whose composed base refreshes through the native collection.
func (r *ModelRegistry) remoteCatalogProviderIDs(selected []string) []string {
	var ids []string
	for _, id := range ai.ListProviders() {
		if r.isRadiusProvider(id) || r.GetProvider(id) != nil {
			continue
		}
		if len(selected) > 0 && !slices.Contains(selected, id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func (r *ModelRegistry) isRadiusProvider(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.radius[id] != nil || id == RadiusProviderID
}

// refreshRemoteCatalogs restores the stored catalog overlay of each built-in provider and, when the network is allowed and the provider has
// credentials, refreshes it from the catalog endpoint. The per-provider engine is the Models collection's (generation supersession,
// credential resolution, ordered publication).
func (r *ModelRegistry) refreshRemoteCatalogs(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	ids := r.remoteCatalogProviderIDs(options.Providers)
	if len(ids) == 0 {
		return ai.ModelsRefreshResult{Errors: map[string]error{}}
	}
	r.remoteMu.Lock()
	if r.remoteCollection == nil {
		r.remoteCollection = ai.CreateModelsWithModelAuth(func(ctx context.Context, model *ai.Model, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
			return r.ResolveRegistryModelAuth(ctx, model, overrides)
		}, ai.CreateModelsOptions{Credentials: registryCredentials{r}, ModelsStore: registryModelsStore{r}})
	}
	collection := r.remoteCollection
	r.remoteMu.Unlock()
	for _, id := range ids {
		// The provider's base URL is the one models.json (or an extension) configures: the refresh request follows it, as Pi's Models passes the registered
		// provider's baseUrl, including the override a composer applies, in RefreshModelsContext.baseUrl.
		override := r.providerBaseURLOverride(id)
		r.remoteMu.Lock()
		registered, applied := collection.GetProvider(id) != nil, r.remoteBaseURLs[id]
		r.remoteMu.Unlock()
		if registered && applied == override {
			continue
		}
		if base := r.builtinBase(id, nil); base != nil {
			if override != "" {
				overridden := *base
				overridden.BaseURL = override
				base = &overridden
			}
			collection.SetProvider(base)
			r.remoteMu.Lock()
			if r.remoteBaseURLs == nil {
				r.remoteBaseURLs = map[string]string{}
			}
			r.remoteBaseURLs[id] = override
			r.remoteMu.Unlock()
		}
	}
	options.Providers = ids
	// The refresh is model work of this registry: CloseModelTasks cancels it, rejects one that would start after it, and then joins the operations the collection is still running for it.
	result := ai.ModelsRefreshResult{Aborted: true, Errors: map[string]error{}}
	done := make(chan struct{})
	if !r.StartModelTask(ctx, func(ctx context.Context) {
		defer close(done)
		result = collection.Refresh(ctx, options)
	}) {
		return result
	}
	<-done
	return result
}
