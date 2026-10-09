package nodeerrno

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// FSError is the error Node's fs module reports for a failed call. Its message
// is Node's: "<CODE>: <description>, <syscall> '<path>'", without the path for
// a call on a file descriptor, and "<CODE>: <description>, copyfile '<path>' -> '<dest>'" for a call with a Dest. Code, when set, is the code Node reports in
// place of the one Describe finds for Err. Unwrap returns the Go error.
type FSError struct {
	Syscall, Path, Dest, Code string
	Err                       error
}

func (e *FSError) Error() string {
	code, description := e.Code, ""
	if code == "" {
		code, description, _ = Describe(e.Err)
	} else {
		description, _ = Description(code)
	}
	message := code + ": " + description + ", " + e.Syscall
	if e.Path != "" {
		message += " '" + e.Path + "'"
	}
	if e.Dest != "" {
		message += " -> '" + e.Dest + "'"
	}
	return message
}

func (e *FSError) Unwrap() error { return e.Err }

// FromPathError reports err, a failed Go file operation, as the Node fs call
// behind it. open keeps its path; read, write and close act on a file
// descriptor, and Node names no path for them. Any other error is returned
// unchanged.
func FromPathError(err error) error {
	pathErr, ok := errors.AsType[*fs.PathError](err)
	if !ok {
		return err
	}
	switch pathErr.Op {
	case "read", "write", "close":
		return &FSError{Syscall: pathErr.Op, Err: pathErr.Err}
	}
	return &FSError{Syscall: pathErr.Op, Path: pathErr.Path, Err: pathErr.Err}
}

// MkdirAll creates dir and any missing parents as Node's
// fs.mkdirSync(dir, {recursive: true, mode}) does, and reports a failure as
// Node does: with the requested path, EEXIST when dir exists but is not a
// directory, and ENOTDIR when a parent is not a directory.
func MkdirAll(dir string, perm os.FileMode) error {
	err := os.MkdirAll(dir, perm)
	if err == nil {
		return nil
	}
	pathErr, ok := errors.AsType[*fs.PathError](err)
	if !ok {
		return err
	}
	// os.MkdirAll reports a path that exists but is not a directory as syscall.ENOTDIR, which is ERROR_PATH_NOT_FOUND on Windows.
	if errors.Is(pathErr.Err, syscall.ENOTDIR) {
		if info, statErr := os.Stat(pathErr.Path); statErr == nil && !info.IsDir() {
			code := "ENOTDIR"
			if pathErr.Path == dir {
				code = "EEXIST"
			}
			return &FSError{Syscall: "mkdir", Path: dir, Code: code, Err: err}
		}
	}
	return &FSError{Syscall: "mkdir", Path: dir, Err: pathErr.Err}
}
