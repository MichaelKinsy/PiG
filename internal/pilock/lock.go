// Package pilock implements Pi's proper-lockfile directory-lock protocol for shared stores.
// Ports packages/coding-agent/src/core/auth-storage.ts
package pilock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Pi 0.87.1 auth-storage.ts:69-93,119-143 and settings-manager.ts:241-265.
const (
	SyncAttempts = 10
	SyncDelay    = 20 * time.Millisecond
	// proper-lockfile 4.1.2 lib/lockfile.js:208.
	SyncStale  = 10 * time.Second
	asyncStale = 30 * time.Second
	maxDelay   = 2 * time.Second
)

// ErrLocked reports contention under Pi's directory protocol. Its text is proper-lockfile 4.1.2's ELOCKED message (lib/lockfile.js:53,68), which Pi's stores rethrow unchanged.
var ErrLocked = errors.New("Lock file is already being held")

// ErrLegacyLocked reports an older PiG writer; acquisition uses the same retry budget as directory contention.
var ErrLegacyLocked = fmt.Errorf("%w by an older PiG process; stop the older PiG before upgrading", ErrLocked)
var errCompromised = errors.New("lock compromised")

// Lock owns a directory and its mtime heartbeat. Compromise cancels its operation context; Release joins the heartbeat and removes only the owned directory.
type Lock struct {
	path       string
	mu         sync.Mutex
	mtime      time.Time
	precision  mtimePrecision
	err        error
	ctx        context.Context
	cancel     context.CancelCauseFunc
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
	releaseErr error
	// onCompromised receives the first heartbeat-detected compromise once, off the lock mutex.
	onCompromised func(error)
}

// AcquireSync follows Pi's bounded synchronous acquisition loop, reclaiming stale idle legacy sidecars.
func AcquireSync(path string) (*Lock, error) {
	for attempt := 1; ; attempt++ {
		lock, err := acquire(path, SyncStale)
		if !errors.Is(err, ErrLocked) || attempt == SyncAttempts {
			return lock, err
		}
		time.Sleep(SyncDelay)
	}
}

// Acquire follows Pi's cancellable auth acquisition loop, reclaiming stale idle legacy sidecars. The caller owns the returned lock until Release.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	deadline := time.Now().Add(asyncStale)
	baseDelay := 10 * time.Millisecond
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		lock, err := tryAcquire(ctx, path, asyncStale)
		if err == nil {
			return lockUnlessAborted(ctx, lock)
		}
		// An abort that arrived during the attempt wins over its failure (auth-storage.ts:135).
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		remaining := time.Until(deadline)
		if !errors.Is(err, ErrLocked) || remaining <= 0 {
			return nil, err
		}
		jitter := time.Duration(math.Floor(float64(baseDelay/time.Millisecond)*(1+rand.Float64())+0.5)) * time.Millisecond
		baseDelay = min(baseDelay*2, maxDelay/2)
		timer := time.NewTimer(min(jitter, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, context.Cause(ctx)
		case <-timer.C:
		}
	}
}

// AcquireOptions selects the stale, heartbeat, and bounded retry intervals of a directory lock.
type AcquireOptions struct {
	Stale, Update, Retry, Wait time.Duration
	// OnCompromised is proper-lockfile's onCompromised: it receives the error when the heartbeat finds the lock removed, replaced or not refreshed within Stale. A nil value only cancels Context, which is the behavior auth storage needs. The callback runs on the heartbeat goroutine after Context is cancelled.
	OnCompromised func(error)
}

// AcquireWithOptions acquires a proper-lockfile directory with caller-selected fixed retry and heartbeat intervals.
func AcquireWithOptions(ctx context.Context, path string, options AcquireOptions) (*Lock, error) {
	if options.Stale <= 0 || options.Update <= 0 || options.Retry <= 0 || options.Wait < 0 {
		return nil, errors.New("invalid directory lock intervals")
	}
	deadline := time.Now().Add(options.Wait)
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		lock, err := tryAcquireWithUpdate(ctx, path, options.Stale, options.Update, options.OnCompromised)
		if err == nil {
			return lockUnlessAborted(ctx, lock)
		}
		remaining := time.Until(deadline)
		if !errors.Is(err, ErrLocked) || remaining <= 0 {
			return nil, err
		}
		timer := time.NewTimer(min(options.Retry, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, context.Cause(ctx)
		case <-timer.C:
		}
	}
}

