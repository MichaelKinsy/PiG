// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func writeSubprocessFrame(t *testing.T, w io.Writer, v Envelope) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := w.Write(append(length[:], data...)); err != nil {
		t.Fatal(err)
	}
}

// TestPostAckGateHoldsPostBlockedCallsOnly verifies that:
//   - a pre-blocked call (synchronous prefix) of an unawaited event runs at once even while the post-ack gate is closed;
//   - post-blocked calls of all seven postAckOrderedCalls wait off readLoop until the gate closes;
//   - non-postAckOrderedCalls (e.g. setActiveTools) run immediately even after blocked while the gate is closed;
//   - readLoop is never stalled while a post-blocked call waits (another request frame is read and answered while the gate holds);
//   - a second handler dispatched with an already-acknowledged invocation context starts sealed so its prefix call also waits for the gate.
func TestPostAckGateHoldsPostBlockedCallsOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		var mu sync.Mutex
		var applied []string
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			mu.Lock()
			applied = append(applied, call.Method+":"+call.ParentRequestID)
			mu.Unlock()
			return &CallResultPayload{}, nil
		})
		gate := make(chan struct{})
		h.SetPostAckGate(func(event string) <-chan struct{} {
			if event == "session_info_changed" {
				return gate
			}
			return nil
		})

		hostSide, extSide := net.Pipe()
		conn := NewConn("post-ack-gate", hostSide)
		conn.postAckGate = &h.postAckGate
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "post-ack-gate"}, host: h}, conn)
		me.shuttingDown.Store(true)
		h.goHandleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
			_ = conn.Close("test done")
			_ = extSide.Close()
			h.Shutdown("test done")
		}()

		dispatched := make(chan struct{})
		conn.onDispatch = func() {
			select {
			case <-dispatched:
			default:
				close(dispatched)
			}
		}
		var acked atomic.Int32
		invCtx := invocation.WithAcknowledgment(t.Context(), func() { acked.Add(1) })
		reqDone := make(chan struct{})
		go func() {
			_, _ = conn.request(invCtx, &Envelope{
				ID:      "r1",
				Type:    MsgRequest,
				Request: &RequestPayload{Method: "event", Event: "session_info_changed"},
			}, 0, "event session_info_changed")
			close(reqDone)
		}()
		<-dispatched

		// Pre-blocked prefix call applies immediately even though gate is still closed.
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall,
			ID:   "c-prefix",
			Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r1"},
		})
		synctest.Wait()
		mu.Lock()
		gotPrefix := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(gotPrefix, []string{"ui.notify:r1"}) {
			t.Fatalf("pre-blocked prefix call did not run immediately: applied=%v", gotPrefix)
		}

		// Handler suspends on external I/O -> readLoop seals hold and acknowledges.
		writeSubprocessFrame(t, extSide, Envelope{
			Type:         MsgRequestState,
			RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
		})
		synctest.Wait()
		if acked.Load() != 1 {
			t.Fatalf("blocked report did not acknowledge: acked=%d", acked.Load())
		}

		// Second handler on the same emission sees invocation.Acknowledged(invCtx)==true -> starts sealed.
		req2Done := make(chan struct{})
		go func() {
			_, _ = conn.request(invCtx, &Envelope{
				ID:      "r2",
				Type:    MsgRequest,
				Request: &RequestPayload{Method: "event", Event: "session_info_changed"},
			}, 0, "event session_info_changed")
			close(req2Done)
		}()
		synctest.Wait()

		// Post-blocked calls for all 7 postAckOrderedCalls on r1, plus a prefix call on already-acknowledged r2.
		ordered := []string{"ui.notify", "ui.setStatus", "ui.setWidget", "ui.setTitle", "ui.setEditorText", "ui.pasteToEditor", "appendEntry"}
		for _, method := range ordered {
			writeSubprocessFrame(t, extSide, Envelope{
				Type: MsgCall,
				ID:   "c-post-" + method,
				Call: &CallPayload{Method: method, ParentRequestID: "r1"},
			})
		}
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall,
			ID:   "c-r2-prefix",
			Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r2"},
		})
		// Unrelated request r3 and non-postAckOrderedCalls frame prove readLoop is never stalled.
		req3Done := make(chan struct{})
		go func() {
			_, _ = conn.request(t.Context(), &Envelope{
				ID:      "r3",
				Type:    MsgRequest,
				Request: &RequestPayload{Method: "event", Event: "other_event"},
			}, 0, "event other_event")
			close(req3Done)
		}()
		synctest.Wait()
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall,
			ID:   "c-r3",
			Call: &CallPayload{Method: "setActiveTools", ParentRequestID: "r3"},
		})
		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r3", Response: &ResponsePayload{}})
		synctest.Wait()
		<-req3Done

		mu.Lock()
		whileClosed := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(whileClosed, []string{"ui.notify:r1", "setActiveTools:r3"}) {
			t.Fatalf("post-ack calls ran while gate was closed (or unrelated call stalled): applied=%v", whileClosed)
		}

		// Close gate -> all 7 post-ack calls on r1 plus r2 prefix call run.
		close(gate)
		synctest.Wait()

		mu.Lock()
		afterOpen := slices.Clone(applied)
		mu.Unlock()
		var r1PostAck []string
		for _, item := range afterOpen {
			if strings.HasSuffix(item, ":r1") {
				r1PostAck = append(r1PostAck, strings.TrimSuffix(item, ":r1"))
			}
		}
		wantR1 := append([]string{"ui.notify"}, ordered...)
		if !slices.Equal(r1PostAck, wantR1) {
			t.Fatalf("r1 call order = %v, want exact order %v (all=%v)", r1PostAck, wantR1, afterOpen)
		}
		if !slices.Contains(afterOpen, "ui.notify:r2") {
			t.Fatalf("missing second-handler prefix call ui.notify:r2 after gate opened; applied=%v", afterOpen)
		}

		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r2", Response: &ResponsePayload{}})
		synctest.Wait()
		<-reqDone
		<-req2Done
	})
}

