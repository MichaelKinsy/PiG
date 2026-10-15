//go:build !windows

package nodefs

import "os"

// readDir sorts the entries by name, as libuv's Unix scandir does with strcmp.
func readDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}
