package codingagent

// Radius provider composition and dynamic catalog refresh for one
// ModelRegistry. Mirrors upstream model-runtime.ts configureRadiusProviders
// and refresh(), and the per-provider refresh engine in ai/src/models.ts
// (Models.refresh, publishProviderModels, resolveRefreshCredential).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

// CatalogRefreshOptions selects providers and whether network access is allowed.
// An empty Providers list refreshes every dynamic provider.
type CatalogRefreshOptions struct {
	AllowNetwork bool
	Providers    []string
}

// CatalogRefreshResult mirrors upstream ModelsRefreshResult.
type CatalogRefreshResult struct {
	Aborted bool
	Errors  map[string]error
}

// ModelNetworkEnabled mirrors ModelRuntime.modelNetworkEnabled
// (PI_OFFLINE === undefined). PIG_OFFLINE is Pig's product-neutral alias.
func ModelNetworkEnabled() bool {
	if _, set := os.LookupEnv("PI_OFFLINE"); set {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE")))
	return value != "1" && value != "true" && value != "yes"
}

type catalogRefreshState struct {
	generation uint64
	cancel     context.CancelFunc
	publish    sync.Mutex
}

var radiusBaseURLVersionSuffix = regexp.MustCompile(`/v1/?$`)

// defaultModelsStore mirrors ModelRuntime.create: models-store.json next to
// models.json, or an in-memory store without an agent directory.
func defaultModelsStore(agentDir string) ai.ModelsStore {
	if agentDir == "" {
		return ai.NewInMemoryModelsStore()
	}
	return ai.NewFileModelsStore(filepath.Join(agentDir, "models-store.json"))
}

// SetModelsStore replaces the dynamic catalog store.
func (r *ModelRegistry) SetModelsStore(store ai.ModelsStore) {
	r.mu.Lock()
	r.modelsStore = store
	r.mu.Unlock()
}

// dropUncomposableProviders removes models.json providers that set "oauth"
// without "baseUrl" and reports them. Upstream applyModelsJson throws for such
// a provider; the runtime records the composition error and keeps the base.
func dropUncomposableProviders(config *modelsConfig) []string {
	var failures []string
	for providerID, provider := range config.Providers {
		if provider.OAuth != nil && provider.OAuth.Kind != "" && provider.BaseURL == "" {
			failures = append(failures, fmt.Sprintf("Provider %q: Provider %s: \"baseUrl\" is required when \"oauth\" is set.", providerID, providerID))
			delete(config.Providers, providerID)
		}
	}
	slices.Sort(failures)
	return failures
}

// configureRadiusProvidersLocked mirrors configureRadiusProviders: the
// built-in provider plus one per models.json provider with "oauth": "radius"
// and a baseUrl. Unchanged providers keep their refreshed catalogs.
func (r *ModelRegistry) configureRadiusProvidersLocked() {
	wanted := map[string]ai.RadiusProviderOptions{RadiusProviderID: {ID: RadiusProviderID}}
	if r.config != nil {
		for providerID, provider := range r.config.Providers {
			if provider.OAuth == nil || provider.OAuth.Kind != "radius" || provider.BaseURL == "" {
				continue
			}
			name := provider.Name
			if name == "" {
				name = providerID
			}
			wanted[providerID] = ai.RadiusProviderOptions{ID: providerID, Name: name, Gateway: radiusBaseURLVersionSuffix.ReplaceAllString(provider.BaseURL, "")}
		}
	}
	next := make(map[string]*ai.RadiusProvider, len(wanted))
	for providerID, options := range wanted {
		candidate := ai.NewRadiusProvider(options)
		if existing := r.radius[providerID]; existing != nil && existing.Name() == candidate.Name() && existing.Gateway() == candidate.Gateway() {
			candidate = existing
		}
		next[providerID] = candidate
	}
	r.radius = next
}

// radiusProviderLocked returns the Radius provider owning providerID. An
// extension registration for the same ID takes precedence.
func (r *ModelRegistry) radiusProviderLocked(providerID string) *ai.RadiusProvider {
	if _, extension := r.dynamic[providerID]; extension {
		return nil
	}
	return r.radius[providerID]
}

// RadiusOAuth returns the OAuth flow of the Radius provider providerID.
func (r *ModelRegistry) RadiusOAuth(providerID string) (*ai.RadiusOAuth, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if provider := r.radiusProviderLocked(providerID); provider != nil {
		return provider.OAuth(), true
	}
	return nil, false
}

// RadiusOAuthFlows returns every configured Radius OAuth flow, sorted by ID.
func (r *ModelRegistry) RadiusOAuthFlows() []*ai.RadiusOAuth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	flows := make([]*ai.RadiusOAuth, 0, len(r.radius))
	for providerID := range r.radius {
		if provider := r.radiusProviderLocked(providerID); provider != nil {
			flows = append(flows, provider.OAuth())
		}
	}
	slices.SortFunc(flows, func(a, b *ai.RadiusOAuth) int { return strings.Compare(a.ID(), b.ID()) })
	return flows
}

