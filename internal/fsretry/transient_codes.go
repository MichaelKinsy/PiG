package fsretry

import (
	"errors"
	"syscall"
)

// windowsTransientCodes are the Windows error numbers (winerror.h) of a rename or replacement refused because another process holds a file open without
// delete sharing: ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION, and the two refusals of ReplaceFileW that leave the replaced file and
// the replacement under their own names (Microsoft, ReplaceFileW, "Return value"): ERROR_UNABLE_TO_REMOVE_REPLACED (1175), the replaced file could not be
// removed, and ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176), the replacement could not be renamed. A retry is safe in each and the end of the hold cures it.
// ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177) is not here: it leaves no file at the path and is for the caller to finish.
//
// They are numbers, not x/sys/windows names, so that the decision is one function that a test on any platform can run; only the Windows build calls it.
var windowsTransientCodes = [...]syscall.Errno{5, 32, 33, 1175, 1176}

// TransientWindowsCode reports whether err is, or wraps, one of [windowsTransientCodes].
func TransientWindowsCode(err error) bool {
	return transientWindowsCode(err)
}

// transientWindowsCode reports whether err is, or wraps, one of [windowsTransientCodes].
func transientWindowsCode(err error) bool {
	for _, code := range windowsTransientCodes {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
