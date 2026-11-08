//go:build darwin || freebsd || netbsd

package tsruntime

import (
	"fmt"
	"os"
	"syscall"
)

// identify reads the device, inode, size, and modification and status-change times of an opened file.
func identify(info os.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, fmt.Errorf("file status of %s carries no inode", info.Name())
	}
	return fileIdentity{
		Device:   uint64(stat.Dev), // int32 on darwin.
		Inode:    stat.Ino,
		Size:     stat.Size,
		Modified: stat.Mtimespec.Nano(),
		Changed:  stat.Ctimespec.Nano(),
	}, nil
}

// ownedByCurrentUser reports whether info belongs to the user this process runs as.
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}

// openRecord opens the digest record without following a symbolic link and without blocking on a FIFO or a device: either is then
// refused by the regular-file check.
func openRecord(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// unlinked reports whether no directory entry names the file any more, as after a rename over its path.
func unlinked(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 0
}