func radiusModelEntry(model ai.PiMessagesModel) ModelEntry {
	return ModelEntry{
		ProviderID: model.Provider, ModelID: model.ID, BaseURL: model.BaseURL, DisplayName: model.Name,
		API: string(ai.APIPiMessages), Reasoning: model.Reasoning,
		ThinkingLevelMap: cloneThinkingLevelMap(model.ThinkingLevelMap), Input: append([]string(nil), model.Input...),
		InputLimits:   model.InputLimits.Clone(),
		ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
		InputCost: model.Cost.Input, OutputCost: model.Cost.Output, CacheReadCost: model.Cost.CacheRead, CacheWriteCost: model.Cost.CacheWrite,
		CostTiers: append([]ai.CostTier(nil), model.Cost.Tiers...), PromptCache: maps.Clone(model.PromptCache),
		SamplingParams: maps.Clone(model.SamplingParams), Headers: maps.Clone(model.Headers), ModelHeaders: maps.Clone(model.Headers),
		Compat: mergeCompat((*providerCompat)(model.Compat), nil),
	}
}

// radiusModel returns the composed catalog entry without resolving credentials.
func (r *ModelRegistry) radiusModel(providerID, modelID string) (ModelEntry, bool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider := r.radiusProviderLocked(providerID)
	if provider == nil {
		return ModelEntry{}, false, false
	}
	for _, entry := range r.radiusComposedModelsLocked(provider) {
		if entry.ModelID == modelID {
			return entry, true, true
		}
	}
	return ModelEntry{}, false, true
}

// resolveRadiusModel resolves one composed Radius model. OAuth token refresh
// belongs to the request path, not catalog lookup.
func (r *ModelRegistry) resolveRadiusModel(providerID, modelID string) (ModelEntry, bool, bool) {
	entry, found, owned := r.radiusModel(providerID, modelID)
	if !found {
		return entry, found, owned
	}
	if credential, ok := r.storedCredential(providerID); ok {
		if credential.Type == ai.CredentialOAuth {
			entry.APIKey = credential.Access
		} else {
			entry.APIKey, _ = r.resolveRadiusAPIKey(providerID, &credential)
		}
	} else {
		entry.APIKey, _ = r.resolveRadiusAPIKey(providerID, nil)
	}
	return entry, true, true
}

func radiusCredentialKey(credential ai.Credential) string {
	if credential.Type == ai.CredentialOAuth {
		return credential.Access
	}
	return credential.Key
}

func (r *ModelRegistry) storedCredential(providerID string) (ai.Credential, bool) {
	r.mu.RLock()
	storage := r.authStorage
	r.mu.RUnlock()
	if storage == nil {
		return ai.Credential{}, false
	}
	credential, ok, err := storage.Get(providerID)
	return credential, ok && err == nil
}

// radiusHasAuth mirrors hasConfiguredAuth for a Radius provider: a stored
// credential, configured API key, or RADIUS_API_KEY.
func (r *ModelRegistry) radiusHasAuth(providerID string) bool {
	if _, ok := r.storedCredential(providerID); ok {
		return true
	}
	if raw, env, ok := r.radiusConfiguredKey(providerID); ok {
		return configvalue.IsCommandConfigValue(raw) || configvalue.IsConfigValueConfigured(raw, env)
	}
	return ai.GetEnvAPIKey(RadiusProviderID, nil) != ""
}

// radiusEntries returns the effective models of every Radius provider,
// limited to providers with configured auth when authenticated is set.
func (r *ModelRegistry) radiusEntries(authenticated bool) []ModelEntry {
	r.mu.RLock()
	providers := make([]*ai.RadiusProvider, 0, len(r.radius))
	for providerID := range r.radius {
		if provider := r.radiusProviderLocked(providerID); provider != nil {
			providers = append(providers, provider)
		}
	}
	r.mu.RUnlock()
	var entries []ModelEntry
	for _, provider := range providers {
		if authenticated && !r.radiusHasAuth(provider.ID()) {
			continue
		}
		r.mu.RLock()
		entries = append(entries, r.radiusComposedModelsLocked(provider)...)
		r.mu.RUnlock()
	}
	return entries
}

