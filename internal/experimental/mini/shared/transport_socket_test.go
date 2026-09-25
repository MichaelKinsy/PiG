package shared

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// transport.ts uses rm(path, {force:true}), without recursive:true. A directory is an error and is never removed to make room for the socket.
func TestSocketTransportRefusesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mini.sock")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := SocketTransport(path).Listen(func(*JSONConnection) {})
	if err == nil {
		_ = listener.Close()
		t.Fatal("Listen removed a directory instead of rejecting it")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory changed: %v %v", info, err)
	}
}

func TestSocketTransportStaleFileAndCancelledConnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mini.sock")
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := SocketTransport(path).Listen(func(c *JSONConnection) { _ = c.Close(); c.Wait() })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if connection, err := SocketTransport(path).Connect(ctx); err == nil {
		_ = connection.Close()
		connection.Wait()
		t.Fatal("cancelled connect succeeded")
	}
}
