package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gofrs/flock"
)

const lockBusyMessage = "another standalone pig update is already running"

// upstream: packages/coding-agent/src/package-manager-cli.ts:171-222 takes
// proper-lockfile's lock around the update and releases it in `finally`;
// proper-lockfile's release removes its lock (lib/lockfile.js unlock → rmdir).
// PiG keeps an OS lock instead (D39) and, like Pi, leaves nothing behind.
func TestStandaloneUpdateLockRemovesItsSidecarOnRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("standalone in-place update is unsupported on Windows")
	}
	path := filepath.Join(t.TempDir(), "pig.update.lock")
	release, err := acquireStandaloneUpdateLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("held lock has no sidecar: %v", err)
	}
	if _, err := acquireStandaloneUpdateLock(path); err == nil || err.Error() != lockBusyMessage {
		t.Fatalf("second holder error = %v, want %q", err, lockBusyMessage)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a refused holder removed the live holder's sidecar: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sidecar after release: %v, want not exist", err)
	}
	again, err := acquireStandaloneUpdateLock(path)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}

// Removing the sidecar must never let two updaters hold "the" lock: a process
// that opened the old inode before the release must not win it after the file
// is gone. Every holder is exclusive, and no sidecar survives the last release.
func TestStandaloneUpdateLockStaysExclusiveWhileItsSidecarIsRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("standalone in-place update is unsupported on Windows")
	}
	path := filepath.Join(t.TempDir(), "pig.update.lock")
	var holders, maxHolders, acquired atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				release, err := acquireStandaloneUpdateLock(path)
				if err != nil {
					if err.Error() != lockBusyMessage {
						t.Errorf("acquire: %v", err)
						return
					}
					continue
				}
				acquired.Add(1)
				n := holders.Add(1)
				for {
					m := maxHolders.Load()
					if n <= m || maxHolders.CompareAndSwap(m, n) {
						break
					}
				}
				runtime.Gosched()
				holders.Add(-1)
				if err := release(); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
	if maxHolders.Load() != 1 {
		t.Fatalf("%d holders at once, want exactly 1", maxHolders.Load())
	}
	if acquired.Load() == 0 {
		t.Fatal("no updater ever acquired the lock")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sidecar after every release: %v, want not exist", err)
	}
}

// The race the verification exists for, made deterministic: between this
// updater's open and its lock, the previous holder removed the sidecar and
// another updater created a new one, so the lock it wins is on a file the path
// no longer names. It must drop that orphan and lock the sidecar the path
// names; holding the orphan would let the next updater lock the real sidecar
// too.
func TestStandaloneUpdateLockReopensWhenItsLockedFileIsNoLongerTheSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("standalone in-place update is unsupported on Windows")
	}
	for _, tc := range []struct {
		name    string
		replace func(t *testing.T, path string)
	}{
		{"replaced", func(t *testing.T, path string) {
			if err := os.WriteFile(path+".new", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"removed", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pig.update.lock")
			tryLock := tryLockStandaloneUpdateSidecar
			t.Cleanup(func() { tryLockStandaloneUpdateSidecar = tryLock })
			attempts := 0
			tryLockStandaloneUpdateSidecar = func(p string) (*flock.Flock, bool, error) {
				attempts++
				lock, locked, err := tryLock(p)
				if attempts == 1 {
					tc.replace(t, p)
				}
				return lock, locked, err
			}
			release, err := acquireStandaloneUpdateLock(path)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			if attempts != 2 {
				t.Fatalf("lock attempts = %d, want 2: the updater must reopen the sidecar the path names", attempts)
			}
			rival := flock.New(path)
			if locked, err := rival.TryLock(); err != nil || locked {
				t.Fatalf("a second updater locked the sidecar while the first holds the lock (locked=%t, err=%v)", locked, err)
			}
			if err := rival.Close(); err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("sidecar after release: %v, want not exist", err)
			}
		})
	}
}
