package subprocess

import (
	"slices"
	"sync"
	"sync/atomic"
)

// reportOrder keeps the order Pi's synchronous runtime.reportError has: the error of a handler is reported before the handler's request is answered
// (loader.ts:537-549 -> runner.ts:749-753, and the command's response is written after its handler returned, agent-session.ts:2172-2178), and before
// the emitter whose emit ran the handler goes on (event-bus.ts:15-27, a rejection after an await included: its microtask runs before the emitter's).
//
// A runtime writes a report_error notification before the response of the request whose handler failed, and before the events.quiesce notification
// that releases the emitter of a rejected handler, on one socket. The read loop acts on a response (it routes it straight to its waiter) and on a
// quiesce (it counts it) itself, while a notification goes through inCh to the host's loop, so either could overtake the report. The read loop
// therefore counts each report it passes to the host (read) until the host's loop has applied it (applied), and a response or a quiesce it reads
// meanwhile is queued through inCh behind the report instead (holds, holdsEmission): the host's loop acts on it (Conn.applyHeld) once the report was
// applied. With no report unapplied, which is nearly always, a response or a quiesce costs one atomic load.
//
// What the report listener waits for is never held behind it: the listener may itself call the extension, or emit, and wait for the answer, and that
// answer would wait for the listener (pig additive; see NotifyReportError). A request the host sent after the oldest unapplied report was read is not
// held, and neither is the quiesce of an emission created after it.
//
// A frame held behind a report is released when the host's loop applied the report (applyHeld, in the order of inCh); when the connection ends (hostEventBus.closed
// removes the connection from every emission, so the emitter's settle goes on whether or not the loop ever applies the report); when the process is restarted
// (the old connection ends, and the new one starts with an order of its own); and when the run is cancelled (the emitter's settle returns its context's error). Nothing
// is on the wire: held and the emission mark are the read loop's own state.
type reportOrder struct {
	// unapplied is the number of reports the read loop passed to the host that the host has not applied yet.
	unapplied atomic.Int32
	mu        sync.Mutex
	// behind holds what each unapplied report must be ordered before, oldest first.
	behind []reportMark
}

// reportMark is what the read loop saw when it read a report.
type reportMark struct {
	// requests are the IDs of the requests the host had sent and not seen answered.
	requests []string
	// emissions is the number of events emissions the host had created.
	emissions uint64
}

// read records a report the read loop is about to pass to the host; the mark is owned by the order from now on.
func (o *reportOrder) read(mark reportMark) {
	o.mu.Lock()
	o.behind = append(o.behind, mark)
	o.mu.Unlock()
	o.unapplied.Add(1)
}

// applied records that the host's loop applied (or dropped) the oldest report the read loop passed to it.
func (o *reportOrder) applied() {
	o.mu.Lock()
	if len(o.behind) > 0 {
		o.behind[0] = reportMark{}
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
	return len(o.behind) > 0 && slices.Contains(o.behind[0].requests, id)
}

// holdsEmission reports whether the quiesce of emission n must wait for the reports read before it: the emission existed when the oldest unapplied report was read.
func (o *reportOrder) holdsEmission(n uint64) bool {
	if !o.pending() {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.behind) > 0 && n <= o.behind[0].emissions
}

// noteReportRead is called on the read loop for each report_error notification before it is queued for the host.
func (c *Conn) noteReportRead() {
	c.pendingMu.Lock()
	inFlight := make([]string, 0, len(c.pending))
	for id := range c.pending {
		inFlight = append(inFlight, id)
	}
	c.pendingMu.Unlock()
	mark := reportMark{requests: inFlight}
	if c.emissionsCreated != nil {
		mark.emissions = c.emissionsCreated()
	}
	c.reports.read(mark)
}

// reportApplied is called by the goroutine that took a report_error notification from Incoming once it applied or dropped it.
func (c *Conn) reportApplied() { c.reports.applied() }

// applyHeld acts on env when the read loop held it behind the reports read before it (Envelope.held), and reports whether it was one: a response goes to its waiter, a notification to onHeldNotify. The goroutine that reads Incoming calls it for every frame, so the frame is acted on after the report it waited for.
func (c *Conn) applyHeld(env *Envelope) bool {
	if !env.held {
		return false
	}
	switch env.Type {
	case MsgResponse, MsgCallResult:
		c.deliverResponse(env.ID, env)
	default:
		if c.onHeldNotify != nil {
			c.onHeldNotify(env)
		}
	}
	return true
}
