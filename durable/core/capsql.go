// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import "github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

// The conformance gate's Storage runs every statement through one capsql serial queue (hopcost.Queue), so a statement that
// finds it busy waits: the status read of a busy line queues behind the off-line range scan of the turn's context read.
// The core models that queue for the two gate profiles only, in the virtual tick of the hops chains it started: a chain
// that began at tick t ends at t+n, and its end is what moves handler.vnow. Every other storageProfile (node-sqlite,
// cloudflare-do, memory, node) has no queue: capsql returns nil and the job and scan holds are the single chains they
// always were.

// maxPlanned is the reads of one line job the statement queue plans: a job that makes more keeps its single chain.
const maxPlanned = 4

// capsql is the gate profile's statement queue, nil when the open payload names any other storageProfile.
func (h *handler) capsql() *hopcost.Queue {
	if !h.qInit {
		h.qInit = true
		switch hopcost.Profile(strOf(h.open, "storageProfile")) {
		case hopcost.Gate, hopcost.GateCapsql:
			h.q = &hopcost.Queue{}
		}
	}
	return h.q
}

// runSteps parks then on the stages of a read. A statement is enqueued at the tick its stage begins (hopcost.Queue.Statement), and
// nothing the core can observe happens when it resolves, so the chain that waits for it also runs the hops that follow it: the
// chains end at the ticks the next statement is enqueued, and at the end of the read. It reports whether it parked then.
func (h *handler) runSteps(steps []hopcost.Step, then func()) bool {
	for len(steps) > 0 {
		n := 0
		for len(steps) > 0 && (n == 0 || !steps[0].Statement) {
			s := steps[0]
			steps = steps[1:]
			if s.Statement {
				n += h.q.Statement(h.vnow) - h.vnow
			} else {
				n += s.Ticks
			}
		}
		if n <= 0 {
			continue
		}
		rest := steps
		if len(rest) == 0 {
			return h.holdFor(n, then)
		}
		return h.holdFor(n, func() {
			if !h.runSteps(rest, then) {
				then()
			}
		})
	}
	return false
}

// scanThen is turn.Host.ScanHops: then runs as a microtask once the range scan of n ticks (the Storage scan and the return to
// its caller) ended. A gate profile runs the scan's statements through the queue (context.ts scanRange awaits scanEntries off
// the line); every other profile waits n hops.
func (h *handler) scanThen(n int, then func()) {
	if h.capsql() == nil {
		h.hopsThen(n, then)
		return
	}
	steps, _ := hopcost.AppendSteps(nil, h.hopProfile(), hopcost.ReadScanEntries)
	if n < hopcost.Sum(steps) {
		// The range the scheduler kept serves the read: no scan, only the return (contextReturnHops).
		h.hopsThen(n, then)
		return
	}
	steps = hopcost.AppendDelay(steps, n-hopcost.Sum(steps))
	h.scans++
	end := func() {
		h.scans--
		h.loop.Defer(then)
	}
	if !h.runSteps(steps, end) {
		end()
	}
}

// A line job's statements contend only with the statements of an off-line scan in flight: jobs serialize on the line, a scan
// starts when its bounds job ends (a job boundary), and a job's last statement resolves before the job does, so a scan that
// starts at a job's end finds the queue idle. A read job that starts while no scan is in flight is therefore one hops chain,
// and the queue is not consulted; while one is in flight the job runs stage by stage (readJobSteps).

// readJobSteps is the stages of a readOnLine job on the gate queue: the reads' statements in order and the tail of the
// run's resolution (hopcost.ReadOnLine). ok is false when the job did something other than reads (a mint, a transaction
// callback), which keeps its single chain.
func (h *handler) readJobSteps(st jobStats, startDelay int) ([]hopcost.Step, bool) {
	if h.capsql() == nil || h.scans == 0 || (!st.readLine && !h.loop.Reading) || st.nreads == 0 || st.nreads > maxPlanned {
		return nil, false
	}
	p := h.hopProfile()
	var steps []hopcost.Step
	steps = hopcost.AppendDelay(steps, startDelay)
	sum := 0
	for _, r := range st.reads[:st.nreads] {
		var ok bool
		if steps, ok = hopcost.AppendSteps(steps, p, r); !ok {
			return nil, false
		}
		sum += hopcost.ReadHops(p, r)
	}
	if sum != st.change {
		return nil, false
	}
	if st.direct {
		steps = hopcost.Trim(steps)
	}
	return hopcost.AppendDelay(steps, 3), true
}

// holdReadJob parks the completion of a readOnLine job on its statements through the gate queue; n is the job's duration
// with its start delay (holdJobNow), which the stages must sum to on an idle queue. It reports whether it parked release.
func (h *handler) holdReadJob(st jobStats, n int, release func()) bool {
	steps, ok := h.readJobSteps(st, n-hopcost.ReadOnLine(st.change, st.direct))
	if !ok || hopcost.Sum(steps) != n {
		return false
	}
	if n >= 2 && h.loop.EndingClient() {
		// A host caller awaits the job's `run` promise, which settles a hop before the tail resolves (session.ts :105-115).
		steps = hopcost.Trim(steps)
		return h.runSteps(steps, func() { h.loop.Settle(); release() })
	}
	return h.runSteps(steps, release)
}
