package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// A runtime sends a command's suspension, then its response. The host applies the suspension after the calls received before it, while a response is routed directly. The request must not return before the suspension it followed was applied, or a command that Pi's exit would have cut off answers after stdin ended.
func TestResponseWaitsForTheSuspensionThatPrecededIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, _ *CallPayload) (*CallResultPayload, error) {
			close(entered)
			<-release
			return &CallResultPayload{}, nil
		})
		hostSide, extSide := net.Pipe()
		conn := NewConn("response-order", hostSide)
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "response-order"}, host: h}, conn)
		me.shuttingDown.Store(true)
		go h.handleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		suspended := false
		returned := make(chan struct{})
		ctx := withRequestSuspension(t.Context(), func(bool) { suspended = true }, nil)
		go func() {
			_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
			close(returned)
		}()
		<-dispatched
		writeFrame := func(v Envelope) {
			data, _ := json.Marshal(v)
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			_, _ = extSide.Write(append(length[:], data...))
		}
		writeFrame(Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "exec", ParentRequestID: "r1"}})
		writeFrame(Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "suspended"}})
		writeFrame(Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		<-entered
		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("the request returned before the suspension that preceded its response was applied")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("the request never returned")
		}
		if !suspended {
			t.Error("the response was delivered without the suspension that preceded it")
		}
	})
}

// A blocked frame that the request had not yet read when its response arrived still admits the caller that waits for the handler's first suspension, since the response of a suspended command can be held.
func TestQueuedBlockedStateAcknowledgesTheInvocationWhenTheResponseWins(t *testing.T) {
	acknowledged := 0
	ctx := invocation.WithSuspensionAcknowledgment(t.Context(), func() { acknowledged++ })
	stateCh := make(chan RequestStatePayload, 8)
	stateCh <- RequestStatePayload{RequestID: "r1", State: "blocked"}
	if drainRequestStates(ctx, stateCh) {
		t.Fatal("a blocked frame reported a suspension")
	}
	if acknowledged != 1 {
		t.Fatalf("invocation acknowledged %d times, want 1", acknowledged)
	}
}

// Before stdin ends a suspension cannot change what a command's response does, so the response must not wait for the calls that the suspension's delivery waits for. Those include calls of other requests on the connection, such as a tool's exec or model stream, which the host counts as applied only when they finish. Pi completes the command when its handler settles (agent-session.ts prompt: preflightResult after _tryExecuteExtensionCommand).
func TestResponseBeforeInputEndDoesNotWaitForAnotherRequestsCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, _ *CallPayload) (*CallResultPayload, error) {
			close(entered)
			<-release
			return &CallResultPayload{}, nil
		})
		hostSide, extSide := net.Pipe()
		conn := NewConn("response-order", hostSide)
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "response-order"}, host: h}, conn)
		me.shuttingDown.Store(true)
		go h.handleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			close(release)
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		// t1 is another request in flight on the connection, such as a tool whose exec is running.
		toolCtx, cancelTool := context.WithCancel(t.Context())
		defer cancelTool()
		toolDispatched := make(chan struct{})
		conn.onDispatch = func() { close(toolDispatched) }
		go func() { _, _ = conn.request(toolCtx, &Envelope{ID: "t1", Type: MsgRequest}, 0, "tool") }()
		<-toolDispatched
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		flight := h.beginCommandFlight(me)
		defer h.endCommandFlight(flight)
		returned := make(chan struct{})
		ctx := h.withCommandSuspension(t.Context(), flight)
		go func() {
			_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
			close(returned)
		}()
		<-dispatched
		writeFrame := func(v Envelope) {
			data, _ := json.Marshal(v)
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			_, _ = extSide.Write(append(length[:], data...))
		}
		writeFrame(Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "exec", ParentRequestID: "t1"}})
		<-entered
		writeFrame(Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "suspended"}})
		writeFrame(Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("before stdin ended, the command's response waited for another request's call")
		}
	})
}

// After stdin ends the suspension decides whether the command's response is held, so the response waits for it even behind another request's call.
func TestResponseAfterInputEndWaitsForItsSuspension(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		entered := make(chan struct{})
		release := make(chan struct{})
		h.SetCallHandler(func(_ string, _ *CallPayload) (*CallResultPayload, error) {
			close(entered)
			<-release
			return &CallResultPayload{}, nil
		})
		hostSide, extSide := net.Pipe()
		conn := NewConn("response-order", hostSide)
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "response-order"}, host: h}, conn)
		me.shuttingDown.Store(true)
		go h.handleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		// t1 is another request in flight on the connection, such as a tool whose exec is running.
		toolCtx, cancelTool := context.WithCancel(t.Context())
		defer cancelTool()
		toolDispatched := make(chan struct{})
		conn.onDispatch = func() { close(toolDispatched) }
		go func() { _, _ = conn.request(toolCtx, &Envelope{ID: "t1", Type: MsgRequest}, 0, "tool") }()
		<-toolDispatched
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		flight := h.beginCommandFlight(me)
		defer h.endCommandFlight(flight)
		h.EndInput()
		returned := make(chan struct{})
		ctx := h.withCommandSuspension(t.Context(), flight)
		go func() {
			_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
			close(returned)
		}()
		<-dispatched
		writeFrame := func(v Envelope) {
			data, _ := json.Marshal(v)
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			_, _ = extSide.Write(append(length[:], data...))
		}
		writeFrame(Envelope{Type: MsgCall, ID: "c1", Call: &CallPayload{Method: "exec", ParentRequestID: "t1"}})
		<-entered
		writeFrame(Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "suspended"}})
		writeFrame(Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("after stdin ended, the command's response overtook its suspension")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("the request never returned")
		}
		if !h.heldAfterInputEnd(flight) {
			t.Error("the command's response was not held after stdin ended although it followed a suspension")
		}
	})
}

// A retired Node generation's host loop drops the suspension it no longer serves. The drop still settles the frame the read loop counted, or the response that follows it would wait for a delivery that never comes.
func TestResponseAfterADroppedSuspensionOfARetiredGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		h.mu.Lock()
		if h.packedCellGeneration == nil {
			h.packedCellGeneration = map[string]int{}
		}
		h.packedCellGeneration["retired"] = 2
		h.mu.Unlock()
		hostSide, extSide := net.Pipe()
		conn := NewConn("retired", hostSide)
		conn.Start(t.Context())
		me := withConn(&managedExt{config: ExtConfig{Name: "retired"}, host: h, packedProcess: &packedProcessState{node: true, key: "retired", generation: 1}}, conn)
		if h.acceptsNodeGeneration(me) {
			t.Fatal("the fixture generation is still accepted")
		}
		me.shuttingDown.Store(true)
		go h.handleIncoming(me, conn)
		go func() { _, _ = io.Copy(io.Discard, extSide) }()
		defer func() {
			_ = conn.Close("test done")
			_ = extSide.Close()
		}()
		dispatched := make(chan struct{})
		conn.onDispatch = func() { close(dispatched) }
		suspended := false
		returned := make(chan struct{})
		ctx := withRequestSuspension(t.Context(), func(bool) { suspended = true }, nil)
		go func() {
			_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
			close(returned)
		}()
		<-dispatched
		for _, frame := range []Envelope{
			{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: "r1", State: "suspended"}},
			{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}},
		} {
			data, _ := json.Marshal(frame)
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			_, _ = extSide.Write(append(length[:], data...))
		}
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("the response waited for a suspension the retired generation dropped")
		}
		if suspended {
			t.Error("a retired generation's suspension was applied")
		}
	})
}
