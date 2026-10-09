package daemon

// Ports packages/env/daemon/src/errors.rs

import (
	"errors"
	"io/fs"
	"maps"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Failure is an error with a Node-style code such as ENOENT; messages are diagnostic only.
type Failure struct {
	Code    string
	Message string
	Syscall string
	Path    string
	// Extra holds more fields for the client, such as spillPath.
	Extra Object
}

func (f *Failure) Error() string { return f.Message }

func newFailure(code, message string) *Failure { return &Failure{Code: code, Message: message} }

func (f *Failure) withPath(path string) *Failure {
	f.Path = path
	return f
}

// synthetic is a code libuv reports where the system call itself reported another one, such as ENOTDIR from a
// recursive mkdir through a file.
type synthetic string

func (s synthetic) Error() string { return string(s) }

// errorCode is libuv's name for err: a synthetic code, the code of a Node fs error, or the system's code.
func errorCode(err error) string {
	if code, ok := errors.AsType[synthetic](err); ok {
		return string(code)
	}
	if fsErr, ok := errors.AsType[*nodeerrno.FSError](err); ok && fsErr.Code != "" {
		return fsErr.Code
	}
	if code := nodeerrno.ErrorCode(err); code != "" {
		return code
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	case errors.Is(err, fs.ErrExist):
		return "EEXIST"
	}
	return "UNKNOWN"
}

// ioFailure is an I/O error of syscall on path, with libuv's error name as code.
func ioFailure(err error, syscallName, path string) *Failure {
	return ioFailureBetween(err, syscallName, path, "")
}

// ioFailureBetween is Node's message for an error of a two-path call: `CODE: description, syscall 'from' -> 'to'`.
func ioFailureBetween(err error, syscallName, path, destination string) *Failure {
	code := errorCode(err)
	description, known := nodeerrno.Description(code)
	if !known {
		// Unknown to libuv's table: the OS text.
		description = strings.ToLower(osText(err))
	}
	target := "'" + path + "'"
	if destination != "" {
		target = "'" + path + "' -> '" + destination + "'"
	}
	failure := newFailure(code, code+": "+description+", "+syscallName+" "+target)
	failure.Syscall = syscallName
	return failure.withPath(path)
}

// osText is the operating system's text for err, without Go's operation and path prefix.
func osText(err error) string {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno.Error()
	}
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// toJSON is the error frame's JSON.
func (f *Failure) toJSON() Object {
	value := Object{"code": f.Code, "message": f.Message}
	if f.Syscall != "" {
		value["syscall"] = f.Syscall
	}
	if f.Path != "" {
		value["path"] = f.Path
	}
	maps.Copy(value, f.Extra)
	return value
}
