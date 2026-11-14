//go:build windows

package fsretry

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// ReplaceFileW's refusals that leave the replaced file and the replacement under their own names (winerror.h; Microsoft, ReplaceFileW, "Return value"):
// the replaced file could not be removed, or the replacement could not be renamed, because another process holds one of them open without delete
// sharing. Retrying the call is safe and is what the hold's end cures. ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177) is not here: it leaves no file at the
// path and is for the caller to finish.
const (
	errUnableToRemoveReplaced  = syscall.Errno(1175) // ERROR_UNABLE_TO_REMOVE_REPLACED
	errUnableToMoveReplacement = syscall.Errno(1176) // ERROR_UNABLE_TO_MOVE_REPLACEMENT
)

// TransientRename reports whether a rename failed because another process,
// often anti-virus or an indexer, holds the file, or a file inside the
// directory, open without delete sharing. NTFS then refuses the rename with an
// access or sharing error until the handle closes.
func TransientRename(err error) bool {
	for _, transient := range []error{
		windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION,
		errUnableToRemoveReplaced, errUnableToMoveReplacement,
		syscall.EPERM, syscall.EACCES, syscall.EBUSY,
	} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}
