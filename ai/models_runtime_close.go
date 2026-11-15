package ai

// Ports packages/ai/src/models.ts (Models.close and the refresh controllers a provider refresh registers).

import "context"

// Close cancels active refreshes and joins every operation the collection runs, including a caller's login and stream. Completed refresh signals keep their existing lifetime semantics.
func (m *Models) Close() {
	m.cancelRefreshes()
	m.operations.Wait()
}

// Drain stops the collection admitting owned work, cancels active refreshes and joins the work admitted before it: provider refreshes with their store and
// credential writes, auth resolution and checks, availability, and the credential writes of any auth resolution. It does not wait for a login or the callbacks
// of a stream's auth, which end with their callers' contexts. Pi lets such work run on after its signal aborts (raceWithAbortSignal) because its process
// exits; an owner that removes the directory the work writes to cannot.
func (m *Models) Drain() {
	m.mu.Lock()
	m.draining = true
	for _, signal := range m.refreshControllers {
		signal.cancel(context.Canceled)
	}
	m.mu.Unlock()
	m.owned.Wait()
}

func (m *Models) cancelRefreshes() {
	m.mu.Lock()
	for _, signal := range m.refreshControllers {
		signal.cancel(context.Canceled)
	}
	m.mu.Unlock()
}

// beginProviderRefresh rejects a stale provider snapshot before allocating a new generation.
func (m *Models) beginProviderRefresh(ctx context.Context, provider *ModelsProvider) (uint64, *modelsRefreshSignal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.draining || m.providers[provider.ID] != provider {
		return 0, nil
	}
	generation := m.supersedeProviderRefreshLocked(provider.ID)
	signal := newModelsRefreshSignal(ctx)
	m.refreshControllers[provider.ID] = signal
	return generation, signal
}
