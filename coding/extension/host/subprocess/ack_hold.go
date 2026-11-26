package subprocess

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// ackHold orders a handler's invocation acknowledgment after the host calls the handler sent before the report that admits its caller.
//
// Pi applies a handler's synchronous host calls (ui.notify, setStatus, appendEntry and the like) inside the handler's synchronous part, before anything that
// runs after the handler's first await (runner.ts:1110). The caller waiting for the handler's first suspension, such as the RPC reply of set_session_name or the
// UI prompt drain that releases a dialog's answer, therefore goes on only after those effects. A runtime reports the suspension and sends the calls as frames, and
// the host applies each call later, on its own lane, so a caller released at the report would run ahead of the handler's own effect: the reply written before the
// notification the handler made first, or the start event's notification applied after the end event's.
//
// Two counts hold the acknowledgment the report would have made. A runtime that reports the suspension of a handler waiting for a host call before it sends the
// call (a Node runtime outside RPC mode) has the read loop announce the report, whose call follows it. The read loop also counts each call frame of the request it
// reads: the frames are read in the order the extension wrote them, so a call sent before the report is counted before the report is read, however the runtime
// orders its report and its calls (a Node runtime in RPC mode reports after the call). The host reports each call applied, and the acknowledgment waits until both
// counts are zero.
//
// What bounds the wait: a call that starts a Promise (startsAsync: a dialog, a model request, a child process) counts as applied once it is queued (queueCallDone), so it
// never holds the acknowledgment; every other call returns when its effect is written, so the wait lasts as long as the handler's own earlier effects take, as the handler's
// synchronous call blocks its caller in Pi. An announced call that never comes (the handler ended or went on) is released at the request's next progress or completed
// report, and the request's end releases all of it, so a request that ends, is cancelled or whose session closes does not leave its waiter held.
type ackHold struct {
	mu              sync.Mutex
	ctx             context.Context
	announced       int
	unapplied       int
	held            bool
	sealed          bool
	afterAck        <-chan struct{}
	postAckCancel   chan struct{}
	postAckReleased bool
}

type ackHoldKey struct{}

// withAckHold binds a hold to the request's context.
func withAckHold(ctx context.Context) context.Context {
	return withAckHoldGate(ctx, nil, false)
}

// withAckHoldGate binds a hold with an optional post-acknowledgment gate channel and initial sealed state (true when an earlier handler on the same emission already acknowledged).
func withAckHoldGate(ctx context.Context, afterAck <-chan struct{}, sealed bool) context.Context {
	var cancel chan struct{}
	if afterAck != nil {
		cancel = make(chan struct{})
	}
	hold := &ackHold{afterAck: afterAck, postAckCancel: cancel, sealed: sealed}
	ctx = context.WithValue(ctx, ackHoldKey{}, hold)
	hold.ctx = ctx
	return ctx
}

func ackHoldOf(ctx context.Context) *ackHold {
	if ctx == nil {
		return nil
	}
	hold, _ := ctx.Value(ackHoldKey{}).(*ackHold)
	return hold
}

// announce records a blocked-on-a-host-call report read before the hold was sealed, whose call follows it.
func (h *ackHold) announce() {
	h.mu.Lock()
	if !h.sealed {
		h.announced++
	}
	h.mu.Unlock()
}

// seal marks that readLoop has read the request's non-host_call blocked report, so later call frames are not counted for this hold.
func (h *ackHold) seal() {
	h.mu.Lock()
	h.sealed = true
	h.mu.Unlock()
}

// releasePostAckLocked closes postAckCancel once so held post-acknowledgment calls on this request's lane proceed immediately.
func (h *ackHold) releasePostAckLocked() {
	if h.postAckReleased {
		return
	}
	h.postAckReleased = true
	if h.postAckCancel != nil {
		close(h.postAckCancel)
	}
}

