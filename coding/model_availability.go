// Ports packages/coding-agent/src/core/model-runtime.ts
package coding

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type modelRuntimeSnapshot struct {
	available       []*ai.Model
	storedProviders map[string]bool
	// configured is Pi's snapshot.configuredProviders: the providers whose check returned an auth result at the time of the pass that published it. Consumers read it, not auth, for hasConfiguredAuth.
	configured map[string]bool
	auth       map[string]*ai.AuthCheck
}

type modelAvailability struct {
	mu                   sync.RWMutex
	refreshSeq, errorSeq uint64
	// catalogSeq orders synchronous catalog projections; only the latest one commits its catalog read.
	catalogSeq      uint64
	providerSeq     map[string]uint64
	snapshot        modelRuntimeSnapshot
	err             string
	listCredentials func(context.Context) ([]ai.CredentialInfo, error)
	readCredential  func(context.Context, string) (*ai.Credential, error)
	getAvailable    func(context.Context, string) ([]*ai.Model, error)
}

// GetAvailable fails fast on the first availability rejection while Services owns the remaining work. Success waits for fresh availability and publishes a full pass only if no newer full/provider synchronization superseded it. A provider-only query returns its own result without replacing the shared snapshot.
func (runtime *ModelRuntime) GetAvailable(ctx context.Context, providerID ...string) ([]*ai.Model, error) {
	if ctx == nil {
		return nil, fmt.Errorf("model runtime: nil context")
	}
	if runtime == nil || runtime.services == nil {
		return nil, ErrNoServices
	}
	id := ""
	if len(providerID) > 0 {
		id = providerID[0]
	}
	if id == "" {
		runtime.services.Registry().YieldToRegistrationRefresh(ctx)
	} else {
		runtime.services.Registry().StartRegistrationRefresh(ctx)
	}
	ctx = runtime.services.Registry().ModelTaskContext(ctx)
	if id == "" {
		if err := runtime.queueAvailabilityRefresh(ctx); err != nil {
			return nil, err
		}
		return runtime.GetAvailableSnapshot(), nil
	}
	state := &runtime.availability
	state.mu.Lock()
	state.errorSeq++
	seq := state.errorSeq
	state.mu.Unlock()
	getAvailable := state.getAvailable
	var available []*ai.Model
	err := runtime.services.Registry().AwaitModelTasks(ctx, func(ctx context.Context) error {
		var err error
		available, err = getAvailable(ctx, id)
		return err
	})
	state.mu.Lock()
	defer state.mu.Unlock()
	if seq == state.errorSeq {
		if err == nil {
			state.err = ""
		} else if ctx.Err() == nil {
			state.err = err.Error()
		}
	}
	if err != nil {
		return nil, err
	}
	return available, nil
}

// syncRegistration mirrors the synchronous part of Pi's registerProvider, registerNativeProvider and unregisterProvider. The queued local refresh that follows is owned by the registry (internal/codingagent/model_refresh_background.go).
//
// The reservation invalidates full and provider availability passes that read the catalog before this change, so a stale pass cannot publish a snapshot without it.
// upstream: packages/coding-agent/src/core/model-runtime.ts:registerProvider,updateModelSnapshot
func (runtime *ModelRuntime) syncRegistration(providerID string, provisional *ai.AuthCheck, providerOrder func() []string) {
	state := &runtime.availability
	state.mu.Lock()
	state.refreshSeq++
	state.providerSeq[providerID]++
	state.mu.Unlock()
	runtime.updateModelSnapshot(providerID, provisional, providerOrder)
}

