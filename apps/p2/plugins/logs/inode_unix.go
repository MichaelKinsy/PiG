//go:build !windows

package logs

import (
	"os"
	"syscall"
)

// inodeOf is the file's inode (part of a snapshot's fingerprint: a replaced file differs even with the same size and time).
func inodeOf(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Ino)
	}
	return 0
}
