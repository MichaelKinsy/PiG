//go:build windows

package tools

import (
	"os"
	"syscall"
)

// accessReadWrite mirrors Node fs.access(path, R_OK | W_OK) on Windows.
// libuv's fs__access reads the path's own attributes (GetFileAttributesW,
// which does not follow a symlink) and fails with EPERM only when W_OK meets
// a read-only file; a directory is always accessible. A missing path reports
// the system error libuv translates (ENOENT).
func accessReadWrite(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_READONLY != 0 && data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return &os.PathError{Op: "access", Path: path, Err: syscall.EPERM}
	}
	return nil
}
