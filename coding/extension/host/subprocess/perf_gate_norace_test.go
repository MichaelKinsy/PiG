//go:build !race && linux

package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// perfResponder answers every request frame it reads with an empty response for the same id. It reuses its buffers, so after warm-up it allocates nothing and the measured allocations are the host's.
func perfResponder(conn net.Conn) {
	var length [4]byte
	frame := make([]byte, 0, 4096)
	reply := make([]byte, 0, 256)
	idKey := []byte(`"id":"`)
	for {
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint32(length[:]))
		if cap(frame) < n {
			frame = make([]byte, n)
		}
		frame = frame[:n]
		if _, err := io.ReadFull(conn, frame); err != nil {
			return
		}
		start := bytes.Index(frame, idKey)
		if start < 0 {
			continue
		}
		start += len(idKey)
		end := bytes.IndexByte(frame[start:], '"')
		reply = append(reply[:4], `{"type":"response","id":"`...)
		reply = append(reply, frame[start:start+end]...)
		reply = append(reply, `","response":{}}`...)
		binary.BigEndian.PutUint32(reply[:4], uint32(len(reply)-4))
		if _, err := conn.Write(reply); err != nil {
			return
		}
	}
}

// One agent event dispatched to an extension (the request frame encoded and written, the response frame read, decoded and delivered to the waiting dispatcher) costs a constant allocation. The fake extension answers without allocating, so the counts are the host side of the crossing.
func TestExtensionEventRoundTripAllocationGate(t *testing.T) {
	hostSide, extSide := net.Pipe()
	conn := NewConn("perf-gate", hostSide)
	conn.Start(t.Context())
	go perfResponder(extSide)
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = extSide.Close()
	})
	args := json.RawMessage(`{"type":"message_update","message":{"role":"assistant","content":[{"type":"text","text":"Hello there, this is a streamed assistant message."}],"api":"openai-responses","provider":"openai","model":"gpt-5","stopReason":"stop","timestamp":1760000000000},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"message."}}`)
	ctx := context.Background()
	run := func(events int) {
		for range events {
			if _, err := conn.request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "message_update", Args: args}}, 0, "event"); err != nil {
				t.Fatal(err)
			}
		}
	}
	marginal := perfgate.CheckLinear(t, "extension.event_roundtrip", 500, 1.5, run)
	perfgate.Check(t, "extension.event_roundtrip.allocs", marginal.Allocs)
	perfgate.Check(t, "extension.event_roundtrip.bytes", marginal.Bytes)
}
