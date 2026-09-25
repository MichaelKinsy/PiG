//go:build !windows

package env

import "syscall"

// systemErrnoName returns the Node error code for errno.
func systemErrnoName(errno syscall.Errno) (string, bool) {
	name, ok := errnoNames[errno]
	return name, ok
}
