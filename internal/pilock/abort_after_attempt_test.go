package pilock

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// abortedAfterFirstCheck is a caller context that reports the abort from its second check on, so the abort arrives while an acquisition attempt runs.
type abortedAfterFirstCheck struct {
	context.Context
	checks atomic.Int32
}

func (c *abortedAfterFirstCheck) Err() error {
	if c.checks.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}

// Pi's cancellable acquisition checks the abort before it classifies a failed attempt (auth-storage.ts:135, `signal?.throwIfAborted()` in the catch; unchanged in 0.99.1): an abort that arrives while an attempt fails is thrown instead of the attempt's error.
func TestAcquireReportsAnAbortThatArrivesDuringAFailedAttemptUpstream(t *testing.T) {
	ctx := &abortedAfterFirstCheck{Context: context.Background()}
	path := filepath.Join(t.TempDir(), "missing", "auth.json")
	lock, err := Acquire(ctx, path)
	if lock != nil {
		_ = lock.Release()
		t.Fatal("acquired a lock under a missing directory")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want the abort that arrived during the failed attempt", err)
	}
}
