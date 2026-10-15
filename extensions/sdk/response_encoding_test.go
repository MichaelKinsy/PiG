package sdk

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

type slowResult struct {
	entered chan struct{}
	release chan struct{}
}

func (r slowResult) MarshalJSON() ([]byte, error) {
	close(r.entered)
	<-r.release
	return []byte(`{"slow":true}`), nil
}

// Encoding a result of hundreds of MiB takes seconds. The write lock also carries the heartbeat's pong, so respond encodes before it takes the lock: a
// host that gets no pong closes the connection.
func TestRespondEncodesTheResultOutsideTheWriteLock(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := newConn(host)
	result := slowResult{entered: make(chan struct{}), release: make(chan struct{})}
	errc := make(chan error, 1)
	go func() { errc <- c.respond("r1", result, nil) }()
	select {
	case <-result.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the result was never encoded")
	}
	locked := c.writeMu.TryLock()
	if locked {
		c.writeMu.Unlock()
	}
	close(result.release)
	if !locked {
		t.Fatal("the write lock was held while the result was being encoded")
	}
	if frame := readResponseTestFrame(t, peer); frame == "" {
		t.Fatal("no request_state frame")
	}
	if frame := readResponseTestFrame(t, peer); frame != `{"type":"response","id":"r1","response":{"result":{"slow":true}}}` {
		t.Fatalf("response frame = %s", frame)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func readResponseTestFrame(t *testing.T, peer net.Conn) string {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatalf("no frame was written: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatal(err)
	}
	return string(body)
}