// TestPostAckGateSlotCallDoesNotDeadlockPrefixSlotCall proves stall case 3:
// a gated post-ack ui.setWidget on request 1 waits BEFORE registering in slotCalls, so a later request 2 prefix ui.setWidget
// applies immediately without waiting for request 1 gate (which in a batch waits for request 2 prefix to finish).
func TestPostAckGateSlotCallDoesNotDeadlockPrefixSlotCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		var mu sync.Mutex
		var applied []string
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			mu.Lock()
			applied = append(applied, call.Method+":"+call.ParentRequestID)
			mu.Unlock()
			return &CallResultPayload{}, nil
		})
		gate := make(chan struct{})
		h.SetPostAckGate(func(event string) <-chan struct{} {
			if event == "session_info_changed" {
				return gate
			}
			return nil
		})

		hostSide, extSide := net.Pipe()
		conn := NewConn("post-ack-slot", hostSide)
		conn.postAckGate = &h.postAckGate
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "post-ack-slot"}, host: h}, conn)
		me.shuttingDown.Store(true)
		h.goHandleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
			_ = conn.Close("test done")
			_ = extSide.Close()
			h.Shutdown("test done")
		}()

		inv1 := invocation.WithAcknowledgment(t.Context(), func() {})
		r1Done := make(chan struct{})
		go func() {
			_, _ = conn.request(inv1, &Envelope{
				ID:      "r1",
				Type:    MsgRequest,
				Request: &RequestPayload{Method: "event", Event: "session_info_changed"},
			}, 0, "event session_info_changed")
			close(r1Done)
		}()
		synctest.Wait()

		// r1 blocks -> post-ack ui.setWidget arrives while gate is closed.
		writeSubprocessFrame(t, extSide, Envelope{
			Type:         MsgRequestState,
			RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
		})
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall,
			ID:   "w1",
			Call: &CallPayload{Method: "ui.setWidget", ParentRequestID: "r1"},
		})
		synctest.Wait()

		// r2 starts (simulating command 2 in the same stdin batch) and makes a prefix ui.setWidget before its blocked report.
		r2Acked := make(chan struct{})
		inv2 := invocation.WithAcknowledgment(t.Context(), func() { close(r2Acked) })
		r2Done := make(chan struct{})
		go func() {
			_, _ = conn.request(inv2, &Envelope{
				ID:      "r2",
				Type:    MsgRequest,
				Request: &RequestPayload{Method: "event", Event: "thinking_level_select"},
			}, 0, "event thinking_level_select")
			close(r2Done)
		}()
		synctest.Wait()

		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall,
			ID:   "w2",
			Call: &CallPayload{Method: "ui.setWidget", ParentRequestID: "r2"},
		})
		writeSubprocessFrame(t, extSide, Envelope{
			Type:         MsgRequestState,
			RequestState: &RequestStatePayload{RequestID: "r2", State: "blocked", Reason: "external_io"},
		})
		synctest.Wait()

		// r2 prefix ui.setWidget MUST have applied and released r2 ackHold while r1 gate is still closed.
		select {
		case <-r2Acked:
		default:
			t.Fatal("r2 prefix ui.setWidget was blocked by r1 gated post-ack ui.setWidget")
		}
		mu.Lock()
		beforeOpen := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(beforeOpen, []string{"ui.setWidget:r2"}) {
			t.Fatalf("applied before gate opened = %v, want [ui.setWidget:r2]", beforeOpen)
		}

		// Now gate opens (command replies written) -> r1 post-ack ui.setWidget applies.
		close(gate)
		synctest.Wait()

		mu.Lock()
		afterOpen := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(afterOpen, []string{"ui.setWidget:r2", "ui.setWidget:r1"}) {
			t.Fatalf("applied after gate opened = %v, want [ui.setWidget:r2 ui.setWidget:r1]", afterOpen)
		}

		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r2", Response: &ResponsePayload{}})
		synctest.Wait()
		<-r1Done
		<-r2Done
	})
}

