//go:build !windows

package cli

import (
	"net"
	"syscall"
)

// listenPrivateUnixSocket binds the --listen socket under umask 0177, so the file is 0600 from the moment it exists: net.Listen creates it with the process umask, and a connection queued in the window before a later chmod would be served a full session. pig additive (D232): the umask is process-wide, so the window applies to any file this process creates meanwhile, which only loses permission bits.
func listenPrivateUnixSocket(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}
