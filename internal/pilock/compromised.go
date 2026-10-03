package pilock

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
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
			properties := []string{fmt.Sprintf("errno: %d", fsErr.errno), "code: 'ECOMPROMISED'", "syscall: " + inspectString(fsErr.syscall), "path: " + inspectString(fsErr.path)}
			return "[Error: " + fsErr.message() + "] {\n  " + strings.Join(properties, ",\n  ") + "\n}"
		}
	}
	return "Error: " + e.Error() + " {\n  code: 'ECOMPROMISED'\n}"
}

// nodeFS reports err, a failure of the fs call named call on path in proper-lockfile's protocol, with the message Node gives it, which Pi surfaces unchanged. A nil err stays nil.
func nodeFS(err error, call, path string) error {
	if err == nil {
		return nil
	}
	return &nodeerrno.FSError{Syscall: call, Path: path, Err: err}
}

type nodeFSFailure struct {
	code, description, syscall, path string
	errno                            int
}

func (f nodeFSFailure) message() string {
	return f.code + ": " + f.description + ", " + f.syscall + " '" + f.path + "'"
}

// nodeFSError maps a failed fs call to the Node fs error for it, as internal/nodeerrno describes it: libuv's code for the errno (a Windows system error translated as uv_translate_sys_error does, so ERROR_ACCESS_DENIED is EPERM and an error libuv does not translate is UNKNOWN; elsewhere uv_err_name's name for the negated errno, "Unknown system error <errno>" when libuv does not name it), uv_strerror's description and error.errno. Node never shows Go's text.
func nodeFSError(err error) (nodeFSFailure, bool) {
	var call, path string
	var cause error
	if failure, ok := errors.AsType[*nodeerrno.FSError](err); ok {
		call, path, cause = failure.Syscall, failure.Path, failure.Err
	} else if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		call, path, cause = nodeSyscall(pathErr.Op), pathErr.Path, pathErr.Err
	} else {
		return nodeFSFailure{}, false
	}
	code, description, errno := nodeerrno.Describe(cause)
	return nodeFSFailure{code: code, description: description, syscall: call, path: path, errno: errno}, true
}

// nodeSyscall names the Node fs call behind a Go path error's operation. os.Chtimes is Node's utime. On Windows os.Stat names the Win32 call that failed (os/stat_windows.go, os/types_windows.go), and Node reports each as stat.
func nodeSyscall(op string) string {
	switch op {
	case "chtimes":
		return "utime"
	case "GetFileAttributesEx", "FindFirstFile", "CreateFile", "GetFileType", "GetFileInformationByHandle", "GetFileInformationByHandleEx":
		return "stat"
	}
	return op
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
