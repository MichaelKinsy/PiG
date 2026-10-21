package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi runs a timer or I/O callback only when its event loop turns, so a toast an extension deferred with setTimeout(0) from a handler reaches the output after the chain of awaited handlers that the host runs (a replacement's session_start, then its command's response). The Node runtime sends that toast with no parent request once the handler settled. A call the embedder's gate holds waits for it; a call of a live request, and a call that is not output-ordered, does not.
func TestBackgroundCallGateHoldsRequestlessUICalls(t *testing.T) {
	var applied atomic.Int32
	order := make(chan string, 8)
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		applied.Add(1)
		order <- call.Method + ":" + call.ParentRequestID
		return &CallResultPayload{}, nil
	})
	open := make(chan struct{})
	consulted := make(chan struct{}, 8)
	f.host.SetBackgroundCallGate(func() <-chan struct{} {
		consulted <- struct{}{}
		select {
		case <-open:
			return nil
		default:
			return open
		}
	})

	// A call of an outstanding request runs while the gate is closed.
	f.conn.pendingMu.Lock()
	f.conn.pending["r7"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	f.call("live", "ui.notify", "r7", map[string]any{})
	f.result("live")
	if got := <-order; got != "ui.notify:r7" {
		t.Fatalf("first applied call = %q, want the live request's notify", got)
	}
	select {
	case <-consulted:
		t.Fatal("the gate held a call of a live request")
	default:
	}

	// A call that no request started waits for the gate.
	f.call("deferred", "ui.notify", "", map[string]any{})
	waitSignal(t, consulted, "the host to consult the gate for the deferred toast")
	if n := applied.Load(); n != 1 {
		t.Fatalf("%d calls applied while the gate was closed, want only the live request's", n)
	}
	close(open)
	f.result("deferred")
	select {
	case got := <-order:
		if got != "ui.notify:" {
			t.Fatalf("second applied call = %q, want the deferred notify", got)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the deferred toast never ran after the gate opened")
	}
}

// A call the gate does not name, such as a state change, is not output-ordered and runs at once.
func TestBackgroundCallGateLeavesOtherRequestlessCallsAlone(t *testing.T) {
	f := newCallOrderFixture(t, func(*CallPayload) (*CallResultPayload, error) { return &CallResultPayload{}, nil })
	f.host.SetBackgroundCallGate(func() <-chan struct{} { return make(chan struct{}) })
	f.call("tools", "setActiveTools", "", map[string]any{})
	f.result("tools")
}

// phaseRecorder counts the run-phase reports a command's transport makes.
type phaseRecorder struct{ resumed, suspended atomic.Int32 }

func (p *phaseRecorder) Resumed()   { p.resumed.Add(1) }
func (p *phaseRecorder) Suspended() { p.suspended.Add(1) }

// A runtime reports its handler blocked on a host call before it sends the call, so the host learns what the handler awaits from the call that follows. A waitForIdle keeps the report for the host, which knows whether the Session is idle: Pi's `await ctx.waitForIdle()` returns at once then (agent-session.ts waitForIdle), so the handler is not suspended and the run its command sent starts after the command's response. Any other call suspends the handler when its frame is read, as before.
func TestBlockedReportWaitsForTheCallThatFollows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		method        string
		settle        func(c *Conn)
		wantSuspended int32
	}{
		{"another call suspends at once", "setActiveTools", nil, 1},
		{"waitForIdle that finds the Session idle suspends nothing", "waitForIdle", func(c *Conn) { c.settleBlockedCall("r1", false) }, 0},
		{"waitForIdle that waits suspends the handler", "waitForIdle", func(c *Conn) { c.settleBlockedCall("r1", true) }, 1},
		{"waitForIdle of no verdict yet has suspended nothing", "waitForIdle", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hostSide, extSide := net.Pipe()
				conn := NewConn("blocked-report", hostSide)
				conn.Start(t.Context())
				go func() { _, _ = io.Copy(io.Discard, extSide) }()
				go func() {
					for range conn.Incoming() {
					}
				}()
				defer func() {
					_ = conn.Close("test done")
					_ = extSide.Close()
				}()
				phase := &phaseRecorder{}
				dispatched := make(chan struct{})
				conn.onDispatch = func() { close(dispatched) }
				ctx := invocation.WithRunPhase(t.Context(), phase)
				go func() { _, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test") }()
				<-dispatched
				var frames []byte
				for _, env := range []Envelope{
					{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "host_call"}},
					{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: tc.method, ParentRequestID: "r1"}},
				} {
					data, err := json.Marshal(env)
					if err != nil {
						t.Fatal(err)
					}
					var length [4]byte
					binary.BigEndian.PutUint32(length[:], uint32(len(data)))
					frames = append(append(frames, length[:]...), data...)
				}
				go func() { _, _ = extSide.Write(frames) }()
				synctest.Wait()
				if tc.settle != nil {
					tc.settle(conn)
				}
				if got := phase.suspended.Load(); got != tc.wantSuspended {
					t.Fatalf("suspensions reported = %d, want %d", got, tc.wantSuspended)
				}
			})
		})
	}
}

// Another request's call frame between a handler's blocked report and its own call is not the call the handler awaits: it reports nothing for the handler, so a waitForIdle that follows can still settle at once.
func TestBlockedReportIgnoresAnotherRequestsCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hostSide, extSide := net.Pipe()
		conn := NewConn("blocked-other", hostSide)
		conn.Start(t.Context())
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		go func() {
			for range conn.Incoming() {
			}
		}()
		defer func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		phase := &phaseRecorder{}
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		ctx := invocation.WithRunPhase(t.Context(), phase)
		go func() { _, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test") }()
		<-dispatched
		var frames []byte
		for _, env := range []Envelope{
			{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "host_call"}},
			{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r2"}},
			{Type: MsgCall, ID: "c2", Call: &CallPayload{Method: "waitForIdle", ParentRequestID: "r1"}},
		} {
			data, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			frames = append(append(frames, length[:]...), data...)
		}
		go func() { _, _ = extSide.Write(frames) }()
		synctest.Wait()
		if got := phase.suspended.Load(); got != 0 {
			t.Fatalf("suspensions reported = %d, want none before the host decides on the waitForIdle", got)
		}
		conn.settleBlockedCall("r1", false)
		if got := phase.suspended.Load(); got != 0 {
			t.Fatalf("suspensions reported after an idle waitForIdle = %d, want none", got)
		}
	})
}
