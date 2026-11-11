package plugins

import (
	"net"
	"testing"
	"time"
)

// TestFramedPortRefusesAFrameTheReaderRejects pins that write sends no frame whose length the other end's read ends the channel on
// (maxFrame), and does not wrap the four-byte length: a payload of 4 GiB would be written as a frame of length 1.
func TestFramedPortRefusesAFrameTheReaderRejects(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	port := newFramedPort(client, func([]byte) {})

	// One more byte than maxFrame allows with the kind byte: no bytes may reach the connection.
	if err := port.write(frameLink, make([]byte, maxFrame)); err == nil {
		t.Fatal("write accepted a frame longer than maxFrame")
	}
	_ = server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := server.Read(make([]byte, 1)); n != 0 {
		t.Fatalf("a refused frame still wrote %d bytes", n)
	}

	// The largest frame the reader accepts is written whole.
	done := make(chan error, 1)
	go func() { done <- port.write(frameLink, make([]byte, maxFrame-1)) }()
	header := make([]byte, 5)
	_ = server.SetReadDeadline(time.Time{})
	if _, err := server.Read(header); err != nil {
		t.Fatal(err)
	}
	if got := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3]); got != maxFrame {
		t.Fatalf("frame length = %d, want %d", got, maxFrame)
	}
	_ = server.Close()
	<-done
}
