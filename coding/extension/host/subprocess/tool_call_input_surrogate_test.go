package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi's handler edits the one parsed object the model's arguments became, so a member it leaves alone keeps a lone UTF-16 unit the model wrote: the
// tool runs with `a\ud800` where encoding/json would hand it U+FFFD. The reply of an extension process carries the edited input as JSON text, and the
// host reads it back into the event's Input map and writes the canonical text beside it; both hold the unit, and the text is JSON.stringify's.
func TestToolCallReplyKeepsALoneSurrogateTheHandlerLeftAlone(t *testing.T) {
	lone := jsstring.FromUTF16([]uint16{0xd800})
	const wireBefore = `{"text":"a\ud800<&>","n":1}`
	const reply = `{"result":{"_pigToolCallInput":{"text":"a\ud800<&>","n":2},"_pigToolCallResult":null}}`
	// What Pi's tool runs with: the object the handler left, written by JSON.stringify.
	wantWire := piEval[string](t, `return JSON.stringify(JSON.parse(input).result._pigToolCallInput);`, reply)

	hostEnd, peer := net.Pipe()
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("tool_call", 1)
	input := map[string]any{"text": "a" + lone + "<&>", "n": 1.0}
	wire := json.RawMessage(wireBefore)
	event := extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call", WireInput: &wire, ToolName: "bash", Input: input}
	done := make(chan error, 1)
	go func() {
		_, err := handler(event, t.Context())
		done <- err
	}()
	request := readLivenessEnvelope(t, peer)
	var response ResponsePayload
	if err := json.Unmarshal([]byte(reply), &response); err != nil {
		t.Fatal(err)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &response})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = peer.Close()
	if want := (map[string]any{"text": "a" + lone + "<&>", "n": 2.0}); !reflect.DeepEqual(input, want) {
		t.Errorf("event input = %q (text units %x), want %q (units %x)", input, jsstring.ToUTF16(input["text"].(string)), want, jsstring.ToUTF16("a"+lone+"<&>"))
	}
	if string(wire) != wantWire {
		t.Errorf("event wire input = %s, want Pi's %s", wire, wantWire)
	}
}
