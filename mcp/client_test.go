package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// Ports packages/mcp/test/client.test.ts.

type testServer struct {
	transport *mcptest.InMemoryTransport
	mu        sync.Mutex
	messages  []mcp.JSONRPCMessage
	handlers  map[string]func(request mcp.JSONRPCMessage) (any, error)
}

func (s *testServer) setHandler(method string, handler func(request mcp.JSONRPCMessage) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

func (s *testServer) recorded() []mcp.JSONRPCMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mcp.JSONRPCMessage(nil), s.messages...)
}

// createServer is client.test.ts createServer: an in-memory server whose
// handlers answer on their own goroutine, as queueMicrotask does upstream.
func createServer(t *testing.T) (*mcptest.InMemoryTransport, *testServer) {
	t.Helper()
	client, serverTransport := mcptest.NewInMemoryTransportPair()
	server := &testServer{transport: serverTransport, handlers: map[string]func(mcp.JSONRPCMessage) (any, error){}}
	serverTransport.OnMessage(func(message mcp.JSONRPCMessage) {
		server.mu.Lock()
		server.messages = append(server.messages, message)
		handler := server.handlers[message.Method]
		server.mu.Unlock()
		if !message.IsRequest() {
			return
		}
		go func() {
			var result any
			var err error
			if handler == nil {
				err = &mcp.McpError{Code: -32601, Message: "Method not found: " + message.Method}
			} else {
				result, err = handler(message)
			}
			if err != nil {
				var mcpErr *mcp.McpError
				if !errors.As(err, &mcpErr) {
					mcpErr = &mcp.McpError{Code: -32603, Message: err.Error()}
				}
				_ = serverTransport.Send(mcp.NewErrorResponse(*message.ID, mcpErr.Code, mcpErr.Message, mcpErr.Data))
				return
			}
			raw, _ := json.Marshal(result)
			_ = serverTransport.Send(mcp.NewResult(*message.ID, raw))
		}()
	})
	if err := serverTransport.Start(); err != nil {
		t.Fatal(err)
	}
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{
			"protocolVersion": mcp.LatestProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "test-server", "version": "1.0.0"},
			"instructions":    "Use test tools.",
		}, nil
	})
	return client, server
}

func connect(t *testing.T) (*mcp.Client, *testServer) {
	t.Helper()
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	return client, server
}

func paramsOf(t *testing.T, request mcp.JSONRPCMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatal(err)
		}
	}
	return params
}

