//go:build unix

package evals

import (
	"io/fs"
	"syscall"
)

func sandboxOwner(info fs.FileInfo) uint32 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return 0
}
