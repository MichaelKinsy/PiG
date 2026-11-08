package runtime

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Ports apps/p2/src/runtime/locked-file.ts: a file written under proper-lockfile's lock, in place (the locking and write
// slice of Pi's auth-storage). `ErrLockedFileClosed` is its "Credential file is closed".

// ErrLockedFileClosed is returned by WithLock after Close.
var ErrLockedFileClosed = errors.New("Credential file is closed")

const (
	lockedFileStale    = 30 * time.Second
	lockedFileMaxDelay = 2 * time.Second
	lockedFileMode     = 0o600
)

// LockedFile is a file whose reads and writes happen under its lock. Writes are in place and 0600.
type LockedFile struct {
	path    string
	mu      sync.Mutex
	closed  bool
	pending sync.WaitGroup
}

// NewLockedFile is the locked file at path (normalized: `~` and relative paths as p2's configuration files).
func NewLockedFile(path string) *LockedFile {
	return &LockedFile{path: NormalizePath(path)}
}

// LockResult is what a WithLock callback decides: Next, when non-nil, replaces the file's content.
type LockResult[T any] struct {
	Result T
	Next   *string
}

// WithLock calls fn with the file's current content (nil when absent: the file is created as `{}` first) under the lock,
// then writes its Next. Cancelling ctx stops waiting for the lock and fails the operation before it writes.
func WithLock[T any](ctx context.Context, file *LockedFile, fn func(current *string) (LockResult[T], error)) (T, error) {
	var zero T
	file.mu.Lock()
	if file.closed {
		file.mu.Unlock()
		return zero, ErrLockedFileClosed
	}
	file.pending.Add(1)
	file.mu.Unlock()
	defer file.pending.Done()
	return runWithLock(ctx, file.path, fn)
}

// Close refuses later operations and waits for the running ones, which may have outlived callers that stopped waiting.
func (f *LockedFile) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.pending.Wait()
}

func ensureFileExists(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, lockedFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, err = file.WriteString("{}")
	return errors.Join(err, file.Close())
}

// acquireLock waits for the lock like proper-lockfile with the retry loop of Pi's auth storage: exponential delays with
// jitter, up to the stale time.
func acquireLock(ctx context.Context, path string) (*pilock.Lock, error) {
	deadline := time.Now().Add(lockedFileStale)
	for retry := 0; ; retry++ {
		lock, err := pilock.AcquireWithOptions(ctx, path, pilock.AcquireOptions{Stale: lockedFileStale, Update: lockedFileStale / 2, Retry: time.Millisecond, Wait: 0})
		if err == nil {
			return lock, nil
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		remaining := time.Until(deadline)
		if !errors.Is(err, pilock.ErrLocked) || remaining <= 0 {
			return nil, err
		}
		base := min(10*time.Millisecond<<min(retry, 20), lockedFileMaxDelay/2)
		delay := min(time.Duration(float64(base)*(1+rand.Float64())), remaining)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, context.Cause(ctx)
		case <-timer.C:
		}
	}
}

func runWithLock[T any](ctx context.Context, path string, fn func(current *string) (LockResult[T], error)) (result T, err error) {
	var zero T
	if err := context.Cause(ctx); err != nil {
		return zero, err
	}
	if err := ensureFileExists(path); err != nil {
		return zero, err
	}
	lock, err := acquireLock(ctx, path)
	if err != nil {
		return zero, err
	}
	defer func() { _ = lock.Release() }() // unlock errors are ignored when the lock was compromised
	// The lock's context carries a heartbeat compromise as its cause.
	if err := context.Cause(lock.Context()); err != nil {
		return zero, err
	}
	var current *string
	if data, readErr := os.ReadFile(path); readErr == nil {
		text := string(data)
		current = &text
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return zero, fmt.Errorf("read %s: %w", path, readErr)
	}
	outcome, err := fn(current)
	if err != nil {
		return zero, err
	}
	if err := context.Cause(lock.Context()); err != nil {
		return zero, err
	}
	if err := context.Cause(ctx); err != nil {
		return zero, err
	}
	if outcome.Next != nil {
		if err := os.WriteFile(path, []byte(*outcome.Next), lockedFileMode); err != nil {
			return zero, fmt.Errorf("write %s: %w", path, err)
		}
	}
	if err := context.Cause(lock.Context()); err != nil {
		return zero, err
	}
	return outcome.Result, nil
}