func TestClientInitializesTheConnectionBeforeExposingServerInformation(t *testing.T) {
	client, server := connect(t)
	if client.ConnectionState() != mcp.ClientStateConnected {
		t.Fatalf("state = %s", client.ConnectionState())
	}
	if client.ProtocolVersion() != mcp.LatestProtocolVersion {
		t.Fatalf("protocol version = %s", client.ProtocolVersion())
	}
	jsonEqual(t, client.ServerInfo(), `{"name":"test-server","version":"1.0.0"}`)
	jsonEqual(t, client.ServerCapabilities(), `{"tools":{"listChanged":true}}`)
	if got := client.Instructions(); got == nil || *got != "Use test tools." {
		t.Fatalf("instructions = %v", got)
	}
	jsonEqual(t, server.recorded(), `[
		{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+mcp.LatestProtocolVersion+`","capabilities":{},"clientInfo":{"name":"test-client","version":"2.0.0"}}},
		{"jsonrpc":"2.0","method":"notifications/initialized"}
	]`)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClientPaginatesToolsAndPreservesProtocolToolDefinitions(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/list", func(request mcp.JSONRPCMessage) (any, error) {
		if _, ok := paramsOf(t, request)["cursor"]; !ok {
			return map[string]any{
				"tools":      []any{map[string]any{"name": "search", "description": "Search", "inputSchema": map[string]any{"type": "object"}}},
				"nextCursor": "page-2",
			}, nil
		}
		return map[string]any{"tools": []any{map[string]any{
			"name": "read", "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"},
			"annotations": map[string]any{"readOnlyHint": true},
		}},
			// Some servers end pagination with an empty cursor instead of omitting it.
			"nextCursor": "",
		}, nil
	})
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, tools, `[
		{"name":"search","description":"Search","inputSchema":{"type":"object"}},
		{"name":"read","inputSchema":{"type":"object"},"outputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}
	]`)
	_ = client.Close()
}

func TestClientListsAndReadsResources(t *testing.T) {
	client, server := connect(t)
	ctx := t.Context()
	server.setHandler("resources/list", func(request mcp.JSONRPCMessage) (any, error) {
		if _, ok := paramsOf(t, request)["cursor"]; !ok {
			return map[string]any{"resources": []any{map[string]any{"uri": "file:///a", "name": "a", "mimeType": "text/plain"}}, "nextCursor": "2"}, nil
		}
		return map[string]any{"resources": []any{map[string]any{"uri": "file:///b"}}}, nil
	})
	server.setHandler("resources/templates/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "repo://{owner}/{repo}", "name": "repo"}}}, nil
	})
	server.setHandler("resources/read", func(request mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"contents": []any{map[string]any{"uri": paramsOf(t, request)["uri"], "text": "hello"}}}, nil
	})
	// A missing name falls back to the URI.
	resources, err := client.ListResources(ctx, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, resources, `[{"uri":"file:///a","name":"a","mimeType":"text/plain"},{"uri":"file:///b","name":"file:///b"}]`)
	templates, err := client.ListResourceTemplates(ctx, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, templates, `[{"uriTemplate":"repo://{owner}/{repo}","name":"repo"}]`)
	// Single pages pass the cursor through.
	page, err := client.ListResourcesPage(ctx, nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, page, `{"resources":[{"uri":"file:///a","name":"a","mimeType":"text/plain"}],"nextCursor":"2"}`)
	cursor := "2"
	page, err = client.ListResourcesPage(ctx, &cursor, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, page, `{"resources":[{"uri":"file:///b","name":"file:///b"}]}`)
	read, err := client.ReadResource(ctx, "file:///a", mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, read, `{"contents":[{"uri":"file:///a","text":"hello"}]}`)

	server.setHandler("resources/read", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"contents": []any{map[string]any{"uri": "file:///a"}}}, nil
	})
	if _, err := client.ReadResource(ctx, "file:///a", mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "Invalid contents in MCP resources/read result") {
		t.Fatalf("read err = %v", err)
	}
	server.setHandler("resources/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resources": []any{map[string]any{"name": "no uri"}}}, nil
	})
	if _, err := client.ListResources(ctx, mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "Invalid entry in MCP resources/list result") {
		t.Fatalf("list err = %v", err)
	}
	_ = client.Close()
}

func TestClientReturnsStructuredToolContentAndSurfacesJSONRPCErrors(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(request mcp.JSONRPCMessage) (any, error) {
		params := paramsOf(t, request)
		if params["name"] == "fail" {
			return nil, &mcp.McpError{Code: 1234, Message: "tool failed", Data: json.RawMessage(`{"retryable":false}`)}
		}
		arguments, _ := params["arguments"].(map[string]any)
		return map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": "ok"}},
			"structuredContent": map[string]any{"count": arguments["count"]},
		}, nil
	})
	result, err := client.CallTool(t.Context(), "count", map[string]any{"count": 3}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"ok"}],"structuredContent":{"count":3}}`)
	_, err = client.CallTool(t.Context(), "fail", nil, mcp.RequestOptions{})
	var mcpErr *mcp.McpError
	if !errors.As(err, &mcpErr) || mcpErr.Code != 1234 || mcpErr.Message != "tool failed" || string(mcpErr.Data) != `{"retryable":false}` {
		t.Fatalf("err = %#v", err)
	}
	_ = client.Close()
}

