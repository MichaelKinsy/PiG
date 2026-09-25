//go:build !windows

package codingagent

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// replacementDirectoryWritable checks the permissions required by the atomic
// rename. It intentionally does not open the running executable: Linux rejects
// that with ETXTBSY even though replacing it in a writable directory is valid.
func replacementDirectoryWritable(path string) bool {
	dir := filepath.Dir(path)
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}
