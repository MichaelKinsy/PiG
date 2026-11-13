package sdk

import (
	"context"
	"errors"
	"net"
	"testing"
)

// A request's signal costs no allocation until something reads it: it is held in the request's scope, and its context, a child of the request's, is made on the first read (requestSignal). TestToolRequestAllocBudget holds a tool_call round trip to its budget on that.
func TestRequestSignalContextIsMadeWhenFirstRead(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	ext := New("signal-lazy")
	ext.conn = newConn(client)
	ctx, scope := ext.armRequestScope("origin")
	defer scope.end()
	if scope.signal.child != nil {
		t.Fatal("arming a request made its signal context")
	}
	if ctx.Err() != nil {
		t.Fatalf("a fresh request's signal reports %v", ctx.Err())
	}
	if scope.signal.child == nil {
		t.Fatal("reading the signal did not make its context")
	}
}

// A signal-only cancel of a request nothing has read the signal of yet (a tool_call whose handler has not looked) aborts it all the same, with the cancel's reason, and the request goes on; an ordinary cancel after it ends the request and leaves the signal's first cause.
func TestSignalOnlyCancelAbortsASignalNobodyHasReadYet(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	ext := New("signal-only-unread")
	ext.conn = newConn(client)
	ctx, scope := ext.armRequestScope("origin")
	defer scope.end()
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "aborted", SignalOnly: true}})
	if !errors.Is(ctx.Err(), context.Canceled) || ctx.CancellationReason() != "aborted" {
		t.Fatalf("after a signal-only cancel: Err = %v, reason %q, want the signal aborted with the cancel's reason", ctx.Err(), ctx.CancellationReason())
	}
	if scope.signal.parent.Err() != nil {
		t.Fatalf("a signal-only cancel ended the request: %v", scope.signal.parent.Err())
	}
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "ordinary"}})
	if scope.signal.parent.Err() == nil {
		t.Fatal("an ordinary cancel left the request running")
	}
	if ctx.CancellationReason() != "aborted" {
		t.Fatalf("the ordinary cancel replaced the signal's cause: %q", ctx.CancellationReason())
	}
}
