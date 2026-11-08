//go:build unix && !linux && !darwin && !freebsd

package plugins

import "syscall"

// statIdentity is the inode of the entry at path, not following a link, and no birth time: the system's stat has none that
// Go's syscall package gives (divergence record D-IDENTITY-BIRTH).
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	var stat syscall.Stat_t
	if syscall.Lstat(path, &stat) != nil {
		return 0, 0, false
	}
	return uint64(stat.Ino), 0, true
}
