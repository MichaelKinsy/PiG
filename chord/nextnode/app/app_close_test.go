package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// failingClose is a store whose close fails after it closed the real one: a step of the app's close that rejects.
type failingClose struct {
	store.Store
	err error
}

func (f failingClose) Close(ctx context.Context) error { return errors.Join(f.Store.Close(ctx), f.err) }

// Pi's app.ts `#close` awaits http.close, each workers.close, host.close and store.close in turn, then the plugins' watcher and the
// lock release: the first rejection ends the chain there (later steps do not run) and rejects `close` with it, and every later
// call returns the same promise. A step that fails leaves the later ones undone, the data directory's lock included.
func TestAppCloseStopsAtTheFirstFailedStepAndLeavesTheLaterStepsUndone(t *testing.T) {
	s := startTestApp(t, nil, nil)
	// Registered after the app's own cleanups, so it runs before them: the steps Close did not run are made here, so nothing outlives the test.
	t.Cleanup(func() {
		s.app.Plugins.Component.Close()
		_ = s.app.lock.Release()
	})
	boom := errors.New("the store failed to close")
	s.app.store = failingClose{Store: s.app.store, err: boom}
	err := s.app.Close(background)
	if !errors.Is(err, boom) {
		t.Fatalf("close = %v, want the store's failure", err)
	}
	// The lock release is the last step: it did not run, the data directory is still held.
	second, err := StartServer(optionsFor(t, s.dataDir, s.root), background)
	if err == nil {
		closeAndWait(t, second)
	}
	if err == nil || !strings.Contains(err.Error(), "another server runs on "+s.dataDir) {
		t.Fatalf("a second server on the data directory after the failed close: %v, want it refused (the lock is still held)", err)
	}
	// A second close is the same close: the same rejection, no step run again.
	if again := s.app.Close(background); !errors.Is(again, boom) {
		t.Fatalf("second close = %v, want the same failure", again)
	}
}
