// Package filelock is the advisory file lock the extension cache and the SDK staging take.
//
// pig additive (D20): the shared cell cache and its locks have no upstream equivalent. A lock is held through flock(2) (LockFileEx on
// Windows); some file systems refuse it, as an NFS mount without a lock manager (ENOLCK), a FUSE file system or an SMB share
// (ENOSYS, EOPNOTSUPP) do.
package filelock

import (
	"context"
	"time"

	"github.com/gofrs/flock"
)

// Lock is the part of gofrs/flock the callers use. A lock is shared (the RLock forms) or exclusive, and is released by process death.
type Lock interface {
	TryLock() (bool, error)
	TryRLock() (bool, error)
	TryLockContext(ctx context.Context, retryDelay time.Duration) (bool, error)
	TryRLockContext(ctx context.Context, retryDelay time.Duration) (bool, error)
	RLock() error
	Unlock() error
}

// New returns the lock on the file at path. Tests replace it with a lock that fails the way a file system without file locks does.
var New = func(path string) Lock { return flock.New(path) }
