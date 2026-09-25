package subprocess

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A packed Node member whose factory fails reports it by connecting to its
// socket and closing the connection, usually before the host accepts that
// member. The listener must deliver that connection, as a Unix socket's
// backlog does, so the host's first read ends the member's load instead of
// waiting for a client that never comes.
func TestExtensionListenerKeepsAConnectionClosedBeforeAccept(t *testing.T) {
	for _, node := range []bool{false, true} {
		t.Run(fmt.Sprintf("node=%t", node), func(t *testing.T) {
			dir, err := os.MkdirTemp("", "pl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			ln, address, err := ListenExtension(filepath.Join(dir, "e.sock"), node)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()

			if node && runtime.GOOS == "windows" {
				client, err := os.OpenFile(address, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				_ = client.Close()
			} else {
				client, err := net.Dial("unix", address)
				if err != nil {
					t.Fatal(err)
				}
				_ = client.Close()
			}

			type accepted struct {
				read error
				err  error
			}
			result := make(chan accepted, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					result <- accepted{err: err}
					return
				}
				defer func() { _ = conn.Close() }()
				_, readErr := conn.Read(make([]byte, 1))
				result <- accepted{read: readErr}
			}()
			select {
			case got := <-result:
				if got.err != nil || !errors.Is(got.read, io.EOF) {
					t.Fatalf("Accept = %v, first read = %v; want the closed connection and end of file", got.err, got.read)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Accept did not return the connection its client closed before the accept")
			}
		})
	}
}
