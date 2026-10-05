//go:build !windows

package node

import (
	"io/fs"
	"syscall"
)

// fileIdentity is the device and inode of a path's metadata, as Node's stat reports dev and ino.
func fileIdentity(_ string, info fs.FileInfo, _ bool) (dev, ino uint64) {
	if stats, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stats.Dev), uint64(stats.Ino) //nolint:unconvert // the field types differ by platform
	}
	return 0, 0
}
