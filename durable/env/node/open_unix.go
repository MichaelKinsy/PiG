//go:build !windows

package node

import (
	"os"
	"syscall"
)

// openReadOnly opens path for reading. It is nonblocking, so opening a FIFO does not wait for a writer; with noFollow
// it fails with ELOOP (EMLINK on some BSDs) for a symbolic link as the final path component.
func openReadOnly(path string, noFollow bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if noFollow {
		flags |= syscall.O_NOFOLLOW
	}
	return os.OpenFile(path, flags, 0)
}
