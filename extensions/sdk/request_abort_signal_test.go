package sdk

import (
	"net"
	"testing"
)

// Only session_before_compact and session_before_tree carry an AbortSignal that can end without the request (host.go beforeEventSignal), so only they pay for a signal context of their own: a tool_call round trip does not (TestToolRequestAllocBudget).
func TestOnlyBeforeEventsCarryAnAbortSignal(t *testing.T) {
	for _, tc := range []struct {
		req  *requestMsg
		want bool
	}{
		{nil, false},
		{&requestMsg{Method: "tool_call", Tool: "t"}, false},
		{&requestMsg{Method: "event", Event: EventSessionStart}, false},
		{&requestMsg{Method: "event", Event: EventSessionBeforeCompact}, true},
		{&requestMsg{Method: "event", Event: EventSessionBeforeTree}, true},
	} {
		if got := carriesAbortSignal(tc.req); got != tc.want {
			t.Errorf("carriesAbortSignal(%+v) = %v, want %v", tc.req, got, tc.want)
		}
	}
}

// A request armed without an abort signal ends only with the request: a signal-only cancel frame for it (the host sends none) leaves it running, and an ordinary cancel ends it.
func TestRequestWithoutAbortSignalHasNoSignalToAbort(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	ext := New("no-abort-signal")
	ext.conn = newConn(client)
	ctx, scope := ext.armRequestScope("origin", false)
	defer scope.end()
	if scope.signal != nil {
		t.Fatal("a request without an abort signal holds a signal function")
	}
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "aborted", SignalOnly: true}})
	if err := ctx.Err(); err != nil {
		t.Fatalf("a signal-only cancel ended a request that has no abort signal: %v", err)
	}
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "ordinary"}})
	if ctx.Err() == nil {
		t.Fatal("an ordinary cancel left the request running")
	}
}
