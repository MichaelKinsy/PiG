//go:build windows

package fsretry

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// TransientRename reports whether a rename failed because another process,
// often anti-virus or an indexer, holds the file, or a file inside the
// directory, open without delete sharing. NTFS then refuses the rename with an
// access or sharing error until the handle closes.
func TransientRename(err error) bool {
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
