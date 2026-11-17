package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
