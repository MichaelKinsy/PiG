package subprocess

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// Pi's abort of a run aborts the signal of the tool in flight and nothing else (agent-loop.ts:817-853 awaits tool.execute with it): the tool's in-process
// host calls, pending at the abort or made after it, complete. Only a nested tool call, which runs with the outer tool's signal when it has none of its
// own (runner.ts:979-981), takes the abort. The host therefore cancels a tool_call with a signal-only cancel frame, ends its nested tool calls, spares its
// other calls, and, when D111's grace ends and the tool is abandoned, sends the ordinary cancel and ends the remaining calls.
func TestToolCallAbortCancelsSignalOnlyAndSparesItsHostCalls(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("tool-abort", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: MethodToolCall, Tool: "probe"}})
		result <- err
	}()
	request := readLivenessEnvelope(t, peer)
	conn.pendingMu.Lock()
	_, pending := conn.pending[request.ID]
	conn.pendingMu.Unlock()
	if !pending {
		t.Fatal("the tool_call is not pending")
	}
	openCall := func(id, method string) context.Context {
		t.Helper()
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, ParentRequestID: request.ID}})
		select {
		case env := <-conn.Incoming():
			if env.Type != MsgCall || env.ID != id {
				t.Fatalf("incoming = %#v, want call %s", env, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("call %s was not delivered", id)
		}
		callCtx, release := conn.hostCallContext(request.ID, id)
		t.Cleanup(release)
		return callCtx
	}
	dialog := openCall("c-dialog", "ui.input")
	nested := openCall("c-nested", CallExecuteTool)

	cancel()
	cancelFrame := readLivenessEnvelope(t, peer)
	if cancelFrame.Type != MsgCancel || cancelFrame.Cancel == nil || cancelFrame.Cancel.RequestID != request.ID || !cancelFrame.Cancel.SignalOnly {
		t.Fatalf("cancel envelope = %+v, want a signal-only cancel of %s", cancelFrame, request.ID)
	}
	clock.expectTimer(t, ToolAbortGrace)
	select {
	case <-nested.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the abort did not end the nested tool call, which runs with the tool's signal")
	}
	if dialog.Err() != nil {
		t.Fatal("the abort cancelled the dialog the tool awaits")
	}
	// A call made after the abort is answered; a nested tool call made after it runs with the aborted signal.
	later := openCall("c-later", "ui.input")
	laterNested := openCall("c-later-nested", CallExecuteTool)
	if later.Err() != nil {
		t.Fatal("the abort cancelled a host call the tool made after it")
	}
	if laterNested.Err() == nil {
		t.Fatal("a nested tool call made after the abort runs with a live signal")
	}
	if !conn.parentAbortedOnly(request.ID) {
		t.Fatal("the request is not marked as aborted alone")
	}

	clock.elapse(ToolAbortGrace)
	if err := <-result; !errors.Is(err, ErrToolAborted) {
		t.Fatalf("request error = %v, want ErrToolAborted", err)
	}
	ordinary := readLivenessEnvelope(t, peer)
	if ordinary.Type != MsgCancel || ordinary.Cancel == nil || ordinary.Cancel.RequestID != request.ID || ordinary.Cancel.SignalOnly {
		t.Fatalf("cancel envelope at the grace's end = %+v, want the ordinary cancel of %s", ordinary, request.ID)
	}
	for name, callCtx := range map[string]context.Context{"dialog": dialog, "later dialog": later} {
		if callCtx.Err() == nil {
			t.Fatalf("the %s the abandoned tool awaited was not cancelled at the grace's end", name)
		}
	}
}

// A tool_call the host abandons for inactivity, and every other request that is not an abort of a tool, keeps the ordinary cancel: the request ends and
// its host calls end with it.
func TestToolCallInactivityCancelStaysOrdinary(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("tool-stalled", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := conn.requestWithInactivity(t.Context(), &Envelope{ID: "r1", Type: MsgRequest, Request: &RequestPayload{Method: MethodToolCall, Tool: "probe"}}, time.Second, "test")
		result <- err
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "ui.input", ParentRequestID: request.ID}})
	select {
	case <-conn.Incoming():
	case <-time.After(5 * time.Second):
		t.Fatal("the call was not delivered")
	}
	callCtx, release := conn.hostCallContext(request.ID, "c1")
	defer release()
	clock.advance(t, time.Second)
	if err := <-result; err == nil {
		t.Fatal("a stalled tool_call did not fail")
	}
	frame := readLivenessEnvelope(t, peer)
	if frame.Type != MsgCancel || frame.Cancel == nil || frame.Cancel.SignalOnly {
		t.Fatalf("cancel envelope = %+v, want the ordinary cancel", frame)
	}
	if callCtx.Err() == nil {
		t.Fatal("the stalled request's host call was not cancelled")
	}
}
