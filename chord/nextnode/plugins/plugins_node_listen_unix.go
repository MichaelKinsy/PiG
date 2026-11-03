//go:build !windows

package plugins

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// socketDirPrefix starts the name of every socket directory the process pid made: a parent that reaps a dead worker's
// directories (RemoveSocketDirsOf) names them by its pid.
func socketDirPrefix(pid int) string { return "chord-plugins-" + strconv.Itoa(pid) + "-" }

// RemoveSocketDirsOf removes the socket directories left in tempDir by the process pid, which is gone. A process killed, or
// exiting, while its NodeHost was still starting cannot clean up after itself (no one closes that host): its parent does.
func RemoveSocketDirsOf(tempDir string, pid int) {
	left, err := filepath.Glob(filepath.Join(tempDir, socketDirPrefix(pid)+"*"))
	if err != nil {
		return
	}
	for _, dir := range left {
		_ = os.RemoveAll(dir)
	}
}

// listenNode listens for the connections of a Node subprocess and returns the address it connects to: a unix socket in a
// directory of its own, which only the current user can enter. The process makes the channel's connection, and later (when
// the host needs synchronous callbacks) one more from a worker thread. cleanup removes the directory.
func listenNode() (listener net.Listener, address string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", socketDirPrefix(os.Getpid()))
	if err != nil {
		return nil, "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	address = filepath.Join(dir, "s")
	listener, err = net.Listen("unix", address)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return listener, address, cleanup, nil
}
