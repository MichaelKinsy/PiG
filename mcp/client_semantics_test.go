package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// failingCloseTransport is a transport whose Close fails after closing the underlying one.
type failingCloseTransport struct {
	*mcptest.InMemoryTransport
	failure error
}

func (t failingCloseTransport) Close() error {
	_ = t.InMemoryTransport.Close()
	return t.failure
}

func recordedByMethod(server *testServer, method string) []mcp.JSONRPCMessage {
	var found []mcp.JSONRPCMessage
	for _, m := range server.recorded() {
		if m.Method == method {
			found = append(found, m)
		}
	}
	return found
}

// packages/mcp/src/client.ts connect/close: connecting twice is an error naming the state; close disposes the client's transport listeners (a message the
// transport emits afterwards reaches nobody), returns the transport's close failure, and waits for the handlers of server requests that are running.
func TestClientConnectAndCloseSemantics(t *testing.T) {
	inner, server := createServer(t)
	failure := errors.New("transport close failed")
	var transport mcp.Transport = failingCloseTransport{inner, failure}
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}})
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Connect(t.Context(), transport); err == nil || err.Error() != "Cannot connect MCP client in connected state" {
		t.Fatalf("a second Connect = %v", err)
	}
	var heard atomic.Int32
	client.OnNotification("notifications/after", func(json.RawMessage) { heard.Add(1) })

	entered, release := make(chan struct{}), make(chan struct{})
	client.SetRequestHandler("slow/request", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-release
		return nil, nil
	})
	if err := server.transport.Send(context.Background(), mcp.NewRequest(mcp.StringID("slow"), "slow/request", nil)); err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while a server request was still running", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-closed; !errors.Is(err, failure) {
		t.Fatalf("Close = %v, want the transport's close failure", err)
	}
	inner.EmitMessage(mcp.NewNotification("notifications/after", nil))
	time.Sleep(50 * time.Millisecond)
	if heard.Load() != 0 {
		t.Fatal("a message emitted after Close still reached the client's listener")
	}
}

// client.ts list helpers: a cursor the server returns twice is "MCP <method> returned duplicate cursor: <cursor>", and a server that never ends its
// pages is stopped with "MCP <method> exceeded 1000 pages".
func TestClientListStopsOnADuplicateCursorAndOnTooManyPages(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	var calls atomic.Int32
	server.setHandler("tools/list", func(request mcp.JSONRPCMessage) (any, error) {
		cursor, _ := paramsOf(t, request)["cursor"].(string)
		switch cursor {
		case "":
			return map[string]any{"tools": []any{}, "nextCursor": "a"}, nil
		case "a":
			return map[string]any{"tools": []any{}, "nextCursor": "b"}, nil
		}
		return map[string]any{"tools": []any{}, "nextCursor": "a"}, nil
	})
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err == nil || err.Error() != "MCP tools/list returned duplicate cursor: a" {
		t.Fatalf("a cursor returned twice = %v", err)
	}
	server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"tools": []any{}, "nextCursor": "page-" + strconv.Itoa(int(calls.Add(1)))}, nil
	})
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err == nil || err.Error() != "MCP tools/list exceeded 1000 pages" {
		t.Fatalf("pages without end = %v", err)
	}
}

// client.ts callTool: `arguments` is left out when there are none, also for a null json.RawMessage.
func TestClientCallToolOmitsArgumentsForANilRawMessage(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) { return map[string]any{"content": []any{}}, nil })
	if _, err := client.CallTool(t.Context(), "t", json.RawMessage(nil), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := recordedByMethod(server, "tools/call")
	if len(calls) != 1 || string(calls[0].Params) != `{"name":"t"}` {
		t.Fatalf("tools/call params = %s, want only the name", calls[0].Params)
	}
}

// client.ts: an aborted request tells the server why: a cancelled signal is "AbortError: This operation was aborted", a deadline is "TimeoutError: The
// operation was aborted due to timeout", and a custom reason is its text.
func TestClientCancelledNotificationCarriesTheAbortReason(t *testing.T) {
	deadlineCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(t.Context(), 50*time.Millisecond)
	}
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want string
	}{
		{"cancelled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx, cancel
		}, "AbortError: This operation was aborted"},
		{"deadline", deadlineCtx, "TimeoutError: The operation was aborted due to timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := connect(t)
			defer func() { _ = client.Close() }()
			server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) { return blockUntilTestEnds(t) })
			ctx, cancel := tc.ctx()
			defer cancel()
			_, err := client.CallTool(ctx, "wait", map[string]any{}, mcp.RequestOptions{})
			if _, ok := errors.AsType[*mcp.McpAbortError](err); !ok {
				t.Fatalf("CallTool = %v, want an abort error", err)
			}
			waitFor(t, func() bool { return len(recordedByMethod(server, "notifications/cancelled")) > 0 })
			var params map[string]any
			_ = json.Unmarshal(recordedByMethod(server, "notifications/cancelled")[0].Params, &params)
			if params["reason"] != tc.want {
				t.Fatalf("cancelled reason = %v, want %q", params["reason"], tc.want)
			}
		})
	}
}

