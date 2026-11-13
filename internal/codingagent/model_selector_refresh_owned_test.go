package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts (`void this.refreshModels()`) and model-catalog-refresh.ts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	ai.ModelsStore
	started chan struct{}
	settled atomic.Bool
	// flush is the write the store finishes after the cancel, when it has a file to write to.
	flush func()
}

func (s *lingeringCatalogStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	if providerID == "radius" {
		select {
		case s.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		time.Sleep(time.Second)
		if s.flush != nil {
			s.flush()
		}
		s.settled.Store(true)
		return nil, ctx.Err()
	}
	return s.ModelsStore.Read(ctx, providerID)
}

// Pi's refreshModelCatalogs lets the runtime's refresh run on after its last waiter aborted (model-catalog-refresh.ts), and nothing joins it. PiG's
// refresh is a task of the registry: closing the registry's model tasks waits for it, so the agent directory is not written after Close returns.
func TestAbandonedModelCatalogRefreshIsJoinedByRegistryClose(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		lingering := &lingeringCatalogStore{ModelsStore: store, started: make(chan struct{}, 4)}
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
		settledAtClose := lingering.settled.Load()
		time.Sleep(2 * time.Second) // lets a refresh that outlived Close end, so the failure reports instead of deadlocking the bubble

		if !settledAtClose {
			t.Error("CloseModelTasks returned while the abandoned catalog refresh was still working on the store")
		}
	})
}

// A registry whose model tasks are closed admits no refresh: the caller gets an aborted result, as Pi's runtime gets from an aborted signal, and
// no work starts after Close.
func TestModelCatalogRefreshAfterRegistryCloseIsAborted(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	registry.CloseModelTasks()

	result, err := RefreshModelCatalogs(t.Context(), registry)

	if err != nil || !result.Aborted {
		t.Errorf("refresh after Close = %+v, %v; want an aborted result", result, err)
	}
}

// Opening the model selector starts a catalog refresh that nothing awaited. Quitting after the selector closes must leave neither the selector's refresh
// task nor the registry's operation running: with a file-backed registry the operation writes auth.json and models-store.json in the agent directory.
func TestModelSelectorRefreshDoesNotOutliveShutdown(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		// The agent directory is real: the store is the registry's file-backed one, and the refresh's late write lands in models-store.json.
		file := ai.NewFileModelsStore(filepath.Join(registry.agentDir, "models-store.json"))
		lingering := &lingeringCatalogStore{ModelsStore: file, started: make(chan struct{}, 4)}
		lingering.flush = func() {
			if err := file.Write(context.Background(), "radius", ai.ModelsStoreEntry{}); err != nil {
				t.Errorf("late store write: %v", err)
			}
		}
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
		settledAtShutdown := lingering.settled.Load()
		if _, err := os.Stat(filepath.Join(registry.agentDir, "models-store.json")); settledAtShutdown && err != nil {
			t.Errorf("the refresh's write did not reach the agent directory: %v", err)
		}
		time.Sleep(2 * time.Second) // lets a refresh that outlived shutdown end, so the failure reports instead of deadlocking the bubble

		if !settledAtShutdown {
			t.Error("shutdown completed while the model selector's catalog refresh was still working on the store")
		}
	})
}

// The interactive mode is the task owner of the selector's refresh (model-selector.ts `void this.refreshModels()`).
var _ tui.ModelSelectorTaskOwner = modelSelectorRuntime{}

// selectorTaskMode is a mode whose background context is live, as Run leaves it, and a runtime for a selector whose caller runs under caller.
func selectorTaskMode(t *testing.T, caller context.Context) (*InteractiveMode, modelSelectorRuntime) {
	t.Helper()
	registry, _, _ := radiusTestRegistry(t, "", nil)
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
	return m, modelSelectorRuntime{m: m, registry: registry, ctx: caller}
}

