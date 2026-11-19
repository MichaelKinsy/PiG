//go:build linux

package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

import (
	"errors"

	"golang.org/x/sys/unix"
)

// statxCall is statx(2); a test replaces it to make the kernel refuse, as an old kernel or a seccomp filter does.
var statxCall = unix.Statx

// statIdentity is the inode and the birth time of the entry at path, not following a link: statx's `stx_ino` and `stx_btime`, as
// libuv gives Node's `ino` and `birthtimeNs` (0 where the file system has no birth time, tmpfs for one). An old kernel, or a
// filter that refuses statx, takes libuv's lstat fallback (lstatIdentity).
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var statx unix.Statx_t
	err := statxCall(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME, &statx)
	switch {
	case err == nil:
		if statx.Mask&unix.STATX_BTIME != 0 {
			birth = statx.Btime.Sec*1e9 + int64(statx.Btime.Nsec)
		}
		return statx.Ino, birth, true
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EPERM), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EINVAL):
		return lstatIdentity(path)
	}
	return 0, 0, false
}

// lstatIdentity is the identity when statx is refused: lstat's inode and its CTIME as the birth time. libuv's `uv__to_stat`
// (deps/uv/src/unix/fs.c, libuv 1.52.1 of Node v24.22.2) has no birth time in a plain `struct stat` and sets
// `st_birthtim` to `st_ctim` (seconds and nanoseconds) on Linux and Android, so Pi's identity there is inode and ctime: a
// change of the entry's attributes (chmod) is a new identity on this path, as it is in Pi.
func lstatIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat unix.Stat_t
	if unix.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return stat.Ino, int64(stat.Ctim.Sec)*1e9 + int64(stat.Ctim.Nsec), true
}
