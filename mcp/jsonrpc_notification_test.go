package mcp

import (
	"testing"
)

// upstream: protocol/jsonrpc.ts isJsonRpcNotification: jsonrpc "2.0", a string method and no id member (an id of null still counts as an id member).
func TestIsJSONRPCNotification(t *testing.T) {
	cases := []struct {
		name string
		wire string
		want bool
	}{
		{"notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, true},
		{"with params", `{"jsonrpc":"2.0","method":"m","params":{"a":1}}`, true},
		{"request has an id", `{"jsonrpc":"2.0","id":1,"method":"m"}`, false},
		{"response", `{"jsonrpc":"2.0","id":1,"result":{}}`, false},
		{"wrong version", `{"jsonrpc":"1.0","method":"m"}`, false},
		{"no method", `{"jsonrpc":"2.0"}`, false},
	}
	for _, tc := range cases {
		// A message that fails to parse is no notification: the parser returns the zero message.
		message, _ := ParseJSONRPCMessage([]byte(tc.wire))
		if got := IsJSONRPCNotification(message); got != tc.want {
			t.Errorf("%s: IsJSONRPCNotification = %v, want %v", tc.name, got, tc.want)
		}
	}
}
