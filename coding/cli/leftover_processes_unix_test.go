//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// killProcessesIn sends SIGKILL to every process whose working directory is dir. A test that ends pig with a signal Pi
// leaves to the default action (SIGINT) kills its own leftovers this way: the shell of an interrupted bash call runs in
// its own process group and outlives pig, as it does under Pi. Where /proc does not exist it does nothing.
func killProcessesIn(dir string) {
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd")); err == nil && cwd == want {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
