//go:build !windows

package testenv

import "os"

// createDirectoryLink creates newname as a symbolic link to the directory oldname.
func createDirectoryLink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}