// lockUnlessAborted returns lock unless the caller was cancelled while it was being taken. Then, like Pi's acquireLockAsync (auth-storage.ts:149-152), it releases the lock and returns the cancellation, or the release's error when the release fails.
func lockUnlessAborted(ctx context.Context, lock *Lock) (*Lock, error) {
	cause := context.Cause(ctx)
	if cause == nil {
		return lock, nil
	}
	if err := lock.Release(); err != nil {
		return nil, err
	}
	return nil, cause
}

// acquire is one attempt of lockSync: it probes the mtime precision on every call.
func acquire(path string, stale time.Duration) (*Lock, error) {
	return acquireWithProbe(context.Background(), path, stale, stale/2, nil, probeMtime)
}

// tryAcquire is one async lockfile.lock attempt: it reuses the process's probed precision.
func tryAcquire(ctx context.Context, path string, stale time.Duration) (*Lock, error) {
	return tryAcquireWithUpdate(ctx, path, stale, stale/2, nil)
}

func tryAcquireWithUpdate(ctx context.Context, path string, stale, update time.Duration, onCompromised func(error)) (*Lock, error) {
	return acquireWithProbe(ctx, path, stale, update, onCompromised, probeMtimeCached)
}

func acquireWithProbe(ctx context.Context, path string, stale, update time.Duration, onCompromised func(error), probe func(string) (time.Time, mtimePrecision, error)) (*Lock, error) {
	path, err := filepath.Abs(path + ".lock")
	if err != nil {
		return nil, err
	}
	if err := mkdir(path, stale); err != nil {
		return nil, err
	}
	mtime, precision, err := probe(path)
	if err != nil {
		// proper-lockfile removes the directory it just made and ignores that rmdir's result (lib/lockfile.js:33-40).
		_ = syscall.Rmdir(path)
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	lock := &Lock{path: path, mtime: mtime, precision: precision, ctx: ctx, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{}), onCompromised: onCompromised}
	held.add(lock)
	go lock.heartbeat(stale, update)
	return lock, nil
}

// osMkdir creates the lock directory; tests replace it to inject platform errors.
var osMkdir = os.Mkdir

// mkdir mirrors proper-lockfile 4.1.2 lib/lockfile.js acquireLock (lines 22-48): only EEXIST means the directory is held. isEEXIST matches Node's EEXIST exactly; fs.ErrExist is wider because it also matches ENOTEMPTY and Windows ERROR_DIR_NOT_EMPTY. Every other mkdir error, including the ERROR_ACCESS_DENIED Windows returns while a just-removed lock directory is pending deletion, is returned unretried. libuv's uv_translate_sys_error (src/win/error.c) surfaces ERROR_ACCESS_DENIED as EPERM, ERROR_SHARING_VIOLATION as EBUSY and ERROR_DIR_NOT_EMPTY as ENOTEMPTY, none of which are ELOCKED, so Pi's auth-storage.ts acquireLockSyncWithRetry (`code !== "ELOCKED"`) and acquireLockAsync throw them. Do not widen this to retry them. Each failure Pi would throw carries Node's fs message for its call (mkdir, stat, rmdir).
func mkdir(path string, stale time.Duration) error {
	err := osMkdir(path, 0o777)
	if err == nil || !isEEXIST(err) {
		return nodeFS(err, "mkdir", path)
	}
	if stale <= 0 {
		return ErrLocked
	}
	info, err := lstatLock(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mkdir(path, 0)
	}
	if err != nil {
		return nodeFS(err, "stat", path)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return mkdirOverLink(path, stale)
	}
	if info.Mode().IsRegular() && info.Size() == 0 {
		if !info.ModTime().Before(time.Now().Add(-stale)) {
			return ErrLocked
		}
		// pig divergence (D73): reclaim stale PiG-owned sidecars under their old OS lock before entering Pi's directory protocol.
		observed, err := observeLegacy(path, info)
		if errors.Is(err, fs.ErrNotExist) {
			return mkdir(path, 0)
		}
		if err != nil {
			return err
		}
		return replaceLegacy(path, observed, stale)
	}
	return removeIfStale(path, info, stale)
}

// mkdirOverLink continues proper-lockfile 4.1.2 acquireLock for a lock path that is a link. Its fs.stat follows the link (lib/lockfile.js:56-65), a dangling link stats ENOENT, and only the target's mtime is judged (lib/lockfile.js:67-69). A link is never a PiG legacy sidecar.
func mkdirOverLink(path string, stale time.Duration) error {
	info, err := statLock(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mkdir(path, 0)
	}
	if err != nil {
		return nodeFS(err, "stat", path)
	}
	return removeIfStale(path, info, stale)
}

