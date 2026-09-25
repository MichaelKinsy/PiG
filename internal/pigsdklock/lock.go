package pigsdklock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
)

const (
	lockFile        = ".transaction.lock"
	defaultTimeout  = 10 * time.Minute
	pollingInterval = 50 * time.Millisecond
)

func waitTimeout() time.Duration {
	if value := os.Getenv("PIG_CELL_BUILD_TIMEOUT"); value != "" {
		if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
			return duration
		}
	}
	return defaultTimeout
}

// WaitObserver is told when an acquisition must wait for another holder. It
// returns the function called when the wait ends, acquired or not.
type WaitObserver func(lockPath string, shared bool) (done func())

var waitObserver atomic.Pointer[WaitObserver]

// SetWaitObserver installs the process-wide observer of contended acquisitions,
// so a waiting startup can say why it is waiting. Nil removes it.
func SetWaitObserver(observer WaitObserver) {
	if observer == nil {
		waitObserver.Store(nil)
		return
	}
	waitObserver.Store(&observer)
}

// AcquireBuild holds a shared lease on one config root's staged SDKs.
func AcquireBuild(ctx context.Context, configRoot string) (func() error, error) {
	return acquire(ctx, configRoot, true)
}

// AcquireBuildForRoot holds a shared lease when sdkRoot is a staged Pig SDK.
func AcquireBuildForRoot(ctx context.Context, sdkRoot string) (func() error, error) {
	absoluteRoot, err := filepath.Abs(sdkRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve staged SDK root: %w", err)
	}
	if configRoot, ok := stagedConfigRoot(absoluteRoot); ok {
		return AcquireBuild(ctx, configRoot)
	}
	physicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return func() error { return nil }, nil
	}
	if configRoot, ok := stagedConfigRoot(physicalRoot); ok {
		return AcquireBuild(ctx, configRoot)
	}
	return func() error { return nil }, nil
}

func stagedConfigRoot(sdkRoot string) (string, bool) {
	pigsdkRoot := filepath.Dir(sdkRoot)
	stateRoot := filepath.Dir(pigsdkRoot)
	if filepath.Base(pigsdkRoot) != "pigsdk" || filepath.Base(stateRoot) != "state" {
		return "", false
	}
	return filepath.Dir(stateRoot), true
}

// WithBuild holds a shared lease while fn consumes one config root's SDKs.
func WithBuild[T any](ctx context.Context, configRoot string, fn func() (T, error)) (result T, err error) {
	release, err := AcquireBuild(ctx, configRoot)
	if err != nil {
		return result, fmt.Errorf("acquire SDK transaction lock: %w", err)
	}
	defer func() { err = errors.Join(err, release()) }()
	return fn()
}

// WithBuildCandidates holds shared leases for staged SDK paths before fn resolves one of them.
func WithBuildCandidates[T any](ctx context.Context, candidates []string, fn func() (T, error)) (result T, err error) {
	releases := make([]func() error, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		release, acquireErr := AcquireBuildForRoot(ctx, candidate)
		if acquireErr != nil {
			for _, release := range slices.Backward(releases) {
				acquireErr = errors.Join(acquireErr, release())
			}
			return result, acquireErr
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range slices.Backward(releases) {
			err = errors.Join(err, release())
		}
	}()
	return fn()
}

type buildEntryResult struct {
	path  string
	valid bool
}

// WithBuildEntryCandidates adapts a cache-entry lookup to [WithBuildCandidates].
func WithBuildEntryCandidates(ctx context.Context, candidates []string, fn func() (string, bool, error)) (string, bool, error) {
	result, err := WithBuildCandidates(ctx, candidates, func() (buildEntryResult, error) {
		path, valid, workErr := fn()
		return buildEntryResult{path: path, valid: valid}, workErr
	})
	return result.path, result.valid, err
}

// WithBuildForRoot holds a shared lease while fn consumes a staged SDK root.
func WithBuildForRoot[T any](ctx context.Context, sdkRoot string, fn func() (T, error)) (result T, err error) {
	release, err := AcquireBuildForRoot(ctx, sdkRoot)
	if err != nil {
		return result, fmt.Errorf("acquire SDK transaction lock: %w", err)
	}
	defer func() { err = errors.Join(err, release()) }()
	return fn()
}

// WithBuildEntryForRoot holds a shared lease while fn selects a staged-SDK artifact.
func WithBuildEntryForRoot(ctx context.Context, sdkRoot string, fn func() (string, bool, error)) (entry string, valid bool, err error) {
	release, err := AcquireBuildForRoot(ctx, sdkRoot)
	if err != nil {
		return "", false, fmt.Errorf("acquire SDK transaction lock: %w", err)
	}
	defer func() { err = errors.Join(err, release()) }()
	return fn()
}

// AcquireStage holds an exclusive lease on one config root's staged SDKs.
func AcquireStage(ctx context.Context, configRoot string) (func() error, error) {
	return acquire(ctx, configRoot, false)
}

func acquire(ctx context.Context, configRoot string, shared bool) (func() error, error) {
	root, err := filepath.Abs(configRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Pig config root: %w", err)
	}
	lockDir := filepath.Join(root, "state", "pigsdk")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create SDK transaction lock directory: %w", err)
	}
	lockPath := filepath.Join(lockDir, lockFile)
	lock := flock.New(lockPath)
	lockCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, waitTimeout())
		defer cancel()
	}
	try := lock.TryLock
	tryContext := lock.TryLockContext
	if shared {
		try, tryContext = lock.TryRLock, lock.TryRLockContext
	}
	locked, err := try()
	if err == nil && !locked {
		if observer := waitObserver.Load(); observer != nil {
			done := (*observer)(lockPath, shared)
			defer done()
		}
		locked, err = tryContext(lockCtx, pollingInterval)
	}
	if err != nil {
		return nil, fmt.Errorf("wait for SDK transaction lock %s: %w", lockPath, err)
	}
	if !locked {
		return nil, fmt.Errorf("timed out waiting for SDK transaction lock %s", lockPath)
	}
	return lock.Unlock, nil
}
