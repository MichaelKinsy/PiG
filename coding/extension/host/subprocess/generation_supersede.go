package subprocess

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/sortx"
)

// unacknowledgedGeneration is a generation whose Node runtime had not acknowledged a reload's invalidation at the pong deadline (supersedeGenerations).
type unacknowledgedGeneration struct {
	me *managedExt
	// deadline is the pong deadline the acknowledgement had.
	deadline time.Duration
	// lostRequests is the number of requests in flight on the generation that the reload ended.
	lostRequests int
}

// supersedeGenerations is the point of a reload at which Pi has invalidated the old runner: right after session_shutdown, before the settings and the extensions load again (agent-session.ts:3825-3826), and from then a captured pi or ctx throws the stale message (runner.ts:729-741). The generations run on until the commit replaces them, with every handler that was in flight, so from here the host answers the calls of a generation a Node runtime hosts with that message (runCall) and its runtime, told with the invalidate notification, throws it from the members it answers locally. Reload calls it once the config loaded, and both of its callers have awaited the session_shutdown handlers by then (coding.Session.Reload, the interactive /reload): a call a session_shutdown handler makes is served, and a config loader failure leaves every generation as it was.
//
// Pi's invalidate() is synchronous: the old ctx is stale before reload() goes on. A runtime in another process applies the notification when its loop reads it, and a handler of the generation that goes on before then finds its ctx live. So the reload stops here until each runtime has acknowledged the invalidation (Conn.invalidateAcknowledged), and the host marks the generations only then: a call the runtime made while its ctx was live is on the wire ahead of the acknowledgement and is served, as a call made before Pi's invalidate() is. Marked before the runtime knew, the host refused such a call while the handler went on: a call the handler awaits was rejected with the stale message, but one it does not await (ctx.ui.notify) was refused with a diagnostic where Pi throws, and the handler ended as if the call had been made. A call the host read before the acknowledgement and still holds past it (a timer's call that waits for the loop's turn) is answered with the stale message.
//
// A Go, Python or Rust generation is served until the commit, as it was before: Pi has no such extension, the host never dropped its calls, and its connection is not its own alone. A helper that the replacement's factory calls before it has connected (the type engine, the frontmatter parser) rides the connection of the running extension on the same socket (extensions/sdk helperConnection), so a stale answer there fails the replacement's load.
//
// A runtime that has not acknowledged by the pong deadline is not waited for longer: a handler that holds the extension's thread keeps the runtime from reading the notification for as long as it runs, and /reload must still replace that extension. Pi is never in this state, because its extensions run on the thread that runs reload(). The host ends the connection with an ExtensionUnresponsiveError that names the reload, which answers every request in flight on it; it first takes the process out of the retained processes, so the replacement generations start in a new one. The generation is returned for the reload's report (unacknowledgedNotices). The generations of one Node process share its thread: while one of them holds it, none acknowledges, and each is ended this way.
//
// The returned function ends it. Reload defers it, so the generations are superseded for the length of that reload only: one that a reload leaves registered is in service again when the reload returns, and a later crash recovery finds nothing marked.
func (h *Host) supersedeGenerations(ctx context.Context) (unacknowledged []unacknowledgedGeneration, end func()) {
	h.mu.Lock()
	members := make(map[*managedExt]struct{})
	for _, me := range h.exts {
		if me.hostsNodeRuntime() {
			members[me] = struct{}{}
		}
	}
	h.mu.Unlock()
	message := (&inproc.StaleError{}).Error()
	// Every runtime is told before any is waited for, so the deadlines of runtimes that never acknowledge run together.
	type invalidation struct {
		me           *managedExt
		conn         *Conn
		acknowledged func(context.Context) bool
	}
	sent := make([]invalidation, 0, len(members))
	for me := range members {
		if conn := me.connection(); conn != nil {
			sent = append(sent, invalidation{me: me, conn: conn, acknowledged: conn.invalidateAcknowledged(message)})
		}
	}
	for _, one := range sent {
		if one.acknowledged(ctx) {
			continue
		}
		one.me.withdrawProcessFromRetention()
		failure := &ExtensionUnresponsiveError{Extension: one.conn.name, ReloadDeadline: one.conn.heartbeatTimeout}
		one.conn.fail(failure)
		// A connection that another failure ended first is that failure's to report.
		if errors.Is(one.conn.failureError(), failure) {
			unacknowledged = append(unacknowledged, unacknowledgedGeneration{me: one.me, deadline: failure.ReloadDeadline, lostRequests: one.conn.requestsEndedByFailure()})
		}
	}
	h.superseded.Store(&members)
	return unacknowledged, func() { h.superseded.Store(nil) }
}

// runtimeProcess identifies the process that hosts me: the generations of one Node process share its thread.
func (me *managedExt) runtimeProcess() any {
	share := me.processShare()
	switch {
	case me.packedProcess != nil && me.packedProcess.share != nil:
		return me.packedProcess.share
	case me.packedProcess != nil:
		return me.packedProcess.spawner()
	case share != nil:
		return share
	}
	return me
}

