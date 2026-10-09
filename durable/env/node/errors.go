package node

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// nodeErrorCode is an error this package raises itself with a fixed Node
// error code, where the syscall.Errno of the same name would be ambiguous (on
// Windows, syscall.ENOTDIR is ERROR_PATH_NOT_FOUND).
type nodeErrorCode string

func (code nodeErrorCode) Error() string {
	description, _ := nodeerrno.Description(string(code))
	return description
}

// errnoCode returns the Node-style error code for an OS error, or "".
func errnoCode(err error) string {
	if code, ok := errors.AsType[nodeErrorCode](err); ok {
		return string(code)
	}
	if fsErr, ok := errors.AsType[*nodeerrno.FSError](err); ok && fsErr.Code != "" {
		return fsErr.Code
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		if name, ok := nodeerrno.Code(errno); ok {
			return name
		}
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "ABORT_ERR"
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	case errors.Is(err, fs.ErrExist):
		return "EEXIST"
	}
	return ""
}

var fileErrorCodes = map[string]durableenv.FileErrorCode{
	"ABORT_ERR": durableenv.FileErrorAborted,
	"ENOENT":    durableenv.FileErrorNotFound,
	"EACCES":    durableenv.FileErrorPermissionDenied,
	"EPERM":     durableenv.FileErrorPermissionDenied,
	"ENOTDIR":   durableenv.FileErrorNotDirectory,
	"EISDIR":    durableenv.FileErrorIsDirectory,
	"EINVAL":    durableenv.FileErrorInvalid,
}

// fsCall names the Node fs syscall reported in an error message; dest is set
// for two-path calls such as rename.
type fsCall struct {
	syscall string
	path    string
	dest    string
	// fd marks a call on an open file descriptor, whose Node message names no
	// path.
	fd bool
}

// toFileError maps an OS error to a FileError with Node's errno mapping and
// message shape ("CODE: description, syscall 'path'").
func toFileError(err error, call fsCall) *durableenv.FileError {
	if fileErr, ok := errors.AsType[*durableenv.FileError](err); ok {
		return fileErr
	}
	code := errnoCode(err)
	mapped, ok := fileErrorCodes[code]
	if !ok {
		mapped = durableenv.FileErrorUnknown
	}
	return durableenv.NewFileError(mapped, nodeErrorMessage(err, code, call), call.path, err)
}

func nodeErrorMessage(err error, code string, call fsCall) string {
	description, ok := nodeerrno.Description(code)
	if !ok {
		return err.Error()
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && (pathErr.Op == "read" || pathErr.Op == "write") {
		return fmt.Sprintf("%s: %s, %s", code, description, pathErr.Op)
	}
	if call.fd {
		return fmt.Sprintf("%s: %s, %s", code, description, call.syscall)
	}
	if call.dest != "" {
		return code + ": " + description + ", " + call.syscall + " " + sq(call.path) + " -> " + sq(call.dest)
	}
	return code + ": " + description + ", " + call.syscall + " " + sq(call.path)
}

func abortedFileError(ctx context.Context, path string) error {
	if ctx.Err() == nil {
		return nil
	}
	return durableenv.NewFileError(durableenv.FileErrorAborted, "aborted", path, nil)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sq wraps s in single quotes for Node-style error text (display only). An embedded single quote is escaped so the
// value cannot end the quoted span; paths without one print exactly as Node prints them.
func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }
