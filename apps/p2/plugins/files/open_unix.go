//go:build !windows

package files

import (
	"os"
	"syscall"
)

// openFile opens path read-only without blocking on a named pipe: fstat rejects non-regular sources afterwards
// (O_NONBLOCK, as Node's snapshot code opens them).
func openFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