// updateModelSnapshot projects the catalog in the current provider order without credential I/O, as Pi's updateModelSnapshot does: every provider's catalog is read again, and every provider whose published snapshot holds an auth check is available with its whole catalog. A provisional check marks the changed provider configured when auth.json stores it or its request auth is configured, and never replaces a published check. Account-filtered membership of other providers widens until the queued local refresh recomputes it, as in Pi.
// upstream: packages/coding-agent/src/core/model-runtime.ts:updateModelSnapshot,registerProvider
func (runtime *ModelRuntime) updateModelSnapshot(providerID string, provisional *ai.AuthCheck, providerOrder func() []string) {
	configured := false
	if provisional != nil {
		status, _ := runtime.services.Registry().ConfiguredRequestAuthStatus(providerID)
		configured = status.Configured
	}
	state := &runtime.availability
	state.mu.Lock()
	if provisional != nil && (configured || state.snapshot.storedProviders[providerID]) && state.snapshot.auth[providerID] == nil {
		auth := maps.Clone(state.snapshot.auth)
		if auth == nil {
			auth = map[string]*ai.AuthCheck{}
		}
		auth[providerID] = provisional
		state.snapshot.auth = auth
		configured := maps.Clone(state.snapshot.configured)
		if configured == nil {
			configured = map[string]bool{}
		}
		configured[providerID] = true
		state.snapshot.configured = configured
	}
	state.catalogSeq++
	seq := state.catalogSeq
	state.mu.Unlock()
	// Read outside the lock: provider catalog callbacks may re-enter the runtime. The order is read after the sequence is taken, so the latest projection sees every registry change whose projection took an earlier sequence.
	// Only a configured provider's models are published. Providers backed by provider code are read in order so their callbacks run as Pi's getModels runs them; the others are composed once they are known to be configured.
	order := providerOrder()
	groups := make(map[string][]*ai.Model, len(order))
	readCatalog := func(id string) {
		models := runtime.services.Registry().GetProviderModelData(id)
		for i, model := range models {
			models[i] = runtime.bindCatalogModel(model)
		}
		groups[id] = models
	}
	for _, id := range order {
		if runtime.services.Registry().GetProvider(id) != nil {
			readCatalog(id)
		}
	}
	state.mu.Lock()
	for {
		var missing []string
		for _, id := range order {
			if _, read := groups[id]; !read && state.snapshot.configured[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			break
		}
		state.mu.Unlock()
		for _, id := range missing {
			readCatalog(id)
		}
		state.mu.Lock()
	}
	defer state.mu.Unlock()
	if seq != state.catalogSeq {
		return
	}
	available := []*ai.Model{}
	for _, id := range order {
		if state.snapshot.configured[id] {
			available = append(available, groups[id]...)
		}
	}
	state.snapshot.available = available
}

// configuredInSnapshot is Pi's ModelRuntime.hasConfiguredAuth: providerID is in the published snapshot's configured set.
func (runtime *ModelRuntime) configuredInSnapshot(providerID string) bool {
	state := &runtime.availability
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.snapshot.configured[providerID]
}

// GetAvailableSnapshot returns the last published availability without credential or catalog I/O. Provider registration and removal update it before returning.
func (runtime *ModelRuntime) GetAvailableSnapshot() []*ai.Model {
	state := &runtime.availability
	state.mu.RLock()
	defer state.mu.RUnlock()
	return slices.Clone(state.snapshot.available)
}

// GetError joins model configuration errors and the latest non-cancelled availability failure.
func (runtime *ModelRuntime) GetError() string {
	var messages []string
	if text := runtime.services.Registry().LoadError(); text != "" {
		messages = append(messages, text)
	}
	state := &runtime.availability
	state.mu.RLock()
	text := state.err
	state.mu.RUnlock()
	if text != "" {
		messages = append(messages, "Availability refresh: "+text)
	}
	return strings.Join(messages, "\n\n")
}

// IsUsingOAuth reports the auth type in the latest published availability snapshot.
func (runtime *ModelRuntime) IsUsingOAuth(providerID string) bool {
	state := &runtime.availability
	state.mu.RLock()
	defer state.mu.RUnlock()
	check := state.snapshot.auth[providerID]
	return check != nil && check.Type == ai.CredentialOAuth
}

// IsUsingSubscription requires both published OAuth usage and the provider's current subscription metadata.
func (runtime *ModelRuntime) IsUsingSubscription(providerID string) bool {
	return runtime.IsUsingOAuth(providerID) && runtime.services.Registry().ProviderIsSubscription(providerID)
}

// GetProviderAuthStatus uses the published availability snapshot for stored credentials. A stale operation cannot reintroduce deleted credential metadata.
func (runtime *ModelRuntime) GetProviderAuthStatus(providerID string) ai.AuthStatus {
	if _, ok := runtime.services.Registry().RuntimeAPIKey(providerID); ok {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceRuntime}
	}
	state := &runtime.availability
	state.mu.RLock()
	stored := state.snapshot.storedProviders[providerID]
	auth := state.snapshot.auth[providerID]
	state.mu.RUnlock()
	if stored {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored}
	}
	if configured, ok := runtime.services.Registry().ConfiguredRequestAuthStatus(providerID); ok {
		return configured
	}
	if auth != nil {
		return ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: auth.Source}
	}
	return ai.AuthStatus{}
}

