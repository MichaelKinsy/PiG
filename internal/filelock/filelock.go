// Package filelock is the advisory file lock the extension cache and the SDK staging take.
//
// pig additive (D20): the shared cell cache and its locks have no upstream equivalent. A lock is held through flock(2) (LockFileEx on
// Windows); some file systems refuse it, as an NFS mount without a lock manager (ENOLCK), a FUSE file system or an SMB share
// (ENOSYS, EOPNOTSUPP) do.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"syscall"
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

// Unsupported reports whether err says the file system refuses file locks: no lock manager (ENOLCK), no flock (ENOSYS), or locking not
// supported (EOPNOTSUPP, ENOTSUP). Other failures, a contended lock among them, are not.
func Unsupported(err error) bool {
	return errors.Is(err, syscall.ENOLCK) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTSUP)
}

// Explain returns err, or, when the file system that holds dir refuses file locks, an error that names dir and the remedy and wraps err.
// The extension cache cannot run without file locks: its builders, readers and collector exclude each other through them, and pig does
// not start an extension from a cache it cannot protect. Such a lock failure is permanent, so the remedy is a different file system.
func Explain(dir string, err error) error {
	if !Unsupported(err) {
		return err
	}
	return fmt.Errorf("the file system that holds %s does not support file locks (%w); the extension cache and the SDK staging need them to build, share and collect entries safely: point PIG_HOME at a directory on a file system that does, such as a local disk", dir, err)
}
