package mcp

import (
	"encoding/json"
	"math"
	"testing"
)

// upstream: protocol/jsonrpc.ts isJsonRpcRequest, isJsonRpcNotification and isJsonRpcResponse accept an unknown value. Each guard gives the same
// answer for the parsed message and for the decoded JSON object, and none accepts a value that is neither.
// Pi: packages/mcp/src/protocol/jsonrpc.ts:93 (isJsonRpcRequest)
// Pi: packages/mcp/src/protocol/jsonrpc.ts:99 (isJsonRpcNotification)
// Pi: packages/mcp/src/protocol/jsonrpc.ts:103 (isJsonRpcResponse)
func TestJSONRPCGuardsAnswerLikeUpstreamForUnknownValues(t *testing.T) {
	cases := []struct {
		name                            string
		wire                            string
		request, notification, response bool
	}{
		{"request", `{"jsonrpc":"2.0","id":1,"method":"m"}`, true, false, false},
		{"string id request", `{"jsonrpc":"2.0","id":"a","method":"m"}`, true, false, false},
		{"notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, false, true, false},
		{"notification with params", `{"jsonrpc":"2.0","method":"m","params":{"a":1}}`, false, true, false},
		{"null id is an id member", `{"jsonrpc":"2.0","id":null,"method":"m"}`, false, false, false},
		{"result response", `{"jsonrpc":"2.0","id":1,"result":{}}`, false, false, true},
		{"error response", `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"bad"}}`, false, false, true},
		{"error without a message", `{"jsonrpc":"2.0","id":1,"error":{"code":-32600}}`, false, false, false},
		{"result and error", `{"jsonrpc":"2.0","id":1,"result":{},"error":{"code":1,"message":"m"}}`, false, false, false},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"m"}`, false, false, false},
		{"no method", `{"jsonrpc":"2.0"}`, false, false, false},
		{"boolean id", `{"jsonrpc":"2.0","id":true,"method":"m"}`, false, false, false},
	}
	for _, tc := range cases {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(tc.wire), &decoded); err != nil {
			t.Fatal(err)
		}
		// A wire message the parser rejects is the zero message, which no guard accepts.
		parsed, _ := ParseJSONRPCMessage([]byte(tc.wire))
		for form, value := range map[string]any{"object": decoded, "message": parsed} {
			if got := IsJSONRPCRequest(value); got != tc.request {
				t.Errorf("%s (%s): IsJSONRPCRequest = %v, want %v", tc.name, form, got, tc.request)
			}
			if got := IsJSONRPCNotification(value); got != tc.notification {
				t.Errorf("%s (%s): IsJSONRPCNotification = %v, want %v", tc.name, form, got, tc.notification)
			}
			if got := IsJSONRPCResponse(value); got != tc.response {
				t.Errorf("%s (%s): IsJSONRPCResponse = %v, want %v", tc.name, form, got, tc.response)
			}
		}
	}
	// isJsonRpcId requires a finite number; JSON text cannot carry NaN or Infinity, so build the object directly.
	for _, id := range []float64{math.NaN(), math.Inf(1)} {
		object := map[string]any{"jsonrpc": "2.0", "id": id, "method": "m"}
		if IsJSONRPCRequest(object) || IsJSONRPCResponse(map[string]any{"jsonrpc": "2.0", "id": id, "result": 1}) {
			t.Errorf("id %v is not a JSON-RPC id", id)
		}
	}
	for _, other := range []any{nil, "x", 3.5, []any{}, &JSONRPCMessage{}} {
		if IsJSONRPCRequest(other) || IsJSONRPCNotification(other) || IsJSONRPCResponse(other) {
			t.Errorf("%T is not a JSON-RPC message", other)
		}
	}
}