func (runtime *ModelRuntime) loadAvailableModels(ctx context.Context, providerID string) ([]*ai.Model, error) {
	models, err := runtime.services.Registry().GetAvailableModelDataContext(ctx, providerID)
	for i, model := range models {
		models[i] = runtime.bindCatalogModel(model)
	}
	return models, err
}

// queueAvailabilityRefresh assigns publication generations when the availability pass starts, after any catalog work. Only a later availability pass invalidates this pass.
func (runtime *ModelRuntime) queueAvailabilityRefresh(ctx context.Context) error {
	ctx = runtime.services.Registry().ModelTaskContext(ctx)
	state := &runtime.availability
	state.mu.Lock()
	state.refreshSeq++
	seq := state.refreshSeq
	state.errorSeq++
	errorSeq := state.errorSeq
	for provider, version := range state.providerSeq {
		state.providerSeq[provider] = version + 1
	}
	state.mu.Unlock()
	var available []*ai.Model
	var auth map[string]*ai.AuthCheck
	var credentials []ai.CredentialInfo
	getAvailable, listCredentials := state.getAvailable, state.listCredentials
	err := runtime.services.Registry().AwaitModelTasks(ctx,
		func(ctx context.Context) error {
			var err error
			available, err = getAvailable(ctx, "")
			return err
		},
		func(ctx context.Context) error {
			var err error
			auth, err = runtime.services.Registry().GetProviderAuthChecks(ctx)
			return err
		},
		func(ctx context.Context) error {
			var err error
			credentials, err = listCredentials(ctx)
			return err
		},
	)
	if err != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if errorSeq == state.errorSeq && ctx.Err() == nil {
			state.err = err.Error()
		}
		return err
	}
	icodingagent.ReadNativeProviderModelData(runtime.services.Registry().ModelRegistry)
	state.mu.Lock()
	defer state.mu.Unlock()
	if seq != state.refreshSeq {
		return nil
	}
	stored := make(map[string]bool, len(credentials))
	for _, credential := range credentials {
		stored[credential.ProviderID] = true
	}
	state.snapshot = modelRuntimeSnapshot{available: available, storedProviders: stored, configured: configuredProviders(auth), auth: auth}
	if errorSeq == state.errorSeq {
		state.err = ""
	}
	return nil
}

