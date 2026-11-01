//go:build !windows

package plugins

import (
	"net"
	"os"
	"path/filepath"
)

// listenNode listens for the one connection of a Node subprocess and returns the address it connects to: a unix socket
// in a directory of its own, which only the current user can enter. cleanup removes the directory.
func listenNode() (listener net.Listener, address string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "chord-plugins-")
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
