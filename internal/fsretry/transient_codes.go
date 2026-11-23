package fsretry

import (
	"errors"
	"syscall"
)

// windowsTransientCodes are the Windows error numbers (winerror.h) of a rename refused because another process holds a file open without delete sharing:
// ERROR_ACCESS_DENIED (5), ERROR_SHARING_VIOLATION (32), and ERROR_LOCK_VIOLATION (33). A retry is safe in each and the end of the hold cures it.
//
// They are numbers, not x/sys/windows names, so that the decision is one function that a test on any platform can run; only the Windows build calls it.
var windowsTransientCodes = [...]syscall.Errno{5, 32, 33}

// transientWindowsCode reports whether err is, or wraps, one of [windowsTransientCodes].
func transientWindowsCode(err error) bool {
	for _, code := range windowsTransientCodes {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
