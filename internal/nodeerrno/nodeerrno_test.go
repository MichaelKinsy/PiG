package nodeerrno

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestWindowsCodeMatchesLibuv pins libuv's uv_translate_sys_error
// (src/win/error.c in libuv 1.52.1, bundled by Node 24.19.0) for the Windows file-system errors Node's fs and
// child_process surface as error.code. The errno values are winerror.h
// numbers, so the table is checked on every platform.
func TestWindowsCodeMatchesLibuv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		errno syscall.Errno
		want  string
	}{
		{"ERROR_INVALID_FUNCTION", 1, "EISDIR"},
		{"ERROR_FILE_NOT_FOUND", 2, "ENOENT"},
		{"ERROR_PATH_NOT_FOUND", 3, "ENOENT"},
		{"ERROR_TOO_MANY_OPEN_FILES", 4, "EMFILE"},
		{"ERROR_ACCESS_DENIED", 5, "EPERM"},
		{"ERROR_INVALID_DATA", 13, "EINVAL"},
		{"ERROR_INVALID_DRIVE", 15, "ENOENT"},
		{"ERROR_NOT_SAME_DEVICE", 17, "EXDEV"},
		{"ERROR_WRITE_PROTECT", 19, "EROFS"},
		{"ERROR_SHARING_VIOLATION", 32, "EBUSY"},
		{"ERROR_LOCK_VIOLATION", 33, "EBUSY"},
		{"ERROR_HANDLE_DISK_FULL", 39, "ENOSPC"},
		{"ERROR_FILE_EXISTS", 80, "EEXIST"},
		{"ERROR_CANNOT_MAKE", 82, "ENOSPC"},
		{"ERROR_INVALID_PARAMETER", 87, "EINVAL"},
		{"ERROR_BUFFER_OVERFLOW", 111, "ENAMETOOLONG"},
		{"ERROR_DISK_FULL", 112, "ENOSPC"},
		{"ERROR_INSUFFICIENT_BUFFER", 122, "EINVAL"},
		{"ERROR_INVALID_NAME", 123, "ENOENT"},
		{"ERROR_MOD_NOT_FOUND", 126, "ENOENT"},
		{"ERROR_DIR_NOT_EMPTY", 145, "ENOTEMPTY"},
		{"ERROR_BAD_PATHNAME", 161, "ENOENT"},
		{"ERROR_ALREADY_EXISTS", 183, "EEXIST"},
		{"ERROR_ENVVAR_NOT_FOUND", 203, "ENOENT"},
		{"ERROR_FILENAME_EXCED_RANGE", 206, "ENAMETOOLONG"},
		{"ERROR_PIPE_BUSY", 231, "EBUSY"},
		{"ERROR_DIRECTORY", 267, "ENOENT"},
		{"ERROR_EA_TABLE_FULL", 277, "ENOSPC"},
		{"ERROR_ELEVATION_REQUIRED", 740, "EACCES"},
		{"ERROR_NOACCESS", 998, "EFAULT"},
		{"ERROR_END_OF_MEDIA", 1100, "ENOSPC"},
		{"ERROR_PRIVILEGE_NOT_HELD", 1314, "EPERM"},
		{"ERROR_SYMLINK_NOT_SUPPORTED", 1464, "EINVAL"},
		{"ERROR_CANT_ACCESS_FILE", 1920, "EACCES"},
		{"ERROR_CANT_RESOLVE_FILENAME", 1921, "ELOOP"},
		{"ERROR_INVALID_REPARSE_DATA", 4392, "ENOENT"},
	}
	if len(cases) != len(windowsCodes) {
		t.Fatalf("windowsCodes has %d entries, the libuv table here has %d", len(windowsCodes), len(cases))
	}
	for _, tc := range cases {
		got, ok := windowsCode(tc.errno)
		if !ok || got != tc.want {
			t.Errorf("windowsCode(%s=%d) = %q, %v; want %q", tc.name, int(tc.errno), got, ok, tc.want)
		}
		if _, ok := Description(tc.want); !ok {
			t.Errorf("%s: code %s has no uv_strerror description", tc.name, tc.want)
		}
	}
	// ERROR_INVALID_HANDLE (6) is libuv EBADF, which has no description
	// here; ERROR_NOT_READY (21) and ERROR_COMMITMENT_LIMIT (1455) are libuv
	// UNKNOWN. None has a code here, so the caller keeps the Go text.
	for _, errno := range []syscall.Errno{0, 6, 21, 1455} {
		if got, ok := windowsCode(errno); ok {
			t.Errorf("windowsCode(%d) = %q, want no code", int(errno), got)
		}
	}
}

