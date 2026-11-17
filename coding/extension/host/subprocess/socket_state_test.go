package subprocess

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The Host's socket directory is a small state machine: not created, created, removed. Once removed, every allocation refuses with errSockRuntimeRemoved and no path inside the deleted directory is returned, whether or not an allocation ran before the removal.
func TestSocketRuntimeDirIsRefusedForeverOnceRemoved(t *testing.T) {
	for _, tc := range []struct {
		name        string
		allocBefore bool
		removals    int
	}{
		{"termination before the first allocation", false, 1},
		{"termination after the first allocation", true, 1},
		{"termination twice after an allocation", true, 2},
		{"termination twice before any allocation", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socketDir := testenv.ShortTempDir(t, "pig-state-")
			h := NewHost(t.TempDir())
			h.socketDir = socketDir
			t.Cleanup(func() { h.Shutdown("test done") })
			var dir string
			if tc.allocBefore {
				path, err := h.newSocketPath()
				if err != nil {
					t.Fatal(err)
				}
				dir = filepath.Dir(path)
			}
			for range tc.removals {
				h.TerminateProcesses()
			}
			for range 3 {
				path, err := h.newSocketPath()
				if !errors.Is(err, errSockRuntimeRemoved) || path != "" {
					t.Fatalf("newSocketPath after the removal = %q, %v; want the refusal %v", path, err, errSockRuntimeRemoved)
				}
			}
			if dir != "" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("the socket directory %q survived the removal: %v", dir, err)
				}
			}
			if entries, err := os.ReadDir(socketDir); err != nil || len(entries) != 0 {
				t.Fatalf("the socket directory holds %v (%v), want nothing", entries, err)
			}
		})
	}
}

// An allocation that races the removal either gets a path whose directory exists, or the refusal. One that starts after the removal began never gets a path; one that returns a path while no removal had started finds its directory.
func TestSocketPathAllocationRacingRemoval(t *testing.T) {
	for round := range 200 {
		socketDir := testenv.ShortTempDir(t, "pig-race-")
		h := NewHost(t.TempDir())
		h.socketDir = socketDir
		t.Cleanup(func() { h.Shutdown("test done") })
		var removalStarted, removalDone atomic.Bool
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				<-start
				for range 20 {
					doneBefore := removalDone.Load()
					path, err := h.newSocketPath()
					switch {
					case err == nil && doneBefore:
						t.Errorf("round %d: path %q made after the removal finished", round, path)
					case err == nil:
						// The directory is read first and the flag after: a missing directory needs a removal that had started by then.
						if _, statErr := os.Stat(filepath.Dir(path)); statErr != nil && !removalStarted.Load() {
							t.Errorf("round %d: path %q returned with no removal started, directory: %v", round, path, statErr)
						}
					case err != nil && !errors.Is(err, errSockRuntimeRemoved):
						t.Errorf("round %d: unexpected error %v", round, err)
					}
				}
			})
		}
		wg.Go(func() {
			<-start
			removalStarted.Store(true)
			h.TerminateProcesses()
			removalDone.Store(true)
		})
		close(start)
		wg.Wait()
		if path, err := h.newSocketPath(); err == nil {
			t.Fatalf("round %d: path %q made after the removal", round, path)
		}
		h.Shutdown("test done")
		if entries, err := os.ReadDir(socketDir); err != nil || len(entries) != 0 {
			t.Fatalf("round %d: the socket directory holds %v (%v)", round, entries, err)
		}
	}
}
