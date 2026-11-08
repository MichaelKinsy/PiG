//go:build linux

package plugins

import (
	"errors"

	"golang.org/x/sys/unix"
)

// statIdentity is the inode and the birth time of the entry at path, not following a link: statx's `stx_ino` and `stx_btime`, as
// libuv gives Node's `ino` and `birthtimeNs` (0 where the file system has no birth time, tmpfs for one). An old kernel, or a
// filter that refuses statx, gives the inode of lstat and no birth time.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var statx unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME, &statx)
	switch {
	case err == nil:
		if statx.Mask&unix.STATX_BTIME != 0 {
			birth = statx.Btime.Sec*1e9 + int64(statx.Btime.Nsec)
		}
		return statx.Ino, birth, true
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EPERM), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EINVAL):
		var stat unix.Stat_t
		if unix.Lstat(path, &stat) != nil {
			return 0, 0, false
		}
		return stat.Ino, 0, true
	}
	return 0, 0, false
}
