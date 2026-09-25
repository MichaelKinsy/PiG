//go:build windows

package env

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// windowsErrnoNames maps Windows system error codes to the Node error codes
// libuv's uv_translate_sys_error reports for them, so an error on Windows
// carries the code Node reports there: a missing parent directory is ENOENT,
// not the ENOTDIR that Go's syscall.ENOTDIR (ERROR_PATH_NOT_FOUND) implies.
var windowsErrnoNames = map[syscall.Errno]string{
	windows.ERROR_INVALID_FUNCTION:      "EISDIR",
	windows.ERROR_FILE_NOT_FOUND:        "ENOENT",
	windows.ERROR_PATH_NOT_FOUND:        "ENOENT",
	windows.ERROR_INVALID_DRIVE:         "ENOENT",
	windows.ERROR_INVALID_NAME:          "ENOENT",
	windows.ERROR_BAD_PATHNAME:          "ENOENT",
	windows.ERROR_DIRECTORY:             "ENOENT",
	windows.ERROR_TOO_MANY_OPEN_FILES:   "EMFILE",
	windows.ERROR_ACCESS_DENIED:         "EPERM",
	windows.ERROR_PRIVILEGE_NOT_HELD:    "EPERM",
	windows.ERROR_NOACCESS:              "EACCES",
	windows.ERROR_CANT_ACCESS_FILE:      "EACCES",
	windows.ERROR_INVALID_DATA:          "EINVAL",
	windows.ERROR_INVALID_PARAMETER:     "EINVAL",
	windows.ERROR_NOT_SAME_DEVICE:       "EXDEV",
	windows.ERROR_WRITE_PROTECT:         "EROFS",
	windows.ERROR_SHARING_VIOLATION:     "EBUSY",
	windows.ERROR_LOCK_VIOLATION:        "EBUSY",
	windows.ERROR_HANDLE_DISK_FULL:      "ENOSPC",
	windows.ERROR_DISK_FULL:             "ENOSPC",
	windows.ERROR_FILE_EXISTS:           "EEXIST",
	windows.ERROR_ALREADY_EXISTS:        "EEXIST",
	windows.ERROR_DIR_NOT_EMPTY:         "ENOTEMPTY",
	windows.ERROR_FILENAME_EXCED_RANGE:  "ENAMETOOLONG",
	windows.ERROR_CANT_RESOLVE_FILENAME: "ELOOP",
}

// systemErrnoName returns the Node error code for errno: a Windows system
// error, or one of the POSIX errnos Go invents on Windows (syscall.Open
// reports EISDIR for a directory opened for writing).
func systemErrnoName(errno syscall.Errno) (string, bool) {
	if name, ok := windowsErrnoNames[errno]; ok {
		return name, true
	}
	name, ok := errnoNames[errno]
	return name, ok
}
