package mcp

import (
	"encoding/json"
	"errors"
	"testing"
)

// Ports packages/mcp/src/protocol/jsonrpc.ts:37-43 JSON_RPC_ERROR_CODES: the five standard JSON-RPC codes, read as members of one object.
func TestJSONRPCErrorCodesAreUpstreams(t *testing.T) {
	codes := JSONRPCErrorCodes
	for name, c := range map[string]struct{ got, want int }{
		"parseError":     {codes.ParseError, -32700},
		"invalidRequest": {codes.InvalidRequest, -32600},
		"methodNotFound": {codes.MethodNotFound, -32601},
		"invalidParams":  {codes.InvalidParams, -32602},
		"internalError":  {codes.InternalError, -32603},
	} {
		if c.got != c.want {
			t.Errorf("JSON_RPC_ERROR_CODES.%s = %d, want %d (jsonrpc.ts:37-43)", name, c.got, c.want)
		}
	}
}

// client.ts:82 (`throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid MCP initialize result")`) and jsonrpc.ts:112
// (`throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid JSON-RPC message")`): a malformed message raises the invalidRequest code.
func TestParseJSONRPCMessageRaisesInvalidRequest(t *testing.T) {
	_, err := ParseJSONRPCMessage([]byte(`{"jsonrpc":"2.0"}`))
	var mcpErr *McpError
	ok := errors.As(err, &mcpErr)
	if !ok || mcpErr.Code != JSONRPCErrorCodes.InvalidRequest || mcpErr.Error() != "Invalid JSON-RPC message" {
		t.Fatalf("got %#v, want McpError code %d \"Invalid JSON-RPC message\"", err, JSONRPCErrorCodes.InvalidRequest)
	}
}

// protocol/jsonrpc.ts:37-43 JSON_RPC_ERROR_CODES: parseError -32700, invalidRequest -32600, methodNotFound -32601, invalidParams -32602 and
// internalError -32603. The Go constants carry those JSON-RPC 2.0 codes on the wire: an error response built from each marshals the Pi value, and the
// message parser reports an invalid message with invalidRequest (jsonrpc.ts:112).
//
// mutation-checked: a changed constant value fails this test.
func TestJSONRPCErrorCodesMatchPi(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want int
	}{
		{"parseError", JSONRPCParseError, -32700},
		{"invalidRequest", JSONRPCInvalidRequest, -32600},
		{"methodNotFound", JSONRPCMethodNotFound, -32601},
		{"invalidParams", JSONRPCInvalidParams, -32602},
		{"internalError", JSONRPCInternalError, -32603},
	} {
		wire, err := json.Marshal(NewErrorResponse(NumberID(1), tc.code, "x", nil))
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Error struct{ Code int }
		}
		if err := json.Unmarshal(wire, &decoded); err != nil || decoded.Error.Code != tc.want {
			t.Errorf("%s: error response carries code %d (err %v), want %d", tc.name, decoded.Error.Code, err, tc.want)
		}
	}
	_, err := ParseJSONRPCMessage([]byte(`[]`))
	var mcpErr *McpError
	if !errors.As(err, &mcpErr) || mcpErr.Code != -32600 {
		t.Errorf("an invalid message is reported with code -32600, got %v", err)
	}
}
