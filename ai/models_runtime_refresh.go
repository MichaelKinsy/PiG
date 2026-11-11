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
	refresh := &modelsRefresh{models: m, opts: opts, result: ModelsRefreshResult{Errors: map[string]error{}}}
	if ctx.Err() != nil {
		refresh.result.Aborted = true
		return refresh.result
	}
	refresh.allowNetwork = opts.AllowNetwork == nil || *opts.AllowNetwork
	// The snapshot and the value-only parent are looked up once: each signal reads them through its own chain otherwise.
	snapshot := fileReadSnapshotOf(ctx)
	values := context.WithoutCancel(ctx)
	// An offline refresh inside an operation that runs no extension code and reads only this package's own stores (ProvidersRunOnCaller) does no
	// I/O but its file reads, so its providers restore one after another on the caller and settle in provider order, as Pi's settle at one await
	// depth.
	inline := !refresh.allowNetwork && snapshot != nil && snapshot.onCaller
	for _, provider := range m.GetProviders() {
		if provider.RefreshModels == nil || (opts.Providers != nil && !slices.Contains(opts.Providers, provider.ID)) {
			continue
		}
		generation, signal := m.beginProviderRefresh(ctx, values, snapshot, provider)
		if signal == nil {
			continue
		}
		job := &providerRefresh{refresh: refresh, provider: provider, signal: signal, generation: generation}
		refresh.wg.Add(1)
		if signal.Err() != nil {
			job.settle(context.Cause(signal))
			continue
		}
		if inline {
			err := job.run()
			if signal.Err() != nil {
				err = context.Cause(signal)
			}
			job.settle(err)
			continue
		}
		// The operation runs on the collection's operations, and the refresh settles when it returns or when its signal is cancelled (Pi's
		// raceWithAbortSignal), whichever is first: the signal's AfterFunc settles a cancelled wait without a waiting goroutine.
		job.stop = context.AfterFunc(signal, job.settleCancelled)
		m.operations.Go(job.execute)
	}
	if !inline {
		done := make(chan struct{})
		m.operations.Go(func() { refresh.wg.Wait(); close(done) })
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	refresh.mu.Lock()
	defer refresh.mu.Unlock()
	return ModelsRefreshResult{Aborted: ctx.Err() != nil, Errors: maps.Clone(refresh.result.Errors), ErrorOrder: slices.Clone(refresh.result.ErrorOrder)}
}

// modelsRefresh is one Refresh call: the options it runs with and the errors its providers settle with.
type modelsRefresh struct {
	models       *Models
	opts         ModelsRefreshOptions
	allowNetwork bool
	wg           sync.WaitGroup
	mu           sync.Mutex // guards result
	result       ModelsRefreshResult
}

// providerRefresh is one provider's refresh inside a Refresh call, settled once, either by its operation or by its cancelled signal.
type providerRefresh struct {
	refresh    *modelsRefresh
	provider   *ModelsProvider
	signal     *modelsRefreshSignal
	generation uint64
	stop       func() bool
	once       sync.Once
}

func (job *providerRefresh) settle(err error) {
	job.once.Do(func() {
		refresh, provider, signal := job.refresh, job.provider, job.signal
		if err != nil && signal.Err() == nil {
			refresh.mu.Lock()
			refresh.result.Errors[provider.ID] = err
			refresh.result.ErrorOrder = append(refresh.result.ErrorOrder, provider.ID)
			refresh.mu.Unlock()
		}
		m := refresh.models
		m.mu.Lock()
		if m.refreshControllers[provider.ID] == signal {
			delete(m.refreshControllers, provider.ID)
		}
		m.mu.Unlock()
		refresh.wg.Done()
	})
}

func (job *providerRefresh) settleCancelled() { job.settle(context.Cause(job.signal)) }

func (job *providerRefresh) execute() {
	err := job.run()
	job.stop()
	if job.signal.Err() != nil {
		err = context.Cause(job.signal)
	}
	job.settle(err)
}

