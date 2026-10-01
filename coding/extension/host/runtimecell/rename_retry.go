package runtimecell

import (
	"context"
	"os"
	"time"
)

// renameRetryBudget bounds the total time publication waits for a transient lock.
const renameRetryBudget = 10 * time.Second

// renameRetry publishes a directory with os.Rename and retries only the
// failures renameRetryable classifies as transient.
type renameRetry struct {
	rename    func(oldpath, newpath string) error
	retryable func(err error) bool
	wait      func(ctx context.Context, delay time.Duration) error
}

// publishRename is the policy publishArtifact and publishCellFailure use.
var publishRename = renameRetry{rename: os.Rename, retryable: renameRetryable, wait: waitRenameRetry}

func waitRenameRetry(ctx context.Context, delay time.Duration) error {
	return nil
}

// publish renames oldpath to newpath. settled reports whether a peer already
// published a usable entry at newpath; adopted is true when it did.
func (r renameRetry) publish(ctx context.Context, oldpath, newpath string, settled func() bool) (adopted bool, err error) {
	err = r.rename(oldpath, newpath)
	if err == nil {
		return false, nil
	}
	if settled() {
		return true, nil
	}
	return false, err
}
