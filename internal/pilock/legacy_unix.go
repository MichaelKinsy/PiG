//go:build unix

package pilock

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// observeLegacy keeps the os.Lstat result, which already records the device and inode.
func observeLegacy(_ string, info fs.FileInfo) (fs.FileInfo, error) {
	return info, nil
}

func openLegacy(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the path names an output or state file that the user or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so writing it grants nothing the user lacks
}

func lockLegacy(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLegacyLocked
	}
	return err
}

func removeLegacy(_ *os.File, path string) error {
	// Unlike os.Remove, unlink cannot remove a directory that replaced the file.
	return syscall.Unlink(path)
}
