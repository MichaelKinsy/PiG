//go:build unix

package pilock

import (
	"io/fs"
	"os"
)

// lstatLock reads the lock path itself and statLock follows a link. Unix removes a directory name at rmdir, so no delete-pending state exists.
func lstatLock(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

func statLock(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}
