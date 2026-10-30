package host

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports packages/chord/src/next/jobs/run.ts
//
// One run of a job (§15, slice 6b §4.1): its record (id, version, key, args, phase, restarts, finish failures, abort
// mark, outcome), its current invocation (the fence of its seal token, its abort signal, the end an abort joins: one
// per invocation, as a persisted run is invoked again after a seal, R3), its `running` entry and the `Run` handle its
// callers wait on.
//
// Forced Go differences. A waiting promise is a blocking call (DESIGN-go-layout.md section 4): `Run.Outcome` and
// `invocation.over` register inside a section, then wait on a channel and on the caller's context; a cancelled wait
// removes its registration, so it keeps nothing. Pi's `context.abortSignal` is the context itself (`ctx.Done`,
// `context.Cause`). The AbortController of an invocation is a `chord/context.WithCancel` pair. A rejection's stack
// receivers (V8) have no Go form.

// Run is a started run as its caller holds it: its id and a wait for its outcome (`Outcome` resolves with the run's
// Outcome once `finish` committed (also without a `finish`); a blocked run keeps waiting, a dropped one returns
// `aborted`, the object's end returns its error; the context ends only this wait, which then keeps nothing of it).
type Run = documents.Run

// AnyOutcome is a run's outcome, as Chord keeps it whatever the job's types.
type AnyOutcome = Outcome

// Phase is where a run's record stands. A persisted run is `pending` from its start's commit until its reservation
// commit (`running`, then its `run` starts), `stopped` once a clean stop cut it off; an in-memory run is `running` from
// its start's commit. `finishing` once its outcome is recorded, `blocked` after five failed finishes or for another
// version.
type Phase string

// The phases of a run's record.
const (
	phasePending   Phase = "pending"
	phaseRunning   Phase = "running"
	phaseStopped   Phase = "stopped"
	phaseFinishing Phase = "finishing"
	phaseBlocked   Phase = "blocked"
)

// BlockedReason is why a run is blocked: its `finish` failed five times in a row, or it was stored by another job
// version (slice 7).
type BlockedReason string

// The reasons a run is blocked.
const (
	// BlockedFinishFailed: `finish` failed five times in a row.
	BlockedFinishFailed BlockedReason = "finish_failed"
	// BlockedVersion: stored by another job version.
	BlockedVersion BlockedReason = "version"
)

// answer is how a run settled for its waiters: an outcome, or an error.
type answer struct {
	outcome AnyOutcome
	err     error
}

// jobRun is one run: changed only by its job, inside its transactions' commit steps (records) and after them
// (actions). Every member is used inside a section.
type jobRun struct {
	id      string
	version int
	key     *string
	args    delta.JsonValue
	handle  Run
	// invocation is its current invocation, from its reservation (or its start in memory) on; a later one replaces it.
	invocation *invocation
	phase      Phase
	// restarts is a persisted run's crashes in a row while running (D6); reset by a state change.
	restarts int
	// finishFailures is the failed finishes in a row (stored for a persisted run, so they count across a crash).
	finishFailures int
	// abortMarked is set by `abort(id)` (stored for a persisted run, D7): a run not invoked yet ends `aborted` without
	// `run`.
	abortMarked bool
	// resuming is a persisted run cut off by a crash or a clean stop, to be resumed per its job's `resume` (no new
	// start).
	resuming bool
	blocked  *BlockedReason
	// trace is the start's trace carrier, when the adapter propagates (stored, slice 9 uses it).
	trace map[string]string
	// outcome is what it ended with, once recorded (finishing and blocked).
	outcome *AnyOutcome
	// entry is its entry in `running`, while its record exists; replaced when its record or state document changes.
	entry *delta.JsonObject
	// waiters are its waiters: apart from the run, so a handle kept by a caller keeps only how the run settled.
	waiters *waiters
	loop    sectionLoop
}

func newJobRun(loop sectionLoop, id string, version int, key *string, args delta.JsonValue, phase Phase, trace map[string]string) *jobRun {
	waiting := &waiters{loop: loop, waiting: map[*waiter]struct{}{}}
	return &jobRun{
		id: id, version: version, key: key, args: args, phase: phase, trace: trace, waiters: waiting, loop: loop,
		handle: &runHandle{id: id, waiters: waiting},
	}
}

// runHandle is the `Run` of a run.
type runHandle struct {
	id      string
	waiters *waiters
}

// ID implements Run.
func (handle *runHandle) ID() string { return handle.id }

