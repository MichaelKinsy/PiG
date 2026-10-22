package subprocess

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// ackHold orders a handler's invocation acknowledgment after the host call its first suspension report announces.
//
// An extension runtime reports the suspension of a handler that waits for a host call before it sends the call (ui.notify, setStatus and the like, which Pi
// applies inside the handler's synchronous part, and which a runtime sends as a call the handler waits for). The report admits the caller waiting for the
// handler's first suspension; the call is applied later, on its own lane. A caller that goes on at the report, such as the UI prompt drain that releases a
// dialog's answer, therefore runs ahead of the handler's own effect: the start event's notification could be applied after the end event's.
//
// The read loop announces each blocked-on-a-host-call report, the host reports each call applied, and the acknowledgment the report would have made waits for
// the announced calls. A call that never comes (the handler ended or went on) releases the acknowledgment at its next progress or completed report, or at the
// request's end.
type ackHold struct {
	mu        sync.Mutex
	ctx       context.Context
	announced int
	held      bool
}

type ackHoldKey struct{}

// withAckHold binds a hold to the request's context.
func withAckHold(ctx context.Context) context.Context {
	hold := &ackHold{}
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

// announce records a blocked-on-a-host-call report read from the connection, whose call follows it.
func (h *ackHold) announce() {
	h.mu.Lock()
	h.announced++
	h.mu.Unlock()
}

// applied records that the host applied a call of the request: the call a report announced, or one of the request that no report announced.
func (h *ackHold) applied() {
	h.mu.Lock()
	if h.announced > 0 {
		h.announced--
	}
	release := h.announced == 0 && h.held
	if release {
		h.held = false
	}
	h.mu.Unlock()
	if release {
		invocation.Acknowledge(h.ctx)
	}
}

// release ends the wait for the announced calls: the handler went on past them, or the request ended.
func (h *ackHold) release() {
	h.mu.Lock()
	h.announced = 0
	release := h.held
	h.held = false
	h.mu.Unlock()
	if release {
		invocation.Acknowledge(h.ctx)
	}
}

// acknowledgeBlocked is the acknowledgment of a blocked report the request's goroutine applies: at once, or, while a call a blocked-on-a-host-call report
// announced has not applied, once it did. A Node runtime reports the handler's wait for that call as an external wait too, so the reason does not matter.
func acknowledgeBlocked(ctx context.Context) {
	hold := ackHoldOf(ctx)
	if hold == nil {
		invocation.Acknowledge(ctx)
		return
	}
	hold.mu.Lock()
	if hold.announced > 0 {
		hold.held = true
		hold.mu.Unlock()
		return
	}
	hold.mu.Unlock()
	invocation.Acknowledge(ctx)
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

// hostCallApplied reports that the host applied a call of the request: its effect is written, or the call started.
func (c *Conn) hostCallApplied(requestID string) {
	if hold := c.requestAckHold(requestID); hold != nil {
		hold.applied()
	}
}
