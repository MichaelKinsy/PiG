//go:build !windows

package nodeerrno

import "syscall"

// Code returns the Node error code for errno.
func Code(errno syscall.Errno) (string, bool) {
	return posixCode(errno)
}
