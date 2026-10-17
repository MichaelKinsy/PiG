//go:build !unix || darwin

package ai

import (
	"net"
	"testing"
)

// closedTCPAddress returns a loopback address whose listener is closed, so a connect is refused unless another process takes the port first.
func closedTCPAddress(t *testing.T, network, address string) *net.TCPAddr {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Skipf("listen %s %s: %v", network, address, err)
	}
	closed := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return closed
}
