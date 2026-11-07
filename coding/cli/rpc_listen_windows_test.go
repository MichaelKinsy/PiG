//go:build windows

package cli

import (
	"net"
	"path/filepath"
	"testing"
)

// TestListenBindsAnAFUnixSocketOnWindows: --listen serves an AF_UNIX socket on Windows 10 version 1803 or later; named pipes are not supported (D232). The permission mode and the private bind directory of the Unix build do not exist there: a socket's access follows the permissions of its directory, so the Unix-only tests (the 0600 mode, the umask, hard links, signals) do not run on Windows.
func TestListenBindsAnAFUnixSocketOnWindows(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	listener, err := listenPrivateUnixSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}
}