// TestErrorCodeUnwrapsWindowsErrno checks that a Windows system error wrapped
// the way os reports it is named by its libuv code, not left as Go text.
func TestErrorCodeUnwrapsWindowsErrno(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "open", Path: `C:\locked.txt`, Err: syscall.Errno(5)}, "EPERM"},
		{&fs.PathError{Op: "open", Path: `C:\missing\file.txt`, Err: syscall.Errno(3)}, "ENOENT"},
		{fmt.Errorf("wrapped: %w", &fs.PathError{Op: "open", Path: `C:\gone.txt`, Err: syscall.Errno(2)}), "ENOENT"},
		{&fs.PathError{Op: "read", Path: `C:\dir`, Err: syscall.Errno(1)}, "EISDIR"},
		{&fs.PathError{Op: "open", Path: `C:\x`, Err: syscall.Errno(6)}, ""},
		{fs.ErrNotExist, ""},
	}
	for _, tc := range cases {
		if got := errorCode(tc.err, windowsCode); got != tc.want {
			t.Errorf("errorCode(%v, windows) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestCodeNamesPlatformErrnos(t *testing.T) {
	t.Parallel()
	cases := map[syscall.Errno]string{
		syscall.ENOENT:       "ENOENT",
		syscall.EACCES:       "EACCES",
		syscall.EPERM:        "EPERM",
		syscall.EISDIR:       "EISDIR",
		syscall.EEXIST:       "EEXIST",
		syscall.EFAULT:       "EFAULT",
		syscall.ELOOP:        "ELOOP",
		syscall.ENAMETOOLONG: "ENAMETOOLONG",
	}
	// On Windows syscall.ENOTDIR is ERROR_PATH_NOT_FOUND, which libuv names
	// ENOENT.
	if runtime.GOOS == "windows" {
		cases[syscall.ENOTDIR] = "ENOENT"
	} else {
		cases[syscall.ENOTDIR] = "ENOTDIR"
	}
	for errno, want := range cases {
		if got, ok := Code(errno); !ok || got != want {
			t.Errorf("Code(%v) = %q, %v; want %q", errno, got, ok, want)
		}
	}
	for code := range descriptions {
		found := false
		for _, name := range posixCodes {
			found = found || name == code
		}
		if !found {
			t.Errorf("description for %s has no errno", code)
		}
	}
}

// TestErrorCodeFromFileSystem checks the codes the running platform reports
// for a missing file, a missing parent directory, and a regular file used as
// a directory, as Node reports them there.
func TestErrorCodeFromFileSystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Open(filepath.Join(dir, "missing.txt"))
	if got := ErrorCode(err); got != "ENOENT" {
		t.Errorf("missing file: ErrorCode(%v) = %q, want ENOENT", err, got)
	}
	_, err = os.Open(filepath.Join(dir, "missing", "file.txt"))
	if got := ErrorCode(err); got != "ENOENT" {
		t.Errorf("missing parent: ErrorCode(%v) = %q, want ENOENT", err, got)
	}
	_, err = os.Open(filepath.Join(file, "child.txt"))
	want := "ENOTDIR"
	if runtime.GOOS == "windows" {
		want = "ENOENT"
	}
	if got := ErrorCode(err); got != want {
		t.Errorf("file as directory: ErrorCode(%v) = %q, want %s", err, got, want)
	}
}
