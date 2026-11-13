package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
)

// A handler's reply reaches the Runner as the raw JSON of the response, in the type the wire carries it: the Runner decodes a result by its type (a boundary result, a context result, a project_trust decision), and a plain byte slice is none of them. Cloning the reply for the handler must keep that type.
func TestEventHandlerResultKeepsTheRawJSONType(t *testing.T) {
	const raw = `{"action":"handled"}`
	hostEnd, peer := net.Pipe()
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("input", 1)
	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := handler(map[string]any{"type": "input"}, t.Context())
		done <- outcome{result, err}
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(raw)}})
	got := <-done
	_ = peer.Close()
	if got.err != nil {
		t.Fatal(got.err)
	}
	if want := reflect.TypeOf(ResponsePayload{}.Result); reflect.TypeOf(got.result) != want {
		t.Fatalf("the handler returned %T, want %v", got.result, want)
	}
	if string(got.result.(json.RawMessage)) != raw {
		t.Fatalf("the handler returned %s, want %s", got.result, raw)
	}
}
