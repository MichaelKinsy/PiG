//go:build windows

package nodeerrno

import (
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsErrnoConstants checks the numeric winerror.h values against the
// Windows declarations.
func TestWindowsErrnoConstants(t *testing.T) {
	t.Parallel()
	cases := map[syscall.Errno]syscall.Errno{
		errorInvalidFunction:     windows.ERROR_INVALID_FUNCTION,
		errorFileNotFound:        windows.ERROR_FILE_NOT_FOUND,
		errorPathNotFound:        windows.ERROR_PATH_NOT_FOUND,
		errorTooManyOpenFiles:    windows.ERROR_TOO_MANY_OPEN_FILES,
		errorAccessDenied:        windows.ERROR_ACCESS_DENIED,
		errorInvalidData:         windows.ERROR_INVALID_DATA,
		errorInvalidDrive:        windows.ERROR_INVALID_DRIVE,
		errorNotSameDevice:       windows.ERROR_NOT_SAME_DEVICE,
		errorWriteProtect:        windows.ERROR_WRITE_PROTECT,
		errorSharingViolation:    windows.ERROR_SHARING_VIOLATION,
		errorLockViolation:       windows.ERROR_LOCK_VIOLATION,
		errorHandleDiskFull:      windows.ERROR_HANDLE_DISK_FULL,
		errorFileExists:          windows.ERROR_FILE_EXISTS,
		errorCannotMake:          windows.ERROR_CANNOT_MAKE,
		errorInvalidParameter:    windows.ERROR_INVALID_PARAMETER,
		errorBufferOverflow:      windows.ERROR_BUFFER_OVERFLOW,
		errorDiskFull:            windows.ERROR_DISK_FULL,
		errorInsufficientBuffer:  windows.ERROR_INSUFFICIENT_BUFFER,
		errorInvalidName:         windows.ERROR_INVALID_NAME,
		errorModNotFound:         windows.ERROR_MOD_NOT_FOUND,
		errorDirNotEmpty:         windows.ERROR_DIR_NOT_EMPTY,
		errorBadPathname:         windows.ERROR_BAD_PATHNAME,
		errorAlreadyExists:       windows.ERROR_ALREADY_EXISTS,
		errorEnvvarNotFound:      windows.ERROR_ENVVAR_NOT_FOUND,
		errorFilenameExcedRange:  windows.ERROR_FILENAME_EXCED_RANGE,
		errorPipeBusy:            windows.ERROR_PIPE_BUSY,
		errorDirectory:           windows.ERROR_DIRECTORY,
		errorEaTableFull:         windows.ERROR_EA_TABLE_FULL,
		errorElevationRequired:   windows.ERROR_ELEVATION_REQUIRED,
		errorNoaccess:            windows.ERROR_NOACCESS,
		errorEndOfMedia:          windows.ERROR_END_OF_MEDIA,
		errorPrivilegeNotHeld:    windows.ERROR_PRIVILEGE_NOT_HELD,
		errorSymlinkNotSupported: windows.ERROR_SYMLINK_NOT_SUPPORTED,
		errorCantAccessFile:      windows.ERROR_CANT_ACCESS_FILE,
		errorCantResolveFilename: windows.ERROR_CANT_RESOLVE_FILENAME,
		errorInvalidReparseData:  windows.ERROR_INVALID_REPARSE_DATA,
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("constant %d, want %d", int(got), int(want))
		}
	}
}

// TestCodeOnWindows checks that Code prefers libuv's Windows translation and
// still names the POSIX errnos Go invents on Windows.
func TestCodeOnWindows(t *testing.T) {
	t.Parallel()
	cases := map[syscall.Errno]string{
		windows.ERROR_ACCESS_DENIED:  "EPERM",
		windows.ERROR_PATH_NOT_FOUND: "ENOENT",
		windows.ERROR_FILE_NOT_FOUND: "ENOENT",
		windows.ERROR_NOACCESS:       "EFAULT",
		syscall.EACCES:               "EACCES",
		syscall.EPERM:                "EPERM",
		syscall.EISDIR:               "EISDIR",
	}
	for errno, want := range cases {
		if got, ok := Code(errno); !ok || got != want {
			t.Errorf("Code(%d) = %q, %v; want %q", int(errno), got, ok, want)
		}
	}
}
