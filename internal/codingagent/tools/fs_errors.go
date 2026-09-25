package tools

import (
	"errors"
	"fmt"
	"syscall"
)

// nodeErrorMessages are libuv's messages for the errno values the file
// tools surface (uv_strerror).
var nodeErrorMessages = map[syscall.Errno][2]string{
	syscall.ENOENT:       {"ENOENT", "no such file or directory"},
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
}

// nodeErrorCode returns the Node error code (error.code) for a file system
// error, or "" when it has none.
func nodeErrorCode(err error) string {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		if m, ok := nodeErrorMessages[errno]; ok {
			return m[0]
		}
	}
	return ""
}

// nodeFSError formats err as Node's fs promises reject it:
// "<CODE>: <message>, <syscall> '<path>'" (read errors carry no path).
// Errors without a known errno keep their Go text.
func nodeFSError(err error, syscallName, path string) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err.Error()
	}
	m, ok := nodeErrorMessages[errno]
	if !ok {
		return err.Error()
	}
	if path == "" {
		return fmt.Sprintf("%s: %s, %s", m[0], m[1], syscallName)
	}
	return fmt.Sprintf("%s: %s, %s '%s'", m[0], m[1], syscallName, path)
}
