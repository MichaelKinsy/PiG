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
