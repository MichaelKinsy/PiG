//go:build unix

package cellpack

import (
	"fmt"
	"os"
	"syscall"
)

// sizeAndIdentity names a file's size and inode, so a snapshot tells a rewritten file from an untouched one.
func sizeAndIdentity(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d inode %d", info.Size(), stat.Ino)
}
