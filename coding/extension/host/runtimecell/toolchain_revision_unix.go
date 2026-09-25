//go:build unix

package runtimecell

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// toolchainFileRevision includes file identity and status-change time, so an
// atomic replacement is detected even when size and mtime are preserved.
func toolchainFileRevision(path string) (string, bool) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Size, stat.Mtim.Nano(), stat.Ctim.Nano()), true
}
