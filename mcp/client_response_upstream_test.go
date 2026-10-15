package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// upstream: packages/mcp/src/client.ts:476-484 handleResponse(JsonRpcResponse) resolves the pending request with a success response's result and
// rejects it with McpError(code, message, data) for an error response.
func TestClientHandleResponseResolvesResultAndRejectsErrorResponse(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("custom/ok", func(mcp.JSONRPCMessage) (any, error) { return map[string]any{"answer": 42}, nil })
	server.setHandler("custom/fail", func(mcp.JSONRPCMessage) (any, error) {
		return nil, &mcp.McpError{Code: -32000, Message: "boom", Data: json.RawMessage(`{"why":"because"}`)}
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.Request(t.Context(), "custom/ok", map[string]any{}, mcp.RequestOptions{})
	if err != nil || string(result) != `{"answer":42}` {
		t.Fatalf("success response: %s, %v", result, err)
	}
	_, err = client.Request(t.Context(), "custom/fail", map[string]any{}, mcp.RequestOptions{})
	var mcpErr *mcp.McpError
	if !errors.As(err, &mcpErr) || mcpErr.Code != -32000 || mcpErr.Message != "boom" || string(mcpErr.Data) != `{"why":"because"}` {
		t.Fatalf("error response: %#v (%v)", mcpErr, err)
	}
}

// upstream: packages/mcp/src/protocol/jsonrpc.ts:22-34,103-108 a JsonRpcResponse is a JsonRpcSuccessResponse (id and result) or a JsonRpcErrorResponse
// (id and an error object with a numeric code and a string message), never both and never without an id.
func TestJSONRPCMessageAsResponseIsTheSuccessOrErrorResponseShape(t *testing.T) {
	parse := func(wire string) mcp.JSONRPCMessage {
		t.Helper()
		message, err := mcp.ParseJSONRPCMessage([]byte(wire))
		if err != nil {
			t.Fatalf("%s: %v", wire, err)
		}
		return message
	}
	response, ok := parse(`{"jsonrpc":"2.0","id":7,"result":null}`).AsResponse()
	success, isSuccess := response.(mcp.JSONRPCSuccessResponse)
	if !ok || !isSuccess || string(success.Result) != "null" || success.ID.String() != "7" || success.JSONRPC != "2.0" || response.ResponseID().String() != "7" {
		t.Fatalf("success response = %+v, ok=%v", response, ok)
	}
	response, ok = parse(`{"jsonrpc":"2.0","id":"a","error":{"code":-32601,"message":"nope","data":{"x":1}}}`).AsResponse()
	failure, isError := response.(mcp.JSONRPCErrorResponse)
	if !ok || !isError || failure.Error.Code != -32601 || failure.Error.Message != "nope" || string(failure.Error.Data) != `{"x":1}` {
		t.Fatalf("error response = %+v, ok=%v", response, ok)
	}
	for _, wire := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/x"}`,
	} {
		if _, ok := parse(wire).AsResponse(); ok {
			t.Errorf("%s is not a response", wire)
		}
	}
}
