//go:build !linux

package node

// filesystemType is unavailable off Linux, where no file system is treated as unreliable.
func filesystemType(string) (uint32, bool) { return 0, false }
