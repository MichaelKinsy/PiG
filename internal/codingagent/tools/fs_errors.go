package tools

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// NodeErrorCode returns the Node error code (error.code) for a file system or spawn error, or "" when it has none.
func NodeErrorCode(err error) string { return nodeErrorCode(err) }

// nodeErrorCode returns the Node error code (error.code) for a file system
// error, or "" when it has none. On Windows it is libuv's translation of the
// system error: a missing file or parent directory is ENOENT and
// ERROR_ACCESS_DENIED is EPERM.
func nodeErrorCode(err error) string {
	return nodeerrno.ErrorCode(err)
}

// NodeFSError formats err as Node's fs promises reject it:
// "<CODE>: <message>, <syscall> '<path>'" (read errors carry no path).
// Errors without a known errno keep their Go text.
func NodeFSError(err error, syscallName, path string) string {
	code := nodeErrorCode(err)
	description, ok := nodeerrno.Description(code)
	if !ok {
		return err.Error()
	}
	if path == "" {
		return fmt.Sprintf("%s: %s, %s", code, description, syscallName)
	}
	return fmt.Sprintf("%s: %s, %s '%s'", code, description, syscallName, path)
}
