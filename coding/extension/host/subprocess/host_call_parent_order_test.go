package subprocess

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// Pi's host calls are in-process: a call the extension made before its command returned completes whatever order the host gets to it in
// (.upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts showExtensionCustom 2858-2940). The connection reads a
// command's response ahead of the host's turn on a call frame that preceded it, so the call must own its context from the read loop, while
// its parent request is still pending.
func TestHostCallReadBeforeItsParentsResponseSurvivesTheResponse(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	conn := NewConn("call-order", a)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn.Start(ctx)
	defer conn.fail(errors.New("test done"))

	respCh := make(chan *Envelope, 1)
	conn.pendingMu.Lock()
	conn.pending["r1"] = respCh
	conn.pendingMu.Unlock()
	writeFramed(t, b, Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "ui.custom", ParentRequestID: "r1"}})
	writeFramed(t, b, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
	select {
	case <-respCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the response was not routed")
	}
	// The request returns: its record leaves the connection before the host's turn on the call.
	conn.pendingMu.Lock()
	delete(conn.pending, "r1")
	conn.pendingMu.Unlock()

	select {
	case env := <-conn.Incoming():
		if env.Type != MsgCall || env.ID != "c1" {
			t.Fatalf("incoming = %#v", env)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call was not delivered")
	}
	callCtx, release := conn.hostCallContext("r1", "c1")
	defer release()
	if callCtx.Err() != nil {
		t.Fatal("a command's return cancelled a host call it made before it returned")
	}
}

// A call of a request the host cancelled stays dropped when the host takes its turn after the request's record is gone.
func TestHostCallOfCancelledParentStaysCancelledAfterCleanup(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	conn := NewConn("call-cancelled", a)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn.Start(ctx)
	defer conn.fail(errors.New("test done"))

	conn.pendingMu.Lock()
	conn.pending["r2"] = make(chan *Envelope, 1)
	conn.pendingMu.Unlock()
	writeFramed(t, b, Envelope{Type: MsgCall, ID: "c2", Call: &CallPayload{Method: "ui.custom", ParentRequestID: "r2"}})
	select {
	case <-conn.Incoming():
	case <-time.After(5 * time.Second):
		t.Fatal("the call was not delivered")
	}
	conn.cancelHostCalls("r2")
	conn.pendingMu.Lock()
	delete(conn.pending, "r2")
	conn.pendingMu.Unlock()
	conn.hostCallMu.Lock()
	delete(conn.cancelledParent, "r2")
	conn.hostCallMu.Unlock()

	first, releaseFirst := conn.hostCallContext("r2", "c2")
	defer releaseFirst()
	late, releaseLate := conn.hostCallContext("r2", "c3")
	defer releaseLate()
	if first.Err() == nil || late.Err() == nil {
		t.Fatalf("a cancelled request's call ran: delivered=%v late=%v", first.Err(), late.Err())
	}
}

// The host cancels a request's calls only once the cancel frame is queued for the extension. A call cancelled first can send its
// cancellation as the call's own error result (TestParentCancellationCancelsBlockedHostCall requires that result), which would reach the
// extension ahead of the cancel and reject a promise it dropped; Pi rejects none (interactive-mode.ts showExtensionCustom 2858-2940). The
// cancel then follows the result on the wire, and the extension cancels the call itself.
func TestParentCancellationQueuesTheCancelBeforeCancellingItsHostCalls(t *testing.T) {
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("cancel-order", hostEnd)
	ctx, cancelConn := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelConn()
	conn.Start(ctx)
	defer conn.fail(errors.New("test done"))

	requestCtx, cancelRequest := context.WithCancel(ctx)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = conn.Request(requestCtx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "ask"}})
	}()
	parent := readLivenessEnvelope(t, peer)
	callCtx, release := conn.hostCallContext(parent.ID, "child-call")
	defer release()

	// The peer stops reading: the writer holds one frame and the queue fills, so the cancel frame cannot be queued.
	filled := make(chan struct{})
	go func() {
		defer close(filled)
		for range cap(conn.outCh) + 2 {
			if conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "filler"}}) != nil {
				return
			}
		}
	}()
	pollUntil(t, testTimeout(t, 10*time.Second), "the writer queue never filled", func() bool { return len(conn.outCh) == cap(conn.outCh) })
	cancelRequest()
	select {
	case <-callCtx.Done():
		t.Fatal("the request's host call was cancelled before its cancel frame was queued")
	case <-time.After(300 * time.Millisecond):
	}

	sawCancel := false
	for !sawCancel {
		env := readLivenessEnvelope(t, peer)
		sawCancel = env.Type == MsgCancel && env.Cancel != nil && env.Cancel.RequestID == parent.ID
	}
	select {
	case <-callCtx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the request's host call was not cancelled after its cancel frame")
	}
	<-requestDone
	<-filled
}
