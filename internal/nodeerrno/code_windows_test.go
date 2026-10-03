//go:build windows

package nodeerrno

import (
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsErrnoConstants checks the numeric winerror.h and winsock2.h
// values of the translation table against the Windows declarations.
func TestWindowsErrnoConstants(t *testing.T) {
	t.Parallel()
	declared := map[string]syscall.Errno{
		"WSAEACCES":                        windows.WSAEACCES,
		"ERROR_ELEVATION_REQUIRED":         windows.ERROR_ELEVATION_REQUIRED,
		"ERROR_CANT_ACCESS_FILE":           windows.ERROR_CANT_ACCESS_FILE,
		"ERROR_ADDRESS_ALREADY_ASSOCIATED": windows.ERROR_ADDRESS_ALREADY_ASSOCIATED,
		"WSAEADDRINUSE":                    windows.WSAEADDRINUSE,
		"WSAEADDRNOTAVAIL":                 windows.WSAEADDRNOTAVAIL,
		"WSAEAFNOSUPPORT":                  windows.WSAEAFNOSUPPORT,
		"WSAEWOULDBLOCK":                   windows.WSAEWOULDBLOCK,
		"ERROR_NO_DATA":                    windows.ERROR_NO_DATA,
		"WSAEALREADY":                      windows.WSAEALREADY,
		"ERROR_INVALID_FLAGS":              windows.ERROR_INVALID_FLAGS,
		"ERROR_INVALID_HANDLE":             windows.ERROR_INVALID_HANDLE,
		"ERROR_LOCK_VIOLATION":             windows.ERROR_LOCK_VIOLATION,
		"ERROR_PIPE_BUSY":                  windows.ERROR_PIPE_BUSY,
		"ERROR_SHARING_VIOLATION":          windows.ERROR_SHARING_VIOLATION,
		"ERROR_OPERATION_ABORTED":          windows.ERROR_OPERATION_ABORTED,
		"WSAEINTR":                         windows.WSAEINTR,
		"ERROR_NO_UNICODE_TRANSLATION":     windows.ERROR_NO_UNICODE_TRANSLATION,
		"ERROR_CONNECTION_ABORTED":         windows.ERROR_CONNECTION_ABORTED,
		"WSAECONNABORTED":                  windows.WSAECONNABORTED,
		"ERROR_CONNECTION_REFUSED":         windows.ERROR_CONNECTION_REFUSED,
		"WSAECONNREFUSED":                  windows.WSAECONNREFUSED,
		"ERROR_NETNAME_DELETED":            windows.ERROR_NETNAME_DELETED,
		"WSAECONNRESET":                    windows.WSAECONNRESET,
		"ERROR_ALREADY_EXISTS":             windows.ERROR_ALREADY_EXISTS,
		"ERROR_FILE_EXISTS":                windows.ERROR_FILE_EXISTS,
		"ERROR_NOACCESS":                   windows.ERROR_NOACCESS,
		"WSAEFAULT":                        windows.WSAEFAULT,
		"ERROR_HOST_UNREACHABLE":           windows.ERROR_HOST_UNREACHABLE,
		"WSAEHOSTUNREACH":                  windows.WSAEHOSTUNREACH,
		"ERROR_INSUFFICIENT_BUFFER":        windows.ERROR_INSUFFICIENT_BUFFER,
		"ERROR_INVALID_DATA":               windows.ERROR_INVALID_DATA,
		"ERROR_INVALID_PARAMETER":          windows.ERROR_INVALID_PARAMETER,
		"ERROR_SYMLINK_NOT_SUPPORTED":      windows.ERROR_SYMLINK_NOT_SUPPORTED,
		"WSAEINVAL":                        windows.WSAEINVAL,
		"WSAEPFNOSUPPORT":                  windows.WSAEPFNOSUPPORT,
		"ERROR_BEGINNING_OF_MEDIA":         windows.ERROR_BEGINNING_OF_MEDIA,
		"ERROR_BUS_RESET":                  windows.ERROR_BUS_RESET,
		"ERROR_CRC":                        windows.ERROR_CRC,
		"ERROR_DEVICE_DOOR_OPEN":           windows.ERROR_DEVICE_DOOR_OPEN,
		"ERROR_DEVICE_REQUIRES_CLEANING":   windows.ERROR_DEVICE_REQUIRES_CLEANING,
		"ERROR_DISK_CORRUPT":               windows.ERROR_DISK_CORRUPT,
		"ERROR_EOM_OVERFLOW":               windows.ERROR_EOM_OVERFLOW,
		"ERROR_FILEMARK_DETECTED":          windows.ERROR_FILEMARK_DETECTED,
		"ERROR_GEN_FAILURE":                windows.ERROR_GEN_FAILURE,
		"ERROR_INVALID_BLOCK_LENGTH":       windows.ERROR_INVALID_BLOCK_LENGTH,
		"ERROR_IO_DEVICE":                  windows.ERROR_IO_DEVICE,
		"ERROR_NO_DATA_DETECTED":           windows.ERROR_NO_DATA_DETECTED,
		"ERROR_NO_SIGNAL_SENT":             windows.ERROR_NO_SIGNAL_SENT,
		"ERROR_OPEN_FAILED":                windows.ERROR_OPEN_FAILED,
		"ERROR_SETMARK_DETECTED":           windows.ERROR_SETMARK_DETECTED,
		"ERROR_SIGNAL_REFUSED":             windows.ERROR_SIGNAL_REFUSED,
		"WSAEISCONN":                       windows.WSAEISCONN,
		"ERROR_CANT_RESOLVE_FILENAME":      windows.ERROR_CANT_RESOLVE_FILENAME,
		"ERROR_TOO_MANY_OPEN_FILES":        windows.ERROR_TOO_MANY_OPEN_FILES,
		"WSAEMFILE":                        windows.WSAEMFILE,
		"WSAEMSGSIZE":                      windows.WSAEMSGSIZE,
		"ERROR_BUFFER_OVERFLOW":            windows.ERROR_BUFFER_OVERFLOW,
		"ERROR_FILENAME_EXCED_RANGE":       windows.ERROR_FILENAME_EXCED_RANGE,
		"ERROR_NETWORK_UNREACHABLE":        windows.ERROR_NETWORK_UNREACHABLE,
		"WSAENETUNREACH":                   windows.WSAENETUNREACH,
		"WSAENOBUFS":                       windows.WSAENOBUFS,
		"ERROR_BAD_PATHNAME":               windows.ERROR_BAD_PATHNAME,
		"ERROR_DIRECTORY":                  windows.ERROR_DIRECTORY,
		"ERROR_ENVVAR_NOT_FOUND":           windows.ERROR_ENVVAR_NOT_FOUND,
		"ERROR_FILE_NOT_FOUND":             windows.ERROR_FILE_NOT_FOUND,
		"ERROR_INVALID_NAME":               windows.ERROR_INVALID_NAME,
		"ERROR_INVALID_DRIVE":              windows.ERROR_INVALID_DRIVE,
		"ERROR_INVALID_REPARSE_DATA":       windows.ERROR_INVALID_REPARSE_DATA,
		"ERROR_MOD_NOT_FOUND":              windows.ERROR_MOD_NOT_FOUND,
		"ERROR_PATH_NOT_FOUND":             windows.ERROR_PATH_NOT_FOUND,
		"WSAHOST_NOT_FOUND":                windows.WSAHOST_NOT_FOUND,
		"WSANO_DATA":                       windows.WSANO_DATA,
		"ERROR_NOT_ENOUGH_MEMORY":          windows.ERROR_NOT_ENOUGH_MEMORY,
		"ERROR_OUTOFMEMORY":                windows.ERROR_OUTOFMEMORY,
		"ERROR_CANNOT_MAKE":                windows.ERROR_CANNOT_MAKE,
		"ERROR_DISK_FULL":                  windows.ERROR_DISK_FULL,
		"ERROR_EA_TABLE_FULL":              windows.ERROR_EA_TABLE_FULL,
		"ERROR_END_OF_MEDIA":               windows.ERROR_END_OF_MEDIA,
		"ERROR_HANDLE_DISK_FULL":           windows.ERROR_HANDLE_DISK_FULL,
		"ERROR_NOT_CONNECTED":              windows.ERROR_NOT_CONNECTED,
		"WSAENOTCONN":                      windows.WSAENOTCONN,
		"ERROR_DIR_NOT_EMPTY":              windows.ERROR_DIR_NOT_EMPTY,
		"WSAENOTSOCK":                      windows.WSAENOTSOCK,
		"ERROR_NOT_SUPPORTED":              windows.ERROR_NOT_SUPPORTED,
		"ERROR_BROKEN_PIPE":                windows.ERROR_BROKEN_PIPE,
		"ERROR_ACCESS_DENIED":              windows.ERROR_ACCESS_DENIED,
		"ERROR_PRIVILEGE_NOT_HELD":         windows.ERROR_PRIVILEGE_NOT_HELD,
		"ERROR_BAD_PIPE":                   windows.ERROR_BAD_PIPE,
		"ERROR_PIPE_NOT_CONNECTED":         windows.ERROR_PIPE_NOT_CONNECTED,
		"WSAESHUTDOWN":                     windows.WSAESHUTDOWN,
		"WSAEPROTONOSUPPORT":               windows.WSAEPROTONOSUPPORT,
		"ERROR_WRITE_PROTECT":              windows.ERROR_WRITE_PROTECT,
		"ERROR_SEM_TIMEOUT":                windows.ERROR_SEM_TIMEOUT,
		"WSAETIMEDOUT":                     windows.WSAETIMEDOUT,
		"ERROR_NOT_SAME_DEVICE":            windows.ERROR_NOT_SAME_DEVICE,
		"ERROR_INVALID_FUNCTION":           windows.ERROR_INVALID_FUNCTION,
		"ERROR_META_EXPANSION_TOO_LONG":    windows.ERROR_META_EXPANSION_TOO_LONG,
		"WSAESOCKTNOSUPPORT":               windows.WSAESOCKTNOSUPPORT,
		"ERROR_BAD_EXE_FORMAT":             windows.ERROR_BAD_EXE_FORMAT,
	}
	for _, entry := range windowsTranslations {
		if want, ok := declared[entry.name]; !ok || entry.errno != want {
			t.Errorf("%s = %d, want %d", entry.name, int(entry.errno), int(want))
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
