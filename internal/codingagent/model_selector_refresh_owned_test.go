package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts (`void this.refreshModels()`) and model-catalog-refresh.ts

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// lingeringCatalogStore ends a read when its context ends, after the time a store takes to finish the write it had started: the work a catalog refresh
// leaves behind in the agent directory when its callers cancel it.
type lingeringCatalogStore struct {
	*ai.InMemoryModelsStore
	started chan struct{}
	settled atomic.Bool
}

func (s *lingeringCatalogStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	if providerID == "radius" {
		select {
		case s.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		time.Sleep(time.Second)
		s.settled.Store(true)
		return nil, ctx.Err()
	}
	return s.InMemoryModelsStore.Read(ctx, providerID)
}

// Pi's refreshModelCatalogs lets the runtime's refresh run on after its last waiter aborted (model-catalog-refresh.ts), and nothing joins it. PiG's
// refresh is a task of the registry: closing the registry's model tasks waits for it, so the agent directory is not written after Close returns.
func TestAbandonedModelCatalogRefreshIsJoinedByRegistryClose(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		lingering := &lingeringCatalogStore{InMemoryModelsStore: store, started: make(chan struct{}, 4)}
		registry.SetModelsStore(lingering)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := RefreshModelCatalogs(ctx, registry); done <- err }()
		<-lingering.started
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned caller's error = %v, want cancellation", err)
		}

		registry.CloseModelTasks()

		if !lingering.settled.Load() {
			t.Error("CloseModelTasks returned while the abandoned catalog refresh was still working on the store")
		}
	})
}

// A registry whose model tasks are closed admits no refresh: the caller gets an aborted result, as Pi's runtime gets from an aborted signal, and
// nothing runs after Close.
func TestModelCatalogRefreshAfterRegistryCloseIsAborted(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		lingering := &lingeringCatalogStore{InMemoryModelsStore: store, started: make(chan struct{}, 4)}
		registry.SetModelsStore(lingering)
		registry.CloseModelTasks()

		result, err := RefreshModelCatalogs(t.Context(), registry)

		if err != nil || !result.Aborted {
			t.Errorf("refresh after Close = %+v, %v; want an aborted result", result, err)
		}
		if len(lingering.started) != 0 {
			t.Error("a closed registry started a catalog refresh")
		}
	})
}

// Opening the model selector starts a catalog refresh that nothing awaited. Quitting after the selector closes must leave neither the selector's refresh
// task nor the registry's operation running: with a file-backed registry the operation writes auth.json and models-store.json in the agent directory.
func TestModelSelectorRefreshDoesNotOutliveShutdown(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		lingering := &lingeringCatalogStore{InMemoryModelsStore: store, started: make(chan struct{}, 4)}
		registry.SetModelsStore(lingering)
		var output bytes.Buffer
		m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
		m.runCtx = t.Context()
		m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
		m.tuiInst = tui.NewWithOutput(&output, 100, 40)
		m.installRenderDispatcher()
		m.statusLine = NewFooterComponent(nil, "", nil)
		done := make(chan bool, 1)
		go func() {
			_, ok := m.buildSlashContext(t.Context()).PickModel("")
			done <- ok
		}()
		<-lingering.started
		synctest.Wait()
		input, _ := m.modalRoute()
		input <- []byte("\x1b")
		if ok := <-done; ok {
			t.Fatal("Escape selected a model")
		}

		// Run's teardown cancels the background context and joins the mode's tasks; the owner of the registry then closes its model tasks.
		m.backgroundCancel()
		m.backgroundTasks.Wait()
		registry.CloseModelTasks()

		if !lingering.settled.Load() {
			t.Error("shutdown completed while the model selector's catalog refresh was still working on the store")
		}
	})
}
