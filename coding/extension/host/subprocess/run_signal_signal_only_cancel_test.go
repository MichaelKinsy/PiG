package subprocess

import (
	"context"
	"net"
	"testing"
)

// Pi aborts the run's one signal: a handler waiting on ctx.signal and the request it answers see the same abort. The host carries it as two frames, the run_signal
// frame that aborts the runtime's copy of the run's signal and the request's signal-only cancel, and a runtime that reads the cancel alone has nothing that wakes
// a handler waiting on ctx.signal. A runtime that finds its event loop empty between the two reads reports that handler as held, and the host's owner exits
// without the session_shutdown the abort would have woken. So the run_signal frame is queued first, whichever of the signal's two watchers (the run's, which
// syncs every runtime, and the request's) runs first. The Host holds no extension here, so the run's own watcher reaches no connection: the case where it loses.
func TestSignalOnlyCancelFollowsTheRunSignalFrame(t *testing.T) {
	run, abort := context.WithCancel(context.Background())
	t.Cleanup(abort)
	h := newRunSignalHost(t, func() context.Context { return run })
	host, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("signal-only-cancel", host)
	conn.beforeCancel = func() { _ = h.syncRunSignal(conn) } // the wiring of managedExt.setConnLocked
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })

	// The runtime holds the live run.
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true}); got != want {
		t.Fatalf("frame = %+v, want %+v", got, want)
	}
	// A request the run awaits (tool_call, tool_result, context): its context hides the run's cancellation and carries the run as its signal.
	ctx := &signalOnlyCtx{Context: t.Context(), signal: run}
	go func() {
		_, _ = conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "tool_call"}})
	}()
	if request := readLivenessEnvelope(t, peer); request.Type != MsgRequest {
		t.Fatalf("frame = %+v, want the request", request)
	}
	abort()
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true, Aborted: true}); got != want {
		t.Fatalf("the frame before the request's cancel = %+v, want %+v: the runtime read the cancel with the run's signal still unaborted", got, want)
	}
	if cancel := readLivenessEnvelope(t, peer); cancel.Type != MsgCancel || cancel.Cancel == nil || !cancel.Cancel.SignalOnly {
		t.Fatalf("frame after the run signal = %+v, want the request's signal-only cancel", cancel)
	}
}
