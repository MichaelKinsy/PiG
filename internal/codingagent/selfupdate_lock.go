package codingagent

import (
	"errors"
	"fmt"
	"os"

	"github.com/gofrs/flock"
)

// maxStandaloneUpdateLockAttempts bounds how often acquisition reopens the
// sidecar after a concurrent holder removed it.
// pig divergence (D39): Pi's proper-lockfile directory lock never locks a removed file, so it has no reopen bound; the OS-lock sidecar does.
const maxStandaloneUpdateLockAttempts = 16

var errStandaloneUpdateBusy = errors.New("another standalone pig update is already running")

// tryLockStandaloneUpdateSidecar opens the sidecar at path, creating it when
// absent, and tries to take its OS lock without waiting. Tests replace it to
// interleave a concurrent release between the open and the lock.
var tryLockStandaloneUpdateSidecar = func(path string) (*flock.Flock, bool, error) {
	lock := flock.New(path)
	locked, err := lock.TryLock()
	return lock, locked, err
}

// acquireStandaloneUpdateLock takes the per-executable OS lock at path and
// returns the function that releases it. Release removes the sidecar, as Pi's
// proper-lockfile release removes its lock (package-manager-cli.ts:219).
//
// Removing a lock file is safe only while the holder still owns it and every
// acquirer checks, after it locks, that its descriptor is the file the path
// names. A process that opened the sidecar before a release removed it locks
// an unlinked inode; it sees that the path names another file (or none), drops
// the orphan and reopens, so two processes never hold the lock at once.
func acquireStandaloneUpdateLock(path string) (release func() error, err error) {
	for range maxStandaloneUpdateLockAttempts {
		lock, locked, err := tryLockStandaloneUpdateSidecar(path)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("lock standalone update: %w", err), lock.Close())
		}
		if !locked {
			return nil, errors.Join(errStandaloneUpdateBusy, lock.Close())
		}
		held, heldErr := lock.Stat()
		named, namedErr := os.Stat(path)
		if heldErr == nil && namedErr == nil && os.SameFile(held, named) {
			return func() error {
				removeErr := os.Remove(path)
				if errors.Is(removeErr, os.ErrNotExist) {
					removeErr = nil
				}
				return errors.Join(removeErr, lock.Close())
			}, nil
		}
		if err := lock.Close(); err != nil {
			return nil, fmt.Errorf("lock standalone update: %w", err)
		}
	}
	return nil, errStandaloneUpdateBusy
}
