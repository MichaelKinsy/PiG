package nodespawn

import (
	"errors"
	"os/exec"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Error is the error Node's child_process.spawn(file, args) reports for a
// program it does not start.
type Error struct {
	// Code is Node's error.code, such as "ENOENT", "EINVAL", or
	// "ERR_INVALID_ARG_VALUE".
	Code string
	// Syscall is Node's error.syscall: "spawn " + file for an emitted error
	// and "spawn" for a thrown one. An argument error has none.
	Syscall string
	// Thrown reports that spawn throws the error. Otherwise spawn returns the
	// child, which emits the error as its 'error' event.
	Thrown bool
	// message is Node's error.message when it is not Syscall and Code.
	message string
	errno   syscall.Errno
}

// Error is Node's error.message: the syscall and the code, or the message of
// an argument error.
func (e *Error) Error() string {
	if e.message != "" {
		return e.message
	}
	return e.Syscall + " " + e.Code
}

// Unwrap returns the errno that Code names, so an ENOENT error matches
// fs.ErrNotExist. An argument error names no errno.
func (e *Error) Unwrap() error {
	if e.errno == 0 {
		return nil
	}
	return e.errno
}

// SpawnMessage is the message of the error Node's spawn reports for file when err stopped it: nodespawn errors already carry
// it, and any other start error gets "spawn <file> <code>" for the errnos libuv reports with the file (a missing program,
// which os/exec reports as exec.ErrNotFound, is ENOENT) and the bare "spawn <code>" for the rest, as ChildProcess.spawn throws
// those (internal/child_process.js). An error without an errno keeps its text.
func SpawnMessage(file string, err error) string {
	if spawnErr, ok := errors.AsType[*Error](err); ok {
		return spawnErr.Error()
	}
	code := nodeerrno.ErrorCode(err)
	if errors.Is(err, exec.ErrNotFound) {
		code = "ENOENT"
	}
	if code == "" {
		return err.Error()
	}
	switch code {
	case "EACCES", "EAGAIN", "EMFILE", "ENFILE", "ENOENT":
		return "spawn " + file + " " + code
	}
	return "spawn " + code
}
