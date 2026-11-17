//go:build aix

package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

import "syscall"

// statIdentity is the inode of the entry at path, not following a link, and its CTIME in whole seconds as the birth time: libuv's
// `uv__to_stat` (deps/uv/src/unix/fs.c, libuv 1.52.1 of Node v24.22.2) sets `st_birthtim` to `st_ctime` with nanoseconds 0 on AIX.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), int64(stat.Ctim.Sec) * 1e9, true
}
