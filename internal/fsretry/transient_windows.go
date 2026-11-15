//go:build windows

package fsretry

import (
	"errors"
	"syscall"
)

// TransientRename reports whether a rename failed because another process,
// often anti-virus or an indexer, holds the file, or a file inside the
// directory, open without delete sharing. NTFS then refuses the rename with an
// access or sharing error until the handle closes.
func TransientRename(err error) bool {
	if transientWindowsCode(err) {
		return true
	}
	for _, transient := range []error{syscall.EPERM, syscall.EACCES, syscall.EBUSY} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}
