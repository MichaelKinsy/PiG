package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/MichaelKinsy/PiG/agent/harness"
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

var fileErrorCodes = map[string]harness.FileErrorCode{
	"ABORT_ERR": harness.FileErrorAborted,
	"ENOENT":    harness.FileErrorNotFound,
	"EACCES":    harness.FileErrorPermissionDenied,
	"EPERM":     harness.FileErrorPermissionDenied,
	"ENOTDIR":   harness.FileErrorNotDirectory,
	"EISDIR":    harness.FileErrorIsDirectory,
	"EINVAL":    harness.FileErrorInvalid,
}

// fsCall names the Node fs syscall reported in an error message; dest is set
// for two-path calls such as rename.
type fsCall struct {
	syscall string
	path    string
	dest    string
}

// toFileError maps an OS error to a FileError with Node's errno mapping and
// message shape ("CODE: description, syscall 'path'").
func toFileError(err error, call fsCall) *harness.FileError {
	if fileErr, ok := errors.AsType[*harness.FileError](err); ok {
		return fileErr
	}
	code := errnoCode(err)
	mapped, ok := fileErrorCodes[code]
	if !ok {
		mapped = harness.FileErrorUnknown
	}
	return &harness.FileError{Code: mapped, Message: nodeErrorMessage(err, code, call), Path: call.path, Cause: err}
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
	if call.dest != "" {
		return fmt.Sprintf("%s: %s, %s '%s' -> '%s'", code, description, call.syscall, call.path, call.dest)
	}
	return fmt.Sprintf("%s: %s, %s '%s'", code, description, call.syscall, call.path)
}

func abortedFileError(ctx context.Context, path string) error {
	if ctx.Err() == nil {
		return nil
	}
	return &harness.FileError{Code: harness.FileErrorAborted, Message: "aborted", Path: path}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
