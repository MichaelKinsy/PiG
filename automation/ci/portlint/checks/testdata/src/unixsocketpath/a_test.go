package unixsocketpath

import (
	"net"
	"path/filepath"
	"testing"
)

func TestBad(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "very-long-name.sock")
	l, err := net.Listen("unix", sock) // want `unix socket in a function that builds paths under TempDir`
	if err == nil {
		_ = l.Close()
	}
}

func TestTCP(t *testing.T) {
	_ = t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		_ = l.Close()
	}
}
