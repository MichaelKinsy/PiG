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
	"syscall"
	"time"
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

// ErrLocked reports contention under Pi's directory protocol.
var ErrLocked = errors.New("lock file is already being held")

// ErrLegacyLocked reports an older PiG writer; acquisition uses the same retry budget as directory contention.
var ErrLegacyLocked = fmt.Errorf("%w by an older PiG process; stop the older PiG before upgrading", ErrLocked)
var errCompromised = errors.New("lock compromised")

// Lock owns a directory and its mtime heartbeat. Compromise cancels its operation context; Release joins the heartbeat and removes only the owned directory.
type Lock struct {
	path       string
	mu         sync.Mutex
	mtime      time.Time
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
			if err := context.Cause(ctx); err != nil {
				return nil, errors.Join(err, lock.Release())
			}
			return lock, nil
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
			if err := context.Cause(ctx); err != nil {
				return nil, errors.Join(err, lock.Release())
			}
			return lock, nil
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

func acquire(path string, stale time.Duration) (*Lock, error) {
	return tryAcquire(context.Background(), path, stale)
}

func tryAcquire(ctx context.Context, path string, stale time.Duration) (*Lock, error) {
	return tryAcquireWithUpdate(ctx, path, stale, stale/2, nil)
}

func tryAcquireWithUpdate(ctx context.Context, path string, stale, update time.Duration, onCompromised func(error)) (*Lock, error) {
	path, err := filepath.Abs(path + ".lock")
	if err != nil {
		return nil, err
	}
	if err := mkdir(path, stale); err != nil {
		return nil, err
	}
	mtime, err := touch(path)
	if err != nil {
		return nil, errors.Join(err, syscall.Rmdir(path))
	}
	ctx, cancel := context.WithCancelCause(ctx)
	lock := &Lock{path: path, mtime: mtime, ctx: ctx, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{}), onCompromised: onCompromised}
	held.add(lock)
	go lock.heartbeat(stale, update)
	return lock, nil
}

func mkdir(path string, stale time.Duration) error {
	err := os.Mkdir(path, 0o777)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return err
	}
	if stale <= 0 {
		return ErrLocked
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mkdir(path, 0)
	}
	if err != nil {
		return err
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
	if !info.IsDir() {
		return fmt.Errorf("lock path is neither a directory nor an empty legacy sidecar: %s", path)
	}
	if !info.ModTime().Before(time.Now().Add(-stale)) {
		return ErrLocked
	}
	if err := syscall.Rmdir(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return mkdir(path, 0)
}

func touch(path string) (time.Time, error) {
	now := time.Now().Truncate(time.Millisecond)
	if err := os.Chtimes(path, now, now); err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (l *Lock) check() error {
	if l.err != nil {
		return l.err
	}
	info, err := os.Stat(l.path)
	if err != nil {
		l.err = &CompromisedError{Cause: err}
	} else if !info.IsDir() || !info.ModTime().Equal(l.mtime) {
		l.err = &CompromisedError{}
	}
	if l.err != nil {
		held.remove(l)
		l.cancel(l.err)
	}
	return l.err
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
		info, err := os.Stat(l.path)
		if err == nil && (!info.IsDir() || !info.ModTime().Equal(l.mtime)) {
			l.err = &CompromisedError{}
		} else {
			if err == nil {
				var next time.Time
				next, err = touch(l.path)
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

// Release joins the heartbeat before removing the owned directory. Repeated calls return the same result.
func (l *Lock) Release() error {
	l.once.Do(func() {
		held.remove(l)
		close(l.stop)
		<-l.done
		l.mu.Lock()
		defer l.mu.Unlock()
		l.releaseErr = l.check()
		if l.releaseErr == nil {
			l.releaseErr = errors.Join(context.Cause(l.ctx), syscall.Rmdir(l.path))
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
