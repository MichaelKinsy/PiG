package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A tool_call reply carries the input the handler left (`_pigToolCallInput`) beside its block result (`_pigToolCallResult`). The host writes the input into the event's own Input map, which the agent loop reads after the handlers ran, and answers the block result alone (runner.ts emitToolCall). The edit holds when the handler failed, a reply with no input leaves the event as it was, and an input that is not an object is not an input a tool accepts. The reply has exactly these two members: any other shape is an error, which blocks the call, so a block result cannot be lost.
func TestToolCallReplyEditsTheEventInputInPlace(t *testing.T) {
	const wireBefore = `{"timeout":5,"drop":"dropped","command":"git status"}`
	cases := []struct {
		name       string
		reply      string
		wantInput  map[string]any
		wantWire   string // the bytes the event's WireInput holds afterwards: the handler's members in its order
		wantResult string
		wantErr    string
	}{
		{name: "edit and block", reply: `{"result":{"_pigToolCallInput":{"command":"git status --short","added":true},"_pigToolCallResult":{"block":true,"reason":"no"}}}`, wantInput: map[string]any{"command": "git status --short", "added": true}, wantWire: `{"command":"git status --short","added":true}`, wantResult: `{"block":true,"reason":"no"}`},
		{name: "edit only", reply: `{"result":{"_pigToolCallInput":{"command":"x"},"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "x"}, wantWire: `{"command":"x"}`},
		{name: "no edit", reply: `{"result":{"_pigToolCallResult":{"block":false}}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: wireBefore, wantResult: `{"block":false}`},
		{name: "edit then failure", reply: `{"result":{"_pigToolCallInput":{"command":"y"},"_pigToolCallResult":null},"error":{"message":"handler failed"}}`, wantInput: map[string]any{"command": "y"}, wantWire: `{"command":"y"}`, wantErr: "handler failed"},
		{name: "input is not an object", reply: `{"result":{"_pigToolCallInput":null,"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: wireBefore},
		{name: "array input", reply: `{"result":{"_pigToolCallInput":["a"],"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: wireBefore},
		// Pi's handler leaves the object it edited, in its member order (JSON.stringify): command kept its place, aaa followed. Python writes `1.0` and spaces, and the host keeps the JSON.stringify form.
		{name: "insertion order", reply: `{"result":{"_pigToolCallInput":{"timeout":5,"command":"echo rewritten","aaa":1.0},"_pigToolCallResult":null}}`, wantInput: map[string]any{"timeout": 5.0, "command": "echo rewritten", "aaa": 1.0}, wantWire: `{"timeout":5,"command":"echo rewritten","aaa":1}`},
		{name: "reorder only", reply: `{"result":{"_pigToolCallInput":{"drop":"dropped","command":"git status","timeout":5},"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: `{"drop":"dropped","command":"git status","timeout":5}`},
		// A Node handler that returns undefined and leaves the input sends neither member.
		{name: "empty reply", reply: `{"result":{}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: wireBefore},
		// A reply in another shape, such as the bare block result of an extension built against an older SDK, fails the call instead of losing its block.
		{name: "reply without the envelope", reply: `{"result":{"block":true,"reason":"legacy"}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}, wantWire: wireBefore, wantErr: `decode tool_call response: json: unknown field "block": "observe" ships a prebuilt binary; if you updated pig, rebuild it against the current pig SDK`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostEnd, peer := net.Pipe()
			conn := NewConn("observe", hostEnd)
			conn.Start(t.Context())
			managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
			handler := managed.makeEventHandler("tool_call", 1)
			input := map[string]any{"command": "git status", "drop": "dropped", "timeout": 5.0}
			wire := json.RawMessage(wireBefore)
			event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call", WireInput: &wire}, ToolName: "bash", Input: input}
			type outcome struct {
				result any
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := handler(event, t.Context())
				done <- outcome{result, err}
			}()
			request := readLivenessEnvelope(t, peer)
			// The extension receives the event as Pi writes it: members in Pi's order, input in the model's order.
			if want := `{"type":"tool_call","toolName":"bash","toolCallId":"call","input":` + wireBefore + `}`; string(request.Request.Args) != want {
				t.Errorf("event sent = %s, want %s", request.Request.Args, want)
			}
			var reply ResponsePayload
			if err := json.Unmarshal([]byte(tc.reply), &reply); err != nil {
				t.Fatal(err)
			}
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &reply})
			got := <-done
			_ = peer.Close()
			if !reflect.DeepEqual(input, tc.wantInput) {
				t.Errorf("event input = %v, want %v", input, tc.wantInput)
			}
			if string(wire) != tc.wantWire {
				t.Errorf("event wire input = %s, want %s", wire, tc.wantWire)
			}
			if (got.err == nil) != (tc.wantErr == "") || (got.err != nil && got.err.Error() != tc.wantErr) {
				t.Errorf("error = %v, want %q", got.err, tc.wantErr)
			}
			if tc.wantResult == "" {
				if got.result != nil {
					t.Errorf("result = %#v, want none", got.result)
				}
				return
			}
			if raw, ok := got.result.(json.RawMessage); !ok || string(raw) != tc.wantResult {
				t.Errorf("result = %#v, want %s", got.result, tc.wantResult)
			}
		})
	}
}
