package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/eventwire"
)

// A handler that assigns a new object to event.input leaves it in the one event the next handlers receive, and the tool runs with the args object the event carried (runner.ts emitToolCall,
// agent-loop.ts prepareToolCall). The reply names it apart from an edit (`_pigToolCallEventInput`): the host gives it to the handlers that follow, applies their edits and assignments to it,
// and never writes it into the call's own input.
func TestToolCallAssignedInputReachesOnlyTheNextHandlers(t *testing.T) {
	const wireBefore = `{"timeout":5,"command":"git status"}`
	hostEnd, peer := net.Pipe()
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	t.Cleanup(func() { _ = peer.Close() })
	ctx, _ := eventwire.WithDispatch(t.Context())
	input := map[string]any{"command": "git status", "timeout": 5.0}
	wire := json.RawMessage(wireBefore)
	event := extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call", WireInput: &wire, ToolName: "bash", Input: input}

	// call runs one handler of the dispatch against the reply and returns the event it was sent.
	call := func(id int, reply string) string {
		t.Helper()
		handler := managed.makeEventHandler("tool_call", id)
		done := make(chan error, 1)
		go func() {
			_, err := handler(event, ctx)
			done <- err
		}()
		request := readLivenessEnvelope(t, peer)
		var payload ResponsePayload
		if err := json.Unmarshal([]byte(reply), &payload); err != nil {
			t.Fatal(err)
		}
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &payload})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		return string(request.Request.Args)
	}
	sent := func(inputJSON string) string {
		return `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":` + inputJSON + `}`
	}

	if got, want := call(1, `{"result":{"_pigToolCallEventInput":{"command":"assigned","n":1},"_pigToolCallResult":null}}`), sent(wireBefore); got != want {
		t.Errorf("first handler was sent %s, want %s", got, want)
	}
	// The second handler reads the assigned object and edits it in place; the edit applies to that object, as it does in Pi.
	if got, want := call(2, `{"result":{"_pigToolCallInput":{"command":"assigned","n":2},"_pigToolCallResult":null}}`), sent(`{"command":"assigned","n":1}`); got != want {
		t.Errorf("second handler was sent %s, want %s", got, want)
	}
	if got, want := call(3, `{"result":{"_pigToolCallResult":null}}`), sent(`{"command":"assigned","n":2}`); got != want {
		t.Errorf("third handler was sent %s, want %s", got, want)
	}
	if want := map[string]any{"command": "git status", "timeout": 5.0}; !reflect.DeepEqual(input, want) {
		t.Errorf("the call's input = %v, want %v: an assigned input must not reach the tool", input, want)
	}
	if string(wire) != wireBefore {
		t.Errorf("the call's wire input = %s, want %s", wire, wireBefore)
	}
}
