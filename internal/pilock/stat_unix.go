//go:build unix

package pilock

import (
	"io/fs"
	"os"
)

// lstatLock reads the lock path. Unix removes a directory name at rmdir, so no delete-pending state exists.
func lstatLock(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}
