package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:382 callTool sends `arguments` only when the caller passed some: an absent argument (undefined) omits the member. A nil json.RawMessage
// is the Go spelling of an absent raw argument, so it omits the member like a nil args; a non-nil one is sent as given.
// Pi: packages/mcp/src/client.ts:382 (callTool params: `...(args === undefined ? {} : { arguments: args })`)
func TestClientCallToolOmitsArgumentsForAnAbsentRawArgument(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) { return map[string]any{"content": []any{}}, nil })
	for _, args := range []any{nil, json.RawMessage(nil), json.RawMessage(`{"k":1}`)} {
		if _, err := client.CallTool(t.Context(), "t", args, mcp.RequestOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	var sent []string
	for _, m := range server.recorded() {
		if m.Method == "tools/call" {
			sent = append(sent, string(m.Params))
		}
	}
	want := []string{`{"name":"t"}`, `{"name":"t"}`, `{"name":"t","arguments":{"k":1}}`}
	if len(sent) != len(want) {
		t.Fatalf("tools/call params = %v, want %v", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("call %d params = %s, want %s", i, sent[i], want[i])
		}
	}
}