// Run's teardown cancels the background context and then joins m.backgroundTasks: the selector's refresh is one of them, so the join waits for it.
func TestModelSelectorTaskIsJoinedByTheModesBackgroundTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, runtime := selectorTaskMode(t, t.Context())
		started := make(chan struct{})
		var finished atomic.Bool
		if !runtime.StartModelSelectorTask(func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			time.Sleep(time.Second) // the refresh call is still returning
			finished.Store(true)
		}) {
			t.Fatal("a live mode refused the selector's refresh")
		}
		<-started

		m.backgroundCancel()
		m.backgroundTasks.Wait()
		finishedAtJoin := finished.Load()
		time.Sleep(2 * time.Second) // lets a task the join did not wait for end, so the failure reports instead of deadlocking the bubble

		if !finishedAtJoin {
			t.Error("the mode's background-task join returned while the selector's refresh was still running")
		}
	})
}

// The mode's background context, which Run cancels at teardown, ends the selector's refresh while its caller is still alive.
func TestModelSelectorTaskEndsWithTheModesBackgroundContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		caller, cancelCaller := context.WithCancel(t.Context())
		m, runtime := selectorTaskMode(t, caller)
		taskCtx := make(chan context.Context, 1)
		runtime.StartModelSelectorTask(func(ctx context.Context) { taskCtx <- ctx; <-ctx.Done() })
		ctx := <-taskCtx

		m.backgroundCancel()
		synctest.Wait()
		ended := ctx.Err() != nil
		cancelCaller() // lets a task that ignored the background context end
		m.backgroundTasks.Wait()

		if !ended {
			t.Error("cancelling the mode's background context did not end the selector's refresh")
		}
	})
}

// The selector's caller ends the refresh too (the picker's context), while the mode is still running.
func TestModelSelectorTaskEndsWithItsCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		caller, cancelCaller := context.WithCancel(t.Context())
		m, runtime := selectorTaskMode(t, caller)
		taskCtx := make(chan context.Context, 1)
		runtime.StartModelSelectorTask(func(ctx context.Context) { taskCtx <- ctx; <-ctx.Done() })
		ctx := <-taskCtx

		cancelCaller()
		synctest.Wait()
		ended := ctx.Err() != nil
		m.backgroundCancel() // lets a task that ignored its caller end
		m.backgroundTasks.Wait()

		if !ended {
			t.Error("cancelling the selector's caller did not end its refresh")
		}
	})
}

// A mode that is already shutting down admits no refresh.
func TestModelSelectorTaskIsRefusedAfterTheModeShutsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, runtime := selectorTaskMode(t, t.Context())
		m.backgroundCancel()
		ran := false
		if runtime.StartModelSelectorTask(func(context.Context) { ran = true }) {
			t.Error("a shut-down mode admitted the selector's refresh")
		}
		m.backgroundTasks.Wait()
		if ran {
			t.Error("a shut-down mode ran the selector's refresh")
		}
	})
}

// The production path: opening the selector through PickModel, the mode's background cancellation (Run's teardown) ends the catalog operation the
// selector's refresh started, although the picker is still open and its caller alive.
func TestOpenModelSelectorRefreshEndsWhenTheModesBackgroundContextIsCancelled(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
		registry.SetModelsStore(blocked)
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
		operation := <-blocked.started
		synctest.Wait()

		m.backgroundCancel()
		synctest.Wait()
		ended := operation.Err() != nil
		close(blocked.release) // lets an operation that kept running end

		input, _ := m.modalRoute()
		input <- []byte("\x1b")
		<-done
		m.backgroundTasks.Wait()
		registry.CloseModelTasks()
		if !ended {
			t.Error("cancelling the mode's background context left the open selector's catalog operation running")
		}
	})
}

// A refresh that panics is reported to the uncaught handler as an unawaited Promise's rejection is, and every waiter gets the aborted result
// rather than waiting for an operation that will never settle.
func TestPanickingCatalogRefreshIsReportedAndSettlesItsWaiters(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		var reported atomic.Value
		defer SetUncaughtGoroutineHandler(func(value any, _ []byte) { reported.Store(value) })()
		detach := registry.ObserveChanges(func() { panic("observer failed") })
		defer detach()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		result, err := RefreshModelCatalogs(ctx, registry)

		if err != nil || !result.Aborted {
			t.Errorf("waiter of a panicking refresh = %+v, %v; want the aborted result", result, err)
		}
		if got := reported.Load(); got != "observer failed" {
			t.Errorf("uncaught handler received %v, want the panic value", got)
		}
	})
}