// unacknowledgedNotices returns the lines of ReloadReport.Unacknowledged for the generations a reload ended because their runtime had not acknowledged its invalidation (supersedeGenerations). Reload calls it after its commit, so a line says whether the extension is loaded again.
//
// A line names an extension that lost a request, or that has its process to itself. An extension that shares the process of one that is named, and lost no request, has no line: the reload replaced it as it replaces every extension, and nothing of it was cut short. When no generation of a shared process lost a request, a callback outside any request holds the thread and the host cannot tell whose it is: one line names the runtime and the extensions that share it.
func (h *Host) unacknowledgedNotices(unacknowledged []unacknowledgedGeneration) []string {
	if len(unacknowledged) == 0 {
		return nil
	}
	sortx.Func(unacknowledged, func(a, b unacknowledgedGeneration) int { return cmp.Compare(a.me.config.Name, b.me.config.Name) })
	var order []any
	processes := make(map[any][]unacknowledgedGeneration)
	for _, generation := range unacknowledged {
		process := generation.me.runtimeProcess()
		if _, seen := processes[process]; !seen {
			order = append(order, process)
		}
		processes[process] = append(processes[process], generation)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	outcome := func(generations ...unacknowledgedGeneration) string {
		for _, generation := range generations {
			if current := h.exts[generation.me.config.Name]; current != nil && current != generation.me {
				return "restarted"
			}
		}
		return "stopped"
	}
	// A line is written as fmt writes it: a name as %q (strconv.AppendQuote), the deadline as %s (Duration.String).
	unacknowledgedWithin := func(line []byte, deadline time.Duration, outcome string) string {
		line = append(line, " did not acknowledge the reload within "...)
		line = append(line, deadline.String()...)
		line = append(line, " and was "...)
		return string(append(line, outcome...))
	}
	var lines []string
	// The lines are built in one buffer, which stays on the stack for the usual names.
	var buffer [192]byte
	line := buffer[:0]
	for _, process := range order {
		generations := processes[process]
		named := generations
		if len(generations) > 1 {
			named = slices.DeleteFunc(slices.Clone(generations), func(generation unacknowledgedGeneration) bool { return generation.lostRequests == 0 })
		}
		for _, generation := range named {
			line = strconv.AppendQuote(append(line[:0], "extension "...), generation.me.config.Name)
			lines = append(lines, unacknowledgedWithin(line, generation.deadline, outcome(generation)))
		}
		if len(named) == 0 {
			line = append(line[:0], "the runtime that extensions "...)
			for i, generation := range generations {
				if i > 0 {
					line = append(line, ", "...)
				}
				line = strconv.AppendQuote(line, generation.me.config.Name)
			}
			line = append(line, " share"...)
			lines = append(lines, unacknowledgedWithin(line, generations[0].deadline, outcome(generations...)))
		}
	}
	return lines
}

// invalidateAcknowledged sends the stale message (NotifyInvalidate) asking for the runtime's acknowledgement, and returns the wait for it: until NotifyInvalidateAck arrives, the connection ends or ctx ends. The acknowledgement has the deadline of a pong, the connection's heartbeat timeout, from the send. The heartbeat itself does not bound the wait: a runtime can answer its heartbeat while a handler holds the extension's thread, and it never reads the notification then, and /reload must still replace an extension that hangs. The wait reports false when the deadline came first; the caller, which runs it once, then ends the connection (Host.supersedeGenerations).
func (c *Conn) invalidateAcknowledged(message string) (acknowledged func(ctx context.Context) bool) {
	args, err := json.Marshal(InvalidateArgs{Message: message, Ack: c.invalidations.next()})
	if err != nil {
		return func(context.Context) bool { return true }
	}
	if err := c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidate, Args: args}}); err != nil {
		return func(context.Context) bool { return true }
	}
	deadline := c.clock.NewTimer(c.heartbeatTimeout)
	return func(ctx context.Context) bool {
		defer deadline.Stop()
		return c.invalidations.awaitBy(ctx, c.Done(), deadline.C())
	}
}

// supersedes reports whether a running reload is replacing me and Pi has invalidated its runner (supersedeGenerations).
func (h *Host) supersedes(me *managedExt) bool {
	members := h.superseded.Load()
	if members == nil {
		return false
	}
	_, ok := (*members)[me]
	return ok
}

// releasesBusListeners reports whether method is one of the calls a runtime makes to release its bus listeners when the invalidate notification arrives: events.off, after its lazy xref.hello. Pi's runtime unsubscribes them in invalidate, which never throws (loader.ts:200-207), so the host applies them for an invalidated generation instead of answering with the stale message, which the runtime would take for a failure of its own and exit on.
func releasesBusListeners(method string) bool {
	return method == callEventsOff || method == callXrefHello
}
