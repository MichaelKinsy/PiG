package runtimecell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var errTransientLock = errors.New("transient lock")

// recordingRetry builds a policy whose rename and wait are injected, so the
// retry schedule is observable without sleeping.
type recordingRetry struct {
	renames int
	delays  []time.Duration
}

func (r *recordingRetry) policy(rename func(attempt int) error, retryable func(error) bool) renameRetry {
	return renameRetry{
		rename: func(string, string) error {
			r.renames++
			return rename(r.renames)
		},
		retryable: retryable,
		wait: func(_ context.Context, delay time.Duration) error {
			r.delays = append(r.delays, delay)
			return nil
		},
	}
}

func (r *recordingRetry) totalDelay() time.Duration {
	var total time.Duration
	for _, delay := range r.delays {
		total += delay
	}
	return total
}

func never() bool { return false }

func retryEverything(error) bool { return true }

func TestRenameRetryRetriesTransientErrorsUntilRenameSucceeds(t *testing.T) {
	var rec recordingRetry
	policy := rec.policy(func(attempt int) error {
		if attempt <= 3 {
			return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errTransientLock}
		}
		return nil
	}, func(err error) bool { return errors.Is(err, errTransientLock) })

	adopted, err := policy.publish(t.Context(), "a", "b", never)
	if err != nil || adopted {
		t.Fatalf("publish = (adopted %v, %v), want (false, nil)", adopted, err)
	}
	if rec.renames != 4 {
		t.Fatalf("renames = %d, want 4 (three transient failures then success)", rec.renames)
	}
	if len(rec.delays) != 3 {
		t.Fatalf("waits = %v, want one per transient failure", rec.delays)
	}
	for i, delay := range rec.delays {
		if delay <= 0 {
			t.Fatalf("delay[%d] = %v, want positive backoff", i, delay)
		}
		if i > 0 && delay < rec.delays[i-1] {
			t.Fatalf("delays %v must not shrink", rec.delays)
		}
	}
	if rec.delays[len(rec.delays)-1] <= rec.delays[0] {
		t.Fatalf("delays %v must back off", rec.delays)
	}
}

func TestRenameRetryReportsNonRetryableErrorUnchangedWithoutRetrying(t *testing.T) {
	var rec recordingRetry
	want := &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errors.New("disk full")}
	policy := rec.policy(func(int) error { return want }, func(error) bool { return false })

	adopted, err := policy.publish(t.Context(), "a", "b", never)
	if adopted {
		t.Fatal("a failed rename must not report a peer's entry")
	}
	if err != error(want) {
		t.Fatalf("err = %#v, want the rename error unchanged", err)
	}
	if rec.renames != 1 || len(rec.delays) != 0 {
		t.Fatalf("renames = %d, waits = %v; a non-retryable error must not retry", rec.renames, rec.delays)
	}
}

func TestRenameRetryStopsAtTheBudgetAndReportsTheLastError(t *testing.T) {
	var rec recordingRetry
	var last error
	policy := rec.policy(func(attempt int) error {
		last = &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errors.New("access denied attempt " + string(rune('0'+attempt%10)))}
		return last
	}, retryEverything)

	adopted, err := policy.publish(t.Context(), "a", "b", never)
	if adopted {
		t.Fatal("a failed rename must not report a peer's entry")
	}
	if err != last {
		t.Fatalf("err = %v, want the final rename error unchanged (%v)", err, last)
	}
	if got := rec.totalDelay(); got != renameRetryBudget {
		t.Fatalf("total delay = %v, want exactly the %v budget", got, renameRetryBudget)
	}
	if rec.renames != len(rec.delays)+1 {
		t.Fatalf("renames = %d, waits = %d; each wait precedes one retry", rec.renames, len(rec.delays))
	}
	for _, delay := range rec.delays {
		if delay <= 0 || delay > time.Second {
			t.Fatalf("delay %v outside (0, 1s]; backoff must be capped so a lock is noticed promptly", delay)
		}
	}
}