// Outcome implements Run.
func (handle *runHandle) Outcome(ctx context.Context) (any, error) {
	outcome, err := handle.waiters.wait(ctx)
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

// entryFor is its `running` entry for its current record and `state` (the job's state document, nil without one): a
// pending or stopped run shows as `running` (it runs, or will run again).
func (run *jobRun) entryFor(state *delta.JsonObject) *delta.JsonObject {
	entry := delta.NewJsonObject(6)
	entry.Set("id", run.id)
	entry.Set("version", float64(run.version))
	if run.phase == phaseBlocked {
		entry.Set("phase", string(run.phase))
		entry.Set("reason", string(*run.blocked))
		return entry
	}
	if run.key != nil {
		entry.Set("key", *run.key)
	}
	entry.Set("args", run.args)
	if state != nil {
		entry.Set("state", state)
	}
	if run.phase == phaseFinishing {
		entry.Set("phase", "finishing")
	} else {
		entry.Set("phase", "running")
	}
	return entry
}

// begin is a new invocation, the current one from now on (a run marked aborted is never invoked: it ends without one).
func (run *jobRun) begin() *invocation {
	next := newInvocation(run.loop)
	run.invocation = next
	return next
}

// invoked reports whether its current invocation runs (or is reserved): it is not invoked again meanwhile.
func (run *jobRun) invoked() bool {
	return run.invocation != nil && run.invocation.state == invocationRunning
}

// over blocks until its current invocation ended, at once when none runs (`abort` joins it; one a seal cut off is
// fenced and is not waited for); ctx ends only the wait.
func (run *jobRun) over(ctx context.Context) error {
	var current *invocation
	run.loop.Run(func() {
		if run.invocation != nil && run.invocation.state == invocationRunning {
			current = run.invocation
		}
	})
	if current == nil {
		return nil
	}
	return current.over(ctx)
}

// resolve gives its waiters the outcome.
func (run *jobRun) resolve(outcome AnyOutcome) {
	run.waiters.settle(answer{outcome: outcome})
}

// reject gives its waiters err.
func (run *jobRun) reject(err error) {
	run.waiters.settle(answer{err: err})
}

type invocationState int

const (
	// invocationRunning: from its reservation until `run` settled or a seal cut it off.
	invocationRunning invocationState = iota
	invocationSettled
)

// invocation is one invocation of a run's `run`: its fence (`ctx.state.change` admitted while it runs, under its own
// seal token, which its attempt's seal fences), its abort signal and the waits for its end. Captured by its ctx and its
// settling, so a replaced invocation never acts for its successor.
type invocation struct {
	token *documents.SealToken
	// signal is `run`'s abort signal: `abort(id)`, the attempt's seal, the object's end and `host.close` fire it.
	signal context.Context
	// abort fires the signal with a cause (AbortController.abort).
	abort func(cause error)
	state invocationState
	joins *joins
}

func newInvocation(loop sectionLoop) *invocation {
	signal, abort := chordctx.WithCancel(chordctx.Background())
	return &invocation{token: documents.NewSealToken(), signal: signal, abort: abort, joins: &joins{loop: loop, waiting: map[*join]struct{}{}}}
}

// settle closes the fence: a change made later is refused.
func (current *invocation) settle() { current.state = invocationSettled }

// ended is: nothing of it runs here any more (its `run` settled, or the object ended): every join resolves.
func (current *invocation) ended() { current.joins.end() }

// over blocks until it ended; ctx ends only the wait.
func (current *invocation) over(ctx context.Context) error { return current.joins.wait(ctx) }

// waiter is one wait for a run's outcome, removed when its context ends.
type waiter struct {
	settled chan answer
}

// waiters are a run's waits for its outcome: each removed when its context ends; late ones answered at once.
type waiters struct {
	loop sectionLoop
	// done is how the run settled, once it did.
	done    *answer
	waiting map[*waiter]struct{}
}

// settle is: the run settled (once): every wait gets it.
func (set *waiters) settle(settled answer) {
	if set.done != nil {
		return
	}
	set.done = &settled
	for each := range set.waiting {
		each.settled <- settled
	}
	clear(set.waiting)
}

// wait blocks until the outcome: at once once settled; otherwise until the run settles or ctx ends.
func (set *waiters) wait(ctx context.Context) (AnyOutcome, error) {
	var each *waiter
	var immediate *answer
	var failed error
	set.loop.Run(func() {
		if set.done != nil {
			immediate = set.done
			return
		}
		if ctx.Err() != nil {
			failed = context.Cause(ctx)
			return
		}
		each = &waiter{settled: make(chan answer, 1)}
		set.waiting[each] = struct{}{}
	})
	if failed != nil {
		return AnyOutcome{}, failed
	}
	if immediate != nil {
		return immediate.outcome, immediate.err
	}
	select {
	case settled := <-each.settled:
		return settled.outcome, settled.err
	case <-ctx.Done():
		set.loop.Run(func() { delete(set.waiting, each) })
		select {
		case settled := <-each.settled:
			return settled.outcome, settled.err
		default:
		}
		return AnyOutcome{}, context.Cause(ctx)
	}
}

// join is one wait for an invocation to be over.
type join struct {
	over chan struct{}
}

// joins are the waits for a run's invocation to be over: each removed when its context ends, so a cancelled one keeps
// nothing.
type joins struct {
	loop    sectionLoop
	done    bool
	waiting map[*join]struct{}
}

// end is: the invocation is over (once): every wait resolves.
func (set *joins) end() {
	if set.done {
		return
	}
	set.done = true
	for each := range set.waiting {
		close(each.over)
	}
	clear(set.waiting)
}

// wait blocks until the invocation is over, at once when it is; otherwise until it is or ctx ends (returning its cause).
func (set *joins) wait(ctx context.Context) error {
	var each *join
	var failed error
	var ready bool
	set.loop.Run(func() {
		if set.done {
			ready = true
			return
		}
		if ctx.Err() != nil {
			failed = context.Cause(ctx)
			return
		}
		each = &join{over: make(chan struct{})}
		set.waiting[each] = struct{}{}
	})
	if failed != nil {
		return failed
	}
	if ready {
		return nil
	}
	select {
	case <-each.over:
		return nil
	case <-ctx.Done():
		set.loop.Run(func() { delete(set.waiting, each) })
		select {
		case <-each.over:
			return nil
		default:
		}
		return context.Cause(ctx)
	}
}
