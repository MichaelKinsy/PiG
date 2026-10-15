// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

// The capsql facade's serial queue (durable/contract/lib/capsql.mjs piDurableDatabase :141-166). The reference's Storage runs
// every statement and transaction through one `serial(work)`:
//
//	let queue = Promise.resolve()
//	const serial = work => { const r = queue.then(work); queue = r.catch(() => {}); return r }
//
// so a statement enqueued at tick t runs its work one tick after the later of t and the tick the queue's previous item
// resolved (`queue.then(work)` waits for `queue`), and resolves three ticks after its work started (the work's promise is
// adopted, then the reaction): four ticks on an idle queue (statement). A transaction is one item (:155-159, `transaction:
// cb => serial(async () => { cap.begin(); ... await cb(direct); cap.commit() })`): its callback's statements run through
// `direct` and do not queue, and the queue stays held for the whole callback. The rules hold for the Gate and GateCapsql
// profiles only; every other profile has no queue (Memory: no statements; Node: Promise.resolve) and its code never reaches
// this file's types.
//
// Not modelled: the occupancy of the queue by a commit transaction while an off-line scan is in flight. Occupy is the rule; no
// core call site registers a commit job's transaction, and no row measures a scan that overlaps one.
//
// Time is the handler's virtual tick (core.handler.vnow): a hops chain that began at tick t ends at t+n, and a chain's
// end is the only event that moves it, so a statement's enqueue tick is known to the tick whenever a chain started the work
// that enqueues it.

// statementWork is the ticks from a statement's start (its work runs) to its resolution: the work's promise is adopted
// (a thenable job, then the reaction) and `r.then` fires (capsql.mjs :152-155; trace: enq 181, start 182, done 185).
const statementWork = 3

// Queue is the capsql facade's serial queue. The zero value is an idle queue.
type Queue struct {
	free int // the tick the last item enqueued resolves; the next item starts one tick after it
}

// Occupy enqueues one item at tick now whose work takes work ticks and returns the tick it resolves.
func (q *Queue) Occupy(now, work int) int {
	start := max(now, q.free) + 1
	q.free = start + work
	return q.free
}

// Statement enqueues one statement at tick now and returns the tick it resolves (capsql.mjs :152-155): now+4 on an idle queue,
// the predecessor's resolution + 4 behind a busy one.
func (q *Queue) Statement(now int) int { return q.Occupy(now, statementWork) }

// Step is one stage of the awaits a read makes: Ticks idle microtask hops, or one statement through the queue.
type Step struct {
	Statement bool
	Ticks     int // the hops of a delay; unused for a statement
}

// AppendSteps appends the stages of read r on the profile to steps and returns it: the statements of the read in order
// with the hops between them. The stages sum to ReadHops(p, r) on an idle queue. A read that has no stage list (a
// transaction read: ReadDocument, a read with no statement: MintID) is one delay of its ticks; ok is false for a profile
// without a queue.
func AppendSteps(steps []Step, p Profile, r Read) ([]Step, bool) {
	if p != Gate && p != GateCapsql {
		return steps, false
	}
	if p == Gate {
		// The lazy proxy awaits `ready`, then adopts the method's promise (viaProxy): one tick before the method, one after.
		steps = appendDelay(steps, 1)
	}
	switch r {
	case ReadRow:
		steps = append(steps, Step{Statement: true})
		steps = appendDelay(steps, 1) // the async method returns
	case ReadEntry:
		steps = append(steps, Step{Statement: true})
		steps = appendDelay(steps, 2) // readEntry returns, admitRead returns
	case ReadEntryIn, ReadScanEntries, ReadHeadMarker:
		steps = append(steps, Step{Statement: true}) // readConversation
		steps = appendDelay(steps, 1)                // it returns
		steps = append(steps, Step{Statement: true}) // the read's own statement
		steps = appendDelay(steps, 2)                // readX returns, admitRead returns
	default:
		steps = appendDelay(steps, readBackend(GateCapsql, r))
	}
	if p == Gate {
		steps = appendDelay(steps, 1)
	}
	// The Session's Storage guard (session.ts #call) resumes the caller max(d,2)+2 ticks after the call: its ticks follow
	// the backend's, which the statements above describe.
	if d := Sum(steps); d < 2 {
		steps = appendDelay(steps, 2-d)
	}
	steps = appendDelay(steps, 2)
	return steps, true
}

// appendDelay adds n idle hops, merged with a delay that ends steps.
func appendDelay(steps []Step, n int) []Step {
	if n <= 0 {
		return steps
	}
	if k := len(steps); k > 0 && !steps[k-1].Statement {
		steps[k-1].Ticks += n
		return steps
	}
	return append(steps, Step{Ticks: n})
}

// AppendDelay adds n idle hops to steps, merged with a delay that ends it.
func AppendDelay(steps []Step, n int) []Step { return appendDelay(steps, n) }

// Trim removes one hop from the end of steps (a delay): `direct` jobs return the Storage promise itself and settle a tick before
// the read's caller would resume (ReadOnLine).
func Trim(steps []Step) []Step {
	if k := len(steps); k > 0 && !steps[k-1].Statement && steps[k-1].Ticks > 0 {
		steps[k-1].Ticks--
	}
	return steps
}

// Sum is the ticks of steps on an idle queue.
func Sum(steps []Step) int {
	n := 0
	for _, s := range steps {
		if s.Statement {
			n += statementWork + 1
		} else {
			n += s.Ticks
		}
	}
	return n
}