func TestRenameRetryAdoptsAPeerEntryPublishedWhileWaiting(t *testing.T) {
	var rec recordingRetry
	checks := 0
	policy := rec.policy(func(int) error { return errTransientLock }, retryEverything)

	adopted, err := policy.publish(t.Context(), "a", "b", func() bool {
		checks++
		return checks == 3
	})
	if err != nil || !adopted {
		t.Fatalf("publish = (adopted %v, %v), want the peer's entry adopted", adopted, err)
	}
	if checks != 3 || rec.renames != 3 {
		t.Fatalf("checks = %d, renames = %d, want the destination re-checked after every failed rename and no rename after adoption", checks, rec.renames)
	}
}

func TestRenameRetryAdoptsAPeerEntryAfterANonRetryableFailure(t *testing.T) {
	var rec recordingRetry
	policy := rec.policy(func(int) error { return errors.New("exists") }, func(error) bool { return false })

	adopted, err := policy.publish(t.Context(), "a", "b", func() bool { return true })
	if err != nil || !adopted {
		t.Fatalf("publish = (adopted %v, %v), want a peer's valid entry to satisfy any failed rename", adopted, err)
	}
	if rec.renames != 1 {
		t.Fatalf("renames = %d, want 1", rec.renames)
	}
}

func TestRenameRetryDoesNotCheckForAPeerAfterASuccessfulRename(t *testing.T) {
	var rec recordingRetry
	policy := rec.policy(func(int) error { return nil }, retryEverything)
	adopted, err := policy.publish(t.Context(), "a", "b", func() bool {
		t.Fatal("settled consulted after a successful rename")
		return true
	})
	if err != nil || adopted {
		t.Fatalf("publish = (adopted %v, %v), want (false, nil)", adopted, err)
	}
}

func TestRenameRetryCancellationDuringWaitKeepsBothCauses(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	renames := 0
	policy := renameRetry{
		rename:    func(string, string) error { renames++; return errTransientLock },
		retryable: retryEverything,
		wait: func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		},
	}
	adopted, err := policy.publish(ctx, "a", "b", never)
	if adopted {
		t.Fatal("cancelled publish adopted a peer entry")
	}
	if !errors.Is(err, errTransientLock) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want both the lock failure and the cancellation", err)
	}
	if renames != 1 {
		t.Fatalf("renames = %d, want no retry after cancellation", renames)
	}
}

func TestRenameRetryCancelledBeforeStartStillTriesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	renames := 0
	policy := renameRetry{
		rename:    func(string, string) error { renames++; return nil },
		retryable: retryEverything,
		wait:      waitRenameRetry,
	}
	if _, err := policy.publish(ctx, "a", "b", never); err != nil {
		t.Fatalf("publish error = %v; a rename that succeeds needs no wait", err)
	}
	if renames != 1 {
		t.Fatalf("renames = %d, want 1", renames)
	}
}

func TestWaitRenameRetryHonoursCancellationPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	if err := waitRenameRetry(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitRenameRetry = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waitRenameRetry blocked %v on a cancelled context", elapsed)
	}
	if err := waitRenameRetry(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("waitRenameRetry = %v, want nil after the delay", err)
	}
}

// publishWithInjectedRename swaps the package policy for one whose rename is
// scripted and whose waits do not sleep.
func publishWithInjectedRename(t *testing.T, retryable func(error) bool, rename func(attempt int, oldpath, newpath string) error) *int {
	t.Helper()
	previous := publishRename
	attempts := new(int)
	publishRename = renameRetry{
		rename: func(oldpath, newpath string) error {
			*attempts++
			return rename(*attempts, oldpath, newpath)
		},
		retryable: retryable,
		wait:      func(context.Context, time.Duration) error { return nil },
	}
	t.Cleanup(func() { publishRename = previous })
	return attempts
}

