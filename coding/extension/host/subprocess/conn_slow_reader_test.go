package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A healthy peer that reads a large frame slowly keeps its connection: bytes
// it accepts prove liveness, so no fixed write deadline kills it mid-frame.
func TestSlowReaderKeepsConnectionWhileDrainingLargeFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("drains one frame over several seconds of wall time")
	}
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("slow-reader", host)
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()

	const chunk = 32 * 1024
	const chunks = 24 // 768 KiB at one chunk per 250 ms: longer than any 5 s write deadline
	payload := strings.Repeat("x", chunk*chunks)
	sent := make(chan error, 1)
	go func() {
		sent <- conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "large", Args: json.RawMessage(`"` + payload + `"`)}})
	}()

	var length [4]byte
	if _, err := io.ReadFull(peer, length[:]); err != nil {
		t.Fatal(err)
	}
	remaining := int(binary.BigEndian.Uint32(length[:]))
	buf := make([]byte, chunk)
	for remaining > 0 {
		time.Sleep(250 * time.Millisecond)
		n, err := io.ReadFull(peer, buf[:min(chunk, remaining)])
		if err != nil {
			t.Fatalf("read large frame with %d bytes left: %v (connection failure: %v)", remaining, err, conn.failureError())
		}
		remaining -= n
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}

	// Answer the heartbeat pings queued behind the frame, then prove the
	// connection still carries frames.
	go func() {
		for {
			env, err := readEnvelopeFrom(peer)
			if err != nil {
				return
			}
			if env.Type == MsgPing && env.Ping != nil {
				_ = writeEnvelopeTo(peer, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}()
	if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "after"}}); err != nil {
		t.Fatal(err)
	}
	if failure := conn.failureError(); failure != nil {
		t.Fatalf("connection failed while a healthy peer drained a large frame: %v", failure)
	}
}

func readEnvelopeFrom(r io.Reader) (*Envelope, error) {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return nil, err
	}
	data := make([]byte, binary.BigEndian.Uint32(length[:]))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	var env Envelope
	return &env, json.Unmarshal(data, &env)
}

func writeEnvelopeTo(w io.Writer, env *Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
