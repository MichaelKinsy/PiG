package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts (`void this.refreshModels()`) and packages/ai/src/models.ts (refresh, raceWithAbortSignal)

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's Models.refresh settles when its signal aborts (raceWithAbortSignal) and lets the provider's refreshModels run on; nothing joins it. A Go process
// that tears a Session down in a directory it then removes cannot let that work run on: the native collection's provider refresh writes there. Closing the
// registry's model tasks cancels the collection's refreshes and returns only once the provider's refresh has returned.
func TestModelSelectorNativeProviderRefreshDoesNotOutliveShutdown(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	var armed, settled atomic.Bool
	started := make(chan struct{}, 4)
	provider := credentialTestProvider("lingering-native")
	// A provider's refresh ends its HTTP request when the signal aborts, then finishes the write it had started to the agent directory.
	provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
		if !armed.Load() {
			return nil
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-refresh.Signal.Done()
		time.Sleep(time.Second)
		if err := os.WriteFile(filepath.Join(registry.agentDir, "native-catalog-late-write"), []byte("late"), 0o600); err != nil {
			t.Errorf("late write: %v", err)
		}
		settled.Store(true)
		return context.Cause(refresh.Signal)
	}
	if err := registry.RegisterNativeModelsProvider(provider); err != nil {
		t.Fatal(err)
	}
	// The registration queues Pi's unawaited local refresh; it ends before the provider is armed.
	registry.YieldToRegistrationRefresh(t.Context())
	armed.Store(true)
	synctest.Test(t, func(t *testing.T) {
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
		<-started
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
		settledAtShutdown := settled.Load()
		time.Sleep(2 * time.Second) // lets a refresh that outlived shutdown end, so the failure reports instead of deadlocking the bubble

		if !settledAtShutdown {
			t.Error("shutdown completed while the native provider's catalog refresh was still running")
		}
		if _, err := os.Stat(filepath.Join(registry.agentDir, "native-catalog-late-write")); settledAtShutdown && err != nil {
			t.Errorf("the refresh's write did not reach the agent directory before shutdown completed: %v", err)
		}
	})
}
