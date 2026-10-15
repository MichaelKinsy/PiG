package sdk

import (
	"errors"
	"net"
	"testing"
)

// A socket never takes the by-parts read of a fused connection: an extension in its own process reads byte frames (fused-direct review Q1).
func TestSocketsAreNotFramePartsReaders(t *testing.T) {
	for _, conn := range []net.Conn{&net.UnixConn{}, &net.TCPConn{}} {
		if _, ok := conn.(framePartsReader); ok {
			t.Fatalf("%T satisfies framePartsReader", conn)
		}
	}
	a, b := net.Pipe()
	t.Cleanup(func() {
		if err := errors.Join(a.Close(), b.Close()); err != nil {
			t.Error(err)
		}
	})
	if _, ok := a.(framePartsReader); ok {
		t.Fatal("net.Pipe satisfies framePartsReader")
	}
}
