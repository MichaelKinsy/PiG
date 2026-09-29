package pilock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLockStaleAndCompromisedOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * SyncStale)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	// An external replacement must be surfaced before commit and must not be
	// removed by the former owner's release (Pi onCompromised contract).
	changed := time.Now().Add(time.Second)
	if err := os.Chtimes(path+".lock", changed, changed); err != nil {
		t.Fatal(err)
	}
	if err := lock.Check(); err == nil {
		t.Fatal("lost lock was accepted")
	}
	if err := lock.Release(); err == nil {
		t.Fatal("compromised lock released without error")
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("removed new owner's lock: %v", err)
	}
}

func TestCancelledAcquisitionLeavesOwnerIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	owner, err := AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		lock, err := Acquire(ctx, path)
		if lock != nil {
			err = errors.Join(err, lock.Release())
		}
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: %v", err)
	}
	if err := owner.Check(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockHeartbeatAndReleaseJoin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	// A short injected stale interval exercises the identical heartbeat loop
	// without changing the production protocol's 10/30-second thresholds.
	const stale = 40 * time.Millisecond
	lock, err := acquire(path, stale)
	if err != nil {
		t.Fatal(err)
	}
	lock.mu.Lock()
	initial := lock.mtime
	lock.mu.Unlock()
	time.Sleep(3 * stale)
	lock.mu.Lock()
	advanced := lock.mtime.After(initial)
	lock.mu.Unlock()
	if !advanced {
		t.Error("heartbeat did not update mtime")
	}
	if err := lock.Check(); err != nil {
		t.Error(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lock.done:
	default:
		t.Fatal("release did not join heartbeat")
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock remains: %v", err)
	}
}

func BenchmarkLockRoundTrip(b *testing.B) {
	path := filepath.Join(b.TempDir(), "settings.json")
	b.ReportAllocs()
	for b.Loop() {
		lock, err := AcquireSync(path)
		if err != nil {
			b.Fatal(err)
		}
		if err := lock.Release(); err != nil {
			b.Fatal(err)
		}
	}
}

// proper-lockfile 4.1.2 lib/lockfile.js:119-138,153-156 reports a removed lock, a replaced lock or a lock the heartbeat cannot refresh within stale to onCompromised once; setLockAsCompromised (lib/lockfile.js:185-201) marks the lock released first.
func TestAcquireWithOptionsReportsHeartbeatCompromiseOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned")
	compromised := make(chan error, 2)
	lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) {
		compromised <- err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-compromised:
		if !errors.Is(err, errCompromised) || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("compromise = %v", err)
		}
		if lock.Context().Err() == nil {
			t.Fatal("callback ran before Context was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("removed lock was not reported")
	}
	if err := lock.Release(); !errors.Is(err, errCompromised) {
		t.Fatalf("Release = %v, want compromise", err)
	}
	select {
	case err := <-compromised:
		t.Fatalf("compromise reported twice: %v", err)
	default:
	}
}

// A healthy lock or a released lock never reports compromise, and a nil callback only cancels Context.
func TestAcquireWithOptionsHealthyAndReleasedLocksAreNotCompromised(t *testing.T) {
	compromised := make(chan error, 1)
	options := AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }}
	lock, err := AcquireWithOptions(t.Context(), filepath.Join(t.TempDir(), "healthy"), options)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-compromised:
		t.Fatalf("healthy lock reported compromise: %v", err)
	default:
	}
	options.OnCompromised = nil
	path := filepath.Join(t.TempDir(), "silent")
	silent, err := AcquireWithOptions(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-silent.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("nil callback did not cancel Context")
	}
	if err := silent.Release(); err == nil {
		t.Fatal("compromised lock released without error")
	}
}

type cancelAfterFirstCheck struct {
	context.Context
	calls int
}

func (c *cancelAfterFirstCheck) Err() error {
	c.calls++
	if c.calls == 1 {
		return nil
	}
	return context.Canceled
}

// Acquire re-checks the caller's context after taking the directory so a lock is never returned to a cancelled caller, and AcquireWithOptions does the same.
func TestAcquireWithOptionsRechecksContextAfterAcquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned")
	lock, err := AcquireWithOptions(&cancelAfterFirstCheck{Context: context.Background()}, path, AcquireOptions{Stale: time.Second, Update: time.Second, Retry: time.Millisecond})
	if !errors.Is(err, context.Canceled) || lock != nil {
		t.Fatalf("AcquireWithOptions = %v, %v; want context.Canceled and no lock", lock, err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled acquisition left its lock directory: %v", err)
	}
}

