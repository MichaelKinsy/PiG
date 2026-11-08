package models

import "syscall"

// statTimes are the modification and change times in nanoseconds.
func statTimes(info *syscall.Stat_t) (mtime, ctime int64) {
	return info.Mtimespec.Nano(), info.Ctimespec.Nano()
}
