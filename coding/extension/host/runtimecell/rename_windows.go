//go:build windows

package runtimecell

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// renameRetryable reports whether err is a transient lock worth retrying: a
// directory rename fails with an access or sharing error while any process,
// often anti-virus or an indexer, holds a file inside it open.
func renameRetryable(err error) bool {
	for _, transient := range []error{
		windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION,
		syscall.EPERM, syscall.EACCES, syscall.EBUSY,
	} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}
