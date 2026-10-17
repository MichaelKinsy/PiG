//go:build !linux

package ai

import "net"

// newSocketReader has no raw read outside Linux; the connection's own Read serves every read.
func newSocketReader(net.Conn, string) func([]byte) (int, error) { return nil }
