//go:build windows

package cli

import "net"

// listenPrivateUnixSocket binds the --listen socket. Windows keeps no mode bits on a socket file: its access follows the directory's permissions. pig additive (D232).
func listenPrivateUnixSocket(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
