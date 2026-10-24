//go:build !windows

package nodeerrno

import (
	"os"
	"syscall"
)

// Rename is rename(2), as libuv calls it. os.Rename checks the destination first and answers EEXIST for a directory over
// a directory, and an error of its own for a file over a directory, where the system says ENOTEMPTY and EISDIR, as
// Node's fs.rename reports.
func Rename(oldpath, newpath string) error {
	if err := syscall.Rename(oldpath, newpath); err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}
