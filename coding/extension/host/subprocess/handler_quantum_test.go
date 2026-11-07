package subprocess

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// startQuantumRequest sends a handler request that carries the quantum and an invocation acknowledgment, and returns the acknowledgment's channel, the count of quantum runs and the request's result.
func startQuantumRequest(t *testing.T, conn *Conn) (acknowledged <-chan struct{}, runs *atomic.Int32, result <-chan error) {
	t.Helper()
	ack := make(chan struct{})
	runs = new(atomic.Int32)
	ctx := invocation.WithSuspensionAcknowledgment(t.Context(), func() { close(ack) })
	ctx = withRequestQuantum(ctx, func() { runs.Add(1) })
	done := make(chan error, 1)
	go func() {
		_, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "agent_settled"}})
		done <- err
	}()
	return ack, runs, done
}

// A runtime that cannot report its waits (Go, Rust, Python, Lisp) blocks in the language's own call, so the host takes a handler that outlives HandlerWaitQuantum to be waiting: the invocation is acknowledged as suspended and the caller's quantum function runs once. The handler's answer still completes the request.
func TestHandlerOutlivingTheQuantumIsSuspended(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("quantum", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	acknowledged, runs, result := startQuantumRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	clock.waitForTimer(t, HandlerWaitQuantum)
	select {
	case <-acknowledged:
		t.Fatal("the invocation was acknowledged before the quantum elapsed")
	default:
	}
	clock.elapse(HandlerWaitQuantum)
	select {
	case <-acknowledged:
	case <-time.After(5 * time.Second):
		t.Fatal("a handler that outlived the quantum was not acknowledged as suspended")
	}
	for runs.Load() == 0 {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal("the quantum function did not run")
		}
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(`null`)}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("quantum function ran %d times, want 1", got)
	}
}

// A handler that answers within the quantum is Pi's microtask-only continuation: nothing is suspended, and the timer is stopped.
func TestHandlerAnsweringWithinTheQuantumIsNotSuspended(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("quantum-fast", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	acknowledged, runs, result := startQuantumRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	clock.waitForTimer(t, HandlerWaitQuantum)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(`null`)}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	clock.elapse(HandlerWaitQuantum)
	select {
	case <-acknowledged:
		t.Fatal("a handler that answered within the quantum was acknowledged as suspended")
	default:
	}
	if got := runs.Load(); got != 0 {
		t.Fatalf("quantum function ran %d times for a handler that answered in time", got)
	}
}

// A request without the option, such as a Node handler that reports its own window or an event that is no tail handler, command or input handler, sets no timer. The request is answered before the timer count is read, so a request that armed one at any point shows it.
func TestRequestWithoutTheQuantumSetsNoTimer(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("quantum-none", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "session_info_changed"}})
		done <- err
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(`null`)}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	asked := clock.requested[HandlerWaitQuantum]
	clock.mu.Unlock()
	if asked != 0 {
		t.Fatalf("a request without the quantum asked for %d timers of %s", asked, HandlerWaitQuantum)
	}
}
