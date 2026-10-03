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
		overlay = &remoteCatalogOverlay{}
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

// builtinBase is the built-in provider with its remote catalog overlay, or nil for a provider with no built-in model of any type.
func (r *ModelRegistry) builtinBase(id string, fallback ai.ModelsStreamFunction) *ai.ModelsProvider {
	base := builtinTypedBase(id, fallback)
	if base == nil || id == RadiusProviderID {
		return base
	}
	r.remoteMu.Lock()
	baseURL := r.catalogBaseURL
	r.remoteMu.Unlock()
	return withRemoteCatalogOverlay(base, baseURL, builtinModelDataGeneratedAt(), r.remoteOverlay(id))
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
		if collection.GetProvider(id) != nil {
			continue
		}
		if base := r.builtinBase(id, nil); base != nil {
			collection.SetProvider(base)
		}
	}
	options.Providers = ids
	return collection.Refresh(ctx, options)
}
