//go:build darwin || freebsd || netbsd

package signature

import (
	"fmt"
	"os"
	"syscall"
)

// identify reads the device, inode, size, and modification and status-change times of an opened file.
func identify(_ *os.File, info os.FileInfo) (fileIdentity, error) {
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
