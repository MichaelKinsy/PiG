package subprocess

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// supersedeGenerations is the point of a reload at which Pi has invalidated the old runner: right after session_shutdown, before the settings and the extensions load again (agent-session.ts:3825-3826), and from then a captured pi or ctx throws the stale message (runner.ts:729-741). The generations run on until the commit replaces them, with every handler that was in flight, so from here the host answers the calls of a generation a Node runtime hosts with that message (runCall) and its runtime, told with the invalidate notification, throws it from the members it answers locally. Reload calls it once the config loaded, and both of its callers have awaited the session_shutdown handlers by then (coding.Session.Reload, the interactive /reload): a call a session_shutdown handler makes is served, and a config loader failure leaves every generation as it was.
//
// Pi's invalidate() is synchronous: the old ctx is stale before reload() goes on. A runtime in another process applies the notification when its loop reads it, and a handler of the generation that goes on before then finds its ctx live. So the reload stops here until each runtime has acknowledged the invalidation (Conn.invalidateAcknowledged), and the host marks the generations only then: a call the runtime made while its ctx was live is on the wire ahead of the acknowledgement and is served, as a call made before Pi's invalidate() is. Marked before the runtime knew, the host refused such a call while the handler went on: a call the handler awaits was rejected with the stale message, but one it does not await (ctx.ui.notify) was refused with a diagnostic where Pi throws, and the handler ended as if the call had been made. A call the host read before the acknowledgement and still holds past it (a timer's call that waits for the loop's turn) is answered with the stale message.
//
// A Go, Python or Rust generation is served until the commit, as it was before: Pi has no such extension, the host never dropped its calls, and its connection is not its own alone. A helper that the replacement's factory calls before it has connected (the type engine, the frontmatter parser) rides the connection of the running extension on the same socket (extensions/sdk helperConnection), so a stale answer there fails the replacement's load.
//
// The returned function ends it. Reload defers it, so the generations are superseded for the length of that reload only: one that a reload leaves registered is in service again when the reload returns, and a later crash recovery finds nothing marked.
func (h *Host) supersedeGenerations(ctx context.Context) (end func()) {
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
	waits := make([]func(context.Context), 0, len(members))
	for me := range members {
		if conn := me.connection(); conn != nil {
			waits = append(waits, conn.invalidateAcknowledged(message))
		}
	}
	for _, wait := range waits {
		wait(ctx)
	}
	h.superseded.Store(&members)
	return func() { h.superseded.Store(nil) }
}

// invalidateAcknowledged sends the stale message (NotifyInvalidate) asking for the runtime's acknowledgement, and returns the wait for it: until NotifyInvalidateAck arrives, the connection ends or ctx ends. The acknowledgement has the deadline of a pong, the connection's heartbeat timeout, from the send. The heartbeat itself does not bound the wait: a runtime can answer its heartbeat while a handler holds the extension's thread, and it never reads the notification then, and /reload must still replace an extension that hangs. A runtime that has not acknowledged by the deadline is failed as one that misses a pong is (ExtensionUnresponsiveError), the host treats it as it treats any unresponsive extension, and the reload goes on. The caller runs the returned wait once.
func (c *Conn) invalidateAcknowledged(message string) (wait func(ctx context.Context)) {
	args, err := json.Marshal(InvalidateArgs{Message: message, Ack: c.invalidations.next()})
	if err != nil {
		return func(context.Context) {}
	}
	if err := c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidate, Args: args}}); err != nil {
		return func(context.Context) {}
	}
	deadline := c.clock.NewTimer(c.heartbeatTimeout)
	return func(ctx context.Context) {
		defer deadline.Stop()
		if !c.invalidations.awaitBy(ctx, c.Done(), deadline.C()) {
			c.fail(&ExtensionUnresponsiveError{Extension: c.name, Operation: "a reload waited for it to acknowledge its invalidation"})
		}
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
