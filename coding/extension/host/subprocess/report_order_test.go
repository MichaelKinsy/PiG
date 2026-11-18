package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi reports a handler's error synchronously (loader.ts:537-549 -> runner.ts:749-753) and answers the request whose handler failed after the handler
// returned, so an extension_error line always precedes the response of the command whose handler caused it. A runtime writes the report_error
// notification before the response on one socket; the read loop must not let the response overtake the report on its way to the host's loop.

type reportOrderRig struct {
	h        *Host
	me       *managedExt
	conn     *Conn
	peer     net.Conn
	requests chan Envelope
}

// newReportOrderRig starts a connection whose host loop (handleIncoming) applies the reports to a runner with the given error listener. The peer's reader hands every request the host sends to requests. It runs inside a synctest bubble.
func newReportOrderRig(t *testing.T, listener func(*extension.ExtensionError)) *reportOrderRig {
	t.Helper()
	h := NewHost(t.TempDir())
	runner := inproc.NewRunner(nil, t.TempDir(), h.Runtime())
	runner.AddErrorListener(listener)
	hostSide, peer := net.Pipe()
	conn := NewConn("report-order", hostSide)
	h.observeXref(conn)
	conn.Start(t.Context())
	me := withConn(&managedExt{config: ExtConfig{Name: "report-order", Path: "/report-order.mjs"}, host: h}, conn)
	me.shuttingDown.Store(true)
	rig := &reportOrderRig{h: h, me: me, conn: conn, peer: peer, requests: make(chan Envelope, 8)}
	go func() {
		for {
			var header [4]byte
			if _, err := io.ReadFull(peer, header[:]); err != nil {
				return
			}
			body := make([]byte, binary.BigEndian.Uint32(header[:]))
			if _, err := io.ReadFull(peer, body); err != nil {
				return
			}
			var env Envelope
			if json.Unmarshal(body, &env) == nil && env.Type == MsgRequest {
				rig.requests <- env
			}
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = peer.Close()
	})
	return rig
}

// write sends the envelopes to the host in ONE write, as a runtime's reply to a request is often written: the host reads them in order.
func (r *reportOrderRig) write(t *testing.T, envs ...Envelope) {
	t.Helper()
	var buf []byte
	for _, env := range envs {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(body)))
		buf = append(buf, body...)
	}
	if _, err := r.peer.Write(buf); err != nil {
		t.Fatal(err)
	}
}

func reportFrame(t *testing.T, message string) Envelope {
	t.Helper()
	args, err := json.Marshal(ReportErrorPayload{Event: "events:order", Error: message})
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyReportError, Args: args}}
}

func (r *reportOrderRig) request(ctx context.Context, id string) (*Envelope, error) {
	return r.conn.request(ctx, &Envelope{ID: id, Type: MsgRequest, Request: &RequestPayload{Method: "command"}}, 0, "test")
}

// The report a runtime wrote before the response of its handler's request is applied before the request returns, whether the response is the fast shape (a result) or one the read loop decodes whole (an error). The listener blocks, so the order does not depend on scheduling.
// mutation-checked: a read loop that routes a response while a report is unapplied (reportOrder.holds returning false) returns the request while the listener is still running.
func TestResponseWaitsForTheReportWrittenBeforeIt(t *testing.T) {
	for name, response := range map[string]ResponsePayload{
		"result": {Result: json.RawMessage(`{"ok":true}`)},
		"empty":  {},
		"error":  {Error: &ErrorInfo{Code: "handler_error", Message: "the handler failed"}},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered := make(chan struct{})
				release := make(chan struct{})
				// A failed assertion returns with the listener still blocked: it is released on every exit, or the bubble ends with a blocked goroutine, which synctest reports as a deadlock that aborts the whole package.
				releaseListener := sync.OnceFunc(func() { close(release) })
				defer releaseListener()
				var listenerDone atomic.Int32
				rig := newReportOrderRig(t, func(*extension.ExtensionError) {
					close(entered)
					<-release
					listenerDone.Add(1)
				})
				go rig.h.handleIncoming(rig.me, rig.conn)
				var returned atomic.Int32
				go func() {
					_, _ = rig.request(t.Context(), "r1")
					returned.Add(1)
				}()
				<-rig.requests
				rig.write(t, reportFrame(t, "first"), Envelope{Type: MsgResponse, ID: "r1", Response: &response})
				<-entered
				synctest.Wait()
				if got := returned.Load(); got != 0 {
					t.Fatalf("the request returned (%d) while the report written before its response was still being applied (listeners done: %d)", got, listenerDone.Load())
				}
				releaseListener()
				synctest.Wait()
				if listenerDone.Load() != 1 || returned.Load() != 1 {
					t.Fatalf("after the listener returned: listeners done %d, requests returned %d, want 1 and 1", listenerDone.Load(), returned.Load())
				}
			})
		})
	}
}