// removeIfStale finishes proper-lockfile 4.1.2 acquireLock once fs.stat has read the lock path as info, whatever its type: a fresh one is ELOCKED (lib/lockfile.js:67-69), and a stale one is removed with rmdir before mkdir is tried again (lib/lockfile.js:71-79,88-96). rmdir ignores only ENOENT. Windows removes a directory or a directory link; libuv reports rmdir of a file or a file link as ENOENT (src/win/fs.c fs__unlink_rmdir; RemoveDirectory's ERROR_DIRECTORY translates to ENOENT), so the repeated mkdir reports ELOCKED and the file stays. Unix rmdir(2) of a file or a link fails with ENOTDIR, which is returned.
func removeIfStale(path string, info fs.FileInfo, stale time.Duration) error {
	if !info.ModTime().Before(time.Now().Add(-stale)) {
		return ErrLocked
	}
	if err := syscall.Rmdir(path); err != nil && !errors.Is(err, fs.ErrNotExist) && nodeerrno.ErrorCode(err) != "ENOENT" {
		return nodeFS(err, "rmdir", path)
	}
	return mkdir(path, 0)
}

// mtimePrecision is proper-lockfile's 's' or 'ms' file-system timestamp precision (lib/mtime-precision.js).
type mtimePrecision int

const (
	precisionUnknown mtimePrecision = iota
	precisionMillisecond
	precisionSecond
)

// cachedPrecision is lib/mtime-precision.js's cache on the graceful-fs object that every async lockfile.lock in a Node process shares (lib/mtime-precision.js:6-17,36-37). lockSync never hits it: lib/adapter.js:5-8 gives each call a copy of fs, and the copy drops the non-enumerable cache.
var cachedPrecision atomic.Int32

// osChtimes sets the lock directory's mtime; tests replace it to place a foreign write at an exact point of the heartbeat.
var osChtimes = os.Chtimes

// probeMtimeCached is lib/mtime-precision.js probe on the shared async fs: once a probe has set the cache, it only stats the new lock directory and returns that mtime with the cached precision (lines 8-17). The first probe to finish keeps the cache.
func probeMtimeCached(path string) (time.Time, mtimePrecision, error) {
	if precision := mtimePrecision(cachedPrecision.Load()); precision != precisionUnknown {
		info, err := statLock(path)
		if err != nil {
			return time.Time{}, 0, nodeFS(err, "stat", path)
		}
		return info.ModTime(), precision, nil
	}
	mtime, precision, err := probeMtime(path)
	if err == nil {
		cachedPrecision.CompareAndSwap(int32(precisionUnknown), int32(precision))
	}
	return mtime, precision, err
}

// probeMtime is lib/mtime-precision.js probe without a cached precision (lines 19-40): it sets an mtime that is 5 ms past a whole second, reads it back, and reports 's' when the file system dropped the milliseconds. The read-back mtime is the lock's initial mtime, as in lockfile.js:33-43.
func probeMtime(path string) (time.Time, mtimePrecision, error) {
	mtime := time.UnixMilli((time.Now().UnixMilli()+999)/1000*1000 + 5)
	if err := osChtimes(path, mtime, mtime); err != nil {
		return time.Time{}, 0, nodeFS(err, "utime", path)
	}
	info, err := statLock(path)
	if err != nil {
		return time.Time{}, 0, nodeFS(err, "stat", path)
	}
	observed := info.ModTime()
	if nodeDateMs(observed)%1000 == 0 {
		return observed, precisionSecond, nil
	}
	return observed, precisionMillisecond, nil
}

// touch is lib/mtime-precision.js getMtime (lines 44-52) plus lockfile.js:143's utimes. It returns the mtime it wrote and does not read the directory back: lockfile.js:164 records the written value, so a foreign write after the utimes still differs from the recorded mtime at the next heartbeat. A read-back would adopt that write as the lock's own mtime and hide the compromise.
func touch(path string, precision mtimePrecision) (time.Time, error) {
	now := time.Now().UnixMilli()
	if precision == precisionSecond {
		now = (now + 999) / 1000 * 1000
	}
	mtime := time.UnixMilli(now)
	if err := osChtimes(path, mtime, mtime); err != nil {
		return time.Time{}, nodeFS(err, "utime", path)
	}
	return mtime, nil
}

