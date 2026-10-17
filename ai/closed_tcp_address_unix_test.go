//go:build unix && !darwin

package ai

import (
	"net"
	"syscall"
	"testing"
)

// closedTCPAddress returns a loopback address that refuses connections for the rest of the test. (Not on darwin: macOS drops a connect to a bound socket that never listens, so the dial times out instead of being refused.) The port stays bound to a socket that never listens, so the kernel answers a connect with a reset and no other process can take the port; a closed listener's port can be reused by a concurrent process before the test dials it.
func closedTCPAddress(t *testing.T, network, address string) *net.TCPAddr {
	t.Helper()
	resolved, err := net.ResolveTCPAddr(network, address)
	if err != nil {
		t.Fatal(err)
	}
	family, sockaddr := syscall.AF_INET6, syscall.Sockaddr(nil)
	if ip4 := resolved.IP.To4(); ip4 != nil && network != "tcp6" {
		inet4 := &syscall.SockaddrInet4{Port: resolved.Port}
		copy(inet4.Addr[:], ip4)
		family, sockaddr = syscall.AF_INET, inet4
	} else {
		inet6 := &syscall.SockaddrInet6{Port: resolved.Port}
		copy(inet6.Addr[:], resolved.IP.To16())
		sockaddr = inet6
	}
	fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Skipf("socket %s: %v", network, err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, sockaddr); err != nil {
		t.Skipf("bind %s %s: %v", network, address, err)
	}
	bound, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	switch bound := bound.(type) {
	case *syscall.SockaddrInet4:
		return &net.TCPAddr{IP: net.IP(bound.Addr[:]).To4(), Port: bound.Port}
	case *syscall.SockaddrInet6:
		return &net.TCPAddr{IP: net.IP(bound.Addr[:]), Port: bound.Port}
	}
	t.Fatalf("bound address %T", bound)
	return nil
}