func (job *providerRefresh) run() error {
	m, provider, signal := job.refresh.models, job.provider, job.signal
	stored, credentialErr := readProviderCredential(signal, m.credentials, provider.ID)
	if err := m.runProviderRefreshPhase(provider, stored, false, nil, job.generation, signal); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if !job.refresh.allowNetwork || signal.Err() != nil {
		return nil
	}
	network, err := m.resolveNetworkRefresh(signal, provider, stored)
	if err != nil || network == nil {
		return err
	}
	return m.runProviderRefreshPhase(provider, network.credential, true, job.refresh.opts.Force, job.generation, signal)
}

func (m *Models) runProviderRefreshPhase(provider *ModelsProvider, credential *Credential, allowNetwork bool, force *bool, generation uint64, signal context.Context) error {
	stored, err := m.modelsStore.Read(signal, provider.ID)
	if err != nil {
		return err
	}
	if stored != nil {
		// Records of model types this version does not know stay aside in the entry and never reach the provider.
		stored = new(stored.Clone())
	}
	return provider.RefreshModels(RefreshModelsContext{Credential: credential, Stored: stored, AllowNetwork: allowNetwork, Force: force, Signal: signal, BaseURL: provider.BaseURL, Publish: func(publication ModelsPublication) (bool, error) {
		return m.publishProviderModels(provider.ID, generation, signal, publication)
	}})
}

func (m *Models) publishProviderModels(providerID string, generation uint64, signal context.Context, publication ModelsPublication) (bool, error) {
	m.mu.Lock()
	previous := m.publicationChains[providerID]
	done := make(chan struct{})
	m.publicationChains[providerID] = done
	m.mu.Unlock()
	if previous == nil && publication.Persist == nil && !publication.PersistSet {
		// No earlier publication to wait for and nothing to write: Pi's queued publication runs on an already-settled chain without I/O
		// (models.ts publishProviderModels), so it runs here rather than on a goroutine the caller would wait for.
		next := m.publishChained(providerID, generation, signal, publication, previous, done)
		if signal.Err() != nil {
			return false, context.Cause(signal)
		}
		return next.value, next.err
	}
	result := make(chan modelsOperationResult[bool], 1)
	m.operations.Go(func() { result <- m.publishChained(providerID, generation, signal, publication, previous, done) })
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

// publishChained runs one publication on its provider's chain after previous, closing done when it is over.
func (m *Models) publishChained(providerID string, generation uint64, signal context.Context, publication ModelsPublication, previous, done chan struct{}) modelsOperationResult[bool] {
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
		return modelsOperationResult[bool]{}
	}
	var err error
	if publication.Persist != nil {
		err = m.modelsStore.Write(signal, providerID, publication.Persist.Clone())
	} else if publication.PersistSet {
		err = m.modelsStore.Delete(signal, providerID)
	}
	if err != nil {
		return modelsOperationResult[bool]{err: err}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if signal.Err() != nil || m.refreshGenerations[providerID] != generation {
		return modelsOperationResult[bool]{}
	}
	if publication.Update != nil {
		publication.Update()
	}
	return modelsOperationResult[bool]{value: true}
}

// networkRefresh is the credential for the network refresh phase: nil for a provider that needs only to be configured.
type networkRefresh struct{ credential *Credential }

// resolveNetworkRefresh returns nil when the provider is not configured.
// upstream: packages/ai/src/models.ts:resolveNetworkRefresh
func (m *Models) resolveNetworkRefresh(ctx context.Context, provider *ModelsProvider, stored *Credential) (*networkRefresh, error) {
	if provider.RefreshModelsAuth == RefreshModelsAuthConfigured {
		check, err := m.checkProviderAuth(ctx, provider, stored)
		if err != nil || check == nil {
			return nil, err
		}
		return &networkRefresh{}, nil
	}
	credential, err := m.resolveRefreshCredential(ctx, provider, stored)
	if err != nil || credential == nil {
		return nil, err
	}
	return &networkRefresh{credential: credential}, nil
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
		// A refresh that has started survives cancellation or a superseding model refresh, so a rotated refresh token is always persisted. A newer refresh then sees the fresh credential.
		// upstream: packages/ai/src/models.ts:resolveRefreshCredential
		return refreshStoredOAuthCredential(ctx, m.credentials, provider.ID, oauth, func(current Credential) bool { return float64(nowMillis()) >= current.ExpiresMillis() })
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
