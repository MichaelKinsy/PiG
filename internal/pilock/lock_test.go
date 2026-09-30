package pilock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
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
	// The acquisition probe leaves an mtime up to a second ahead (lib/mtime-precision.js), so the heartbeat's write moves it, in either direction. Waiting for the write is scheduling only: the heartbeat's own 20 ms interval is unchanged.
	advanced := false
	for deadline := time.Now().Add(5 * time.Second); !advanced && time.Now().Before(deadline); time.Sleep(stale) {
		lock.mu.Lock()
		advanced = !lock.mtime.Equal(initial)
		lock.mu.Unlock()
	}
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
//
// A foreign write that lands between the heartbeat's stat and its utimes is overwritten by that utimes in proper-lockfile too (lib/lockfile.js:114-143), and the compromise is then never reported for that write. The harness therefore repeats the damage on every poll, as a still-running foreign writer would, so the test measures the heartbeat's detection and not the scheduling of a single write. One heartbeat interval is 5 ms and proper-lockfile's own default is stale/2 (lib/lockfile.js:220), so the 5 s wait is 1000 intervals.
func TestHeartbeatCompromiseMessages(t *testing.T) {
	for _, test := range []struct {
		name   string
		damage func(t *testing.T, lockPath string)
		want   func(lockPath string) []string
	}{
		{"removed", func(t *testing.T, lockPath string) {
			if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
		}, func(lockPath string) []string {
			// A removal that lands between the heartbeat's stat and its utimes fails the utimes (lockfile.js:143-156).
			return []string{"ENOENT: no such file or directory, stat '" + lockPath + "'", "ENOENT: no such file or directory, utime '" + lockPath + "'"}
		}},
		{"replaced", func(t *testing.T, lockPath string) {
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(lockPath, later, later); err != nil {
				t.Fatal(err)
			}
		}, func(string) []string { return []string{"Unable to update lock within the stale threshold"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned")
			compromised := make(chan error, 1)
			lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Release() }()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
			deadline := time.After(5 * time.Second)
			test.damage(t, path+".lock")
			for {
				select {
				case err := <-compromised:
					var compromise *CompromisedError
					if !errors.As(err, &compromise) || !slices.Contains(test.want(path+".lock"), err.Error()) {
						t.Fatalf("compromise = %#v (%v), want one of %q", err, err, test.want(path+".lock"))
					}
					return
				case <-poll.C:
					test.damage(t, path+".lock")
				case <-deadline:
					t.Fatal("compromise not reported")
				}
			}
		})
	}
}

// The heartbeat records the mtime it wrote and never reads the directory back (lib/lockfile.js:141-164). A foreign write that lands right after the heartbeat's utimes must therefore differ from the recorded mtime at the next heartbeat and be reported. Reading the mtime back adopted that write as the lock's own and hid the compromise for good, the flake behind "compromise not reported".
func TestHeartbeatReportsForeignWriteAfterItsOwnUtimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned")
	lockPath := path + ".lock"
	var armed atomic.Bool
	var damaged atomic.Int32
	previous := osChtimes
	osChtimes = func(name string, atime, mtime time.Time) error {
		if err := previous(name, atime, mtime); err != nil {
			return err
		}
		if name == lockPath && armed.Load() && damaged.Add(1) == 1 {
			later := time.Now().Add(time.Hour)
			return previous(name, later, later)
		}
		return nil
	}
	t.Cleanup(func() { osChtimes = previous })
	compromised := make(chan error, 1)
	lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Second, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	armed.Store(true)
	select {
	case err := <-compromised:
		if err.Error() != "Unable to update lock within the stale threshold" {
			t.Fatalf("compromise = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compromise not reported")
	}
}

// The heartbeat's utimes is lib/mtime-precision.js getMtime (lines 44-52): Date.now() on a millisecond file system, and Math.ceil to a whole second on one that keeps none.
func TestTouchHonorsMtimePrecision(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().UnixMilli()
	written, err := touch(dir, precisionSecond)
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatal(err)
	}
	if ms := written.UnixMilli(); ms%1000 != 0 || ms < before || ms > (after+999)/1000*1000 {
		t.Fatalf("second precision wrote %d ms, want Math.ceil of a time in [%d, %d] to a whole second", ms, before, after)
	}
	before = time.Now().UnixMilli()
	written, err = touch(dir, precisionMillisecond)
	after = time.Now().UnixMilli()
	if err != nil {
		t.Fatal(err)
	}
	if ms := written.UnixMilli(); ms < before || ms > after || !written.Equal(time.UnixMilli(ms)) {
		t.Fatalf("millisecond precision wrote %v, want a whole millisecond in [%d, %d]", written, before, after)
	}
	if info, err := os.Stat(dir); err != nil || !sameMtime(info.ModTime(), written) {
		t.Fatalf("millisecond precision wrote %v, directory has %v (%v)", written, info, err)
	}
}

