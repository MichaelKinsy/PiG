package subprocess

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A writer that stops on a write failure completes every frame still queued,
// so no sendAndWait blocks on a frame nothing will write. The writer runs
// synchronously; there is no timing assumption.
func TestWriteFailureCompletesQueuedFrameWaiters(t *testing.T) {
	host, peer := net.Pipe()
	conn := NewConn("queued", host)
	defer func() { _ = host.Close() }()
	_ = peer.Close()
	first := make(chan error, 1)
	second := make(chan error, 1)
	conn.outCh <- outboundFrame{data: []byte(`{"type":"request","id":"first"}`), result: first}
	conn.outCh <- outboundFrame{data: []byte(`{"type":"request","id":"second"}`), result: second}
	conn.writeLoop(context.Background())
	for name, ch := range map[string]chan error{"first": first, "second": second} {
		select {
		case err := <-ch:
			if err == nil {
				t.Fatalf("%s write unexpectedly succeeded", name)
			}
		default:
			t.Fatalf("writer exited leaving the %s frame's waiter blocked", name)
		}
	}
}

// Requests queued behind a write that fails all return the connection's
// error; none stays blocked waiting for its frame to be written.
func TestQueuedRequestsFailWhenTheWriterFails(t *testing.T) {
	host, peer := net.Pipe()
	conn := newConnWithOptions("queued-requests", host, connOptions{Clock: newManualLivenessClock(), HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()
	const n = 5
	results := make(chan error, n)
	for i := range n {
		go func() {
			_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: fmt.Sprintf("t%d", i)}})
			results <- err
		}()
	}
	// The peer never reads, so the first write blocks and the rest queue.
	// Wait until every request has committed its frame to the writer.
	for {
		conn.workMu.Lock()
		work := conn.work
		conn.workMu.Unlock()
		if work >= n {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_ = peer.Close()
	for range n {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("a request succeeded after its write failed")
			}
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("a queued request stayed blocked after the writer failed")
		}
	}
}
