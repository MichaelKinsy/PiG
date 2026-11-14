//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"syscall"
)

// linkCount is the number of directory entries that name the file.
func linkCount(_ *os.File, info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Nlink) //nolint:unconvert // Nlink is a uint16 on darwin and a uint64 on linux
	}
	return 1
}

// refusesNewFile reports whether err says the directory does not let this process create the temporary file: the directory is not writable, the file
// system is read-only, or the operation is not permitted. A full disk or an I/O error is not a refusal.
func refusesNewFile(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EPERM)
}

// refusesReplace reports whether err says the target cannot be replaced by a rename: it is a mount point (a file mounted into a container on its
// own), on another file system, or a running executable.
func refusesReplace(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.ETXTBSY)
}
