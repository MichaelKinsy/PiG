package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
)

type heldResponseConn struct {
	net.Conn
	written chan struct{}
	release chan struct{}
}

func (c *heldResponseConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if bytes.Contains(data, []byte(`"type":"response"`)) {
		close(c.written)
		<-c.release
	}
	return n, err
}

func TestNormalCompletionPublishedBeforeResponseIsVisible(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	transport := &heldResponseConn{Conn: client, written: make(chan struct{}), release: make(chan struct{})}
	connection, host := newConn(transport), newConn(server)
	connection.start()
	host.start()
	parent := connection.armParent("origin", t.Context(), t.Context())
	done := make(chan error, 1)
	go func() { done <- connection.respond("origin", nil, nil) }()
	<-transport.written
	_, completed := parent.lifetime()
	if !completed {
		t.Error("response became visible before normal completion")
	}
	close(transport.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func retainedContextHarness(t *testing.T) (*Extension, *conn, *conn, Context, func()) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	connection, host := newConn(client), newConn(server)
	connection.start()
	host.start()
	ext := New("parent-lifetime")
	ext.conn = connection
	ctx, finish := ext.armRequest("origin")
	return ext, connection, host, ctx, finish
}

func nextLifetimeCall(t *testing.T, host *conn) envelope {
	t.Helper()
	for env := range host.incoming {
		if env.Type == msgCall {
			return env
		}
	}
	t.Fatal("connection closed before host call")
	return envelope{}
}

func TestActiveContextParentCancellationIsNotPromoted(t *testing.T) {
	ext, connection, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	result := make(chan error, 1)
	go func() { _, err := ctx.callHost("ui.getEditorText", nil); result <- err }()
	call := nextLifetimeCall(t, host)
	if call.Call.ParentRequestID != "origin" {
		t.Fatalf("active parent = %q", call.Call.ParentRequestID)
	}
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin"})
	if err := <-result; err == nil {
		t.Fatal("pending call ignored active cancellation")
	}
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.callHost("ui.getEditorText", nil); err == nil {
		t.Fatal("cancelled origin was promoted to the runtime")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context cancellation = %v", ctx.Err())
	}
	connection.pendingMu.Lock()
	defer connection.pendingMu.Unlock()
	if len(connection.pending) != 0 || len(connection.requestParents) != 0 {
		t.Fatal("cancelled call/request correlation retained")
	}
}

// Pi's host calls belong to no request (loader.ts createExtensionRuntime has no request scope; interactive-mode.ts:2858-2940 settles a call only through its own completion), so a call a handler left pending when it returned keeps running and receives the host's result. The row asserted the opposite before #103 gave the Node runtime the same rule; TestExtensionAPIHostCallStartedByAHandlerOutlivesItsResponseGo drives it through the Host.
func TestCompletingContextLeavesUnsettledParentCallRunning(t *testing.T) {
	_, connection, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	pending, err := ctx.beginHostCall("ui.getEditorText", nil)
	if err != nil {
		t.Fatal(err)
	}
	call := nextLifetimeCall(t, host)
	if call.Call.ParentRequestID != "origin" {
		t.Fatalf("active parent = %q", call.Call.ParentRequestID)
	}
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	finish()
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`"text"`)}}); err != nil {
		t.Fatal(err)
	}
	result, err := connection.waitCall(pending)
	if err != nil || string(result.Result) != `"text"` {
		t.Fatalf("the unsettled call after its request's response = %+v, %v, want the host's result", result, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("normal completion invalidated retained context: %v", ctx.Err())
	}
	connection.pendingMu.Lock()
	defer connection.pendingMu.Unlock()
	if len(connection.pending) != 0 || len(connection.requestParents) != 0 {
		t.Fatal("completed call/request correlation retained")
	}
}

func TestRetainedContextParentSelectionRacesNormalResponse(t *testing.T) {
	ext, connection, host, _, initialFinish := retainedContextHarness(t)
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	initialFinish()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		completed := make(map[string]bool)
		for env := range host.incoming {
			if env.Type == msgResponse {
				completed[env.ID] = true
			}
			if env.Type != msgCall {
				continue
			}
			if parent := env.Call.ParentRequestID; parent != "" && completed[parent] {
				t.Errorf("call %s follows completed parent %s on the wire", env.ID, parent)
			}
			_ = host.send(envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: []byte(`{"text":"current"}`)}})
		}
	}()
	for i := range 100 {
		id := fmt.Sprintf("request-%d", i)
		ctx, finish := ext.armRequest(id)
		var workers sync.WaitGroup
		workers.Go(func() {
			defer finish()
			if err := connection.respond(id, nil, nil); err != nil {
				t.Error(err)
			}
		})
		workers.Go(func() { _, _ = ctx.callHost("ui.getEditorText", nil) })
		workers.Wait()
		if got, err := ctx.GetEditorText(); err != nil || got != "current" {
			t.Fatalf("retained getter = %q, %v", got, err)
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	_ = connection.nc.Close()
	<-readerDone
}

func TestRetainedContextNeverRebindsToReplacementConnection(t *testing.T) {
	ext, connection, _, ctx, finish := retainedContextHarness(t)
	defer finish()
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	finish()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	replacement := newConn(client)
	ext.conn = replacement
	_ = connection.nc.Close()
	<-connection.done
	if _, err := ctx.callHost("ui.getEditorText", nil); err == nil {
		t.Fatal("closed Context connection was rebound")
	}
	if replacement.callID.Load() != 0 {
		t.Fatal("retained context sent to replacement connection")
	}
}
