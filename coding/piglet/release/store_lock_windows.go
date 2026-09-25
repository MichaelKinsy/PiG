package release

import (
	"os"

	"golang.org/x/sys/windows"
)

// Closing the handle releases the lock, including on process exit. The lock file must never be unlinked while the store exists.
func lockFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
