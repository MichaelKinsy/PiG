//go:build openbsd

package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

import "syscall"

// statIdentity is the inode and the birth time of the entry at path, not following a link: lstat's `st_ino` and `st_birthtim`
// (Go's syscall names it `X__st_birthtim`), as libuv gives Node's `ino` and `birthtimeNs`.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), int64(stat.X__st_birthtim.Sec)*1e9 + int64(stat.X__st_birthtim.Nsec), true
}
