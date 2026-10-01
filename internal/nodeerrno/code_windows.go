//go:build windows

package nodeerrno

import "syscall"

// Code returns the Node error code for errno: a Windows system error as
// libuv's uv_translate_sys_error names it, or one of the POSIX errnos Go
// invents on Windows.
func Code(errno syscall.Errno) (string, bool) {
	if code, ok := windowsCode(errno); ok {
		return code, true
	}
	return posixCode(errno)
}

// Errno returns the errno Node reports (error.errno) for code: libuv's own
// value on Windows.
func Errno(code string) (int, bool) {
	entry, ok := codes[code]
	return entry.libuvErrno, ok
}

// describeErrno is uv_translate_sys_error's code for a Windows error, which is
// UNKNOWN for an error it does not name.
func describeErrno(errno syscall.Errno) (string, string, int) {
	if code, ok := Code(errno); ok {
		return describeCode(code)
	}
	return describeCode("UNKNOWN")
}
