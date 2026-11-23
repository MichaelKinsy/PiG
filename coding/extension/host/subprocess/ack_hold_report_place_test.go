package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// A request with a state channel (withRequestSuspension or an inactivity lease) receives request_state frames on stateCh while readLoop continues reading later frames.
// A call frame read AFTER a non-host_call blocked report must not hold that earlier blocked report's acknowledgment when the request goroutine processes stateCh.
// Companion: a call frame read BEFORE the blocked report must still hold the acknowledgment until that call is applied.
// Out-of-order startsAsync: a post-report startsAsync call applied inline on handleIncoming ahead of an unapplied pre-report sync call must NOT decrement unapplied and prematurely release the acknowledgment.
func TestBlockedAcknowledgmentIgnoresCallReadAfterTheReport(t *testing.T) {
	writeFrame := func(w io.Writer, v Envelope) {
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

	t.Run("call read after blocked report does not hold acknowledgment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hostSide, extSide := net.Pipe()
			conn := NewConn("ack-after-report", hostSide)
			conn.Start(t.Context())
			go func() { _, _ = io.Copy(io.Discard, extSide) }()
			defer func() {
				_ = conn.Close("test done")
				_ = extSide.Close()
			}()

			dispatched := make(chan struct{})
			conn.onDispatch = func() { close(dispatched) }
			interject := make(chan func())
			acknowledged := 0
			ctx := invocation.WithAcknowledgment(t.Context(), func() { acknowledged++ })
			ctx = withRequestSuspension(ctx, func(bool) {}, nil)
			ctx = withRequestInterjections(ctx, interject)

			returned := make(chan struct{})
			go func() {
				_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
				close(returned)
			}()
			<-dispatched

			// Hold the request goroutine on an interjection so it cannot read stateCh while readLoop reads both frames.
			inInterject := make(chan struct{})
			releaseInterject := make(chan struct{})
			interject <- func() {
				close(inInterject)
				<-releaseInterject
			}
			<-inInterject

			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
			})
			writeFrame(extSide, Envelope{
				Type: MsgCall,
				ID:   "c1",
				Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r1"},
			})
			synctest.Wait()

			close(releaseInterject)
			synctest.Wait()

			if acknowledged != 1 {
				t.Fatalf("call read after the blocked report held its acknowledgment: acknowledged = %d, want 1", acknowledged)
			}
			writeFrame(extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
			synctest.Wait()
			<-returned
		})
	})

	t.Run("call read before blocked report still holds acknowledgment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			entered := make(chan struct{})
			releaseCall := make(chan struct{})
			h.SetCallHandler(func(_ string, _ *CallPayload) (*CallResultPayload, error) {
				close(entered)
				<-releaseCall
				return &CallResultPayload{}, nil
			})
			hostSide, extSide := net.Pipe()
			conn := NewConn("ack-before-report", hostSide)
			conn.Start(t.Context())
			me := withConn(&managedExt{config: ExtConfig{Name: "ack-before-report"}, host: h}, conn)
			me.shuttingDown.Store(true)
			go h.handleIncoming(me, conn)
			go func() { _, _ = io.Copy(io.Discard, extSide) }()
			defer func() {
				_ = conn.Close("test done")
				_ = extSide.Close()
			}()

			dispatched := make(chan struct{})
			conn.onDispatch = func() { close(dispatched) }
			acknowledged := 0
			ctx := invocation.WithAcknowledgment(t.Context(), func() { acknowledged++ })
			ctx = withRequestSuspension(ctx, func(bool) {}, nil)

			returned := make(chan struct{})
			go func() {
				_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
				close(returned)
			}()
			<-dispatched

			writeFrame(extSide, Envelope{
				Type: MsgCall,
				ID:   "c1",
				Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r1"},
			})
			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
			})
			<-entered
			synctest.Wait()

			if acknowledged != 0 {
				t.Fatalf("blocked report acknowledged before pre-report call applied: acknowledged = %d, want 0", acknowledged)
			}
			close(releaseCall)
			synctest.Wait()

			if acknowledged != 1 {
				t.Fatalf("applying pre-report call did not release acknowledgment: acknowledged = %d, want 1", acknowledged)
			}
			writeFrame(extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
			synctest.Wait()
			<-returned
		})
	})

	t.Run("post-report startsAsync call does not satisfy unapplied pre-report call", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			syncEntered := make(chan struct{})
			releaseSync := make(chan struct{})
			h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
				if call.Method == "ui.notify" {
					close(syncEntered)
					<-releaseSync
				}
				return &CallResultPayload{}, nil
			})
			hostSide, extSide := net.Pipe()
			conn := NewConn("ack-async-guard", hostSide)
			conn.Start(t.Context())
			me := withConn(&managedExt{config: ExtConfig{Name: "ack-async-guard"}, host: h}, conn)
			me.shuttingDown.Store(true)
			go h.handleIncoming(me, conn)
			go func() { _, _ = io.Copy(io.Discard, extSide) }()
			defer func() {
				_ = conn.Close("test done")
				_ = extSide.Close()
			}()

			dispatched := make(chan struct{})
			conn.onDispatch = func() { close(dispatched) }
			acknowledged := 0
			ctx := invocation.WithAcknowledgment(t.Context(), func() { acknowledged++ })
			ctx = withRequestSuspension(ctx, func(bool) {}, nil)

			returned := make(chan struct{})
			go func() {
				_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
				close(returned)
			}()
			<-dispatched

			// Pre-report sync call (ui.notify) is held in its call handler.
			writeFrame(extSide, Envelope{
				Type: MsgCall,
				ID:   "c1",
				Call: &CallPayload{Method: "ui.notify", ParentRequestID: "r1"},
			})
			<-syncEntered

			// Blocked report seals the hold while c1 is still unapplied.
			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
			})
			// Post-report startsAsync call (exec): handleIncoming applies startsAsync inline before c1 finishes.
			// Because c2 was read after the blocked report (ackCounted == false), it must NOT decrement unapplied for c1.
			writeFrame(extSide, Envelope{
				Type: MsgCall,
				ID:   "c2",
				Call: &CallPayload{Method: "exec", ParentRequestID: "r1"},
			})
			synctest.Wait()

			if acknowledged != 0 {
				t.Fatalf("post-report startsAsync call prematurely released acknowledgment held for pre-report call: acknowledged = %d, want 0", acknowledged)
			}
			close(releaseSync)
			synctest.Wait()

			if acknowledged != 1 {
				t.Fatalf("applying pre-report call did not release acknowledgment: acknowledged = %d, want 1", acknowledged)
			}
			writeFrame(extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
			synctest.Wait()
			<-returned
		})
	})

	t.Run("post-seal blocked host_call announcement and startsAsync call do not hold acknowledgment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			dialogEntered := make(chan struct{})
			releaseDialog := make(chan struct{})
			h.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
				if call.Method == "ui.confirm" {
					close(dialogEntered)
					<-releaseDialog
				}
				return &CallResultPayload{}, nil
			})
			hostSide, extSide := net.Pipe()
			conn := NewConn("ack-post-seal-announce", hostSide)
			conn.Start(t.Context())
			me := withConn(&managedExt{config: ExtConfig{Name: "ack-post-seal-announce"}, host: h}, conn)
			me.shuttingDown.Store(true)
			go h.handleIncoming(me, conn)
			go func() { _, _ = io.Copy(io.Discard, extSide) }()
			defer func() {
				close(releaseDialog)
				_ = conn.Close("test done")
				_ = extSide.Close()
			}()

			dispatched := make(chan struct{})
			conn.onDispatch = func() { close(dispatched) }
			interject := make(chan func())
			acknowledged := 0
			ctx := invocation.WithAcknowledgment(t.Context(), func() { acknowledged++ })
			ctx = withRequestSuspension(ctx, func(bool) {}, nil)
			ctx = withRequestInterjections(ctx, interject)

			returned := make(chan struct{})
			go func() {
				_, _ = conn.request(ctx, &Envelope{ID: "r1", Type: MsgRequest}, 0, "test")
				close(returned)
			}()
			<-dispatched

			// Hold the request goroutine before it reads stateCh so readLoop reads all four frames first:
			// blocked(external_io) ; progress ; blocked(host_call) ; call(ui.confirm, startsAsync).
			inInterject := make(chan struct{})
			releaseInterject := make(chan struct{})
			interject <- func() {
				close(inInterject)
				<-releaseInterject
			}
			<-inInterject

			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "external_io"},
			})
			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "progress"},
			})
			writeFrame(extSide, Envelope{
				Type:         MsgRequestState,
				RequestState: &RequestStatePayload{RequestID: "r1", State: "blocked", Reason: "host_call"},
			})
			writeFrame(extSide, Envelope{
				Type: MsgCall,
				ID:   "c1",
				Call: &CallPayload{Method: "ui.confirm", ParentRequestID: "r1"},
			})
			<-dialogEntered
			synctest.Wait()

			// Now let the request goroutine drain stateCh (NO further progress frame from the handler).
			close(releaseInterject)
			synctest.Wait()

			if acknowledged != 1 {
				t.Fatalf("post-seal blocked:host_call announcement held acknowledgment until handler progress: acknowledged = %d, want 1", acknowledged)
			}
			writeFrame(extSide, Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
			synctest.Wait()
			<-returned
		})
	})
}
