// Package fsretry retries file system operations that Windows refuses for a
// short time while another process holds a file open.
package fsretry

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Backoff for a transient rename lock: the delay doubles from RenameFirstDelay
// up to RenameMaxDelay, and the delays sum to RenameBudget. Anti-virus scanners
// and indexers hold a freshly written or just-started file open for a short,
// unpredictable time; npm's graceful-fs retries its Windows renames the same
// way.
const (
	RenameBudget     = 10 * time.Second
	RenameFirstDelay = 25 * time.Millisecond
	RenameMaxDelay   = 500 * time.Millisecond
)

// Rename renames oldpath to newpath and retries a failure TransientRename
// classifies as a transient lock until the rename succeeds or RenameBudget is
// spent. wait performs each delay; nil waits on a timer. A failure that is not
// transient, and the one that exhausts the budget, are returned unchanged. A
// wait that ctx ends returns the last rename error together with ctx's error.
func Rename(ctx context.Context, oldpath, newpath string, wait func(context.Context, time.Duration) error) error {
	if wait == nil {
		wait = waitTimer
	}
	delay := RenameFirstDelay
	remaining := RenameBudget
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || !TransientRename(err) || remaining <= 0 {
			return err
		}
		pause := min(delay, remaining)
		if waitErr := wait(ctx, pause); waitErr != nil {
			return fmt.Errorf("%w (retry stopped: %w)", err, waitErr)
		}
		remaining -= pause
		delay = min(delay*2, RenameMaxDelay)
	}
}

func waitTimer(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
