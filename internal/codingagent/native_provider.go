package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type registeredNativeProvider struct {
	provider  *extension.NativeProvider
	check     *ai.AuthCheck
	available []string
}

// RegisterNativeProvider installs the object and model snapshot and queues the local refresh, as Pi's registerNativeProvider does. It performs no authentication and calls no provider callback; both happen in the queued refresh. The bound availability projection runs before listeners are notified.
func (r *ModelRegistry) RegisterNativeProvider(ctx context.Context, p *extension.NativeProvider) error {
	// A compiled-in extension's carrier (extension.NativeProviderOf) holds the Provider object's own members and none of a subprocess
	// registration's host callbacks: it registers as the object.
	if p != nil && p.ID != "" && p.CheckAuth == nil && p.ResolveAuth == nil && p.ResolveRefreshCredential == nil && p.Models == nil && p.GetModels != nil {
		return r.RegisterNativeModelsProvider(p.ProviderObject())
	}
	if p == nil || p.ID == "" || p.Stream == nil || p.CheckAuth == nil || p.ResolveAuth == nil || p.ResolveRefreshCredential == nil {
		return errors.New("incomplete native provider")
	}
	config := extension.ProviderConfig{Name: p.Name, BaseURL: p.BaseURL, Models: p.Models}
	parsed, ok := providerConfigFromRegistration(config)
	if !ok {
		return errors.New("invalid native provider models")
	}
	r.mu.Lock()
	if p.IsCurrent != nil && !p.IsCurrent() {
		r.mu.Unlock()
		return nil
	}
	if r.native == nil {
		r.native = map[string]registeredNativeProvider{}
	}
	if r.dynamic == nil {
		r.dynamic = map[string]providerConfig{}
	}
	current := r.native[p.ID]
	current.provider = p
	current.available = nil
	if current.check != nil {
		for _, model := range p.Models {
			current.available = append(current.available, model.ID)
		}
	}
	r.native[p.ID] = current
	r.dynamic[p.ID] = parsed
	r.noteDynamicLocked(p.ID)
	r.noteRegistrationLocked(p.ID, true)
	listener := r.onChange
	r.mu.Unlock()
	extension.CallInitiated(ctx)
	// Pi's registerNativeProvider stores, recomposes and republishes the snapshot, then starts its unawaited refresh (model-runtime.ts:744-750). Authentication and catalog refresh run in that refresh.
	// upstream: model-runtime.ts:885-897 (registerNativeProvider marks the provider provisionally configured; an OAuth-only provider's type is oauth)
	provisional := &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured provider"}
	if p.Auth.OAuth != nil && p.Auth.APIKey == nil {
		provisional.Type = ai.CredentialOAuth
	}
	r.syncRegistration(p.ID, provisional)
	listener.publish()
	return nil
}

func (r *ModelRegistry) NativeProvider(id string) *extension.NativeProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.native[id].provider
}

// NativeProviderAuth resolves auth and commits a rotated credential atomically.
// The credential-store lock spans the reverse callback just as Pi's modify
// spans OAuth refresh. Missing credentials and callback failures never fall back.
func (r *ModelRegistry) NativeProviderAuth(ctx context.Context, id string, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	p := r.NativeProvider(id)
	if p == nil {
		return nil, fmt.Errorf("native provider %s is no longer registered", id)
	}
	r.mu.Lock()
	credentials := r.runtimeCredentialsLocked()
	r.mu.Unlock()
	if key, ok := r.RuntimeAPIKey(id); ok && overrides.APIKey == nil {
		overrides.APIKey = &key
	}
	var resolution *ai.AuthResult
	_, err := credentials.Modify(ctx, id, func(current *ai.Credential) (*ai.Credential, error) {
		resolved, updated, err := p.ResolveAuth(ctx, current, overrides)
		if err != nil {
			return nil, err
		}
		resolution = resolved
		return updated, nil
	})
	return resolution, err
}

