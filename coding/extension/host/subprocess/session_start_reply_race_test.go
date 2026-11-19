package subprocess

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// stackHolds reports whether a goroutine's stack holds fn. A test that learns through it that a goroutine parked or ended has no happens-before edge to that
// goroutine, so the race detector sees what the test does next as unordered with the goroutine's last accesses, as the accesses of two production goroutines are.
func stackHolds(fn string) bool {
	buf := make([]byte, 1<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), fn)
}

// awaitStack waits until some goroutine's stack holds fn (holds) or none does (not holds).
func awaitStack(t *testing.T, what, fn string, holds bool) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for stackHolds(fn) != holds {
		if time.Now().After(deadline) {
			t.Fatalf("%s: holds(%s) is still %v", what, fn, !holds)
		}
		time.Sleep(time.Millisecond)
	}
}

// sessionStartCaller is the goroutine of the test that calls the handler; the test finds it by this name.
func sessionStartCaller(handler extension.HandlerFn) {
	_, _ = handler(map[string]any{"type": "session_start"})
}

// The read loop passes a request's arrival callback (withRequestArrived) the moment it reads the reply, before the reply reaches the request's goroutine, and the
// session_start handler receives the dispatch marker that callback queues through it. A request that ends without its reply (the connection fails while the read
// loop is still queueing the marker behind frames the host has not dispatched) returns while the callback still runs, so the marker is handed over by an
// atomic, not by the reply: a plain variable is reported by the race detector as a read in the handler's goroutine against the read loop's write.
//
// The race is ordered here, not left to the scheduler: the connection's incoming channel is full and nothing takes from it, so the read loop parks in
// dispatchBarrier after it read the reply; the connection fails, the handler's request returns and its goroutine ends; only then a frame is taken out and the
// read loop stores the marker. The test learns of the parked read loop and the ended handler from goroutine stacks, which orders nothing, so the only
// edge between the handler's read and the read loop's write is the one the product provides. Only the race detector can see the difference: this test is red
// under go test -race against a plain variable and passes without -race against either.
//
// The tool call's hand-off (toolExecuteFunc, `dispatched`) has the same shape and the same fix, and no test of its own: its race needs the read loop to stop
// between the write and the delivery of the reply, and the product has no point there that a test can hold: the only code that runs between them is
// jsloop.SettleSlot.Settle, which takes a mutex for a few instructions, and its slot is created only by agent's parallel batch executor (agent/tool_execution.go,
// withToolSettle, unexported). TestRestartReplacesRegistrationBesideEveryReader (the tool call row of the fused, go isolated and node isolated rigs) guards
// it under -race.
func TestSessionStartRequestEndedByAConnectionFailureLeavesTheReadLoopsMarkOrdered(t *testing.T) {
	rig := newEndedCallRig(t)
	// Nothing reads the connection's incoming frames, so the marker's send waits until the test takes a frame out.
	for range cap(rig.conn.inCh) {
		rig.conn.inCh <- &Envelope{Type: MsgNotify}
	}
	go sessionStartCaller(rig.me.makeEventHandler("session_start", 1))
	if state := rig.next(t, "the state the handler runs against"); state.Type != MsgNotify {
		t.Fatalf("first frame = %+v, want the state push", state)
	}
	request := rig.next(t, "the session_start request")
	if request.Type != MsgRequest || request.Request == nil || request.Request.Event != "session_start" {
		t.Fatalf("second frame = %+v, want the session_start request", request)
	}
	if err := writeEnvelopeTo(rig.peer, &Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}}); err != nil {
		t.Fatal(err)
	}
	// The read loop read the reply and waits to queue the marker behind the frames nobody takes.
	awaitStack(t, "the read loop does not wait to queue the marker", "dispatchBarrier", true)
	rig.conn.fail(errors.New("the extension process ended"))
	awaitStack(t, "the handler does not return", "sessionStartCaller", false)
	<-rig.conn.inCh
	awaitStack(t, "the read loop does not queue the marker", "dispatchBarrier", false)
}
