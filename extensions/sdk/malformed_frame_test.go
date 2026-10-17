package sdk

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// A frame that is not a JSON envelope ends the connection, as it does in the Node, Python and Rust SDKs (and in the host's own reader): skipping it would leave the host waiting for the answer to a request it carried until a liveness lease expires.
func TestMalformedFrameEndsTheConnection(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := newConn(host)
	c.start()
	body := []byte(`{"type":`)
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	go func() { _, _ = peer.Write(append(frame, body...)) }()
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection is still open after a malformed frame")
	}
	if _, open := <-c.incoming; open {
		t.Error("a malformed frame produced an envelope")
	}
}
