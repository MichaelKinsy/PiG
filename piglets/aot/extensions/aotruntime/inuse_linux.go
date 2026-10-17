//go:build linux

package aotruntime

import (
	"os"
	"path/filepath"
	"strings"
)

// inUse reports whether a running process executes a file inside dir. It reads /proc/<pid>/exe, which another user's process does not allow, so a
// process of another user does not count.
func inUse(dir string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] < '0' || entry.Name()[0] > '9' {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err == nil && strings.HasPrefix(strings.TrimSuffix(exe, " (deleted)"), prefix) {
			return true
		}
	}
	return false
}
