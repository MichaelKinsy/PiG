package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// PiG-only: packages/mcp/test/client.test.ts drives the client through listTools, callTool and friends but never calls
// request, notify, ping, setRequestHandler, listResourceTemplatesPage or reads options directly.
func lastRecorded(t *testing.T, server *testServer, method string) mcp.JSONRPCMessage {
	t.Helper()
	var found mcp.JSONRPCMessage
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == method {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func TestClientRequestSendsTheMethodAndParamsAndReturnsTheRawResult(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	server.setHandler("custom/echo", func(request mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"echo": paramsOf(t, request)["value"]}, nil
	})
	raw, err := client.Request(t.Context(), "custom/echo", map[string]any{"value": 7}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, json.RawMessage(raw), `{"echo":7}`)
	server.setHandler("custom/fail", func(mcp.JSONRPCMessage) (any, error) {
		return nil, &mcp.McpError{Code: -32000, Message: "nope"}
	})
	_, err = client.Request(t.Context(), "custom/fail", nil, mcp.RequestOptions{})
	var rpc *mcp.McpError
	if !errors.As(err, &rpc) || rpc.Code != -32000 || rpc.Message != "nope" {
		t.Fatalf("err = %v, want the server's McpError", err)
	}
}

func TestClientPingSendsAPingRequestAndSurfacesAServerError(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	server.setHandler("ping", func(mcp.JSONRPCMessage) (any, error) { return map[string]any{}, nil })
	if err := client.Ping(t.Context(), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if m := lastRecorded(t, server, "ping"); !m.IsRequest() {
		t.Fatalf("ping = %+v, want a request", m)
	}
	server.setHandler("ping", func(mcp.JSONRPCMessage) (any, error) { return nil, &mcp.McpError{Code: -32603, Message: "down"} })
	if err := client.Ping(t.Context(), mcp.RequestOptions{}); err == nil {
		t.Fatal("ping must fail when the server answers with an error")
	}
}

func TestClientNotifySendsANotificationWithoutAnIDAndFailsWhenNotConnected(t *testing.T) {
	client, server := connect(t)
	if err := client.Notify("custom/event", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	m := lastRecorded(t, server, "custom/event")
	if m.IsRequest() || m.ID != nil {
		t.Fatalf("notification = %+v, want no id", m)
	}
	jsonEqual(t, paramsOf(t, m), `{"n":1}`)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Notify("custom/late", nil); err == nil {
		t.Fatal("notify after close must fail")
	}
}

func TestClientSetRequestHandlerAnswersServerRequestsUntilDisposed(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	var seen json.RawMessage
	dispose := client.SetRequestHandler("custom/ask", func(_ context.Context, params json.RawMessage) (any, error) {
		seen = params
		return map[string]any{"answer": 42}, nil
	})
	answer := func(id string) mcp.JSONRPCMessage {
		if err := server.transport.Send(mcp.NewRequest(mcp.StringID(id), "custom/ask", json.RawMessage(`{"q":"x"}`))); err != nil {
			t.Fatal(err)
		}
		var response mcp.JSONRPCMessage
		waitFor(t, func() bool {
			for _, m := range server.recorded() {
				if m.IsResponse() && m.ID.String() == id {
					response = m
					return true
				}
			}
			return false
		})
		return response
	}
	jsonEqual(t, answer("one"), `{"jsonrpc":"2.0","id":"one","result":{"answer":42}}`)
	jsonEqual(t, json.RawMessage(seen), `{"q":"x"}`)
	dispose()
	jsonEqual(t, answer("two"), `{"jsonrpc":"2.0","id":"two","error":{"code":-32601,"message":"Method not found: custom/ask"}}`)
}

func TestClientSetRequestHandlerDisposeKeepsAHandlerThatReplacedIt(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	first := client.SetRequestHandler("custom/ask", func(context.Context, json.RawMessage) (any, error) { return "first", nil })
	client.SetRequestHandler("custom/ask", func(context.Context, json.RawMessage) (any, error) { return "second", nil })
	first()
	if err := server.transport.Send(mcp.NewRequest(mcp.StringID("r"), "custom/ask", nil)); err != nil {
		t.Fatal(err)
	}
	var response mcp.JSONRPCMessage
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.IsResponse() && m.ID.String() == "r" {
				response = m
				return true
			}
		}
		return false
	})
	jsonEqual(t, response, `{"jsonrpc":"2.0","id":"r","result":"second"}`)
}

func TestClientListResourceTemplatesPageSendsTheCursorAndReturnsOnePage(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	server.setHandler("resources/templates/list", func(request mcp.JSONRPCMessage) (any, error) {
		if paramsOf(t, request)["cursor"] != "c1" {
			return map[string]any{"resourceTemplates": []any{}}, nil
		}
		return map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "file:///{path}", "name": "files"}}, "nextCursor": "c2"}, nil
	})
	cursor := "c1"
	page, err := client.ListResourceTemplatesPage(t.Context(), &cursor, mcp.RequestOptions{})
	if err != nil || len(page.ResourceTemplates) != 1 || page.ResourceTemplates[0].Name != "files" || page.NextCursor != "c2" {
		t.Fatalf("page = %+v, %v", page, err)
	}
}

// Pi source: packages/mcp/src/client.ts
// mutation-checked: zeroing the results of Client.Options fails it
// Pi: packages/mcp/src/client.ts:154 (options)
// packages/mcp/src/client.ts:154 `readonly options: Readonly<McpClientOptions>`, set from the constructor options at client.ts:172.
// packages/mcp/src/client.ts:154: McpClient.options is the frozen configuration it was built with.
func TestClientOptionsReturnsTheConfiguration(t *testing.T) {
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "n", Version: "v"}, ProtocolVersion: "2025-06-18", RequestTimeoutMs: 123})
	got := client.Options()
	if got.Name != "n" || got.Version != "v" || got.ProtocolVersion != "2025-06-18" || got.RequestTimeoutMs != 123 {
		t.Fatalf("options = %+v", got)
	}
}

// Pi's transports keep the options they were built with (`readonly options`); the host reads them back.
// mutation-checked: zeroing the results of StdioTransport.Options, StreamableHTTPTransport.Options fails it
// Pi: packages/mcp/src/transports/stdio.ts:70 (options)
// Pi: packages/mcp/src/transports/streamable-http.ts:35 (options)
// Pi's transports keep the options they were built with (`readonly options`): packages/mcp/src/transports/stdio.ts:70 and
// packages/mcp/src/transports/streamable-http.ts:190; the host reads them back.
// packages/mcp/src/transports/stdio.ts:70,79: StdioTransport.options is the frozen construction options and packages/mcp/src/transports/streamable-http.ts:190 for the HTTP transport.
func TestTransportsExposeTheirConstructionOptions(t *testing.T) {
	stdio := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: "server", Args: []string{"--x"}, Cwd: "/work", CloseTimeoutMs: 7})
	if got := stdio.Options(); got.Command != "server" || len(got.Args) != 1 || got.Args[0] != "--x" || got.Cwd != "/work" || got.CloseTimeoutMs != 7 {
		t.Fatalf("stdio options = %+v", got)
	}
	http, err := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPTransportOptions{URL: "https://mcp.example/rpc", Headers: map[string]string{"X-A": "1"}, MaxMessageBytes: 99})
	if err != nil {
		t.Fatal(err)
	}
	if got := http.Options(); got.URL != "https://mcp.example/rpc" || got.Headers["X-A"] != "1" || got.MaxMessageBytes != 99 {
		t.Fatalf("http options = %+v", got)
	}
}
