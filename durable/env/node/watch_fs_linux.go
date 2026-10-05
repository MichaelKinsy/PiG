//go:build linux

package node

import "syscall"

// filesystemType is the statfs magic number of the file system holding path.
func filesystemType(path string) (uint32, bool) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, false
	}
	return uint32(stats.Type), true
}
