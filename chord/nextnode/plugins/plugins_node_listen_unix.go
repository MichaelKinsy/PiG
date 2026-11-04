//go:build !windows

package plugins

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

// socketDirPrefix starts the name of every socket directory this process makes: "chord-plugins-", then the owner the
// process was started as (SocketOwnerEnv) when it has one, so the parent that started it can find them.
func socketDirPrefix(owner string) string {
	if owner == "" {
		return "chord-plugins-"
	}
	return "chord-plugins-" + owner + "-"
}

// RemoveSocketDirsOf removes the socket directories in tempDir that a process started with SocketOwnerEnv=owner left behind,
// once that process has ended. A process killed, or exiting, while its NodeHost was still starting cannot clean up after
// itself (no one closes that host): its parent does. The owner is unique to one start, so a directory of another process
// (one that shares the temporary directory from another pid namespace) is never named.
func RemoveSocketDirsOf(tempDir, owner string) {
	if owner == "" {
		return
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}
	prefix := socketDirPrefix(owner)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			_ = os.RemoveAll(filepath.Join(tempDir, entry.Name()))
		}
	}
}

// listenNode listens for the connections of a Node subprocess and returns the address it connects to: a unix socket in a
// directory of its own, which only the current user can enter. The process makes the channel's connection, and later (when
// the host needs synchronous callbacks) one more from a worker thread. cleanup removes the directory.
func listenNode() (listener net.Listener, address string, cleanup func(), err error) {
	return listenNodeIn("")
}

// listenNodeIn is listenNode with its directory made in tempDir ("" is the process's).
func listenNodeIn(tempDir string) (listener net.Listener, address string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(tempDir, socketDirPrefix(currentSocketOwner()))
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