// lib/mtime-precision.js probe (lines 19-40) writes Math.ceil(Date.now() / 1000) * 1000 + 5, reads it back, and reports 's' only when the file system dropped the 5 ms.
func TestProbeMtimeReadsTheFileSystemPrecision(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().UnixMilli()
	observed, precision, err := probeMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Every file system PiG tests on (ext4, tmpfs, APFS, NTFS) keeps milliseconds.
	if ms := nodeDateMs(observed); precision != precisionMillisecond || ms%1000 != 5 || ms < before+5 || ms > (time.Now().UnixMilli()+999)/1000*1000+5 {
		t.Fatalf("probe = %v (%d ms), %v; want ms precision and a whole second after %d plus 5 ms", observed, ms, precision, before)
	}
	previous := osChtimes
	osChtimes = func(name string, atime, mtime time.Time) error {
		return previous(name, atime.Truncate(time.Second), mtime.Truncate(time.Second))
	}
	t.Cleanup(func() { osChtimes = previous })
	observed, precision, err = probeMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if precision != precisionSecond || nodeDateMs(observed)%1000 != 0 {
		t.Fatalf("probe on a whole-second file system = %v, %v; want s precision", observed, precision)
	}
}

// Async lockfile.lock calls share the graceful-fs object that caches the probed precision, so only the first one in a process writes the probe mtime; later ones stat the new directory (lib/mtime-precision.js:6-17,36-37). lockSync hands each call a copy of fs without the cache (lib/adapter.js:5-8,68) and probes every time.
func TestAsyncAcquisitionsReuseTheProbedPrecision(t *testing.T) {
	root := t.TempDir()
	cachedPrecision.Store(int32(precisionUnknown))
	t.Cleanup(func() { cachedPrecision.Store(int32(precisionUnknown)) })
	var probes atomic.Int32
	previous := osChtimes
	// A whole-second file system makes the first probe report 's'; the heartbeat never runs within these acquisitions.
	osChtimes = func(name string, atime, mtime time.Time) error {
		probes.Add(1)
		return previous(name, atime.Truncate(time.Second), mtime.Truncate(time.Second))
	}
	t.Cleanup(func() { osChtimes = previous })
	for i, step := range []struct {
		name    string
		acquire func(path string) (*Lock, error)
		probes  int32
	}{
		{"first async", func(path string) (*Lock, error) { return Acquire(t.Context(), path) }, 1},
		{"second async", func(path string) (*Lock, error) { return Acquire(t.Context(), path) }, 1},
		{"async with options", func(path string) (*Lock, error) {
			return AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Minute, Update: time.Minute, Retry: time.Millisecond})
		}, 1},
		{"first sync", AcquireSync, 2},
		{"second sync", AcquireSync, 3},
	} {
		lock, err := step.acquire(filepath.Join(root, fmt.Sprint(i)))
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := probes.Load(); got != step.probes {
			t.Errorf("%s: %d probe writes in total, want %d", step.name, got, step.probes)
		}
		if lock.precision != precisionSecond {
			t.Errorf("%s: precision %v, want the probed s", step.name, lock.precision)
		}
		if err := lock.Release(); err != nil {
			t.Fatalf("%s: release: %v", step.name, err)
		}
	}
}

// lockfile.js:129 compares Node's stat Date, whose getTime() rounds mtimeMs to the nearest millisecond. A foreign mtime 0.6 ms past the recorded one is a different millisecond and compromises the lock; one 0.4 ms before it is the same millisecond and does not.
func TestHeartbeatComparesMtimesAsNodeDates(t *testing.T) {
	for _, test := range []struct {
		name        string
		offset      time.Duration
		compromised bool
	}{
		{"rounds up to another millisecond", 600 * time.Microsecond, true},
		{"rounds to the recorded millisecond", -400 * time.Microsecond, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned")
			lockPath := path + ".lock"
			compromised := make(chan error, 1)
			lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Minute, Update: 5 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Release() }()
			// Holding the lock mutex keeps the heartbeat between runs while the foreign mtime replaces the recorded one.
			lock.mu.Lock()
			recorded := time.UnixMilli(nodeDateMs(lock.mtime))
			foreign := recorded.Add(test.offset)
			err = os.Chtimes(lockPath, foreign, foreign)
			info, statErr := os.Stat(lockPath)
			lock.mu.Unlock()
			if err != nil || statErr != nil {
				t.Fatal(err, statErr)
			}
			if !info.ModTime().Equal(foreign) {
				t.Skipf("the file system stored %v for %v", info.ModTime(), foreign)
			}
			deadline := time.After(5 * time.Second)
			for {
				select {
				case err := <-compromised:
					if !test.compromised {
						t.Fatalf("compromise = %v, want the heartbeat to take %v as its own", err, foreign)
					}
					return
				case <-deadline:
					t.Fatal("heartbeat did not run")
				case <-time.After(5 * time.Millisecond):
					lock.mu.Lock()
					refreshed := !sameMtime(lock.mtime, recorded)
					lock.mu.Unlock()
					if refreshed {
						if test.compromised {
							t.Fatalf("heartbeat refreshed a lock whose mtime is %v, recorded %v", foreign, recorded)
						}
						return
					}
				}
			}
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
