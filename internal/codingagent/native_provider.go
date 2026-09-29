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
	listener := r.onChange
	r.mu.Unlock()
	extension.CallInitiated(ctx)
	// Pi's registerNativeProvider stores, recomposes and republishes the snapshot, then starts its unawaited refresh (model-runtime.ts:744-750). Authentication and catalog refresh run in that refresh.
	r.syncRegistration(p.ID, nil)
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
	publish := func(publication extension.NativeProviderPublication) error {
		state := r.catalogState(p.ID)
		state.publish.Lock()
		defer state.publish.Unlock()
		if !r.isCurrentCatalogRefresh(state, generation) {
			return context.Canceled
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.NativeProvider(p.ID) != p {
			return errors.New("native provider registration was replaced")
		}
		if len(publication.Persist) > 0 {
			if string(publication.Persist) == "null" {
				if err := store.Delete(ctx, p.ID); err != nil {
					return err
				}
			} else {
				var entry ai.ModelsStoreEntry
				if err := json.Unmarshal(publication.Persist, &entry); err != nil {
					return err
				}
				if err := store.Write(ctx, p.ID, entry); err != nil {
					return err
				}
			}
		}
		if publication.Models != nil {
			parsed, ok := providerConfigFromRegistration(extension.ProviderConfig{Name: p.Name, BaseURL: p.BaseURL, Models: *publication.Models})
			if !ok {
				return errors.New("invalid native model publication")
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
		return nil
	}
	if p.RefreshModels != nil {
		models, err = p.RefreshModels(ctx, credential, stored, false, nil, publish)
		if err != nil {
			return err
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
				models, err = p.RefreshModels(ctx, credential, stored, true, force, publish)
				if err != nil {
					return err
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
		visible := models
		if p.FilterModels != nil {
			visible, err = p.FilterModels(ctx, models, credential)
			if err != nil {
				return err
			}
		}
		for _, model := range visible {
			available = append(available, model.ID)
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
