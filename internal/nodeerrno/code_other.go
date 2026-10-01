//go:build !windows

package nodeerrno

import "syscall"

// Code returns the Node error code for errno.
func Code(errno syscall.Errno) (string, bool) {
	return posixCode(errno)
}

// Errno returns the errno Node reports (error.errno) for code: the negated
// POSIX errno, which libuv passes through, when the platform defines one for
// the code, and libuv's own value otherwise.
func Errno(code string) (int, bool) {
	entry, ok := codes[code]
	if !ok {
		return 0, false
	}
	if errno, ok := posixErrnos[code]; ok {
		return -int(errno), true
	}
	return entry.libuvErrno, true
}

// describeErrno is libuv's naming of the negated errno, which Node's fs errors
// keep as error.errno.
func describeErrno(errno syscall.Errno) (string, string, int) {
	code, description := Name(-int(errno))
	return code, description, -int(errno)
}

// posixErrnos is posixCodes reversed: the POSIX errno of each code the
// platform defines.
var posixErrnos = func() map[string]syscall.Errno {
	errnos := make(map[string]syscall.Errno, len(posixCodes))
	for errno, code := range posixCodes {
		errnos[code] = errno
	}
	return errnos
}()
