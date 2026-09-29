//go:build windows

package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestNodeFSErrorTranslatesWindowsErrors checks the libuv
// uv_translate_sys_error names the file tools report for Windows system
// errors instead of Go's Windows message text.
func TestNodeFSErrorTranslatesWindowsErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		errno syscall.Errno
		want  string
	}{
		{windows.ERROR_ACCESS_DENIED, `EPERM: operation not permitted, open 'C:\p'`},
		{windows.ERROR_PRIVILEGE_NOT_HELD, `EPERM: operation not permitted, open 'C:\p'`},
		{windows.ERROR_FILE_NOT_FOUND, `ENOENT: no such file or directory, open 'C:\p'`},
		{windows.ERROR_PATH_NOT_FOUND, `ENOENT: no such file or directory, open 'C:\p'`},
		{windows.ERROR_INVALID_NAME, `ENOENT: no such file or directory, open 'C:\p'`},
		{windows.ERROR_CANT_ACCESS_FILE, `EACCES: permission denied, open 'C:\p'`},
		{windows.ERROR_SHARING_VIOLATION, `EBUSY: resource busy or locked, open 'C:\p'`},
		{windows.ERROR_INVALID_FUNCTION, `EISDIR: illegal operation on a directory, open 'C:\p'`},
	}
	for _, tc := range cases {
		err := &fs.PathError{Op: "open", Path: `C:\p`, Err: tc.errno}
		if got := NodeFSError(err, "open", `C:\p`); got != tc.want {
			t.Errorf("NodeFSError(%d) = %q, want %q", int(tc.errno), got, tc.want)
		}
	}
	if !isMissingPathError(&fs.PathError{Op: "lstat", Path: `C:\p`, Err: windows.ERROR_INVALID_NAME}) {
		t.Error("ERROR_INVALID_NAME (ENOENT) is not classified as missing")
	}
	if isMissingPathError(&fs.PathError{Op: "lstat", Path: `C:\p`, Err: windows.ERROR_ACCESS_DENIED}) {
		t.Error("ERROR_ACCESS_DENIED (EPERM) is classified as missing")
	}
}

// TestEditAccessDeniedIsEPERMOnWindows checks the edit tool's access error
// for a Windows access-denied failure: Node reports error.code EPERM.
func TestEditAccessDeniedIsEPERMOnWindows(t *testing.T) {
	t.Parallel()
	tool := &EditTool{CWD: t.TempDir(), Queue: NewFileMutationQueue(), Operations: &EditOperations{
		Access: func(path string) error {
			return &fs.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
		},
		ReadFile:  func(string) ([]byte, error) { return []byte("hello\n"), nil },
		WriteFile: func(string, string) error { return nil },
	}}
	res := runFileTool(t, tool, context.Background(), map[string]any{"path": "locked.txt", "edits": []editEntry{{"hello", "world"}}})
	if want := "Could not edit file: locked.txt. Error code: EPERM."; !res.IsError || res.Text() != want {
		t.Fatalf("edit = %+v, want %q", res, want)
	}
}

// TestAccessReadWriteMatchesLibuvOnWindows checks libuv fs__access: W_OK
// fails with EPERM only for a read-only file, never for a directory.
func TestAccessReadWriteMatchesLibuvOnWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "readonly.txt")
	if err := os.WriteFile(file, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if got := nodeErrorCode(accessReadWrite(file)); got != "EPERM" {
		t.Errorf("read-only file: code %q, want EPERM", got)
	}
	sub := filepath.Join(dir, "readonly-dir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_READONLY); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_NORMAL) })
	if err := accessReadWrite(sub); err != nil {
		t.Errorf("read-only directory: %v, want nil", err)
	}
	if got := nodeErrorCode(accessReadWrite(filepath.Join(dir, "missing", "file.txt"))); got != "ENOENT" {
		t.Errorf("missing parent: code %q, want ENOENT", got)
	}
}