func TestClientRenewsTheTimeoutOnProgress(t *testing.T) {
	// vitest fake timers upstream; the virtual clock stands in for them.
	clock := &fakeClock{}
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	mcp.SetClientAfterFuncForTest(client, clock.AfterFunc)
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	server.setHandler("tools/call", func(request mcp.JSONRPCMessage) (any, error) {
		meta, _ := paramsOf(t, request)["_meta"].(map[string]any)
		token := meta["progressToken"]
		clock.AfterFunc(40*time.Millisecond, func() {
			raw, _ := json.Marshal(map[string]any{"progressToken": token, "progress": 1, "total": 2})
			_ = server.transport.Send(mcp.NewNotification("notifications/progress", raw))
		})
		clock.Sleep(80 * time.Millisecond)
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}}}, nil
	})
	progressSeen := make(chan mcp.ProgressNotification, 4)
	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan callResult, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		result, err := client.CallTool(t.Context(), "slow", map[string]any{}, mcp.RequestOptions{
			TimeoutMs: 50, OnProgress: func(p mcp.ProgressNotification) { progressSeen <- p },
		})
		done <- callResult{result, err}
	}()
	<-started
	// The request and the tool handler must be waiting on the clock before it moves.
	waitForTimers(t, clock, 3)
	clock.Advance(40 * time.Millisecond)
	// advanceTimersByTimeAsync flushes microtasks between timers; the notification
	// crosses goroutines here, so wait for it before advancing again.
	select {
	case progress := <-progressSeen:
		jsonEqual(t, progress, `{"progressToken":2,"progress":1,"total":2}`)
	case <-time.After(5 * time.Second):
		t.Fatal("no progress notification")
	}
	clock.Advance(40 * time.Millisecond)
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		jsonEqual(t, r.result, `{"content":[{"type":"text","text":"done"}]}`)
	case <-time.After(5 * time.Second):
		t.Fatal("call did not finish")
	}
	_ = client.Close()
}

func waitForTimers(t *testing.T, clock *fakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.pending() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d timers pending", clock.pending())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestClientCancelsAbortedAndTimedOutRequests(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		select {}
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, "wait", map[string]any{}, mcp.RequestOptions{})
		errs <- err
	}()
	// The abort must follow the request on the wire: wait until the server saw it.
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == "tools/call" {
				return true
			}
		}
		return false
	})
	cancel(errors.New("stop"))
	var abortErr *mcp.McpAbortError
	if err := <-errs; !errors.As(err, &abortErr) {
		t.Fatalf("aborted err = %v", err)
	}
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == "notifications/cancelled" {
				return true
			}
		}
		return false
	})
	var cancelled mcp.JSONRPCMessage
	for _, m := range server.recorded() {
		if m.Method == "notifications/cancelled" {
			cancelled = m
		}
	}
	jsonEqual(t, cancelled, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2,"reason":"stop"}}`)
	// Pi builds `{ requestId: id, ...(reason ? { reason } : {}) }` (client.ts), so requestId leads on the wire.
	if string(cancelled.Params) != `{"requestId":2,"reason":"stop"}` {
		t.Fatalf("cancelled params bytes = %s, want requestId before reason", cancelled.Params)
	}

	_, err := client.CallTool(t.Context(), "wait", map[string]any{}, mcp.RequestOptions{TimeoutMs: 5})
	if _, ok := errors.AsType[*mcp.McpTimeoutError](err); !ok {
		t.Fatalf("timeout err = %v", err)
	}
	_ = client.Close()
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

// Pi source: packages/mcp/src/transports/in-memory.ts
// mutation-checked: zeroing the results of InMemoryTransport.EmitError fails it
// Pi: packages/mcp/src/transports/in-memory.ts:35 (emitError)
// packages/mcp/src/transports/in-memory.ts:35-36: emitError(error) reports a transport error to the listeners.
func TestClientReportsTransportErrorsWithoutFailingPendingRequests(t *testing.T) {
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var errs []string
	client.OnError(func(err error) {
		mu.Lock()
		errs = append(errs, err.Error())
		mu.Unlock()
	})
	respond := make(chan struct{})
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		<-respond
		return map[string]any{"content": []any{}}, nil
	})
	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	call := make(chan callResult, 1)
	go func() {
		result, err := client.CallTool(t.Context(), "wait", nil, mcp.RequestOptions{})
		call <- callResult{result, err}
	}()
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == "tools/call" {
				return true
			}
		}
		return false
	})
	clientTransport.EmitError(errors.New("stray log line"))
	close(respond)
	r := <-call
	if r.err != nil {
		t.Fatal(r.err)
	}
	jsonEqual(t, r.result, `{"content":[]}`)
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || errs[0] != "stray log line" {
		t.Fatalf("errors = %v", errs)
	}
	_ = client.Close()
}

func TestClientAcceptsServersThatAnswerWithAnOlderProtocolVersion(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "old-server", "version": "0.1.0"}}, nil
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	if client.ProtocolVersion() != "2024-11-05" {
		t.Fatalf("version = %s", client.ProtocolVersion())
	}
	_ = client.Close()

	unsupportedTransport, unsupported := createServer(t)
	unsupported.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"protocolVersion": "1999-01-01", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "ancient-server", "version": "0.1.0"}}, nil
	})
	rejected := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"}})
	if _, err := rejected.Connect(t.Context(), unsupportedTransport); err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("err = %v", err)
	}
	if rejected.ConnectionState() != mcp.ClientStateClosed {
		t.Fatalf("state = %s", rejected.ConnectionState())
	}
}

func TestClientDefaultsMissingToolResultContentToAnEmptyList(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"structuredContent": map[string]any{"ok": true}}, nil
	})
	result, err := client.CallTool(t.Context(), "structured", nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[],"structuredContent":{"ok":true}}`)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"content": "not a list"}, nil
	})
	if _, err := client.CallTool(t.Context(), "broken", nil, mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "Invalid MCP tools/call result") {
		t.Fatalf("err = %v", err)
	}
	_ = client.Close()
}

