//go:build linux || darwin || freebsd

package node

import (
	"os"
	"syscall"
)

// openWalIndex opens the wal-index file at path for reading. These systems keep one page cache for a file whether it is
// mapped or read, so a read of the file sees what SQLite stored through its mapping of it as soon as the store happened.
func openWalIndex(path string) (*os.File, error) { return os.Open(path) }

// walIndexKey identifies the file at path by device and inode.
func walIndexKey(path string) ([2]uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return [2]uint64{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return [2]uint64{}, false
	}
	return [2]uint64{uint64(stat.Dev), uint64(stat.Ino)}, true //nolint:unconvert // the types differ between systems
}
