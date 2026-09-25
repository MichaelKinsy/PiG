//go:build unix

package release

import (
	"os"

	"golang.org/x/sys/unix"
)

// Closing the descriptor releases the lock, including on process exit. The lock file must never be unlinked while the store exists.
func lockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