func TestClientDoesNotSendCancelledForATimedOutInitialize(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) { select {} })
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"}, RequestTimeoutMs: 5})
	_, err := client.Connect(t.Context(), clientTransport)
	if _, ok := errors.AsType[*mcp.McpTimeoutError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	for _, m := range server.recorded() {
		if m.Method == "notifications/cancelled" {
			t.Fatal("cancelled sent for initialize")
		}
	}
}

func TestClientNotifiesCloseListenersOnceWhenTheTransportDrops(t *testing.T) {
	client, server := connect(t)
	var closed sync.WaitGroup
	closed.Add(1)
	var mu sync.Mutex
	calls := 0
	client.OnClose(func() {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) { select {} })
	errs := make(chan error, 1)
	go func() {
		_, err := client.CallTool(t.Context(), "wait", nil, mcp.RequestOptions{})
		errs <- err
	}()
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == "tools/call" {
				return true
			}
		}
		return false
	})
	_ = server.transport.Close()
	if err := <-errs; err == nil || err.Error() != "MCP connection closed" {
		t.Fatalf("err = %v", err)
	}
	waitFor(t, func() bool { return client.ConnectionState() == mcp.ClientStateClosed })
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("close listener calls = %d", calls)
	}
}

func TestClientAnswersRootsListAndDispatchesNotifications(t *testing.T) {
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{
		Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"},
		Roots:          []mcp.Root{{URI: "file:///workspace", Name: "workspace"}},
	})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	changed := make(chan json.RawMessage, 1)
	client.OnNotification("notifications/tools/list_changed", func(params json.RawMessage) { changed <- params })
	if err := server.transport.Send(mcp.NewRequest(mcp.StringID("roots"), "roots/list", nil)); err != nil {
		t.Fatal(err)
	}
	if err := server.transport.Send(mcp.NewNotification("notifications/tools/list_changed", nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case params := <-changed:
		if params != nil {
			t.Fatalf("params = %s, want none", params)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	var answer mcp.JSONRPCMessage
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.IsResponse() && m.ID.String() == "roots" {
				answer = m
				return true
			}
		}
		return false
	})
	jsonEqual(t, answer, `{"jsonrpc":"2.0","id":"roots","result":{"roots":[{"uri":"file:///workspace","name":"workspace"}]}}`)
	_ = client.Close()
}

// upstream: packages/mcp/src/client.ts:143-150: a tools/call result whose blocks hold members of another JSON type reaches the
// caller (callTool resolves); toLlmContent then renders the values (content.ts blockToLlmContent).
func TestClientCallToolResolvesResultsWithIllTypedBlockMembers(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{
			"content": []any{map[string]any{"type": "resource_link", "name": 5, "uri": true}, "stray", map[string]any{"type": "audio"}},
			"isError": "yes",
		}, nil
	})
	result, err := client.CallTool(t.Context(), "odd", nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatalf("callTool rejected a result upstream passes through: %v", err)
	}
	var texts []string
	for _, block := range mcp.ToLLMContent(*result) {
		texts = append(texts, block.Text)
	}
	want := []string{"5: true", "[unsupported MCP content undefined]", "[audio undefined omitted]"}
	if !slices.Equal(texts, want) {
		t.Fatalf("content = %q, want %q", texts, want)
	}
	if result.IsError == nil || !*result.IsError {
		t.Fatalf("isError = %v, want the truthiness of \"yes\"", result.IsError)
	}
	_ = client.Close()
}
