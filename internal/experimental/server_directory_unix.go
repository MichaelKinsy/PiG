//go:build !windows

package experimental

import (
	"os"
	"syscall"
)

func serverDirectoryOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
