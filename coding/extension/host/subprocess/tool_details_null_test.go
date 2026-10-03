package subprocess

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// A subprocess tool that returns details: null hands the agent a result that holds details: null (agent-loop.ts
// createToolResultMessage keeps it), and a renderResult call receives it as null, as Pi's tool-execution component
// keeps the result it was given. A tool that returns no details holds none.
func TestToolResponseKeepsExplicitNullDetails(t *testing.T) {
	for _, tc := range []struct {
		name, raw, render string
		null              bool
	}{
		{"explicit null", `{"content":[],"details":null}`, `{"content":[],"details":null}`, true},
		{"absent", `{"content":[]}`, `{"content":[]}`, false},
		{"value", `{"content":[],"details":{"b":1,"a":2}}`, `{"content":[],"details":{"b":1,"a":2}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostEnd, peer := net.Pipe()
			conn := NewConn("details", hostEnd)
			conn.Start(t.Context())
			t.Cleanup(func() { _ = peer.Close(); _ = conn.Close("test done") })
			host := NewHost(t.TempDir())
			defer host.Shutdown("test done")
			managed := withConn(&managedExt{config: ExtConfig{Name: "details"}, host: host}, conn)
			handler := host.makeToolExecuteFunc(managed, "details")
			type outcome struct {
				result any
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := handler(t.Context(), "call", json.RawMessage(`{}`), nil)
				done <- outcome{result, err}
			}()
			request := readLivenessEnvelope(t, peer)
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(tc.raw)}})
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			result, ok := got.result.(agent.AgentToolResult)
			if !ok {
				t.Fatalf("result %T", got.result)
			}
			if result.DetailsNull() != tc.null {
				t.Fatalf("DetailsNull %t, want %t (%#v)", result.DetailsNull(), tc.null, result)
			}
			data, err := json.Marshal(renderToolResultPayload(result))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.render {
				t.Fatalf("render payload %s, want %s", data, tc.render)
			}
		})
	}
}