func TestPublishArtifactRetriesATransientPublishRename(t *testing.T) {
	final := filepath.Join(t.TempDir(), "cells", "node", "deadbeef")
	attempts := publishWithInjectedRename(t, func(err error) bool { return errors.Is(err, errTransientLock) }, func(attempt int, oldpath, newpath string) error {
		if attempt <= 2 {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errTransientLock}
		}
		return os.Rename(oldpath, newpath)
	})
	entry, err := PublishArtifact(t.Context(), final, "runner", "deadbeef", "node", func(scratch string) (string, error) {
		path := filepath.Join(scratch, "runner")
		return path, os.WriteFile(path, []byte("launcher"), 0o644)
	})
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	if *attempts != 3 || entry.Reused {
		t.Fatalf("attempts = %d, reused = %v, want 3 renames and a fresh publication", *attempts, entry.Reused)
	}
	if _, ok := ValidEntry(final, EntryIdentity{InputDigest: "deadbeef", Artifact: "runner", Language: "node"}); !ok {
		t.Fatal("published entry is not valid")
	}
}

func TestPublishArtifactReportsAPersistentPublishRenameFailureUnchanged(t *testing.T) {
	final := filepath.Join(t.TempDir(), "cells", "node", "deadbeef")
	attempts := publishWithInjectedRename(t, retryEverything, func(_ int, oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errTransientLock}
	})
	_, err := PublishArtifact(t.Context(), final, "runner", "deadbeef", "node", func(scratch string) (string, error) {
		path := filepath.Join(scratch, "runner")
		return path, os.WriteFile(path, []byte("launcher"), 0o644)
	})
	var linkErr *os.LinkError
	if !errors.Is(err, errTransientLock) || !errors.As(err, &linkErr) {
		t.Fatalf("err = %v, want the rename error with its cause", err)
	}
	if want := "publish cell entry: rename "; len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
		t.Fatalf("err = %q, want the %q prefix preserved", err, want)
	}
	if *attempts < 2 {
		t.Fatalf("attempts = %d, want the retry policy consulted", *attempts)
	}
	if _, statErr := os.Stat(final); !os.IsNotExist(statErr) {
		t.Fatalf("final entry exists after a failed publication: %v", statErr)
	}
}

func TestPublishArtifactAdoptsAPeerEntryWhenRenameKeepsFailing(t *testing.T) {
	final := filepath.Join(t.TempDir(), "cells", "node", "deadbeef")
	identity := EntryIdentity{InputDigest: "deadbeef", Artifact: "runner", Language: "node"}
	var peerErr error
	publishWithInjectedRename(t, retryEverything, func(attempt int, oldpath, newpath string) error {
		if attempt == 2 {
			// A peer that does not honour the build lock publishes between attempts.
			peerErr = os.Rename(oldpath, newpath)
		}
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errTransientLock}
	})
	entry, err := PublishArtifact(t.Context(), final, "runner", "deadbeef", "node", func(scratch string) (string, error) {
		path := filepath.Join(scratch, "runner")
		return path, os.WriteFile(path, []byte("launcher"), 0o644)
	})
	if peerErr != nil {
		t.Fatalf("simulated peer publication: %v", peerErr)
	}
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	art, ok := ValidEntry(final, identity)
	if !ok || !entry.Reused || entry.ArtifactPath != art {
		t.Fatalf("entry = %+v (valid %v at %q), want the peer's entry adopted", entry, ok, art)
	}
}

func TestPublishArtifactWithFailureCacheRetriesATransientFailurePublish(t *testing.T) {
	final := filepath.Join(t.TempDir(), "cells", "node", "deadbeef")
	attempts := publishWithInjectedRename(t, func(err error) bool { return errors.Is(err, errTransientLock) }, func(attempt int, oldpath, newpath string) error {
		if attempt <= 2 {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errTransientLock}
		}
		return os.Rename(oldpath, newpath)
	})
	buildErr := errors.New("deterministic compile error")
	_, err := publishArtifactWithFailureCache(t.Context(), final, "runner", "deadbeef", "node", func(string) (string, error) {
		return "", cacheBuildFailure(buildErr)
	})
	if !errors.Is(err, buildErr) {
		t.Fatalf("err = %v, want the build failure", err)
	}
	if *attempts != 3 {
		t.Fatalf("attempts = %d, want the failure publication retried to success", *attempts)
	}
	if _, ok := readCellFailureMetadata(final); !ok {
		t.Fatal("deterministic failure was not cached after the retry")
	}
}
