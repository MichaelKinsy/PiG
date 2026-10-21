package subprocess

import (
	"context"
	"net"
	"testing"
)

// Pi's ctx.abort() returns after agent.abort() aborted the active run's controller (agent.ts:340-342), so a handler that holds ctx.signal and reads it once its
// abort call returned sees it aborted. A runtime replicates the signal with run_signal frames. The run's cancellation starts a watcher that forwards the abort,
// and nothing orders that frame before the reply to the abort call, so the reply must wait for no frame but follow the one the host sends for the caller.
// No watcher reaches the connection here (the Host holds no extension), the case where it loses the race to the reply.
func TestAbortReplyFollowsTheRunSignalFrame(t *testing.T) {
	run, abort := context.WithCancel(context.Background())
	t.Cleanup(abort)
	h := newRunSignalHost(t, func() context.Context { return run })
	host, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("abort-reply", host)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })

	// The runtime holds the live run.
	if err := h.syncRunSignal(conn); err != nil {
		t.Fatal(err)
	}
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true}); got != want {
		t.Fatalf("frame = %+v, want %+v", got, want)
	}

	// The host's abort action cancelled the run; the host now answers the call.
	abort()
	h.replyToCall(nil, conn, "abort-call", &CallPayload{Method: "abort"}, t.Context(), &CallResultPayload{}, nil)
	if got, want := readRunSignal(t, peer), (runSignalFrame{Run: 1, Active: true, Aborted: true}); got != want {
		t.Fatalf("the frame before the reply = %+v, want %+v", got, want)
	}
	if reply := readLivenessEnvelope(t, peer); reply.Type != MsgCallResult || reply.ID != "abort-call" {
		t.Fatalf("after the run_signal frame the peer read %+v, want the reply to the abort call", reply)
	}
}

// A call that is not an abort sends no run_signal frame of its own: the frame follows the abort, not every reply.
func TestOtherCallRepliesSendNoRunSignalFrame(t *testing.T) {
	run, abort := context.WithCancel(context.Background())
	t.Cleanup(abort)
	h := newRunSignalHost(t, func() context.Context { return run })
	host, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("other-reply", host)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })

	abort()
	h.replyToCall(nil, conn, "other-call", &CallPayload{Method: "isIdle"}, t.Context(), &CallResultPayload{}, nil)
	if reply := readLivenessEnvelope(t, peer); reply.Type != MsgCallResult || reply.ID != "other-call" {
		t.Fatalf("the peer read %+v, want the reply to the call alone", reply)
	}
}
