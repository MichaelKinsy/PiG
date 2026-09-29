package pilock

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"syscall"
)

// CompromisedError is the ECOMPROMISED error that proper-lockfile 4.1.2 passes to onCompromised (lib/lockfile.js:119-138,153-156,185-201). Cause is the failed stat or utime, or nil when the lock directory's mtime is no longer ours.
type CompromisedError struct {
	Cause error
}

// Code is the error code proper-lockfile assigns to every compromise (lib/lockfile.js:121,137,155).
func (*CompromisedError) Code() string { return "ECOMPROMISED" }

// Unwrap keeps errors.Is matching the compromise itself and the file-system failure behind it.
func (e *CompromisedError) Unwrap() []error {
	if e.Cause == nil {
		return []error{errCompromised}
	}
	return []error{errCompromised, e.Cause}
}

// Error is the Node message: the fs error text `<CODE>: <description>, <syscall> '<path>'`, or lib/lockfile.js:136's message when the mtime changed.
func (e *CompromisedError) Error() string {
	if e.Cause == nil {
		return "Unable to update lock within the stale threshold"
	}
	if fsErr, ok := nodeFSError(e.Cause); ok {
		return fsErr.message()
	}
	return e.Cause.Error()
}

// Inspect renders the error as Node's util.inspect does when a throw from a timer reaches the uncaught-exception handler, without the stack frames of the JavaScript source.
func (e *CompromisedError) Inspect() string {
	if e.Cause != nil {
		if fsErr, ok := nodeFSError(e.Cause); ok {
			properties := []string{}
			if errno, known := fsErr.errno(); known {
				properties = append(properties, fmt.Sprintf("errno: %d", errno))
			}
			properties = append(properties, "code: 'ECOMPROMISED'", "syscall: "+inspectString(fsErr.syscall), "path: "+inspectString(fsErr.path))
			return "[Error: " + fsErr.message() + "] {\n  " + strings.Join(properties, ",\n  ") + "\n}"
		}
	}
	return "Error: " + e.Error() + " {\n  code: 'ECOMPROMISED'\n}"
}

type nodeFSFailure struct {
	code, description, syscall, path string
	errnoValue                       syscall.Errno
}

func (f nodeFSFailure) message() string {
	return f.code + ": " + f.description + ", " + f.syscall + " '" + f.path + "'"
}

var nodeErrnoNames = []struct {
	errno                   syscall.Errno
	code, description       string
	windowsLibuvErrno       int
	matchesFileSystemErrors func(error) bool
}{
	{syscall.ENOENT, "ENOENT", "no such file or directory", -4058, func(err error) bool { return errors.Is(err, fs.ErrNotExist) }},
	{syscall.EACCES, "EACCES", "permission denied", -4092, nil},
	{syscall.EPERM, "EPERM", "operation not permitted", -4048, nil},
	{syscall.ENOTDIR, "ENOTDIR", "not a directory", -4052, nil},
	{syscall.EIO, "EIO", "i/o error", -4070, nil},
	{syscall.EROFS, "EROFS", "read-only file system", -4030, nil},
}

// nodeFSError maps a Go path error to the Node fs error it corresponds to for the errors proper-lockfile can meet. An error outside the table keeps Go's text.
func nodeFSError(err error) (nodeFSFailure, bool) {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		return nodeFSFailure{}, false
	}
	call := pathErr.Op
	if call == "chtimes" {
		call = "utime"
	}
	for _, entry := range nodeErrnoNames {
		if errors.Is(pathErr.Err, entry.errno) || (entry.matchesFileSystemErrors != nil && entry.matchesFileSystemErrors(pathErr.Err)) {
			return nodeFSFailure{code: entry.code, description: entry.description, syscall: call, path: pathErr.Path, errnoValue: entry.errno}, true
		}
	}
	return nodeFSFailure{}, false
}

func (f nodeFSFailure) errno() (int, bool) {
	for _, entry := range nodeErrnoNames {
		if entry.errno != f.errnoValue {
			continue
		}
		if runtime.GOOS == "windows" {
			return entry.windowsLibuvErrno, true
		}
		return -int(entry.errno), true
	}
	return 0, false
}

// inspectString quotes as util.inspect does: single quotes unless the text contains one and no double quote.
func inspectString(value string) string {
	quote := "'"
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		quote = `"`
	}
	escaped := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value)
	if quote == "'" {
		escaped = strings.ReplaceAll(escaped, "'", `\'`)
	}
	return quote + escaped + quote
}