func (r *ModelRegistry) refreshNativeProviders(ctx context.Context, selected []string, network bool, force *bool) map[string]error {
	r.mu.RLock()
	providers := maps.Clone(r.native)
	r.mu.RUnlock()
	failures := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, native := range providers {
		if len(selected) > 0 && !slices.Contains(selected, id) {
			continue
		}
		wg.Go(func() {
			if err := r.refreshNativeProvider(ctx, native.provider, network, force); err != nil && ctx.Err() == nil {
				mu.Lock()
				failures[id] = err
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return failures
}

func (r *ModelRegistry) refreshNativeProvider(parent context.Context, p *extension.NativeProvider, network bool, force *bool) (resultErr error) {
	r.mu.RLock()
	if r.native[p.ID].provider != p {
		r.mu.RUnlock()
		return nil
	}
	ctx, generation, done := r.beginCatalogRefresh(parent, p.ID)
	r.mu.RUnlock()
	defer func() {
		if ctx.Err() != nil {
			resultErr = nil
		}
		done()
	}()
	r.mu.Lock()
	credentials := r.runtimeCredentialsLocked()
	r.mu.Unlock()
	credential, err := credentials.Read(ctx, p.ID)
	if err != nil {
		return err
	}
	store := r.catalogStore()
	stored, err := store.Read(ctx, p.ID)
	if err != nil {
		return err
	}
	models := p.Models
	// upstream: models.ts Provider.getModels / getAllModels are the provider's current list, read at each use; a throwing implementation is an empty catalog.
	if current := nativeProviderCurrentModels(ctx, p); current != nil {
		models = *current
	}
	publish := func(publication ai.ModelsPublication) (bool, error) {
		state := r.catalogState(p.ID)
		state.publish.Lock()
		defer state.publish.Unlock()
		if !r.isCurrentCatalogRefresh(state, generation) {
			return false, context.Canceled
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if r.NativeProvider(p.ID) != p {
			return false, errors.New("native provider registration was replaced")
		}
		if publication.Persist != nil {
			if err := store.Write(ctx, p.ID, *publication.Persist); err != nil {
				return false, err
			}
		} else if publication.PersistSet {
			if err := store.Delete(ctx, p.ID); err != nil {
				return false, err
			}
		}
		if publication.Update != nil {
			publication.Update()
		}
		// upstream: the provider's list after its update runs is the catalog the registry publishes
		if current := nativeProviderCurrentModels(ctx, p); current != nil {
			parsed, ok := providerConfigFromRegistration(extension.ProviderConfig{Name: p.Name, BaseURL: p.BaseURL, Models: *current})
			if !ok {
				return false, errors.New("invalid native model publication")
			}
			r.mu.Lock()
			if r.native[p.ID].provider == p {
				r.dynamic[p.ID] = parsed
			}
			listener := r.onChange
			r.mu.Unlock()
			if !backgroundRefresh(ctx) {
				listener.publish()
			}
		}
		return true, nil
	}
	refresh := func(credential *ai.Credential, stored *ai.ModelsStoreEntry, network bool, force *bool) error {
		return p.RefreshModels(ai.RefreshModelsContext{Credential: credential, Stored: stored, Publish: publish, AllowNetwork: network, Force: force, Signal: ctx})
	}
	if p.RefreshModels != nil {
		if err = refresh(credential, stored, false, nil); err != nil {
			return err
		}
		if current := nativeProviderCurrentModels(ctx, p); current != nil {
			models = *current
		}
		if network {
			var refreshCredential *ai.Credential
			_, err = credentials.Modify(ctx, p.ID, func(current *ai.Credential) (*ai.Credential, error) {
				// The transaction owns this overlay; reacquiring r.mu here inverts the registry-to-credential lock order used by catalog readers.
				if key, ok := credentials.RuntimeAPIKey(p.ID); ok {
					current = &ai.Credential{Type: ai.CredentialAPIKey, Key: key}
				}
				next, update, resolveErr := p.ResolveRefreshCredential(ctx, current)
				refreshCredential = next
				return update, resolveErr
			})
			if err != nil {
				return err
			}
			if refreshCredential != nil {
				credential = refreshCredential
				stored, err = store.Read(ctx, p.ID)
				if err != nil {
					return err
				}
				if err = refresh(credential, stored, true, force); err != nil {
					return err
				}
				if current := nativeProviderCurrentModels(ctx, p); current != nil {
					models = *current
				}
			}
		}
	}
	credential, err = credentials.Read(ctx, p.ID)
	if err != nil {
		return err
	}
	check, err := p.CheckAuth(ctx, credential)
	if err != nil {
		return err
	}
	available := []string{}
	if check != nil {
		typed, err := nativeTypedModels(p.ID, models)
		if err != nil {
			return err
		}
		visible, err := nativeVisibleModels(ctx, p, typed, credential)
		if err != nil {
			return err
		}
		for _, model := range visible {
			available = append(available, model.ModelID())
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	parsed, ok := providerConfigFromRegistration(extension.ProviderConfig{Name: p.Name, BaseURL: p.BaseURL, Models: models})
	if !ok {
		return errors.New("invalid native provider refreshed models")
	}
	r.mu.Lock()
	if r.native[p.ID].provider == p {
		r.dynamic[p.ID] = parsed
		r.native[p.ID] = registeredNativeProvider{provider: p, check: check, available: available}
	}
	r.mu.Unlock()
	return nil
}

// nativeVisibleModels is the models of the list a credential can use. A provider's filterAllModels filters every model type; without it filterModels filters the chat models and every other model is kept.
// upstream: packages/ai/src/models.ts:730 (getAllAvailable), :720-735
func nativeVisibleModels(ctx context.Context, p *extension.NativeProvider, models []ai.AnyModel, credential *ai.Credential) ([]ai.AnyModel, error) {
	if p.FilterAllModels != nil {
		return p.FilterAllModels(ctx, models, credential)
	}
	if p.FilterModels == nil {
		return models, nil
	}
	// models.ts:732-735: filterModels sees the provider's chat models, and the result keeps the listed models in order, dropping only the chat
	// models whose ids it did not return.
	var chat []*ai.Model
	if p.GetModels != nil {
		listed, err := p.GetModels(ctx)
		if err != nil {
			return nil, err
		}
		chat = listed
	} else {
		for _, model := range models {
			if typed, ok := model.(*ai.Model); ok {
				chat = append(chat, typed)
			}
		}
	}
	kept, err := p.FilterModels(ctx, chat, credential)
	if err != nil {
		return nil, err
	}
	availableChatIDs := make(map[string]bool, len(kept))
	for _, model := range kept {
		availableChatIDs[model.ID] = true
	}
	visible := make([]ai.AnyModel, 0, len(models))
	for _, model := range models {
		if typed, ok := model.(*ai.Model); !ok || availableChatIDs[typed.ID] {
			visible = append(visible, model)
		}
	}
	return visible, nil
}

// nativeTypedModels reads registered model configs as Pi's models of the provider: the config JSON is the model record without its provider id.
func nativeTypedModels(providerID string, configs []extension.ProviderModelConfig) ([]ai.AnyModel, error) {
	records := make([]json.RawMessage, len(configs))
	for i, config := range configs {
		data, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		id, err := json.Marshal(providerID)
		if err != nil {
			return nil, err
		}
		fields["provider"] = id
		if records[i], err = json.Marshal(fields); err != nil {
			return nil, err
		}
	}
	models, err := ai.DecodeStoredModels(records)
	if err != nil {
		return nil, err
	}
	return ai.DecodeModelsCatalog(models, providerID)
}

// nativeModelConfigs is the inverse of nativeTypedModels: the registry keeps a provider's catalog as model configs.
func nativeModelConfigs(models []ai.AnyModel) ([]extension.ProviderModelConfig, error) {
	records, err := ai.EncodeModelsCatalog(models)
	if err != nil {
		return nil, err
	}
	configs := make([]extension.ProviderModelConfig, len(records))
	for i, record := range records {
		if err := json.Unmarshal(record, &configs[i]); err != nil {
			return nil, err
		}
	}
	return configs, nil
}

// nativeProviderCurrentModels asks the provider object for its current models (every type when it lists them, otherwise its chat models). It returns nil when the object has neither member.
func nativeProviderCurrentModels(ctx context.Context, p *extension.NativeProvider) *[]extension.ProviderModelConfig {
	var models []ai.AnyModel
	switch {
	case p.GetAllModels != nil:
		models, _ = p.GetAllModels(ctx)
	case p.GetModels != nil:
		chat, _ := p.GetModels(ctx)
		for _, model := range chat {
			models = append(models, model)
		}
	default:
		return nil
	}
	configs, err := nativeModelConfigs(models)
	if err != nil || configs == nil {
		configs = []extension.ProviderModelConfig{}
	}
	return &configs
}