func waitPostAckGateState(t *testing.T, wantParked bool, desc string) {
	t.Helper()
	deadline := time.After(testbudget.Wait(t))
	buf := make([]byte, 1<<20)
	for {
		parked := false
		for g := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, "(*Host).awaitPostAckGate(") {
				parked = true
				break
			}
		}
		if parked == wantParked {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s (wantParked=%t, parked=%t)", desc, wantParked, parked)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestPostAckGateReleasesOnCancelAndShutdown verifies that a stuck post-ack gate (reply never written)
// releases promptly with zero leaked goroutines on request cancellation, connection close, and Host.Shutdown,
// for both normal calls (ui.notify) and replaceOnlySlot calls (ui.setWidget).
func TestPostAckGateReleasesOnCancelAndShutdown(t *testing.T) {
	for _, method := range []string{"ui.notify", "ui.setWidget"} {
		for _, mode := range []string{"request-cancel", "conn-close", "host-shutdown"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				baseline := testenv.BaselineGoroutines()
				h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
				var called atomic.Int32
				h.SetCallHandler(func(_ string, _ *CallPayload) (*CallResultPayload, error) {
					called.Add(1)
					return &CallResultPayload{}, nil
				})
				// A gate that never closes (simulates a reply that is never written).
				stuckGate := make(chan struct{})
				h.SetPostAckGate(func(string) <-chan struct{} { return stuckGate })

				hostSide, extSide := net.Pipe()
				conn := NewConn("post-ack-release", hostSide)
				conn.postAckGate = &h.postAckGate
				conn.Start(t.Context())
				me := withConn(&managedExt{config: ExtConfig{Name: "post-ack-release"}, host: h}, conn)
				me.shuttingDown.Store(true)
				h.mu.Lock()
				h.exts["post-ack-release"] = me
				h.mu.Unlock()
				h.goHandleIncoming(me, conn)

				readDone := make(chan struct{})
				go func() {
					defer close(readDone)
					_, _ = io.Copy(io.Discard, extSide)
				}()
				defer func() {
					_ = extSide.Close()
					h.Shutdown("cleanup")
					<-readDone
				}()

				reqCtx, cancelReq := context.WithCancel(t.Context())
				defer cancelReq()
				invCtx := invocation.WithAcknowledgment(reqCtx, func() {})
				dispatched := make(chan struct{})
				conn.onDispatch = func() { close(dispatched) }
				reqDone := make(chan struct{})
				go func() {
					_, _ = conn.request(invCtx, &Envelope{
						ID:      "r1",
						Type:    MsgRequest,
						Request: &RequestPayload{Method: "event", Event: "session_info_changed"},
					}, 0, "event session_info_changed")
					close(reqDone)
				}()
				waitSignal(t, dispatched, "request dispatch")

				writeSubprocessFrame(t, extSide, Envelope{
					Type:         MsgRequestState,
					RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
				})
				writeSubprocessFrame(t, extSide, Envelope{
					Type: MsgCall,
					ID:   "c1",
					Call: &CallPayload{Method: method, ParentRequestID: "r1"},
				})
				// Confirm the call worker actually reached awaitPostAckGate BEFORE triggering teardown.
				waitPostAckGateState(t, true, "call worker parked in awaitPostAckGate")

				switch mode {
				case "request-cancel":
					cancelReq()
					waitSignal(t, reqDone, "cancelled request return")
					// Observe the held call worker exit awaitPostAckGate while conn is still open and stuckGate is still closed.
					waitPostAckGateState(t, false, "held call worker exit after request cancellation while conn open")
					_ = conn.Close("done")
				case "conn-close":
					_ = conn.Close("closed")
					waitSignal(t, reqDone, "closed-conn request return")
				case "host-shutdown":
					shutDone := make(chan struct{})
					go func() {
						h.Shutdown("shutdown")
						close(shutDone)
					}()
					select {
					case <-shutDone:
					case <-time.After(testbudget.Wait(t)):
						t.Fatal("Host.Shutdown hung on stuck post-ack gate")
					}
					waitSignal(t, reqDone, "shutdown request return")
				}

				_ = extSide.Close()
				h.Shutdown("final")
				waitSignal(t, readDone, "ext reader exit")
				if called.Load() != 0 {
					t.Fatalf("cancelled post-ack call ran handler %d times, want 0", called.Load())
				}
				baseline.RequireEnded(t, testbudget.Wait(t))
			})
		}
	}
}

