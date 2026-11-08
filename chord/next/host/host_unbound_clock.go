package host

import (
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/host/unbound-clock.ts. The `unbound_long` report (decision "Mistakes that wait are reported at
// once" (c)): a request waiting unbound after the host's publication (a type or registry not served here, not at its
// version, a registry whose first `compose` threw) is reported once through `onError` after 5 s, naming what it waits for
// and why; it keeps waiting as before. One clock per host, one timer armed for the earliest wait not reported yet: no
// timer per request. It is used inside the host's sections.

// unboundReportedAfter is how long a request waits unbound before it is reported (decided: 5 s).
const unboundReportedAfter = 5 * time.Second

// unboundLong is what the clock reports: a request waiting unbound for waitingFor since since (`Date.now()` time, ms), and
// why.
type unboundLong struct {
	waitingFor string
	err        *data.ChordError
	since      int64
}

// unboundWait is one wait not reported yet: what it waits for, its latest error, since when.
type unboundWait struct {
	request    any
	waitingFor string
	err        *data.ChordError
	since      int64
}

// unboundClock is a host's waits not reported yet and the one timer of their sweep.
type unboundClock struct {
	loop   eventLoop
	report func(unboundLong)
	// order is the requests in the order they started, so by since: the sweep reads from the front and stops at the first
	// due later.
	order unboundSet[any]
	by    map[any]*unboundWait
	timer Timer
}

func newUnboundClock(loop eventLoop, report func(unboundLong)) *unboundClock {
	return &unboundClock{loop: loop, report: report, by: map[any]*unboundWait{}}
}

// Start puts request on the clock, waiting for waitingFor with err: reported after 5 s unless it stops before.
func (c *unboundClock) Start(request any, waitingFor string, err *data.ChordError) {
	if _, ok := c.by[request]; ok {
		return
	}
	wait := &unboundWait{request: request, waitingFor: waitingFor, err: err, since: c.loop.Now()}
	c.by[request] = wait
	c.order.add(request)
	if c.timer == nil {
		c.timer = c.loop.AfterFunc(unboundReportedAfter, c.sweep)
	}
}

// Failed is the wait of request having a new error (a registry's failure changed): the report names the latest.
func (c *unboundClock) Failed(request any, err *data.ChordError) {
	if wait := c.by[request]; wait != nil {
		wait.err = err
	}
}

// Stop is request bound, ended or woken: no report for this wait.
func (c *unboundClock) Stop(request any) {
	wait := c.by[request]
	if wait == nil {
		return
	}
	delete(c.by, request)
	c.order.delete(request)
	if c.order.size() == 0 {
		c.stopTimer()
	}
}

// Close is the host closing: nothing is reported any more.
func (c *unboundClock) Close() {
	c.order = unboundSet[any]{}
	clear(c.by)
	c.stopTimer()
}

func (c *unboundClock) stopTimer() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// sweep reports every wait due, each once, then arms the timer for the next one, if any.
func (c *unboundClock) sweep() {
	c.timer = nil
	now := c.loop.Now()
	for c.order.size() > 0 {
		wait := c.by[c.order.head.item]
		due := wait.since + unboundReportedAfter.Milliseconds()
		if due > now {
			c.timer = c.loop.AfterFunc(time.Duration(due-now)*time.Millisecond, c.sweep)
			return
		}
		c.order.delete(wait.request)
		delete(c.by, wait.request)
		c.report(unboundLong{waitingFor: wait.waitingFor, err: wait.err, since: wait.since})
	}
}
