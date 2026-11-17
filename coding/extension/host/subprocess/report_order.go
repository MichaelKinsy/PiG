package subprocess

import (
	"slices"
	"sync"
	"sync/atomic"
)

// reportOrder keeps the order Pi's synchronous runtime.reportError has: the error of a handler is reported before the handler's request is answered
// (loader.ts:537-549 -> runner.ts:749-753, and the command's response is written after its handler returned, agent-session.ts:2172-2178).
//
// A runtime writes a report_error notification before the response of the request whose handler failed, on one socket, but the read loop hands a
// response straight to its waiter while a notification goes through inCh to the host's loop, so the response could overtake the report. The read loop
// therefore counts each report it passes to the host (read) until the host's loop has applied it (applied), and a response it reads meanwhile for a
// request that was in flight when the report was read is queued through inCh behind the report instead (holds): the host's loop delivers it
// (Conn.deliverDeferredResponse) once the report was applied. With no report unapplied, which is nearly always, a response costs one atomic load.
//
// A request the host sent after the oldest unapplied report was read is never deferred behind it: the error listener that applies the report may
// itself call the extension and wait for the answer, and that answer would wait for the listener (pig additive; see NotifyReportError).
type reportOrder struct {
	// unapplied is the number of reports the read loop passed to the host that the host has not applied yet.
	unapplied atomic.Int32
	mu        sync.Mutex
	// behind holds, for each unapplied report, oldest first, the IDs of the requests in flight when the read loop read it.
	behind [][]string
}

// read records a report the read loop is about to pass to the host. inFlight is the IDs of the requests the host has sent and not seen answered; it is owned by the order from now on.
func (o *reportOrder) read(inFlight []string) {
	o.mu.Lock()
	o.behind = append(o.behind, inFlight)
	o.mu.Unlock()
	o.unapplied.Add(1)
}

// applied records that the host's loop applied (or dropped) the oldest report the read loop passed to it.
func (o *reportOrder) applied() {
	o.mu.Lock()
	if len(o.behind) > 0 {
		o.behind[0] = nil
		o.behind = o.behind[1:]
	}
	o.mu.Unlock()
	o.unapplied.Add(-1)
}

// pending reports whether a report has been read and not applied.
func (o *reportOrder) pending() bool { return o.unapplied.Load() != 0 }

// holds reports whether the response of request id must wait for the reports read before it: the request was in flight when the oldest unapplied report was read.
func (o *reportOrder) holds(id string) bool {
	if !o.pending() {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.behind) > 0 && slices.Contains(o.behind[0], id)
}

// noteReportRead is called on the read loop for each report_error notification before it is queued for the host.
func (c *Conn) noteReportRead() {
	c.pendingMu.Lock()
	inFlight := make([]string, 0, len(c.pending))
	for id := range c.pending {
		inFlight = append(inFlight, id)
	}
	c.pendingMu.Unlock()
	c.reports.read(inFlight)
}

// reportApplied is called by the goroutine that took a report_error notification from Incoming once it applied or dropped it.
func (c *Conn) reportApplied() { c.reports.applied() }

// deliverDeferredResponse delivers env when it is a response the read loop queued behind a report (holds), and reports whether it was one. The goroutine that reads Incoming calls it for every frame, before the report it waited for is no longer ahead of it.
func (c *Conn) deliverDeferredResponse(env *Envelope) bool {
	if env.Type != MsgResponse && env.Type != MsgCallResult {
		return false
	}
	c.deliverResponse(env.ID, env)
	return true
}
