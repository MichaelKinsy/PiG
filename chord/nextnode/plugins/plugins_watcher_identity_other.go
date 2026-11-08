//go:build unix && !linux && !darwin && !freebsd && !netbsd && !openbsd

package plugins

import "syscall"

// statIdentity is the inode of the entry at path, not following a link, and no birth time: Go's syscall package gives none here.
//
// pig divergence (D540): Pi's identity is inode and birth time; on these systems it is the inode alone.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), 0, true
}
