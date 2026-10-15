package sdk

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// writeSpy records each Write call: a frame's header and body written by two
// calls cost the host two reads.
type writeSpy struct {
	net.Conn
	writes [][]byte
}

func (s *writeSpy) Write(p []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (*writeSpy) SetWriteDeadline(time.Time) error { return nil }

func TestSmallFrameIsOneWriteCall(t *testing.T) {
	spy := &writeSpy{}
	c := &conn{nc: spy}
	body := []byte(`{"type":"pong","pong":{"nonce":"n1"}}`)
	if err := c.writeFrame(body); err != nil {
		t.Fatal(err)
	}
	if len(spy.writes) != 1 {
		t.Fatalf("a small frame took %d write calls, want 1", len(spy.writes))
	}
	frame := spy.writes[0]
	if got := binary.BigEndian.Uint32(frame[:4]); int(got) != len(body) || !bytes.Equal(frame[4:], body) {
		t.Fatalf("frame = %d %q", got, frame[4:])
	}
}

// A frame above the copy limit still reaches the peer intact, header first.
func TestLargeFrameArrivesIntact(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := &conn{nc: host}
	body := make([]byte, smallFrameBytes*3+17)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	errc := make(chan error, 1)
	go func() { errc <- c.writeFrame(body) }()
	got := make([]byte, 4+len(body))
	for read := 0; read < len(got); {
		n, err := peer.Read(got[read:])
		if err != nil {
			t.Fatal(err)
		}
		read += n
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if int(binary.BigEndian.Uint32(got[:4])) != len(body) || !bytes.Equal(got[4:], body) {
		t.Fatal("large frame corrupted")
	}
}
