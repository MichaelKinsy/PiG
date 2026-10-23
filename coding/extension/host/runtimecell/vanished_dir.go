package runtimecell

import (
	"errors"
	"io/fs"
	"os"
)

// directoryVanished reports whether err, from opening or listing a directory that a walk had just found, means another process
// removed it, so that the directory held no source of the build. A directory that no longer exists did. On Windows a directory
// being deleted by another process is not reported as missing: opening it fails with ERROR_DELETE_PENDING or ERROR_ACCESS_DENIED
// until its last handle closes. An access denial is that deletion, not a directory the walk may not read, only when lstat (the
// re-check of the same directory) then finds it gone or still pending deletion; a directory that is there and denies the walk
// stays an error, so an unreadable source tree fails the hash.
func directoryVanished(err error, lstat func() error, deletion func(error) (pending, denied bool)) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	pending, denied := deletion(err)
	if pending {
		return true
	}
	if !denied {
		return false
	}
	lerr := lstat()
	if lerr == nil {
		return false
	}
	lerrPending, lerrDenied := deletion(lerr)
	return errors.Is(lerr, fs.ErrNotExist) || lerrPending || lerrDenied
}

// DirectoryVanished is directoryVanished for the directory dir of the local file system.
func DirectoryVanished(dir string, err error) bool {
	return directoryVanished(err, func() error { _, lerr := os.Lstat(dir); return lerr }, windowsDeletion) //perf:ok runs only for a directory that failed to open mid-walk
}
