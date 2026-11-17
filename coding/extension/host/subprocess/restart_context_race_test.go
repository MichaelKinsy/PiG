package subprocess

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
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
	stopReaders := sync.OnceFunc(func() { close(stop) })
	t.Cleanup(stopReaders)
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
	callCtx, endCall := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer endCall()
	_ = crash.Handler(callCtx, "")
	waitDeclarationGeneration(t, h, "fixture", held, "gen 2")
	stopReaders()
	readers.Wait()
	if n := stopped.Load(); n != 0 {
		t.Fatalf("%d of %d failed calls were marked as stopped by the host while nothing stopped the extension", n, reads.Load())
	}
}

// A call that fails once its owner's context has ended is the host's own stop, and the runner does not report it (recordHandlerError); a restart
// then starts the generation under the host's own context, under which nobody has stopped it, and a failure of its is its own and is reported.
// Each answer is right for the generation it asks about, and the replacement is whole: the restart's first step gives the old context's cancel
// function up exactly once, and the stop that follows ends the new one, never the old.
func TestRestartGivesTheGenerationItsNewOwnerWhole(t *testing.T) {
	failure := errors.New("event session_start: connection closed")
	var oldEnded, newEnded atomic.Int32
	oldOwner, endOldOwner := context.WithCancel(t.Context())
	me := &managedExt{host: &Host{}}
	me.setStartContext(func() { oldEnded.Add(1) }, oldOwner)

	if errors.Is(me.dispatchError(failure), extension.ErrHandlerStopped) {
		t.Fatal("a failure under a live owner is the handler's own")
	}
	endOldOwner()
	if err := me.dispatchError(failure); !errors.Is(err, extension.ErrHandlerStopped) || !errors.Is(err, failure) {
		t.Fatalf("a failure after the owner ended = %v, want it marked as stopped by the host", err)
	}

	me.releaseStart()
	me.cancelStart()
	if got := oldEnded.Load(); got != 1 {
		t.Fatalf("the replaced generation's cancel ran %d times, want once", got)
	}
	newOwner, endNewOwner := context.WithCancel(t.Context())
	defer endNewOwner()
	me.setStartContext(func() { newEnded.Add(1) }, newOwner)
	if err := me.dispatchError(failure); errors.Is(err, extension.ErrHandlerStopped) || !errors.Is(err, failure) {
		t.Fatalf("a failure of the restarted generation = %v, want the failure unchanged", err)
	}
	me.cancelStart()
	if oldEnded.Load() != 1 || newEnded.Load() != 1 {
		t.Fatalf("after the stop the old cancel ran %d times and the new one %d, want 1 and 1", oldEnded.Load(), newEnded.Load())
	}
}
