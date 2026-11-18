//go:build !windows

package app

import (
	"errors"
	"os"
	"syscall"
)

// syncOpenDirectory flushes an open directory to its device; tests inject the failures of a file system (not syncFile, which is the flush of the
// intent files).
var syncOpenDirectory = func(dir *os.File) error { return dir.Sync() }

// flushDirectory flushes the directory entries of path to the device, which makes a creation, a rename or a removal in it durable. It is
// here and not a call of PiG's file-write package because Chord may not link that package (the boundary test
// chord/boundary_upstream_test.go lists what Chord may link). A failure is returned, because the entries are not yet safe from a power cut,
// except where the file system cannot flush a directory at all or the process may not open one:
//   - EINVAL, ENOTSUP, EOPNOTSUPP, ENOSYS from the flush: the file system has no such operation (some network and FUSE file systems), so
//     there is nothing more to give;
//   - EACCES from the open: a directory the process can write and search but not read, whose entries it has just changed.
//
// Anything else (EIO, ENOSPC, EDQUOT, EROFS, EMFILE) is a real failure.
func flushDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		if errors.Is(err, syscall.EACCES) {
			return nil
		}
		return err
	}
	err = syncOpenDirectory(dir)
	_ = dir.Close()
	if err != nil && !directoryFlushUnsupported(err) {
		return err
	}
	return nil
}

// directoryFlushUnsupported reports whether err says the file system has no flush of a directory.
func directoryFlushUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS)
}
