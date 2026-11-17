package subprocess

import (
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// An automatic restart replaces the contexts the generation's process starts under (startExtOnce) while the callers of its handlers keep
// asking whether the host stopped it: a command, shortcut or event call that fails reads the owner's context (dispatchError) on the caller's
// own goroutine, and the restart runs on the supervisor's. Both reach one managedExt, so the replacement and every read hold the process
// lock, as the process handle does; without it the race detector reports the pair, and an interface read half-way through its replacement
// can pair one context's type with another's data. An extension nobody stopped is not reported as stopped by the host at any moment of its
// restart.
func TestRestartReplacesTheStartContextBesideItsReaders(t *testing.T) {
	binPath := buildFixture(t)
	shortSockDir(t)
	t.Setenv("FIXTURE_GENERATION_FILE", filepath.Join(t.TempDir(), "generation"))
	h := newTestHost(t)
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	cfg := ExtConfig{Name: "fixture", Path: binPath, Enabled: true, SupervisorConfig: SupervisorConfig{MaxCrashes: 3, CrashWindow: time.Minute, InitialDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, BackoffFactor: 2}}
	if _, err := h.Load(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	me := h.exts["fixture"]
	h.mu.Unlock()
	held := me.ext
	crash, ok := held.Command("crash")
	if !ok {
		t.Fatal("crash command missing")
	}

	failure := errors.New("command crash: connection closed")
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var stopped, reads atomic.Int64
	for range 2 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				reads.Add(1)
				if errors.Is(me.dispatchError(failure), extension.ErrHandlerStopped) {
					stopped.Add(1)
				}
				runtime.Gosched()
			}
		})
	}
	_ = crash.Handler(t.Context(), "")
	waitDeclarationGeneration(t, h, "fixture", held, "gen 2")
	close(stop)
	readers.Wait()
	if n := stopped.Load(); n != 0 {
		t.Fatalf("%d of %d failed calls were marked as stopped by the host while nothing stopped the extension", n, reads.Load())
	}
}
