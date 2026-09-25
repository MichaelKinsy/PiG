//go:build windows

package tools

import (
	"os"
	"syscall"
)

// accessReadWrite mirrors Node fs.access(path, R_OK | W_OK) on Windows,
// where W_OK fails only for a read-only file.
func accessReadWrite(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o200 == 0 {
		return &os.PathError{Op: "access", Path: path, Err: syscall.EACCES}
	}
	return nil
}
