package codingagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// blockedRefresh is a registry whose catalog refresh for one provider family holds until release runs; waitStarted returns once the refresh has begun.
type blockedRefresh struct {
	registry    *ModelRegistry
	waitStarted func()
	release     func()
}

// refreshFamilies are the provider shapes a catalog refresh goes through the coordinator for: a Radius catalog under its own id, a models.json OAuth gateway
// under another id and base URL, and a native Models provider an extension registers. None of them is named by the coordinator.
var refreshFamilies = []struct {
	name  string
	block func(t *testing.T, ignoreCancel bool) blockedRefresh
}{
	{"radius catalog", func(t *testing.T, ignoreCancel bool) blockedRefresh {
		registry, _, store := radiusTestRegistry(t, "", nil)
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), ignoreCancel: ignoreCancel}
		registry.SetModelsStore(blocked)
		return blockedRefresh{registry, func() { <-blocked.started }, func() { close(blocked.release) }}
	}},
	{"models.json gateway", func(t *testing.T, ignoreCancel bool) blockedRefresh {
		registry, _, store := radiusTestRegistry(t, `{"providers":{"gateway":{"baseUrl":"https://gateway.invalid/v1","oauth":"radius"}}}`, nil)
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), ignoreCancel: ignoreCancel, providerID: "gateway"}
		registry.SetModelsStore(blocked)
		return blockedRefresh{registry, func() { <-blocked.started }, func() { close(blocked.release) }}
	}},
	{"native provider", func(t *testing.T, ignoreCancel bool) blockedRefresh {
		registry, _, _ := radiusTestRegistry(t, "", nil)
		var armed atomic.Bool
		started, release := make(chan struct{}, 4), make(chan struct{})
		provider := credentialTestProvider("held-native")
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			if !armed.Load() {
				return nil
			}
			started <- struct{}{}
			if ignoreCancel {
				<-release
				return nil
			}
			select {
			case <-release:
				return nil
			case <-refresh.Signal.Done():
				return context.Cause(refresh.Signal)
			}
		}
		if err := registry.RegisterNativeModelsProvider(provider); err != nil {
			t.Fatal(err)
		}
		// The registration queues Pi's unawaited local refresh; it ends before the provider holds.
		registry.YieldToRegistrationRefresh(t.Context())
		armed.Store(true)
		return blockedRefresh{registry, func() { <-started }, func() { close(release) }}
	}},
}

// The coordinator is process-wide, as Pi's module-level one is, and keyed by registry as Pi's WeakMap is. Pi's entry dies with its runtime; a Go map's
// key keeps its registry reachable, so the entry must be deleted on every path that ends a refresh: a registry dropped without a close (an SDK host, an
// RPC server, a sub-agent) must not stay reachable through the coordinator for the life of the process. Every path runs for each provider family.
func TestModelCatalogRefreshesDoNotRetainRegistries(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	scenarios := []struct {
		name         string
		ignoreCancel bool
		run          func(t *testing.T, b blockedRefresh)
	}{
		{"settled and never closed", false, func(t *testing.T, b blockedRefresh) {
			b.release()
			if _, err := RefreshModelCatalogs(t.Context(), b.registry); err != nil {
				t.Fatal(err)
			}
		}},
		{"abandoned by its only caller", false, func(t *testing.T, b blockedRefresh) {
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(ctx, b.registry); result <- err }()
			b.waitStarted()
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("abandoned refresh = %v", err)
			}
			settleModelTasks(b.registry)
		}},
		{"abandoned while the provider ignores cancellation", true, func(t *testing.T, b blockedRefresh) {
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(ctx, b.registry); result <- err }()
			b.waitStarted()
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("abandoned refresh = %v", err)
			}
			b.release()
			settleModelTasks(b.registry)
		}},
		{"closed while a caller waits", false, func(t *testing.T, b blockedRefresh) {
			result := make(chan CatalogRefreshResult, 1)
			go func() { r, _ := RefreshModelCatalogs(t.Context(), b.registry); result <- r }()
			b.waitStarted()
			b.registry.CloseModelTasks()
			<-result
		}},
	}
	for _, family := range refreshFamilies {
		t.Run(family.name, func(t *testing.T) {
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					blocked := family.block(t, scenario.ignoreCancel)
					scenario.run(t, blocked)
					modelCatalogRefreshes.mu.Lock()
					held := modelCatalogRefreshes.activeByRuntime[blocked.registry] != nil
					modelCatalogRefreshes.mu.Unlock()
					if held {
						t.Error("coordinator still holds the registry after its refresh ended")
					}
				})
			}
		})
	}
}
