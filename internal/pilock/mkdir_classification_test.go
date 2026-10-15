package pilock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// stubMkdir replaces osMkdir for one test; fn receives the 1-based call number and the real mkdir.
func stubMkdir(t *testing.T, fn func(call int, real func() error) error) *atomic.Int32 {
	t.Helper()
	previous := osMkdir
	var calls atomic.Int32
	osMkdir = func(path string, perm os.FileMode) error {
		return fn(int(calls.Add(1)), func() error { return previous(path, perm) })
	}
	t.Cleanup(func() { osMkdir = previous })
	return &calls
}

type acquirer struct {
	name string
	run  func(path string) (*Lock, error)
}

// acquirers are the auth loops, FileAuthStorageBackend.acquireLockSyncWithRetry and acquireLockAsync.
var acquirers = []acquirer{
	{"sync", AcquireAuthSync},
	{"async", func(path string) (*Lock, error) { return Acquire(context.Background(), path) }},
}

// packages/coding-agent/src/core/auth-storage.ts acquireLockSyncWithRetry / acquireLockAsync retry ELOCKED, which proper-lockfile raises only after mkdir returned EEXIST (lib/lockfile.js:22-48): a lock held briefly delays acquisition and the next attempt succeeds once the holder releases.
func TestAcquireRetriesEEXISTUntilHolderReleases(t *testing.T) {
	for _, a := range acquirers {
		t.Run(a.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			held, err := AcquireSync(path)
			if err != nil {
				t.Fatal(err)
			}
			calls := stubMkdir(t, func(call int, real func() error) error {
				if call == 2 {
					// The holder releases between attempts, so no attempt overlaps the removal.
					if err := held.Release(); err != nil {
						t.Error(err)
					}
				}
				return real()
			})
			lock, err := a.run(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("mkdir calls = %d, want 2 (EEXIST while held, then success)", got)
			}
			if err := lock.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Windows returns ERROR_ACCESS_DENIED from mkdir while a just-released lock directory is pending deletion; Node reports it as EPERM and proper-lockfile passes it through (only EEXIST is contention). Pi's auth loops rethrow every code but ELOCKED, and since Pi 1.2.0 (#10801) retry EPERM on Windows (auth-storage.ts:24-29). PiG classifies the same: the injected error is surfaced after exactly one attempt and leaves no lock behind, except Windows EPERM, whose retry finds the removal finished and takes the lock.
func TestAcquireRetriesOnlyWindowsEPERMAmongNonEEXISTMkdirErrors(t *testing.T) {
	for name, injected := range nonEEXISTMkdirErrors {
		retried := runtime.GOOS == "windows" && strings.HasPrefix(nonEEXISTMkdirMessages[name], "EPERM:")
		for _, a := range acquirers {
			t.Run(name+"/"+a.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				held, err := AcquireSync(path)
				if err != nil {
					t.Fatal(err)
				}
				calls := stubMkdir(t, func(call int, real func() error) error {
					switch call {
					case 1:
						return real() // EEXIST: held.
					case 2:
						// Release is still pending when the retry reaches mkdir.
						if err := held.Release(); err != nil {
							t.Error(err)
						}
						return &fs.PathError{Op: "mkdir", Path: path + ".lock", Err: injected}
					}
					if !retried {
						t.Errorf("mkdir call %d: a non-EEXIST error was retried", call)
					}
					return real()
				})
				lock, err := a.run(path)
				if retried {
					if err != nil {
						t.Fatalf("err = %v, want Windows EPERM retried until the removal finished", err)
					}
					if got := calls.Load(); got != 3 {
						t.Fatalf("mkdir calls = %d, want 3", got)
					}
					if err := lock.Release(); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil {
					_ = lock.Release()
					t.Fatal("acquisition succeeded, want the mkdir error")
				}
				if errors.Is(err, ErrLocked) || !errors.Is(err, injected) {
					t.Fatalf("err = %v, want the unwrapped %s and not ErrLocked", err, name)
				}
				// Pi surfaces the fs error's Node message (lib/node_errors.js uvException).
				if want := nonEEXISTMkdirMessages[name] + ", mkdir '" + path + ".lock'"; err.Error() != want {
					t.Fatalf("message = %q, want %q", err.Error(), want)
				}
				if got := calls.Load(); got != 2 {
					t.Fatalf("mkdir calls = %d, want 2", got)
				}
				if _, err := os.Lstat(path + ".lock"); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("lock directory after failed acquisition: %v", err)
				}
			})
		}
	}
}

// Every platform error Node reports as EEXIST is contention: proper-lockfile checks the held directory and raises ELOCKED, which Pi's retry loops retry until the holder releases.
func TestAcquireRetriesEveryEEXISTMkdirError(t *testing.T) {
	for name, injected := range eexistMkdirErrors {
		for _, a := range acquirers {
			t.Run(name+"/"+a.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				held, err := AcquireSync(path)
				if err != nil {
					t.Fatal(err)
				}
				calls := stubMkdir(t, func(call int, real func() error) error {
					if call == 1 {
						return &fs.PathError{Op: "mkdir", Path: path + ".lock", Err: injected}
					}
					if call == 2 {
						if err := held.Release(); err != nil {
							t.Error(err)
						}
					}
					return real()
				})
				lock, err := a.run(path)
				if err != nil {
					t.Fatalf("err = %v, want %s retried as contention", err, name)
				}
				if got := calls.Load(); got != 2 {
					t.Fatalf("mkdir calls = %d, want 2", got)
				}
				if err := lock.Release(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