// client.ts handleMessage/handleResponse: a message that is no request, response or notification is reported as "Received invalid JSON-RPC message"
// with the InvalidRequest code, and a response for an unknown id as "Received response for unknown MCP request <id>"; neither fails the client.
func TestClientReportsInvalidMessagesAndUnknownResponses(t *testing.T) {
	inner, server := createServer(t)
	_ = server
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}})
	errs := make(chan error, 4)
	client.OnError(func(err error) { errs <- err })
	if _, err := client.Connect(t.Context(), inner); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	inner.EmitMessage(mcp.JSONRPCMessage{})
	var invalid *mcp.McpError
	if err := <-errs; !errors.As(err, &invalid) || invalid.Message != "Received invalid JSON-RPC message" || invalid.Code != mcp.JSONRPCErrorCodes.InvalidRequest {
		t.Fatalf("invalid message reported as %v", err)
	}
	inner.EmitMessage(mcp.NewResult(mcp.NumberID(9999), json.RawMessage(`{}`)))
	if err := <-errs; err == nil || err.Error() != "Received response for unknown MCP request 9999" {
		t.Fatalf("unknown response reported as %v", err)
	}
}

// client.ts request handlers: a handler's nil result answers {}, an McpError keeps its code and message, any other error answers InternalError with its text,
// a panicking handler answers InternalError with the panic text, and notifications/cancelled for the request ends the handler's context.
func TestClientAnswersServerRequestsLikePi(t *testing.T) {
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	client.SetRequestHandler("t/nil", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	client.SetRequestHandler("t/mcp", func(context.Context, json.RawMessage) (any, error) {
		return nil, &mcp.McpError{Code: -32602, Message: "bad params"}
	})
	client.SetRequestHandler("t/plain", func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("plain failure") })
	client.SetRequestHandler("t/panic", func(context.Context, json.RawMessage) (any, error) { panic("handler exploded") })
	cancelled := make(chan struct{})
	entered := make(chan struct{})
	client.SetRequestHandler("t/wait", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	for _, id := range []string{"t/nil", "t/mcp", "t/plain", "t/panic", "t/wait"} {
		if err := server.transport.Send(context.Background(), mcp.NewRequest(mcp.StringID(id), id, nil)); err != nil {
			t.Fatal(err)
		}
	}
	<-entered
	if err := server.transport.Send(context.Background(), mcp.NewNotification("notifications/cancelled", json.RawMessage(`{"requestId":"t/wait","reason":"stop"}`))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("notifications/cancelled did not end the handler's context")
	}
	answer := func(id string) mcp.JSONRPCMessage {
		var found mcp.JSONRPCMessage
		waitFor(t, func() bool {
			for _, m := range server.recorded() {
				if m.IsResponse() && m.ID.String() == id {
					found = m
					return true
				}
			}
			return false
		})
		return found
	}
	jsonEqual(t, answer("t/nil"), `{"jsonrpc":"2.0","id":"t/nil","result":{}}`)
	jsonEqual(t, answer("t/mcp"), `{"jsonrpc":"2.0","id":"t/mcp","error":{"code":-32602,"message":"bad params"}}`)
	jsonEqual(t, answer("t/plain"), `{"jsonrpc":"2.0","id":"t/plain","error":{"code":-32603,"message":"plain failure"}}`)
	jsonEqual(t, answer("t/panic"), `{"jsonrpc":"2.0","id":"t/panic","error":{"code":-32603,"message":"handler exploded"}}`)
}

// client.ts notification dispatch: a listener that throws is reported through onError (an Error as it is, any other value as its text) and the listeners
// after it still run; progress whose `progress` is not a number is ignored.
func TestClientIsolatesThrowingNotificationListenersAndIgnoresNonNumericProgress(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	errs := make(chan error, 4)
	client.OnError(func(err error) { errs <- err })
	var after atomic.Int32
	client.OnNotification("notifications/x", func(json.RawMessage) { panic(errors.New("listener error")) })
	client.OnNotification("notifications/x", func(json.RawMessage) { panic("listener text") })
	client.OnNotification("notifications/x", func(json.RawMessage) { after.Add(1) })
	if err := server.transport.Send(context.Background(), mcp.NewNotification("notifications/x", nil)); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err == nil || err.Error() != "listener error" {
		t.Fatalf("first listener failure reported as %v", err)
	}
	if err := <-errs; err == nil || err.Error() != "listener text" {
		t.Fatalf("second listener failure reported as %v", err)
	}
	waitFor(t, func() bool { return after.Load() == 1 })

	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		time.Sleep(300 * time.Millisecond)
		return map[string]any{"content": []any{}}, nil
	})
	var progressed atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := client.CallTool(t.Context(), "slow", nil, mcp.RequestOptions{OnProgress: func(mcp.ProgressNotification) { progressed.Add(1) }})
		done <- err
	}()
	waitFor(t, func() bool { return len(recordedByMethod(server, "tools/call")) > 0 })
	var request map[string]any
	_ = json.Unmarshal(recordedByMethod(server, "tools/call")[0].Params, &request)
	token := request["_meta"].(map[string]any)["progressToken"]
	notice, _ := json.Marshal(map[string]any{"progressToken": token, "progress": "half"})
	if err := server.transport.Send(context.Background(), mcp.NewNotification("notifications/progress", notice)); err != nil {
		t.Fatal(err)
	}
	// A null progress decodes into a Go float64 without error, but it is not a number (client.ts `typeof progress !== "number"`).
	null, _ := json.Marshal(map[string]any{"progressToken": token, "progress": nil})
	if err := server.transport.Send(context.Background(), mcp.NewNotification("notifications/progress", null)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if progressed.Load() != 0 {
		t.Fatal("progress with a non-numeric value reached the progress callback")
	}
}
