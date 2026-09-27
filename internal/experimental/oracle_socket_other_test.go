//go:build !windows

package experimental

import (
	"net"
	"testing"
)

// oracleListenPath returns the address the upstream coordinator listens on in
// place of path: the same socket file path PiG uses.
func oracleListenPath(path string) string { return path }

func dialTestSocket(path string) (net.Conn, error) { return net.Dial("unix", path) }

func listenTestSocket(path string) (net.Listener, error) { return net.Listen("unix", path) }

// productControlPath returns a path PiG's CoordinatorConnection can dial to
// reach the coordinator control socket at path: path itself.
func productControlPath(_ *testing.T, path string) string { return path }
