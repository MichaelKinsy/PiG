//go:build windows

package cli

import "net"

// listenPrivateUnixSocket binds the --listen socket. Windows keeps no mode bits on a socket file: its access follows the directory's permissions. The server removes path itself at exit (removeOwnSocket), so the listener must not unlink it when it closes: Go's Unix listener unlinks the bound path on Close on Windows as on Unix, whatever the path names by then. pig additive (D232).
func listenPrivateUnixSocket(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if unix, ok := listener.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	return listener, nil
}
