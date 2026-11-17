//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"syscall"
)

// syncFile flushes an open directory to its device; tests inject the failures of a file system.
var syncFile = func(file *os.File) error { return file.Sync() }

// syncDirectory flushes the directory entries of path, which makes a rename in it durable. A failure is returned, because the replacement is not yet
// safe from a power cut and the caller must not be told it is, except where the file system cannot flush a directory at all or the process may not open one:
//   - EINVAL, ENOTSUP, EOPNOTSUPP, ENOSYS from the flush: the file system has no such operation (some network and FUSE file systems, fsync(2) says EINVAL for a
//     descriptor that does not support synchronization), so there is nothing more to give;
//   - EACCES from the open: a directory the process can write and search but not read, whose entries it has just changed.
//
// Anything else (EIO, ENOSPC, EDQUOT, EROFS, EMFILE) is a real failure.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		if errors.Is(err, syscall.EACCES) {
			return nil
		}
		return err
	}
	err = syncFile(dir)
	_ = dir.Close()
	if err != nil && !directorySyncUnsupported(err) {
		return err
	}
	return nil
}

// directorySyncUnsupported reports whether err says the file system has no flush of a directory.
func directorySyncUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS)
}
