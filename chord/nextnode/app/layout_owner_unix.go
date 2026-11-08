//go:build unix

package app

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkDirectoryOwner refuses a data directory another user owns (Pi: `uid !== process.getuid()`).
func checkDirectoryOwner(dataDir string, info fs.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("startServer: the data directory %s belongs to another user (uid %d)", dataDir, stat.Uid)
	}
	return nil
}