// RadiusAPIKey resolves the request credential of a Radius provider: a runtime
// key, a stored OAuth token (refreshed when expired) or API key, then
// RADIUS_API_KEY.
func (r *ModelRegistry) RadiusAPIKey(ctx context.Context, providerID string) (string, error) {
	r.mu.RLock()
	provider := r.radiusProviderLocked(providerID)
	r.mu.RUnlock()
	if provider == nil {
		return "", fmt.Errorf("unknown Radius provider %q", providerID)
	}
	// A runtime key (--api-key) masks the stored credential, as upstream's
	// RuntimeCredentials overlay does.
	if key, ok := r.RuntimeAPIKey(providerID); ok && key != "" {
		return key, nil
	}
	credential, ok := r.storedCredential(providerID)
	var stored *ai.Credential
	if ok {
		stored = &credential
	}
	resolved, err := r.resolveRefreshCredential(ctx, provider, stored)
	if err != nil || resolved == nil {
		return "", err
	}
	return radiusCredentialKey(*resolved), nil
}

var errCredentialChanged = errors.New("credential changed during refresh")

// resolveRefreshCredential mirrors Models.resolveRefreshCredential: an OAuth
// credential is refreshed under the auth lock once expired; otherwise the
// stored API key or RADIUS_API_KEY applies.
func (r *ModelRegistry) resolveRefreshCredential(ctx context.Context, provider *ai.RadiusProvider, stored *ai.Credential) (*ai.Credential, error) {
	if stored != nil && stored.Type == ai.CredentialOAuth {
		if time.Now().UnixMilli() < stored.Expires {
			return stored, nil
		}
		if ctx.Err() != nil {
			return nil, nil
		}
		return r.refreshRadiusOAuth(ctx, provider)
	}
	key, err := r.resolveRadiusAPIKey(provider.ID(), stored)
	if err != nil || key == "" {
		return nil, err
	}
	return &ai.Credential{Type: ai.CredentialAPIKey, Key: key}, nil
}

func (r *ModelRegistry) refreshRadiusOAuth(ctx context.Context, provider *ai.RadiusProvider) (*ai.Credential, error) {
	r.mu.RLock()
	storage := r.authStorage
	r.mu.RUnlock()
	if storage == nil {
		return nil, nil
	}
	var refreshed *ai.Credential
	err := storage.Update(provider.ID(), func(current ai.Credential, exists bool) (ai.Credential, error) {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		if !exists || current.Type != ai.CredentialOAuth {
			return current, errCredentialChanged
		}
		if time.Now().UnixMilli() < current.Expires {
			refreshed = &current
			return current, nil
		}
		next, err := provider.OAuth().RefreshTokenContext(ctx, ai.OAuthCredentials{Refresh: current.Refresh, Access: current.Access, Expires: current.Expires})
		if err != nil {
			return current, err
		}
		updated := ai.Credential{Type: ai.CredentialOAuth, Refresh: next.Refresh, Access: next.Access, Expires: next.Expires, Scope: next.Scope}
		refreshed = &updated
		return updated, nil
	})
	if errors.Is(err, errCredentialChanged) {
		return nil, nil
	}
	return refreshed, err
}

