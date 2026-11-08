//go:build linux || android

package app

import (
	"os"
	"syscall"
	"time"
)

// ctimeOf is the time the file's status last changed (Pi's `ctimeMs`).
func ctimeOf(info os.FileInfo) time.Time {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec))
	}
	return info.ModTime()
}
