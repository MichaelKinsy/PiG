//go:build !windows

package cli

import (
	"os"
	"syscall"
	"testing"
)

// TestListenSocketIsPrivateUnderAPermissiveUmask: the --listen socket is 0600 from the moment it exists, whatever umask the server inherited (D232). The server inherits the test's umask, so a permissive one here would show at the socket without the private bind.
func TestListenSocketIsPrivateUnderAPermissiveUmask(t *testing.T) {
	old := syscall.Umask(0)
	server := newListenServer(t)
	syscall.Umask(old)
	client := server.dial()
	defer client.closeInput()
	info, err := os.Stat(server.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode %o, want 0600", perm)
	}
}
