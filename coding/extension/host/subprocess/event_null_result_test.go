package subprocess

import (
	"encoding/json"
	"net"
	"testing"
)

// A Node handler that returns undefined, and a Python handler that returns
// None, answer with {"result":null}. Upstream treats undefined as no result,
// so the host event handler must return nil rather than the JSON literal.
func TestEventHandlerNullResultIsNoResult(t *testing.T) {
	for _, raw := range []string{"null", " null ", ""} {
		hostEnd, peer := net.Pipe()
		conn := NewConn("observe", hostEnd)
		conn.Start(t.Context())
		managed := &managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir()), conn: conn}
		handler := managed.makeEventHandler("tool_call", 1)
		type outcome struct {
			result any
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := handler(map[string]any{"type": "tool_call"}, t.Context())
			done <- outcome{result, err}
		}()
		request := readLivenessEnvelope(t, peer)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(raw)}})
		got := <-done
		_ = peer.Close()
		if got.err != nil || got.result != nil {
			t.Fatalf("result %q: handler returned %#v, %v; want nil, nil", raw, got.result, got.err)
		}
	}
}
