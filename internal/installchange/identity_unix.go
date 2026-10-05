//go:build !windows

package installchange

import (
	"errors"
	"os"
	"syscall"
)

// identityOf stats path, following a final symbolic link.
func identityOf(path string) (Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Identity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, errors.New("installchange: no device and inode for " + path)
	}
	return Identity{
		Path:    path,
		File:    FileID{Device: uint64(stat.Dev), Index: uint64(stat.Ino)}, //nolint:unconvert // the field types differ by platform
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}