// TestPostAckGateSecondHandlerAfterPlainCompleteStartsUnsealed verifies review Finding #2:
// when the first handler finishes synchronously via invocation.Complete (without blocking),
// a second handler in the same emission must NOT start sealed — its synchronous prefix ui.notify
// runs ahead of the reply, matching Pi 1.6.0.
func TestPostAckGateSecondHandlerAfterPlainCompleteStartsUnsealed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		var mu sync.Mutex
		var applied []string
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			mu.Lock()
			applied = append(applied, call.Method+":"+call.ParentRequestID)
			mu.Unlock()
			return &CallResultPayload{}, nil
		})
		gate := make(chan struct{})
		h.SetPostAckGate(func(event string) <-chan struct{} {
			if event == "session_info_changed" {
				return gate
			}
			return nil
		})

		hostSide, extSide := net.Pipe()
		conn := NewConn("post-ack-complete", hostSide)
		conn.postAckGate = &h.postAckGate
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "post-ack-complete"}, host: h}, conn)
		me.shuttingDown.Store(true)
		h.goHandleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			close(gate)
			_ = conn.Close("test done")
			_ = extSide.Close()
			h.Shutdown("test done")
		}()

		dispatchCtx := invocation.WithAcknowledgment(t.Context(), func() {})
		req1Done := make(chan struct{})
		go func() {
			defer close(req1Done)
			_, _ = conn.Request(dispatchCtx, &Envelope{
				Type: MsgRequest, ID: "r1",
				Request: &RequestPayload{Event: "session_info_changed"},
			})
		}()
		synctest.Wait()
		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		synctest.Wait()
		<-req1Done

		if !invocation.Acknowledged(dispatchCtx) {
			t.Fatal("expected dispatchCtx to be Acknowledged after handler 1 completed")
		}
		if invocation.AcknowledgedBySuspension(dispatchCtx) {
			t.Fatal("handler 1 completed without suspension; AcknowledgedBySuspension must be false")
		}

		req2Done := make(chan struct{})
		go func() {
			defer close(req2Done)
			_, _ = conn.Request(dispatchCtx, &Envelope{
				Type: MsgRequest, ID: "r2",
				Request: &RequestPayload{Event: "session_info_changed"},
			})
		}()
		synctest.Wait()
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall, ID: "c-r2-prefix",
			Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r2"},
		})
		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r2", Response: &ResponsePayload{}})
		synctest.Wait()
		<-req2Done

		mu.Lock()
		got := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(got, []string{"ui.notify:r2"}) {
			t.Fatalf("second handler prefix ui.notify after plain completion was held behind gate: applied=%v, want [ui.notify:r2]", got)
		}
	})
}