// proper-lockfile reports a missing lock directory as the fs error itself with code ECOMPROMISED (lib/lockfile.js:119-121) and a replaced lock as `Unable to update lock within the stale threshold` (lib/lockfile.js:129-138).
func TestCompromisedErrorsCarryProperLockfileMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.lock")
	_, statErr := os.Stat(path)
	for _, test := range []struct {
		name    string
		err     *CompromisedError
		message string
		inspect string
	}{
		{"missing", &CompromisedError{Cause: statErr}, "ENOENT: no such file or directory, stat '" + path + "'", ""},
		{"replaced", &CompromisedError{}, "Unable to update lock within the stale threshold", "Error: Unable to update lock within the stale threshold {\n  code: 'ECOMPROMISED'\n}"},
		{"utime", &CompromisedError{Cause: &fs.PathError{Op: "chtimes", Path: "/p", Err: syscall.EACCES}}, "EACCES: permission denied, utime '/p'", ""},
		// os.Stat on Windows names the Win32 call that failed; Node names it stat.
		{"windows stat", &CompromisedError{Cause: &fs.PathError{Op: "GetFileAttributesEx", Path: "/p", Err: syscall.ENOENT}}, "ENOENT: no such file or directory, stat '/p'", ""},
		{"windows stat handle", &CompromisedError{Cause: &fs.PathError{Op: "CreateFile", Path: "/p", Err: syscall.ENOENT}}, "ENOENT: no such file or directory, stat '/p'", ""},
		{"unmapped", &CompromisedError{Cause: &fs.PathError{Op: "stat", Path: "/p", Err: syscall.EMFILE}}, "stat /p: " + syscall.EMFILE.Error(), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.err.Error(); got != test.message {
				t.Fatalf("Error() = %q, want %q", got, test.message)
			}
			if test.err.Code() != "ECOMPROMISED" || !errors.Is(test.err, errCompromised) {
				t.Fatalf("code %q, is compromised %v", test.err.Code(), errors.Is(test.err, errCompromised))
			}
			if test.inspect != "" && test.err.Inspect() != test.inspect {
				t.Fatalf("Inspect() = %q, want %q", test.err.Inspect(), test.inspect)
			}
		})
	}
	if !errors.Is(&CompromisedError{Cause: statErr}, fs.ErrNotExist) {
		t.Fatal("compromise no longer unwraps to the file-system failure")
	}
}

// The heartbeat reports the two proper-lockfile compromise shapes with their Node messages.
func TestHeartbeatCompromiseMessages(t *testing.T) {
	for _, test := range []struct {
		name   string
		damage func(t *testing.T, lockPath string)
		want   func(lockPath string) string
	}{
		{"removed", func(t *testing.T, lockPath string) {
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
		}, func(lockPath string) string { return "ENOENT: no such file or directory, stat '" + lockPath + "'" }},
		{"replaced", func(t *testing.T, lockPath string) {
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(lockPath, later, later); err != nil {
				t.Fatal(err)
			}
		}, func(string) string { return "Unable to update lock within the stale threshold" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned")
			compromised := make(chan error, 1)
			lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }})
			if err != nil {
				t.Fatal(err)
			}
			test.damage(t, path+".lock")
			select {
			case err := <-compromised:
				var compromise *CompromisedError
				if !errors.As(err, &compromise) || err.Error() != test.want(path+".lock") {
					t.Fatalf("compromise = %#v (%v), want message %q", err, err, test.want(path+".lock"))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("compromise not reported")
			}
			_ = lock.Release()
		})
	}
}

// proper-lockfile's exit hook removes the directory of every lock still in its registry (lib/lockfile.js:331-337); a released lock and a compromised lock (lib/lockfile.js:195-197) have already left it.
func TestRemoveHeldLocksRemovesOnlyLocksStillHeld(t *testing.T) {
	root := t.TempDir()
	options := AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(error) {}}
	acquire := func(name string) *Lock {
		lock, err := AcquireWithOptions(t.Context(), filepath.Join(root, name), options)
		if err != nil {
			t.Fatal(err)
		}
		return lock
	}
	heldLock, released, compromised := acquire("held"), acquire("released"), acquire("compromised")
	if err := released.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "compromised.lock")); err != nil {
		t.Fatal(err)
	}
	<-compromised.Context().Done()
	// A foreign owner now holds the compromised path; the exit hook must not remove it.
	if err := os.Mkdir(filepath.Join(root, "compromised.lock"), 0o777); err != nil {
		t.Fatal(err)
	}
	RemoveHeldLocks()
	if _, err := os.Stat(filepath.Join(root, "held.lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("held lock directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "compromised.lock")); err != nil {
		t.Fatalf("compromised lock path was removed: %v", err)
	}
	_ = heldLock.Release()
	_ = compromised.Release()
}

// A compromise found by Check leaves the exit-hook registry at once, as setLockAsCompromised does before it reports (lib/lockfile.js:196-198), so the exit hook cannot remove a directory another process acquired before the next heartbeat.
func TestCheckCompromiseLeavesHeldLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned")
	lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Hour, Update: time.Hour, Retry: time.Millisecond, OnCompromised: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	var compromise *CompromisedError
	if err := lock.Check(); !errors.As(err, &compromise) {
		t.Fatalf("Check() = %v, want a compromise", err)
	}
	if err := os.Mkdir(path+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	RemoveHeldLocks()
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("the exit hook removed another owner's lock directory: %v", err)
	}
}