// sent records a call frame of the request read before the hold was sealed. It returns whether the call was counted, and for a post-seal fire-and-forget output call returns the hold's afterAck gate and per-request cancel channel.
// When a post-seal call is not in postAckOrderedCalls (the handler awaits or waits synchronously for the host's answer), releasing postAckCancel immediately unblocks any earlier held fire-and-forget call on the same request lane so the handler cannot deadlock waiting behind it.
func (h *ackHold) sent(method string) (bool, <-chan struct{}, <-chan struct{}) {
	h.mu.Lock()
	if h.sealed {
		if !postAckOrderedCalls[method] {
			h.releasePostAckLocked()
			h.mu.Unlock()
			return false, nil, nil
		}
		if h.postAckReleased {
			h.mu.Unlock()
			return false, nil, nil
		}
		afterAck, cancel := h.afterAck, h.postAckCancel
		h.mu.Unlock()
		return false, afterAck, cancel
	}
	h.unapplied++
	h.mu.Unlock()
	return true, nil, nil
}

// applied records that the host applied a call of the request: the call a report announced, or one of the request that no report announced. effect is true for a call
// whose upstream API returns nothing (holdsForLoopTurn: ui.notify, setStatus, appendEntry and the like): the handler does not wait for it, so the report that announced it
// says nothing of where the handler's synchronous part ends, and the handler's next progress report (wentOn) ends the wait for it. The calls the handler made after that one were
// written in the same turn, so they are read before that report and counted. Any other call, one the handler awaits, is the suspension its report names.
func (h *ackHold) applied(effect bool) {
	h.mu.Lock()
	if !effect && h.announced > 0 {
		h.announced--
	}
	if h.unapplied > 0 {
		h.unapplied--
	}
	release := h.announced == 0 && h.unapplied == 0 && h.held
	if release {
		h.held = false
	}
	h.mu.Unlock()
	if release {
		invocation.AcknowledgeSuspension(h.ctx)
	}
}

// wentOn ends the wait for the calls reports announced: the handler went on past them. The calls the read loop counted still apply in order, and a progress report
// that follows the first of two calls says nothing of the second.
func (h *ackHold) wentOn() {
	h.mu.Lock()
	h.announced = 0
	release := h.unapplied == 0 && h.held
	if release {
		h.held = false
	}
	h.mu.Unlock()
	if release {
		invocation.AcknowledgeSuspension(h.ctx)
	}
}

// release ends the wait for every call: the handler completed, or the request ended.
func (h *ackHold) release() {
	h.mu.Lock()
	h.announced = 0
	h.unapplied = 0
	release := h.held
	h.held = false
	h.releasePostAckLocked()
	h.mu.Unlock()
	if release {
		invocation.AcknowledgeSuspension(h.ctx)
	}
}

// acknowledgeBlocked is the acknowledgment of a blocked report the request's goroutine applies: at once, or, while a call the handler sent before the report (counted
// when it was read) or a call the report announced has not applied, once it did. A Node runtime reports the handler's wait for that call as an external wait too, so the
// reason does not matter.
func acknowledgeBlocked(ctx context.Context) {
	hold := ackHoldOf(ctx)
	if hold == nil {
		invocation.AcknowledgeSuspension(ctx)
		return
	}
	hold.mu.Lock()
	if hold.announced > 0 || hold.unapplied > 0 {
		hold.held = true
		hold.mu.Unlock()
		return
	}
	hold.mu.Unlock()
	invocation.AcknowledgeSuspension(ctx)
}

// requestAckHold is the hold of an in-flight request, nil for a request that is not (or no longer) waiting for a response.
func (c *Conn) requestAckHold(requestID string) *ackHold {
	c.stateMu.Lock()
	sink, ok := c.requestStates[requestID]
	c.stateMu.Unlock()
	if !ok {
		return nil
	}
	return ackHoldOf(sink.ctx)
}

// holdAckForCall counts a call frame the read loop read before its request's blocked report, until the host applies the call (hostCallApplied), and tags post-seal frames with the request's post-acknowledgment gate. A connection with no
// request that holds an acknowledgment, the common case, skips the lookup.
func (c *Conn) holdAckForCall(env *Envelope) {
	if env == nil || env.Call == nil || env.Call.ParentRequestID == "" || c.ackHolds.Load() == 0 {
		return
	}
	if hold := c.requestAckHold(env.Call.ParentRequestID); hold != nil {
		env.ackCounted, env.afterAck, env.afterAckCancel = hold.sent(env.Call.Method)
	}
}

// hostCallApplied reports that the host applied a call of the request: its effect is written, or the call started. effect is as for ackHold.applied.
func (c *Conn) hostCallApplied(requestID string, effect bool) {
	if hold := c.requestAckHold(requestID); hold != nil {
		hold.applied(effect)
	}
}
