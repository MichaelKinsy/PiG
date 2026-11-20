package subprocess

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The read loop passes a request's reply to its arrival callback (withRequestArrived) before the reply reaches the request's goroutine, and the callbacks of the
// session_start handler and of the tool call queue a dispatch marker behind the frames the host has not dispatched (dispatchBarrier). When the incoming channel is
// full the read loop waits there, as it does for every other frame it queues, and every other wait of the read loop ends with the connection (its context). The wait
// for the marker must too: a connection that ended has no frames left to keep in order, and its goroutines (Done), the Close that joins them, and the requests Close
// answers must not wait for the host's incoming loop to take a frame.

// barrierSite is one request whose arrival callback queues a dispatch marker.
type barrierSite struct {
	name string
	// start makes the request on rig, and returns the function that writes its reply as the extension, and the channel that carries the error the call ended with.
	start func(t *testing.T, rig *endedCallRig) (reply func(t *testing.T), returned <-chan error)
}

var barrierSites = []barrierSite{
	{name: "session_start handler", start: startSessionStartBehindABarrier},
	{name: "tool call", start: startToolCallBehindABarrier},
}

func startSessionStartBehindABarrier(t *testing.T, rig *endedCallRig) (func(t *testing.T), <-chan error) {
	t.Helper()
	returned := make(chan error, 1)
	handler := rig.me.makeEventHandler("session_start", 1)
	go func() {
		_, err := handler(map[string]any{"type": "session_start"})
		returned <- err
	}()
	if state := rig.next(t, "the state the handler runs against"); state.Type != MsgNotify {
		t.Fatalf("first frame = %+v, want the state push", state)
	}
	request := rig.next(t, "the session_start request")
	if request.Type != MsgRequest || request.Request == nil || request.Request.Event != "session_start" {
		t.Fatalf("second frame = %+v, want the session_start request", request)
	}
	return func(t *testing.T) {
		t.Helper()
		if err := writeEnvelopeTo(rig.peer, &Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}}); err != nil {
			t.Fatal(err)
		}
	}, returned
}

func startToolCallBehindABarrier(t *testing.T, rig *endedCallRig) (func(t *testing.T), <-chan error) {
	t.Helper()
	returned := make(chan error, 1)
	go func() {
		_, err := rig.execute(t.Context(), "tc-barrier", json.RawMessage(`{}`), nil)
		returned <- err
	}()
	if state := rig.next(t, "the state the call runs against"); state.Type != MsgNotify {
		t.Fatalf("first frame = %+v, want the state push", state)
	}
	request := rig.next(t, "the tool_call request")
	if request.Type != MsgRequest || request.Request == nil || request.Request.Method != MethodToolCall {
		t.Fatalf("second frame = %+v, want the tool_call request", request)
	}
	return func(t *testing.T) {
		t.Helper()
		rig.respond(t, request, "answered")
	}, returned
}

// awaitCallEnd waits for the call of a barrierSite to end and returns its error.
func awaitCallEnd(t *testing.T, returned <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-returned:
		return err
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// takeFramesUntilDone ends a test that failed before the connection's goroutines did: the read loop parked in dispatchBarrier leaves when a frame is taken, and the
// rig's own cleanup waits for Done.
func takeFramesUntilDone(t *testing.T, conn *Conn) {
	t.Helper()
	t.Cleanup(func() {
		for {
			select {
			case _, open := <-conn.Incoming():
				if !open {
					<-conn.Done()
					return
				}
			case <-conn.Done():
				return
			}
		}
	})
}

// The incoming channel is full and nothing takes from it, as when the host's incoming loop waits for something while the extension goes on writing: the read loop
// reads the reply and waits to queue its marker. The connection then fails (the peer's process ended and the heartbeat's ping could not be written, or a read
// failed): the request ends with the failure, and the read loop and the connection's other goroutines end with it, whatever the incoming channel holds.
func TestAFailedConnectionEndsItsReadLoopWhileItWaitsToQueueTheDispatchMarker(t *testing.T) {
	for _, site := range barrierSites {
		t.Run(site.name, func(t *testing.T) {
			rig := newEndedCallRig(t)
			takeFramesUntilDone(t, rig.conn)
			for range cap(rig.conn.inCh) {
				rig.conn.inCh <- &Envelope{Type: MsgNotify}
			}
			reply, returned := site.start(t, rig)
			reply(t)
			awaitStack(t, "the read loop does not wait to queue the marker", "dispatchBarrier", true)
			rig.conn.fail(errors.New("the extension process ended"))
			if err := awaitCallEnd(t, returned, "the request to end with the failed connection"); err == nil {
				t.Fatal("the request of a failed connection ended without an error")
			}
			waitSignal(t, rig.conn.Done(), "the goroutines of the failed connection to end")
			awaitStack(t, "the read loop of the failed connection still waits to queue the marker", "dispatchBarrier", false)
		})
	}
}

// The host's incoming loop waits for the calls the extension sent before a drain report to apply them (runtimeCommandsDrained), and a call that waits for the
// embedder's background gate applies only when its context ends: the connection's, which Close ends after it joined the read loop. The extension went on writing, so the
// incoming channel is full when a reply arrives, and the read loop waits to queue its marker. Close must not wait for the incoming loop: it ends the read loop, which
// ends the calls, which frees the incoming loop.
func TestCloseEndsTheReadLoopWhileTheIncomingLoopWaitsForAHeldCall(t *testing.T) {
	for _, site := range barrierSites {
		t.Run(site.name, func(t *testing.T) {
			rig := newEndedCallRig(t)
			rig.h.SetBackgroundCallGate(func() <-chan struct{} { return make(chan struct{}) })
			rig.h.goHandleIncoming(rig.me, rig.conn)
			// Releases the held call and the incoming loop when the test fails before Close ends the read loop.
			t.Cleanup(func() { rig.conn.fail(errors.New("test done")) })
			if err := writeEnvelopeTo(rig.peer, &Envelope{Type: MsgCall, ID: "held", Call: &CallPayload{Method: "ui.notify"}}); err != nil {
				t.Fatal(err)
			}
			if err := writeEnvelopeTo(rig.peer, &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyRuntimeCommandsDrained}}); err != nil {
				t.Fatal(err)
			}
			awaitStack(t, "the incoming loop does not wait for the held call", "runtimeCommandsDrained", true)
			reply, returned := site.start(t, rig)
			for range cap(rig.conn.inCh) {
				rig.conn.inCh <- &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "unrelated"}}
			}
			reply(t)
			awaitStack(t, "the read loop does not wait to queue the marker", "dispatchBarrier", true)
			rig.me.shuttingDown.Store(true)
			closed := make(chan struct{})
			go func() {
				_ = rig.conn.Close("shutdown")
				close(closed)
			}()
			waitSignal(t, closed, "Close to return while the incoming loop waits for a held call")
			// Close answers the request as a close the host asked for, which the call may take as its end without an error: the call must end.
			_ = awaitCallEnd(t, returned, "the request to end with the closed connection")
		})
	}
}
