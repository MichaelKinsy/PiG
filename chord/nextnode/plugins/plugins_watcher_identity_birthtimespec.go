//go:build darwin || freebsd

package plugins

import "syscall"

// statIdentity is the inode and the birth time of the entry at path, not following a link: lstat's `st_ino` and
// `st_birthtimespec`, as libuv gives Node's `ino` and `birthtimeNs`.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), int64(stat.Birthtimespec.Sec)*1e9 + int64(stat.Birthtimespec.Nsec), true
}
