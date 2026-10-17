package codingagent

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// anonymousFile creates a file in dir that has no name (O_TMPFILE), or one whose name is removed at once where the file system has no
// O_TMPFILE.
func anonymousFile(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err == nil {
		return os.NewFile(uintptr(fd), dir), nil
	}
	return unlinkedTemp(dir)
}

// cloneFile returns an anonymous file beside path that shares path's blocks (FICLONE), on file systems that clone (Btrfs, XFS).
func cloneFile(src *os.File, path string) (*os.File, bool) {
	dst, err := anonymousFile(filepath.Dir(path))
	if err != nil {
		return nil, false
	}
	if unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())) != nil {
		_ = dst.Close()
		return nil, false
	}
	return dst, true
}
