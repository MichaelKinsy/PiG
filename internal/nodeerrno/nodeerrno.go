// Package nodeerrno maps operating-system errors to the error codes and
// descriptions Node reports for them. Node takes both from libuv:
// uv_translate_sys_error converts a Windows system error to a libuv errno,
// uv_err_name names the errno (error.code), and uv_strerror describes it.
package nodeerrno

import (
	"errors"
	"syscall"
)

// descriptions are libuv's uv_strerror texts for the codes in this package.
var descriptions = map[string]string{
	"ENOENT":       "no such file or directory",
	"EACCES":       "permission denied",
	"EPERM":        "operation not permitted",
	"ENOTDIR":      "not a directory",
	"EISDIR":       "illegal operation on a directory",
	"EEXIST":       "file already exists",
	"EFAULT":       "bad address in system call argument",
	"ENOTEMPTY":    "directory not empty",
	"EINVAL":       "invalid argument",
	"ELOOP":        "too many symbolic links encountered",
	"ENAMETOOLONG": "name too long",
	"EBUSY":        "resource busy or locked",
	"EMFILE":       "too many open files",
	"ENOSPC":       "no space left on device",
	"EROFS":        "read-only file system",
	"EXDEV":        "cross-device link not permitted",
}

// posixCodes names the errnos of the running platform's syscall package. On
// Windows these are the POSIX errnos Go invents (syscall.Open reports EISDIR
// for a directory opened for writing), except syscall.ENOENT and
// syscall.ENOTDIR, which alias ERROR_FILE_NOT_FOUND and ERROR_PATH_NOT_FOUND
// and are named by windowsCodes first.
var posixCodes = map[syscall.Errno]string{
	syscall.ENOENT:       "ENOENT",
	syscall.EACCES:       "EACCES",
	syscall.EPERM:        "EPERM",
	syscall.ENOTDIR:      "ENOTDIR",
	syscall.EISDIR:       "EISDIR",
	syscall.EEXIST:       "EEXIST",
	syscall.EFAULT:       "EFAULT",
	syscall.ENOTEMPTY:    "ENOTEMPTY",
	syscall.EINVAL:       "EINVAL",
	syscall.ELOOP:        "ELOOP",
	syscall.ENAMETOOLONG: "ENAMETOOLONG",
	syscall.EBUSY:        "EBUSY",
	syscall.EMFILE:       "EMFILE",
	syscall.ENOSPC:       "ENOSPC",
	syscall.EROFS:        "EROFS",
	syscall.EXDEV:        "EXDEV",
}

// Windows system error codes (winerror.h). They are declared numerically so
// the translation table builds and is tested on every platform.
const (
	errorInvalidFunction     syscall.Errno = 1
	errorFileNotFound        syscall.Errno = 2
	errorPathNotFound        syscall.Errno = 3
	errorTooManyOpenFiles    syscall.Errno = 4
	errorAccessDenied        syscall.Errno = 5
	errorInvalidData         syscall.Errno = 13
	errorInvalidDrive        syscall.Errno = 15
	errorNotSameDevice       syscall.Errno = 17
	errorWriteProtect        syscall.Errno = 19
	errorSharingViolation    syscall.Errno = 32
	errorLockViolation       syscall.Errno = 33
	errorHandleDiskFull      syscall.Errno = 39
	errorFileExists          syscall.Errno = 80
	errorCannotMake          syscall.Errno = 82
	errorInvalidParameter    syscall.Errno = 87
	errorBufferOverflow      syscall.Errno = 111
	errorDiskFull            syscall.Errno = 112
	errorInsufficientBuffer  syscall.Errno = 122
	errorInvalidName         syscall.Errno = 123
	errorModNotFound         syscall.Errno = 126
	errorDirNotEmpty         syscall.Errno = 145
	errorBadPathname         syscall.Errno = 161
	errorAlreadyExists       syscall.Errno = 183
	errorEnvvarNotFound      syscall.Errno = 203
	errorFilenameExcedRange  syscall.Errno = 206
	errorPipeBusy            syscall.Errno = 231
	errorDirectory           syscall.Errno = 267
	errorEaTableFull         syscall.Errno = 277
	errorElevationRequired   syscall.Errno = 740
	errorNoaccess            syscall.Errno = 998
	errorEndOfMedia          syscall.Errno = 1100
	errorPrivilegeNotHeld    syscall.Errno = 1314
	errorSymlinkNotSupported syscall.Errno = 1464
	errorCantAccessFile      syscall.Errno = 1920
	errorCantResolveFilename syscall.Errno = 1921
	errorInvalidReparseData  syscall.Errno = 4392
)

