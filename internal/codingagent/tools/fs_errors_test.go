package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestNodeFSErrorNamesEveryLibuvCode checks that the file tools format each
// errno libuv names as Node's fs promises reject it, not as Go text.
func TestNodeFSErrorNamesEveryLibuvCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		errno syscall.Errno
		want  string
	}{
		{syscall.ENOENT, "ENOENT: no such file or directory, open '/p'"},
		{syscall.EACCES, "EACCES: permission denied, open '/p'"},
		{syscall.EPERM, "EPERM: operation not permitted, open '/p'"},
		{syscall.EISDIR, "EISDIR: illegal operation on a directory, open '/p'"},
		{syscall.EEXIST, "EEXIST: file already exists, open '/p'"},
		{syscall.EFAULT, "EFAULT: bad address in system call argument, open '/p'"},
		{syscall.ENOTEMPTY, "ENOTEMPTY: directory not empty, open '/p'"},
		{syscall.EBUSY, "EBUSY: resource busy or locked, open '/p'"},
		{syscall.EMFILE, "EMFILE: too many open files, open '/p'"},
		{syscall.ENOSPC, "ENOSPC: no space left on device, open '/p'"},
		{syscall.EROFS, "EROFS: read-only file system, open '/p'"},
		{syscall.EXDEV, "EXDEV: cross-device link not permitted, open '/p'"},
	}
	for _, tc := range cases {
		err := &fs.PathError{Op: "open", Path: "/p", Err: tc.errno}
		if got := NodeFSError(err, "open", "/p"); got != tc.want {
			t.Errorf("NodeFSError(%v) = %q, want %q", tc.errno, got, tc.want)
		}
	}
	unknown := &fs.PathError{Op: "open", Path: "/p", Err: fs.ErrClosed}
	if got := NodeFSError(unknown, "open", "/p"); got != unknown.Error() {
		t.Errorf("NodeFSError(no errno) = %q, want Go text %q", got, unknown.Error())
	}
}

// TestReadMissingParentIsENOENT checks the code Node reports on every
// platform when a parent directory is missing: ENOENT. On Windows the
// system error is ERROR_PATH_NOT_FOUND, which Go's syscall.ENOTDIR aliases.
func TestReadMissingParentIsENOENT(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "file.txt")
	res := runFileTool(t, &ReadTool{CWD: dir}, context.Background(), map[string]any{"path": path})
	want := "ENOENT: no such file or directory, access '" + path + "'"
	if !res.IsError || res.Text() != want {
		t.Fatalf("read = %+v, want %q", res, want)
	}
}

// TestIsMissingPathErrorUsesNodeCodes mirrors upstream isMissingPathError
// (core/tools/file-mutation-queue.ts): only ENOENT and ENOTDIR are missing.
func TestIsMissingPathErrorUsesNodeCodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.txt"), filepath.Join(dir, "missing", "file.txt"), filepath.Join(file, "child.txt")} {
		_, err := filepath.EvalSymlinks(path)
		if err == nil || !isMissingPathError(err) {
			t.Errorf("EvalSymlinks(%s) error %v is not missing", path, err)
		}
	}
	for _, errno := range []syscall.Errno{syscall.EACCES, syscall.EPERM, syscall.ELOOP, syscall.EISDIR} {
		if isMissingPathError(&fs.PathError{Op: "lstat", Path: file, Err: errno}) {
			t.Errorf("%v is classified as missing", errno)
		}
	}
	if runtime.GOOS != "windows" && !isMissingPathError(&fs.PathError{Op: "lstat", Path: file, Err: syscall.ENOTDIR}) {
		t.Error("ENOTDIR is not classified as missing")
	}
}