// RefreshCatalogs mirrors ModelRuntime.refresh for dynamic providers: each
// selected provider restores its stored catalog and, when allowed, refreshes
// it from the network. A newer refresh of the same provider supersedes an
// older one. Errors of cancelled or superseded refreshes are not reported.
func (r *ModelRegistry) RefreshCatalogs(ctx context.Context, options CatalogRefreshOptions) CatalogRefreshResult {
	result := CatalogRefreshResult{Errors: map[string]error{}}
	if ctx.Err() != nil {
		result.Aborted = true
		return result
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, provider := range r.selectRadiusProviders(options.Providers) {
		wg.Go(func() {
			if err := r.refreshRadiusProvider(ctx, provider, options.AllowNetwork); err != nil {
				mu.Lock()
				result.Errors[provider.ID()] = err
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	result.Aborted = ctx.Err() != nil
	r.mu.RLock()
	listener := r.onChange
	r.mu.RUnlock()
	listener.publish()
	return result
}

func (r *ModelRegistry) selectRadiusProviders(selected []string) []*ai.RadiusProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var providers []*ai.RadiusProvider
	for providerID := range r.radius {
		provider := r.radiusProviderLocked(providerID)
		if provider != nil && (len(selected) == 0 || slices.Contains(selected, providerID)) {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (r *ModelRegistry) refreshRadiusProvider(parent context.Context, provider *ai.RadiusProvider, allowNetwork bool) error {
	ctx, generation, done := r.beginCatalogRefresh(parent, provider.ID())
	defer done()
	publish := func(publication ai.ModelsPublication) (bool, error) {
		return r.publishCatalog(ctx, provider.ID(), generation, publication)
	}
	credential, hasCredential, credentialErr := r.readStoredCredential(provider.ID())
	var stored *ai.Credential
	if hasCredential {
		stored = &credential
	}
	// Restore cached provider state before auth resolution or network access.
	err := r.runCatalogRefreshPhase(ctx, provider, stored, false, publish)
	if err == nil {
		err = credentialErr
	}
	if err == nil && allowNetwork && ctx.Err() == nil {
		err = r.refreshCatalogFromNetwork(ctx, provider, stored, publish)
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (r *ModelRegistry) refreshCatalogFromNetwork(ctx context.Context, provider *ai.RadiusProvider, stored *ai.Credential, publish func(ai.ModelsPublication) (bool, error)) error {
	credential, err := r.resolveRefreshCredential(ctx, provider, stored)
	if err != nil || credential == nil {
		return err
	}
	return r.runCatalogRefreshPhase(ctx, provider, credential, true, publish)
}

func (r *ModelRegistry) readStoredCredential(providerID string) (ai.Credential, bool, error) {
	r.mu.RLock()
	storage := r.authStorage
	r.mu.RUnlock()
	if storage == nil {
		return ai.Credential{}, false, nil
	}
	credential, ok, err := storage.GetRaw(providerID)
	if err != nil {
		return ai.Credential{}, false, fmt.Errorf("Credential store read failed for %s: %w", providerID, err)
	}
	return credential, ok, nil
}

func (r *ModelRegistry) runCatalogRefreshPhase(ctx context.Context, provider *ai.RadiusProvider, credential *ai.Credential, allowNetwork bool, publish func(ai.ModelsPublication) (bool, error)) error {
	store := r.catalogStore()
	stored, err := store.Read(ctx, provider.ID())
	if err != nil {
		return err
	}
	return provider.RefreshModels(ctx, ai.RefreshModelsContext{Credential: credential, Stored: stored, AllowNetwork: allowNetwork, Publish: publish})
}

func (r *ModelRegistry) catalogStore() ai.ModelsStore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modelsStore
}

func (r *ModelRegistry) catalogState(providerID string) *catalogRefreshState {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	if r.refreshStates == nil {
		r.refreshStates = map[string]*catalogRefreshState{}
	}
	state := r.refreshStates[providerID]
	if state == nil {
		state = &catalogRefreshState{}
		r.refreshStates[providerID] = state
	}
	return state
}

// beginCatalogRefresh supersedes any running refresh of providerID.
func (r *ModelRegistry) beginCatalogRefresh(parent context.Context, providerID string) (context.Context, uint64, func()) {
	state := r.catalogState(providerID)
	ctx, cancel := context.WithCancel(parent)
	r.refreshMu.Lock()
	if state.cancel != nil {
		state.cancel()
	}
	state.generation++
	generation := state.generation
	state.cancel = cancel
	r.refreshMu.Unlock()
	return ctx, generation, func() {
		r.refreshMu.Lock()
		if state.generation == generation {
			state.cancel = nil
		}
		r.refreshMu.Unlock()
		cancel()
	}
}

func (r *ModelRegistry) isCurrentCatalogRefresh(state *catalogRefreshState, generation uint64) bool {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	return state.generation == generation
}

// publishCatalog mirrors publishProviderModels: publications of one provider
// run in order, persist before updating, and are dropped once superseded.
func (r *ModelRegistry) publishCatalog(ctx context.Context, providerID string, generation uint64, publication ai.ModelsPublication) (bool, error) {
	state := r.catalogState(providerID)
	state.publish.Lock()
	defer state.publish.Unlock()
	if ctx.Err() != nil || !r.isCurrentCatalogRefresh(state, generation) {
		return false, nil
	}
	if publication.Persist != nil {
		if err := r.catalogStore().Write(ctx, providerID, *publication.Persist); err != nil {
			return false, err
		}
	}
	if ctx.Err() != nil || !r.isCurrentCatalogRefresh(state, generation) {
		return false, nil
	}
	if publication.Update != nil {
		publication.Update()
	}
	return true, nil
}