// TestPostAckGateNonGatedCallUnblocksEarlierHeldCallOnSameLane verifies review Finding #1
// at the subprocess layer: when a sealed request sends a post-ack fire-and-forget ui.setStatus
// (held on gate) followed by a call requiring a host response (getContextUsage), the arrival
// of getContextUsage closes the per-request afterAckCancel channel so ui.setStatus and then
// getContextUsage both run immediately without deadlocking the lane while gate stays closed.
func TestPostAckGateNonGatedCallUnblocksEarlierHeldCallOnSameLane(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		var mu sync.Mutex
		var applied []string
		h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			mu.Lock()
			applied = append(applied, call.Method+":"+call.ParentRequestID)
			mu.Unlock()
			return &CallResultPayload{}, nil
		})
		gate := make(chan struct{})
		h.SetPostAckGate(func(event string) <-chan struct{} {
			if event == "session_info_changed" {
				return gate
			}
			return nil
		})

		hostSide, extSide := net.Pipe()
		conn := NewConn("post-ack-unblock", hostSide)
		conn.postAckGate = &h.postAckGate
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "post-ack-unblock"}, host: h}, conn)
		me.shuttingDown.Store(true)
		h.goHandleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			close(gate)
			_ = conn.Close("test done")
			_ = extSide.Close()
			h.Shutdown("test done")
		}()

		dispatchCtx := invocation.WithAcknowledgment(t.Context(), func() {})
		reqDone := make(chan struct{})
		go func() {
			defer close(reqDone)
			_, _ = conn.Request(dispatchCtx, &Envelope{
				Type: MsgRequest, ID: "r1",
				Request: &RequestPayload{Event: "session_info_changed"},
			})
		}()
		synctest.Wait()
		writeSubprocessFrame(t, extSide, Envelope{
			Type:         MsgRequestState,
			RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external"},
		})
		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall, ID: "c-status",
			Call: &CallPayload{Method: "ui.setStatus", ParentRequestID: "r1"},
		})
		synctest.Wait()

		mu.Lock()
		beforeSync := slices.Clone(applied)
		mu.Unlock()
		if len(beforeSync) != 0 {
			t.Fatalf("ui.setStatus ran while gate closed: %v", beforeSync)
		}

		writeSubprocessFrame(t, extSide, Envelope{
			Type: MsgCall, ID: "c-usage",
			Call: &CallPayload{Method: "getContextUsage", ParentRequestID: "r1"},
		})
		synctest.Wait()

		mu.Lock()
		afterSync := slices.Clone(applied)
		mu.Unlock()
		if !slices.Equal(afterSync, []string{"ui.setStatus:r1", "getContextUsage:r1"}) {
			t.Fatalf("after sync call on same lane, applied = %v, want [ui.setStatus:r1 getContextUsage:r1]", afterSync)
		}

		writeSubprocessFrame(t, extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		synctest.Wait()
		<-reqDone
	})
}
