package ai

import (
	"context"
	"maps"
	"slices"
	"sync"
)

func (m *Models) Refresh(ctx context.Context, options ...ModelsRefreshOptions) ModelsRefreshResult {
	var opts ModelsRefreshOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result := ModelsRefreshResult{Errors: map[string]error{}}
	if ctx.Err() != nil {
		result.Aborted = true
		return result
	}
	allowNetwork := opts.AllowNetwork == nil || *opts.AllowNetwork
	var errorsMu sync.Mutex
	var wg sync.WaitGroup
	for _, provider := range m.GetProviders() {
		if provider.RefreshModels == nil || (opts.Providers != nil && !slices.Contains(opts.Providers, provider.ID)) {
			continue
		}
		generation, signal := m.beginProviderRefresh(ctx, provider)
		if signal == nil {
			continue
		}
		wg.Go(func() {
			_, err := awaitModelsOperation(signal, &m.operations, func() (struct{}, error) {
				stored, credentialErr := readProviderCredential(signal, m.credentials, provider.ID)
				if err := m.runProviderRefreshPhase(provider, stored, false, nil, generation, signal); err != nil {
					return struct{}{}, err
				}
				if credentialErr != nil {
					return struct{}{}, credentialErr
				}
				if !allowNetwork || signal.Err() != nil {
					return struct{}{}, nil
				}
				credential, err := m.resolveRefreshCredential(signal, provider, stored)
				if err != nil || credential == nil {
					return struct{}{}, err
				}
				return struct{}{}, m.runProviderRefreshPhase(provider, credential, true, opts.Force, generation, signal)
			})
			if err != nil && signal.Err() == nil {
				errorsMu.Lock()
				result.Errors[provider.ID] = err
				result.ErrorOrder = append(result.ErrorOrder, provider.ID)
				errorsMu.Unlock()
			}
			m.mu.Lock()
			if m.refreshControllers[provider.ID] == signal {
				delete(m.refreshControllers, provider.ID)
			}
			m.mu.Unlock()
		})
	}
	done := make(chan struct{})
	m.operations.Go(func() { wg.Wait(); close(done) })
	select {
	case <-done:
	case <-ctx.Done():
	}
	errorsMu.Lock()
	defer errorsMu.Unlock()
	return ModelsRefreshResult{Aborted: ctx.Err() != nil, Errors: maps.Clone(result.Errors), ErrorOrder: slices.Clone(result.ErrorOrder)}
}

func (m *Models) runProviderRefreshPhase(provider *ModelsProvider, credential *Credential, allowNetwork bool, force *bool, generation uint64, signal context.Context) error {
	stored, err := m.modelsStore.Read(signal, provider.ID)
	if err != nil {
		return err
	}
	if stored != nil {
		stored = withKnownModelTypes(new(stored.Clone()))
	}
	return provider.RefreshModels(RefreshModelsContext{Credential: credential, Stored: stored, AllowNetwork: allowNetwork, Force: force, Signal: signal, Publish: func(publication ModelsPublication) (bool, error) {
		return m.publishProviderModels(provider.ID, generation, signal, publication)
	}})
}

func (m *Models) publishProviderModels(providerID string, generation uint64, signal context.Context, publication ModelsPublication) (bool, error) {
	m.mu.Lock()
	previous := m.publicationChains[providerID]
	done := make(chan struct{})
	m.publicationChains[providerID] = done
	m.mu.Unlock()
	result := make(chan modelsOperationResult[bool], 1)
	m.operations.Go(func() {
		defer func() {
			m.mu.Lock()
			if m.publicationChains[providerID] == done {
				delete(m.publicationChains, providerID)
			}
			close(done)
			m.mu.Unlock()
		}()
		if previous != nil {
			<-previous
		}
		current := func() bool {
			m.mu.RLock()
			defer m.mu.RUnlock()
			return signal.Err() == nil && m.refreshGenerations[providerID] == generation
		}
		if !current() {
			result <- modelsOperationResult[bool]{}
			return
		}
		var err error
		if publication.Persist != nil {
			err = m.modelsStore.Write(signal, providerID, publication.Persist.Clone())
		} else if publication.PersistSet {
			err = m.modelsStore.Delete(signal, providerID)
		}
		if err != nil {
			result <- modelsOperationResult[bool]{err: err}
			return
		}
		m.mu.Lock()
		if signal.Err() != nil || m.refreshGenerations[providerID] != generation {
			m.mu.Unlock()
			result <- modelsOperationResult[bool]{}
			return
		}
		if publication.Update != nil {
			publication.Update()
		}
		m.mu.Unlock()
		result <- modelsOperationResult[bool]{value: true}
	})
	if signal.Err() != nil {
		return false, context.Cause(signal)
	}
	select {
	case next := <-result:
		return next.value, next.err
	case <-signal.Done():
		return false, context.Cause(signal)
	}
}

func (m *Models) resolveRefreshCredential(ctx context.Context, provider *ModelsProvider, stored *Credential) (*Credential, error) {
	if stored != nil && stored.Type == CredentialOAuth {
		oauth := provider.Auth.OAuth
		if oauth == nil {
			return nil, nil
		}
		if float64(nowMillis()) < stored.ExpiresMillis() {
			return stored, nil
		}
		if ctx.Err() != nil {
			return nil, nil
		}
		post, err := m.credentials.Modify(ctx, provider.ID, func(current *Credential) (*Credential, error) {
			if current == nil || current.Type != CredentialOAuth || float64(nowMillis()) < current.ExpiresMillis() {
				return nil, nil
			}
			updated, err := oauth.Refresh(ctx, *current)
			if err != nil {
				return nil, err
			}
			return &updated, nil
		})
		if err != nil || post == nil || post.Type != CredentialOAuth {
			return nil, err
		}
		return post, nil
	}
	apiKey := provider.Auth.APIKey
	if apiKey == nil {
		return nil, nil
	}
	var credential *Credential
	if stored != nil && stored.Type == CredentialAPIKey {
		credential = stored
	}
	result, err := apiKey.Resolve(ctx, APIKeyAuthInput{Ctx: m.authContext, Credential: credential})
	if err != nil || result == nil {
		return nil, err
	}
	return &Credential{Type: CredentialAPIKey, Key: result.Auth.APIKey, Env: result.Env}, nil
}
