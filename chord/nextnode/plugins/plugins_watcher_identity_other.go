//go:build unix && !linux && !darwin && !freebsd && !netbsd && !openbsd && !aix

package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

import "syscall"

// statIdentity is the inode of the entry at path, not following a link, and its CTIME as the birth time: Go's syscall package
// has no birth time here, and libuv's `uv__to_stat` (deps/uv/src/unix/fs.c, libuv 1.52.1 of Node v24.22.2) sets `st_birthtim` to
// `st_ctim`, seconds and nanoseconds, on DragonFly and Solaris / illumos, so Pi's identity is the inode and the ctime.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), int64(stat.Ctim.Sec)*1e9 + int64(stat.Ctim.Nsec), true
}