// A response read while no report is unapplied is routed by the read loop to its waiter at once: the host's loop is not involved. The host loop does not run here at all.
// mutation-checked: queueing every response through Incoming (a read loop that defers with no report pending) never returns the request.
func TestResponseWithNoReportPendingDoesNotNeedTheHostLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rig := newReportOrderRig(t, func(*extension.ExtensionError) {})
		var returned atomic.Int32
		go func() {
			_, _ = rig.request(t.Context(), "r1")
			returned.Add(1)
		}()
		<-rig.requests
		rig.write(t, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{Result: json.RawMessage(`1`)}})
		synctest.Wait()
		if returned.Load() != 1 {
			t.Fatal("the response with no report pending was not routed by the read loop")
		}
	})
}

// An error listener may call the extension and wait for the answer: that request was sent after the report was read, so its response is never queued behind the report the listener is applying (it would wait for itself). The response of the request that was in flight before the report still follows the listener. The assertions are counts read once every goroutine is blocked, so a deadlock fails them instead of timing out.
// mutation-checked: deferring every response while a report is unapplied (reportOrder.holds ignoring the request's ID) leaves the listener unanswered, the deadlock.
func TestErrorListenerThatCallsTheExtensionIsAnsweredWhileItsReportIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		note := func(step string) {
			mu.Lock()
			order = append(order, step)
			mu.Unlock()
		}
		var rig *reportOrderRig
		listenerCtx, stopListener := context.WithCancel(t.Context())
		defer stopListener()
		var answered atomic.Int32
		rig = newReportOrderRig(t, func(*extension.ExtensionError) {
			// The listener of graph extensions calls back into the extension that reported (session_graph_extension_runner.go onError), on this connection.
			if _, err := rig.request(listenerCtx, "listener-call"); err == nil {
				answered.Add(1)
			}
			note("listener")
		})
		go rig.h.handleIncoming(rig.me, rig.conn)
		var returned atomic.Int32
		go func() {
			_, _ = rig.request(t.Context(), "r1")
			note("response")
			returned.Add(1)
		}()
		<-rig.requests
		rig.write(t, reportFrame(t, "first"), Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		call := <-rig.requests
		if call.ID != "listener-call" {
			t.Fatalf("the host sent %q, want the listener's call", call.ID)
		}
		synctest.Wait()
		if returned.Load() != 0 {
			t.Fatal("the request returned while the listener that applies the report written before its response was still running")
		}
		rig.write(t, Envelope{Type: MsgResponse, ID: "listener-call", Response: &ResponsePayload{}})
		synctest.Wait()
		if answered.Load() != 1 {
			t.Fatalf("the listener's own request was answered %d times, want 1: its response waits behind the report the listener is applying", answered.Load())
		}
		mu.Lock()
		defer mu.Unlock()
		if len(order) != 2 || order[0] != "listener" || order[1] != "response" {
			t.Fatalf("order = %v, want the listener returned, then the response of the request the report preceded", order)
		}
	})
}

// A report the host drops without applying (it is read while the extension is still loading, before its register frame) still counts as applied, or every later response would wait for a report nobody applies.
// mutation-checked: waitForRegister that drops a report without reportApplied leaves the count above zero.
func TestReportDroppedWhileLoadingDoesNotHoldLaterResponses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rig := newReportOrderRig(t, func(*extension.ExtensionError) { t.Error("a report read before the register frame is applied") })
		registered := make(chan struct{})
		go func() {
			_, _ = rig.h.waitForRegister(t.Context(), rig.me, rig.conn)
			close(registered)
		}()
		rig.write(t, reportFrame(t, "while loading"), Envelope{Type: MsgRegister, Register: &RegisterPayload{}})
		synctest.Wait()
		select {
		case <-registered:
		default:
			t.Fatal("waitForRegister did not return the register frame")
		}
		go rig.h.handleIncoming(rig.me, rig.conn)
		var returned atomic.Int32
		go func() {
			_, _ = rig.request(t.Context(), "r1")
			returned.Add(1)
		}()
		<-rig.requests
		rig.write(t, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		synctest.Wait()
		if returned.Load() != 1 {
			t.Fatal("the response after a report dropped while loading is still waiting behind it")
		}
	})
}

func emissionSettled(emission *busEmission) bool {
	select {
	case <-emission.done:
		return true
	default:
		return false
	}
}

// A realm that rejects after an await writes the report of the rejection before the quiesce that releases the emitter (the emitter's events.settle waits for it): Pi reports the error before the emitter's later microtasks run, so the quiesce is counted after the report was applied, and the emitter cannot answer its command first.
// mutation-checked: counting the quiesce on the read loop (the inbound hook never holds it) settles the emission while the listener that applies the report still runs.
func TestQuiesceWaitsForTheReportWrittenBeforeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		releaseListener := sync.OnceFunc(func() { close(release) })
		defer releaseListener()
		rig := newReportOrderRig(t, func(*extension.ExtensionError) {
			close(entered)
			<-release
		})
		id, emission := rig.h.eventBus.newEmission(nil)
		rig.h.eventBus.addPending(emission, rig.conn, 1)
		rig.h.eventBus.endDispatch(emission)
		go rig.h.handleIncoming(rig.me, rig.conn)
		rig.write(t, reportFrame(t, "rejected later"), *quiesceFrame(id))
		<-entered
		synctest.Wait()
		if emissionSettled(emission) {
			t.Fatal("the emission settled while the report written before the realm's quiesce was still being applied")
		}
		releaseListener()
		synctest.Wait()
		if !emissionSettled(emission) {
			t.Fatal("the emission did not settle once the report was applied")
		}
	})
}