// refreshAvailability reconciles availability after catalog refresh. Full-pass failures remain visible through GetError without discarding usable catalogs.
func (runtime *ModelRuntime) refreshAvailability(ctx context.Context, options ai.ModelsRefreshOptions, result ai.ModelsRefreshResult) ai.ModelsRefreshResult {
	ctx = runtime.services.Registry().ModelTaskContext(ctx)
	if options.Providers == nil {
		_ = runtime.queueAvailabilityRefresh(ctx)
	} else {
		ids := slices.Clone(options.Providers)
		seen := map[string]bool{}
		type providerResult struct {
			id  string
			err error
		}
		settled := make(chan providerResult, len(ids))
		var tasks sync.WaitGroup
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			state := &runtime.availability
			state.mu.Lock()
			state.refreshSeq++
			state.providerSeq[id]++
			seq := state.providerSeq[id]
			state.errorSeq++
			errorSeq := state.errorSeq
			state.mu.Unlock()
			tasks.Go(func() {
				settled <- providerResult{id: id, err: runtime.refreshProviderAvailability(ctx, id, seq, errorSeq)}
			})
		}
		for range len(seen) {
			outcome := <-settled
			if outcome.err != nil && ctx.Err() == nil {
				if result.Errors == nil {
					result.Errors = map[string]error{}
				}
				if _, exists := result.Errors[outcome.id]; !exists {
					result.ErrorOrder = append(result.ErrorOrder, outcome.id)
				}
				result.Errors[outcome.id] = outcome.err
			}
		}
		tasks.Wait()
	}
	result.Aborted = result.Aborted || ctx.Err() != nil
	return result
}

func (runtime *ModelRuntime) refreshProviderAvailability(ctx context.Context, providerID string, seq, errorSeq uint64) error {
	ctx = runtime.services.Registry().ModelTaskContext(ctx)
	state := &runtime.availability
	var available []*ai.Model
	var credential *ai.Credential
	var auth *ai.AuthCheck
	getAvailable, readCredential := state.getAvailable, state.readCredential
	err := runtime.services.Registry().AwaitModelTasks(ctx,
		func(ctx context.Context) error {
			var err error
			available, err = getAvailable(ctx, providerID)
			return err
		},
		func(ctx context.Context) error {
			var err error
			credential, err = readCredential(ctx, providerID)
			return err
		},
		func(ctx context.Context) error {
			var err error
			auth, err = runtime.CheckAuth(ctx, providerID)
			return err
		},
	)
	if err != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.providerSeq[providerID] == seq && errorSeq == state.errorSeq && ctx.Err() == nil {
			state.err = err.Error()
		}
		return err
	}
	all := runtime.GetModels()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.providerSeq[providerID] != seq {
		return nil
	}
	stored := maps.Clone(state.snapshot.storedProviders)
	if stored == nil {
		stored = map[string]bool{}
	}
	if credential == nil {
		delete(stored, providerID)
	} else {
		stored[providerID] = true
	}
	checks := maps.Clone(state.snapshot.auth)
	if checks == nil {
		checks = map[string]*ai.AuthCheck{}
	}
	checks[providerID] = auth
	configured := maps.Clone(state.snapshot.configured)
	if configured == nil {
		configured = map[string]bool{}
	}
	if auth != nil {
		configured[providerID] = true
	} else {
		delete(configured, providerID)
	}
	byID := map[string]*ai.Model{}
	for _, model := range state.snapshot.available {
		if model.ProviderMeta.ProviderID != providerID {
			byID[model.ProviderMeta.ProviderID+"\x00"+model.ID] = model
		}
	}
	for _, model := range available {
		byID[model.ProviderMeta.ProviderID+"\x00"+model.ID] = model
	}
	selected := make([]*ai.Model, 0, len(byID))
	for _, model := range all {
		if value := byID[model.ProviderMeta.ProviderID+"\x00"+model.ID]; value != nil {
			selected = append(selected, value)
		}
	}
	state.snapshot = modelRuntimeSnapshot{available: selected, storedProviders: stored, configured: configured, auth: checks}
	if errorSeq == state.errorSeq {
		state.err = ""
	}
	return nil
}

// configuredProviders derives the set of providers whose check returned an auth result, as Pi builds configuredProviders from the same checks.
// upstream: packages/coding-agent/src/core/model-runtime.ts:refreshAvailability
func configuredProviders(checks map[string]*ai.AuthCheck) map[string]bool {
	configured := make(map[string]bool, len(checks))
	for id, check := range checks {
		if check != nil {
			configured[id] = true
		}
	}
	return configured
}
