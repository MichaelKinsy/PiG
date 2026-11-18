package codingagent

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// anonymousFile creates a file in dir and removes its name at once.
func anonymousFile(dir string) (*os.File, error) { return unlinkedTemp(dir) }

// cloneFile returns a file beside path that shares the blocks of src, the file opened from path (APFS clonefile), with its name removed at once.
func cloneFile(src *os.File, path string) (*os.File, bool) {
	file, err := createSnapshotFile(filepath.Dir(path), func(name string) (*os.File, error) {
		if err := unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, name, 0); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(name, os.O_RDWR, 0)
		_ = os.Remove(name)
		return file, err
	})
	return file, err == nil
}