// windowsCodes is libuv's uv_translate_sys_error (src/win/error.c, libuv
// 1.52.1 as bundled by Node 24.19.0) for every non-Winsock system error whose
// libuv code has a description here. A missing file or parent directory is
// ENOENT (never ENOTDIR), ERROR_ACCESS_DENIED is EPERM, not EACCES, and
// ERROR_NOACCESS is EFAULT (libuv 1.50 and later).
var windowsCodes = map[syscall.Errno]string{
	errorElevationRequired:   "EACCES",
	errorCantAccessFile:      "EACCES",
	errorLockViolation:       "EBUSY",
	errorPipeBusy:            "EBUSY",
	errorSharingViolation:    "EBUSY",
	errorAlreadyExists:       "EEXIST",
	errorFileExists:          "EEXIST",
	errorNoaccess:            "EFAULT",
	errorInsufficientBuffer:  "EINVAL",
	errorInvalidData:         "EINVAL",
	errorInvalidParameter:    "EINVAL",
	errorSymlinkNotSupported: "EINVAL",
	errorCantResolveFilename: "ELOOP",
	errorTooManyOpenFiles:    "EMFILE",
	errorBufferOverflow:      "ENAMETOOLONG",
	errorFilenameExcedRange:  "ENAMETOOLONG",
	errorBadPathname:         "ENOENT",
	errorDirectory:           "ENOENT",
	errorEnvvarNotFound:      "ENOENT",
	errorFileNotFound:        "ENOENT",
	errorInvalidName:         "ENOENT",
	errorInvalidDrive:        "ENOENT",
	errorInvalidReparseData:  "ENOENT",
	errorModNotFound:         "ENOENT",
	errorPathNotFound:        "ENOENT",
	errorCannotMake:          "ENOSPC",
	errorDiskFull:            "ENOSPC",
	errorEaTableFull:         "ENOSPC",
	errorEndOfMedia:          "ENOSPC",
	errorHandleDiskFull:      "ENOSPC",
	errorDirNotEmpty:         "ENOTEMPTY",
	errorAccessDenied:        "EPERM",
	errorPrivilegeNotHeld:    "EPERM",
	errorWriteProtect:        "EROFS",
	errorNotSameDevice:       "EXDEV",
	errorInvalidFunction:     "EISDIR",
}

// windowsCode returns the Node error code libuv reports on Windows for the
// system error errno.
func windowsCode(errno syscall.Errno) (string, bool) {
	code, ok := windowsCodes[errno]
	return code, ok
}

// posixCode returns the Node error code for one of the running platform's
// syscall errnos.
func posixCode(errno syscall.Errno) (string, bool) {
	code, ok := posixCodes[errno]
	return code, ok
}

// ErrorCode returns the Node error code (error.code) for the syscall.Errno
// that err wraps, or "" when err wraps none or its errno has no code here.
func ErrorCode(err error) string {
	return errorCode(err, Code)
}

func errorCode(err error, code func(syscall.Errno) (string, bool)) string {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return ""
	}
	name, _ := code(errno)
	return name
}

// Description returns libuv's uv_strerror text for a Node error code.
func Description(code string) (string, bool) {
	description, ok := descriptions[code]
	return description, ok
}
