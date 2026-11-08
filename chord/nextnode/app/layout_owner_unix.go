//go:build unix

package app

import (
	"os"
	"syscall"
)

// ownerOf is the user id that owns the file info describes (Pi's `stat.uid`); ok is false where the platform has none.
func ownerOf(info os.FileInfo) (uid int, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}
