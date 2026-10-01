package runtimecell

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Backoff for a transient rename lock: the delay doubles from renameRetryFirstDelay
// up to renameRetryMaxDelay, and the delays sum to renameRetryBudget.
// pig additive (D20): the shared cell cache publish retry has no upstream
// equivalent.
const (
	renameRetryBudget     = 10 * time.Second
	renameRetryFirstDelay = 25 * time.Millisecond
	renameRetryMaxDelay   = 500 * time.Millisecond
)

// renameRetry publishes a directory with a rename and retries only the
// failures retryable classifies as transient.
// pig additive (D20): the shared cell cache has no upstream equivalent. The
// retry follows npm's graceful-fs, which retries EPERM/EACCES/EBUSY renames on
// Windows with backoff, because anti-virus scanners and indexers hold freshly
// written files open without delete sharing, which makes NTFS refuse to rename
// the directory that contains them.
type renameRetry struct {
	rename    func(oldpath, newpath string) error
	retryable func(err error) bool
	wait      func(ctx context.Context, delay time.Duration) error
}

// publishRename is the policy publishArtifact and publishCellFailure use.
var publishRename = renameRetry{rename: os.Rename, retryable: renameRetryable, wait: waitRenameRetry}

func waitRenameRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// publish renames oldpath to newpath. settled reports whether a peer already
// published a usable entry at newpath; adopted is true when it did. settled is
// consulted after every failed rename, so a peer that finishes while this
// publisher waits is adopted instead of racing it. A failure that is not
// retryable, and the failure that exhausts the budget, are returned unchanged.
// A cancelled wait returns the last rename error together with the context
// error.
func (r renameRetry) publish(ctx context.Context, oldpath, newpath string, settled func() bool) (adopted bool, err error) {
	delay := renameRetryFirstDelay
	remaining := renameRetryBudget
	for {
		err = r.rename(oldpath, newpath)
		if err == nil {
			return false, nil
		}
		if settled() {
			return true, nil
		}
		if !r.retryable(err) || remaining <= 0 {
			return false, err
		}
		pause := min(delay, remaining)
		if waitErr := r.wait(ctx, pause); waitErr != nil {
			return false, fmt.Errorf("%w (retry stopped: %w)", err, waitErr)
		}
		remaining -= pause
		delay = min(delay*2, renameRetryMaxDelay)
	}
}
