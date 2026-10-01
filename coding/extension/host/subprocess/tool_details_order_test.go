package subprocess

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi hands an extension tool's `details` and each partial result to the agent as the JavaScript objects the tool wrote (agent-loop.ts:778-786, 912-919), and every later JSON.stringify of them keeps the member order. The host must not decode a details object into a Go map, which sorts its members, on the way from the extension process to the session, the wire and the other extensions.
func TestToolResultAndPartialResultsKeepMemberOrder(t *testing.T) {
	const details = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	hostEnd, peer := net.Pipe()
	conn := NewConn("order", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = peer.Close(); _ = conn.Close("test done") })
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	managed := withConn(&managedExt{config: ExtConfig{Name: "order"}, host: host}, conn)
	handler := host.makeToolExecuteFunc(managed, "order")
	var partials []agent.AgentToolResult
	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := handler(t.Context(), "call", json.RawMessage(`{}`), agent.ToolUpdateCallback(func(partial agent.AgentToolResult) { partials = append(partials, partial) }))
		done <- outcome{result, err}
	}()
	request := readLivenessEnvelope(t, peer)
	update, err := json.Marshal(ToolUpdatePayload{RequestID: request.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"a"},{"type":"image","data":"aW1n","mimeType":"image/png"},{"type":"text","text":"b"}],"details":` + details + `}`)})
	if err != nil {
		t.Fatal(err)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolUpdate, Args: update}})
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(`{"content":[{"type":"text","text":"done"}],"details":` + details + `}`)}})
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	result, ok := got.result.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("result = %#v", got.result)
	}
	if encoded, err := json.Marshal(result.Details); err != nil || string(encoded) != details {
		t.Errorf("result details = %s, %v, want %s", encoded, err, details)
	}
	if len(partials) != 1 {
		t.Fatalf("partials = %#v", partials)
	}
	if encoded, err := json.Marshal(partials[0].Details); err != nil || string(encoded) != details {
		t.Errorf("partial details = %s, %v, want %s", encoded, err, details)
	}
	// The blocks the tool wrote arrive whole and in order: Pi passes the object on untouched, so a later text block and an image survive.
	wantContent := []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, ai.ImageContent{Data: "aW1n", MimeType: "image/png"}, ai.TextContent{Text: "b"}}
	encoded, _ := json.Marshal(partials[0].Content)
	wantEncoded, _ := json.Marshal(wantContent)
	if string(encoded) != string(wantEncoded) {
		t.Errorf("partial content = %s, want %s", encoded, wantEncoded)
	}
}
