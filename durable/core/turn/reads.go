package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// Pi's runtime reads (runtime.agent, context, snapshot, entry, scheduler.ts #read) are Session line jobs queued behind the
// commits already waiting, so a read observes the state those commits left. Each helper below runs the Host function inside
// that job and continues with its result; a failure ends the phase.

// read runs job on the Session line and calls then after it.
func (r *run) read(job func() error, then func()) {
	r.rt.Read(job, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		then()
	})
}

// context is runtime.context(conversation, {at}).
func (r *run) context(at ID, then func(*history.View)) {
	var view *history.View
	var scan int
	var keep func(ended bool) error
	var kept KeptRange
	if r.g.Host.KeptRange != nil {
		kept = r.g.Host.KeptRange(r.conv)
	}
	r.read(func() error {
		var err error
		view, scan, keep, err = r.g.Host.Context(r.conv, at, kept)
		return err
	}, func() {
		resolve := func() {
			if keep != nil {
				if err := keep(r.rt.Ended()); err != nil {
					r.fail(err)
					return
				}
			}
			then(view)
		}
		// readContextFrom returns after the bounds job, through a range scan off the line when the kept range cannot serve the
		// read (context.ts:75-102): scan hops later.
		if scan > 0 {
			r.hops(scan, resolve)
			return
		}
		resolve()
	})
}

// entry is runtime.entry(id).
func (r *run) entry(id ID, then func(*jv.Object)) {
	var entry *jv.Object
	r.read(func() error {
		var err error
		entry, err = r.g.Host.Entry(id)
		return err
	}, func() { then(entry) })
}

// snapshot is runtime.snapshot(def, conversation): `#read(() => session.snapshot(...))` (scheduler.ts:1175-1178). Session.snapshot
// answers a cached document at once without a line job (session.ts:168-172); the async call resolves immediately, `#read`'s async
// return adopts the settled promise two hops later and the awaiting phase resumes one hop after (3). A miss is a line job.
func (r *run) snapshot(def *session.DocDef, then func(*jv.Object)) {
	r.snapshotResult(def, func(value *jv.Object, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		then(value)
	})
}

// snapshotResult is snapshot with the read's error handed to the caller.
func (r *run) snapshotResult(def *session.DocDef, then func(*jv.Object, error)) {
	if value, err, hit := r.snapshotCached(def); hit {
		r.hops(snapshotHitHops, func() { then(value, err) })
		return
	}
	var value *jv.Object
	var rerr error
	r.rt.Read(func() error {
		value, rerr = r.g.Host.Snapshot(def, r.conv)
		return rerr
	}, func(err error) { then(value, err) })
}

// snapshotCached is the answer of a cached Session.snapshot, if the document is cached (a hit takes snapshotHitHops to reach its caller).
func (r *run) snapshotCached(def *session.DocDef) (value *jv.Object, err error, hit bool) {
	if r.g.Host.SnapshotLoaded == nil || !r.g.Host.SnapshotLoaded(def, r.conv) {
		return nil, nil, false
	}
	value, err = r.g.Host.Snapshot(def, r.conv)
	return value, err, true
}

// snapshotHitHops is the hops from a cached Session.snapshot call to its awaiting caller (dcore-trace E23).
const snapshotHitHops = 3

// agentHops is the hops from the phase's first runtime.agent() to its awaiting caller when pi.agent is cached: Pi resolves the
// agent in its own code, `await snapshot` (1), the resolution's return into phase.agent (1) and awaitWithContext's reaction (1)
// (scheduler.ts #runtime agent, harness.ts resolveAgent, chord context awaitWithContext). It is not a #read and takes no line
// job; the host's resolveAgent answer overlaps these hops.
const agentHops = 3

// agentAgainHops is the hops of a runtime.agent() of a phase whose agent is already resolved: phase.agent is settled, so
// awaitWithContext's `promise.then` reaction (1) and the caller's await (1) (generation.ts startToolRound after beforeRequest).
const agentAgainHops = 2

// agent is runtime.agent(): the host composes the agent (resolveAgent hook) at the phase's first use and the phase continues
// when both it and Pi's own three hops have passed; a later use reuses the phase's resolution.
func (r *run) agent(then func(*Agent)) {
	if r.agentSeen {
		if r.rt.Ended() {
			r.fail(r.rt.EndedError())
			return
		}
		agent := r.phaseAgent
		r.hops(agentAgainHops, func() { then(agent) })
		return
	}
	r.agentAfter(agentHops, then)
}

// agentAfter is agent with the hops Pi's caller spends before it resumes.
func (r *run) agentAfter(hops int, then func(*Agent)) {
	if r.rt.Ended() {
		r.fail(r.rt.EndedError())
		return
	}
	left := 2
	var got *Agent
	var gerr error
	fin := func() {
		if left--; left > 0 {
			return
		}
		if gerr != nil {
			r.fail(gerr)
			return
		}
		r.agentSeen, r.phaseAgent = true, got
		then(got)
	}
	r.hops(hops, fin)
	r.g.Host.Agent(r.conv, r.rt.Signal(), func(agent *Agent, err error) {
		got, gerr = agent, err
		fin()
	})
}

// hooksAgentHops is the hops from the first use of the phase's agent in hooks.each to its handlers: the resolution is ready one tick
// after its snapshot read (scheduler.ts #agent) and `await agent()` resumes one tick later (scheduler.ts :1118-1121; Pi counts 3
// for a hook with no handler, the third being the caller's await of hooks.each).
const hooksAgentHops = 2

// withAgent continues once the invocation has resolved its agent (hooks.each awaits it before it selects handlers): `await agent()`
// of a resolution already made resumes one tick later.
func (r *run) withAgent(next func()) {
	if r.agentSeen {
		r.hops(1, next)
		return
	}
	r.agentAfter(hooksAgentHops, func(*Agent) { next() })
}

// hops runs then after n microtask hops of the shared host/core queue; a Host without a hop queue runs it at once.
func (r *run) hops(n int, then func()) {
	if r.g.Host.Hops == nil {
		then()
		return
	}
	r.g.Host.Hops(n, then)
}

// scanHops is hops for the wait on a context range scan.
func (r *run) scanHops(n int, then func()) {
	if r.g.Host.ScanHops != nil {
		r.g.Host.ScanHops(n, then)
		return
	}
	r.hops(n, then)
}