// sameMtime is lockfile.js:129's `lock.mtime.getTime() === stat.mtime.getTime()`: the comparison is in whole milliseconds as Node's stat Date holds them.
func sameMtime(a, b time.Time) bool { return nodeDateMs(a) == nodeDateMs(b) }

// nodeDateMs is getTime() of a Node fs.Stats Date: Node builds it from mtimeMs (sec*1e3 + nsec/1e6) with Math.round, so a sub-millisecond part of 0.5 ms or more rounds up rather than truncating.
func nodeDateMs(t time.Time) int64 {
	return t.Unix()*1000 + (int64(t.Nanosecond())+500_000)/1_000_000
}

func (l *Lock) check() error {
	if l.err != nil {
		return l.err
	}
	info, err := statLock(l.path)
	if err != nil {
		l.err = &CompromisedError{Cause: err}
	} else if !info.IsDir() || !sameMtime(info.ModTime(), l.mtime) {
		l.err = &CompromisedError{}
	}
	if l.err != nil {
		held.remove(l)
		l.cancel(l.err)
	}
	return l.err
}

// Compromised reports that the lock has been removed or replaced, without the caller's cancellation. It is withLockAsync's throwIfCompromised, which Pi applies after a write (auth-storage.ts:168-172,189).
func (l *Lock) Compromised() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.check()
}

// Check refuses a write after cancellation or after the lock has been removed or replaced.
func (l *Lock) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.check(); err != nil {
		return err
	}
	return context.Cause(l.ctx)
}

func (l *Lock) heartbeat(stale, update time.Duration) {
	defer close(l.done)
	lastUpdate := time.Now()
	delay := update
	for {
		timer := time.NewTimer(delay)
		select {
		case <-l.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		l.mu.Lock()
		info, err := statLock(l.path)
		if err == nil && (!info.IsDir() || !sameMtime(info.ModTime(), l.mtime)) {
			l.err = &CompromisedError{}
		} else {
			if err == nil {
				var next time.Time
				next, err = touch(l.path, l.precision)
				if err == nil {
					l.mtime = next
				}
			}
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) || time.Since(lastUpdate) > stale {
					l.err = &CompromisedError{Cause: err}
				}
				delay = time.Second
			} else {
				lastUpdate = time.Now()
				delay = update
			}
		}
		failed := l.err != nil
		compromise := l.err
		if failed {
			held.remove(l)
			l.cancel(compromise)
		}
		l.mu.Unlock()
		if failed {
			if l.onCompromised != nil {
				l.onCompromised(compromise)
			}
			return
		}
	}
}

// Context cancels with the caller or when another process compromises the lock.
func (l *Lock) Context() context.Context { return l.ctx }

// Release joins the heartbeat before removing the owned directory. Like proper-lockfile's release, its result is the removal's, whether or not the caller's context was cancelled. Repeated calls return the same result.
func (l *Lock) Release() error {
	l.once.Do(func() {
		held.remove(l)
		close(l.stop)
		<-l.done
		l.mu.Lock()
		defer l.mu.Unlock()
		l.releaseErr = l.check()
		if l.releaseErr == nil {
			l.releaseErr = nodeFS(syscall.Rmdir(l.path), "rmdir", l.path)
		}
		l.cancel(context.Canceled)
	})
	return l.releaseErr
}

// heldLocks is proper-lockfile's module-level `locks` registry (lib/lockfile.js:9; added at 243, removed at 196-198 and 290): every acquired lock that has been neither released nor found compromised.
type heldLocks struct {
	mu    sync.Mutex
	locks map[*Lock]struct{}
}

var held = heldLocks{locks: map[*Lock]struct{}{}}

func (h *heldLocks) add(lock *Lock) {
	h.mu.Lock()
	h.locks[lock] = struct{}{}
	h.mu.Unlock()
}

func (h *heldLocks) remove(lock *Lock) {
	h.mu.Lock()
	delete(h.locks, lock)
	h.mu.Unlock()
}

// RemoveHeldLocks removes the lock directory of every lock this process still holds, ignoring failures. It is proper-lockfile's process-exit hook (lib/lockfile.js:331-337), which Node runs on every exit including an uncaught exception. Go has no exit hook, so a caller that ends the process without returning through Release calls it first.
func RemoveHeldLocks() {
	held.mu.Lock()
	paths := make([]string, 0, len(held.locks))
	for lock := range held.locks {
		paths = append(paths, lock.path)
	}
	held.mu.Unlock()
	for _, path := range paths {
		_ = syscall.Rmdir(path)
	}
}
