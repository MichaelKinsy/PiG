package subprocess

import "net"

// Every file of coding/extension/host/subprocess is in scope.
func write(c net.Conn, b []byte) {
	_, _ = c.Write(b) // want `a net.Conn write puts a frame on the socket`
}
