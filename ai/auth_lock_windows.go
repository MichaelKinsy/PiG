//go:build windows

package ai

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile attempts a non-blocking exclusive lock on f. It reports
// (true, nil) when the lock is held, (false, nil) when another holder has it
// (caller should retry), and (false, err) on a real error.
//
// Windows uses LockFileEx with LOCKFILE_FAIL_IMMEDIATELY, the mandatory-lock
// analog of the unix flock path. A contended lock surfaces as
// ERROR_LOCK_VIOLATION, which maps to the unix EWOULDBLOCK retry case.
func tryLockFile(f *os.File) (bool, error) {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped),
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// unlockFile releases the lock held on f. Best effort.
func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
