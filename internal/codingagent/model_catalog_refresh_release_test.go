package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The coordinator is process-wide, as Pi's module-level one is, and keyed by registry as Pi's WeakMap is. Pi's entry dies with its runtime; a Go map's
// key keeps its registry reachable, so the entry must be deleted on every path that ends a refresh: a registry dropped without a close (an SDK host, an
// RPC server, a sub-agent) must not stay reachable through the coordinator for the life of the process.
func TestModelCatalogRefreshesDoNotRetainRegistries(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	scenarios := map[string]func(t *testing.T, registry *ModelRegistry, store *ai.InMemoryModelsStore){
		"settled and never closed": func(t *testing.T, registry *ModelRegistry, _ *ai.InMemoryModelsStore) {
			if _, err := RefreshModelCatalogs(t.Context(), registry); err != nil {
				t.Fatal(err)
			}
		},
		"abandoned by its only caller": func(t *testing.T, registry *ModelRegistry, store *ai.InMemoryModelsStore) {
			blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
			registry.SetModelsStore(blocked)
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(ctx, registry); result <- err }()
			<-blocked.started
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("abandoned refresh = %v", err)
			}
			settleModelTasks(registry)
		},
		"abandoned while its store ignores cancellation": func(t *testing.T, registry *ModelRegistry, store *ai.InMemoryModelsStore) {
			blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), ignoreCancel: true}
			registry.SetModelsStore(blocked)
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(ctx, registry); result <- err }()
			<-blocked.started
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("abandoned refresh = %v", err)
			}
			close(blocked.release)
			settleModelTasks(registry)
		},
		"closed while a caller waits": func(t *testing.T, registry *ModelRegistry, store *ai.InMemoryModelsStore) {
			blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
			registry.SetModelsStore(blocked)
			result := make(chan CatalogRefreshResult, 1)
			go func() { r, _ := RefreshModelCatalogs(t.Context(), registry); result <- r }()
			<-blocked.started
			registry.CloseModelTasks()
			<-result
		},
	}
	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, "", nil)
			run(t, registry, store)
			modelCatalogRefreshes.mu.Lock()
			held := modelCatalogRefreshes.activeByRuntime[registry] != nil
			modelCatalogRefreshes.mu.Unlock()
			if held {
				t.Error("coordinator still holds the registry after its refresh ended")
			}
		})
	}
}
