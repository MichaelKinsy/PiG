//go:build unix

package ai

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile attempts a non-blocking exclusive advisory lock on f. It reports
// (true, nil) when the lock is held, (false, nil) when another holder has it
// (caller should retry), and (false, err) on a real error.
//
// Unix uses flock(2) advisory locks, matching upstream pi's per-instance
// auth.json lock. The windows counterpart uses LockFileEx.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

// unlockFile releases the advisory lock held on f. Best effort.
func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