// The error listener may emit and wait for the emission to settle: the quiesce of an emission created after the report was read is counted at once, not behind the report the listener is applying (it would wait for itself).
// mutation-checked: holding the quiesce of every emission while a report is unapplied (holdsEmission ignoring the emission's number) leaves the listener's emission unsettled, the deadlock.
func TestQuiesceOfAnEmissionTheErrorListenerStartedIsNotHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan string, 1)
		listenerCtx, stopListener := context.WithCancel(t.Context())
		defer stopListener()
		var settledForListener atomic.Int32
		var rig *reportOrderRig
		rig = newReportOrderRig(t, func(*extension.ExtensionError) {
			id, emission := rig.h.eventBus.newEmission(nil)
			rig.h.eventBus.addPending(emission, rig.conn, 1)
			rig.h.eventBus.endDispatch(emission)
			started <- id
			select {
			case <-emission.done:
				settledForListener.Add(1)
			case <-listenerCtx.Done():
			}
		})
		go rig.h.handleIncoming(rig.me, rig.conn)
		rig.write(t, reportFrame(t, "first"))
		id := <-started
		rig.write(t, *quiesceFrame(id))
		synctest.Wait()
		if settledForListener.Load() != 1 {
			t.Fatalf("the emission the error listener started settled %d times while it waited, want 1: its quiesce waits behind the report the listener is applying", settledForListener.Load())
		}
	})
}

// A quiesce held behind a report never leaves the emitter waiting for ever. The listener that applies the report never returns here, so only the end of the connection can release the emission: hostEventBus.closed forgets the connection whether or not the host's loop ever applies the report.
// mutation-checked: a hostEventBus.closed that keeps the ended connection in the emission's pending set leaves the emission unsettled, which the assertion reports within the test (synctest.Wait returns once every goroutine is blocked), not as a hang.
func TestQuiesceHeldBehindAReportIsReleasedWhenTheConnectionEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		releaseListener := sync.OnceFunc(func() { close(release) })
		defer releaseListener()
		rig := newReportOrderRig(t, func(*extension.ExtensionError) {
			close(entered)
			<-release
		})
		id, emission := rig.h.eventBus.newEmission(nil)
		rig.h.eventBus.addPending(emission, rig.conn, 1)
		rig.h.eventBus.endDispatch(emission)
		go rig.h.handleIncoming(rig.me, rig.conn)
		rig.write(t, reportFrame(t, "rejected later"), *quiesceFrame(id))
		<-entered
		synctest.Wait()
		if emissionSettled(emission) {
			t.Fatal("the emission settled while the report written before the realm's quiesce was still being applied")
		}
		_ = rig.peer.Close()
		synctest.Wait()
		if !emissionSettled(emission) {
			t.Fatal("the emission is still waiting after its connection ended: a quiesce held behind a report keeps the emitter waiting for ever")
		}
	})
}

// A run that is cancelled releases the emitter that waits for a held quiesce: its events.settle call returns the context's error.
// mutation-checked: a settle that does not select on its context's Done leaves the call waiting, which the assertion reports within the test.
func TestSettleWaitingForAHeldQuiesceReturnsWhenTheRunIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		releaseListener := sync.OnceFunc(func() { close(release) })
		defer releaseListener()
		rig := newReportOrderRig(t, func(*extension.ExtensionError) {
			close(entered)
			<-release
		})
		id, emission := rig.h.eventBus.newEmission(nil)
		rig.h.eventBus.addPending(emission, rig.conn, 1)
		rig.h.eventBus.endDispatch(emission)
		go rig.h.handleIncoming(rig.me, rig.conn)
		runCtx, cancelRun := context.WithCancel(t.Context())
		defer cancelRun()
		settled := make(chan error, 1)
		go func() { settled <- rig.h.eventBus.settle(runCtx, id) }()
		rig.write(t, reportFrame(t, "rejected later"), *quiesceFrame(id))
		<-entered
		synctest.Wait()
		select {
		case err := <-settled:
			t.Fatalf("the emitter's settle returned (%v) while the report written before the realm's quiesce was still being applied", err)
		default:
		}
		cancelRun()
		synctest.Wait()
		select {
		case err := <-settled:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("settle returned %v after the run was cancelled, want context.Canceled", err)
			}
		default:
			t.Fatal("the emitter's settle is still waiting after the run was cancelled")
		}
	})
}
