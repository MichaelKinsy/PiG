package subprocess

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A tool_call reply carries the input the handler left (`_pigToolCallInput`) beside its block result (`_pigToolCallResult`). The host writes the input into the event's own Input map, which the agent loop reads after the handlers ran, and answers the block result alone (runner.ts emitToolCall). The edit holds when the handler failed, a reply with no input leaves the event as it was, and an input that is not an object is not an input a tool accepts.
func TestToolCallReplyEditsTheEventInputInPlace(t *testing.T) {
	cases := []struct {
		name       string
		reply      string
		wantInput  map[string]any
		wantResult string
		wantErr    string
	}{
		{name: "edit and block", reply: `{"result":{"_pigToolCallInput":{"command":"git status --short","added":true},"_pigToolCallResult":{"block":true,"reason":"no"}}}`, wantInput: map[string]any{"command": "git status --short", "added": true}, wantResult: `{"block":true,"reason":"no"}`},
		{name: "edit only", reply: `{"result":{"_pigToolCallInput":{"command":"x"},"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "x"}},
		{name: "no edit", reply: `{"result":{"_pigToolCallResult":{"block":false}}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped"}, wantResult: `{"block":false}`},
		{name: "edit then failure", reply: `{"result":{"_pigToolCallInput":{"command":"y"},"_pigToolCallResult":null},"error":{"message":"handler failed"}}`, wantInput: map[string]any{"command": "y"}, wantErr: "handler failed"},
		{name: "input is not an object", reply: `{"result":{"_pigToolCallInput":null,"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped"}},
		{name: "array input", reply: `{"result":{"_pigToolCallInput":["a"],"_pigToolCallResult":null}}`, wantInput: map[string]any{"command": "git status", "drop": "dropped"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostEnd, peer := net.Pipe()
			conn := NewConn("observe", hostEnd)
			conn.Start(t.Context())
			managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
			handler := managed.makeEventHandler("tool_call", 1)
			input := map[string]any{"command": "git status", "drop": "dropped"}
			event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call"}, ToolName: "bash", Input: input}
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
