package host

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/jobs/job.ts
//
// Jobs (§15), in memory: the handle `self.job` returns, one per attempt, over the job's records on its object. It holds
// the implementation and the invocations: `run` runs outside transactions, `finish` in one with the cleanup, both on the
// attempt that is live; the attempt's seal cuts its invocations off (slice 6a §1.3).
//
// Forced Go differences. `JobHandle<A, R, S>`, `JobCtx<P, S>`, `RunState<S>` and `JobImplementation<A, R, S, P>` are
// type-level generics (owner class a: TypeScript type-level checks): args, results and states are JSON values (`any`),
// a principal is a data.Principal, and `ctx.state` is nil for a job without a state (`JobCtx.State`). A Promise Pi
// awaits is a blocking call (DESIGN-go-layout.md section 4): `Start`, `Outcome`, `Abort`, `RunState.Change` block;
// a handler (`run`, `finish`) is called off the loop, where it blocks as any handler does, and the continuation Pi
// writes after its `await` re-enters the loop (`sectionLoop.Run`). Work Pi admits to the attempt's drain
// (`owner.admit`) is an owned background task (`sectionLoop.Go`) whose end settles the admission. `ctx.state.value`
// returns `released` as an error, where Pi's getter throws.

// JobHandle is a job's handle, `self.Job(Definition, implementation)`.
type JobHandle interface {
	// Start starts a run in one transaction on the object's line and returns its Run after the commit: the args checked
	// (`invalid/args`); a Key of an unsettled run returns that run; at the `concurrency` limit (running plus finishing
	// runs) it returns `unavailable/busy`. `run` starts after the commit. It blocks.
	Start(args any, ctx context.Context, options StartOptions) (Run, error)
	// Outcome waits for the outcome of the unsettled run `id`; a settled or unknown id returns `gone`.
	Outcome(id string, ctx context.Context) (Outcome, error)
	// Abort aborts run `id` (D7, as Durable's `abortTask`): marks it (stored for a persisted job) and fires its `run`'s
	// signal, returning AbortMarked once its `run` settled (a run not invoked yet ends `aborted` without one); a
	// finishing run is only marked, a blocked one dropped (no `finish`; its waiters get `aborted`). AbortTerminal when no
	// unsettled run has that id. ctx ends only this wait.
	Abort(id string, ctx context.Context) (AbortResult, error)
	// Running is the unsettled runs, one entry each in start order, tagged by phase (a JSON watchable, a state member).
	Running() state.SourceHandle
}

// StartOptions is what `Start` and `tx.Start` take besides the args: Key names one run: a start with the key of an
// unsettled run returns that run (args not compared).
type StartOptions = documents.StartOptions

// JobImplementation is what `self.Job` takes: the run and the optional finish.
type JobImplementation struct {
	// Run is the work, outside transactions; its ctx's abort signal fires on `Abort(id)` and on its object's end.
	Run func(args any, ctx JobCtx) (any, error)
	// Finish records the outcome in one transaction (only `tx.*`); the run's record and state document go with it. nil
	// is no finish.
	Finish func(outcome Outcome, tx documents.Tx) error
}

// JobCtx is a run's ctx: a handler's ctx (the object's principal or the host's), its run's id and, with a state, the
// state.
type JobCtx interface {
	Ctx
	// JobID is the run's id.
	JobID() string
	// State is the run's state document; nil for a job without a state definition.
	State() RunState
}

// ChangeOptions is what `RunState.Change` takes besides the function.
type ChangeOptions struct {
	// Persistence is when the change reaches the store; nil is immediate.
	Persistence *documents.CommitPersistence
}

// RunState is a run's state document.
type RunState interface {
	// Value is the document's value; `released` once the run settled.
	Value() (*delta.JsonObject, error)
	// Change is one transaction on the object's line drafting only this document, under ctx without its abort signal:
	// admitted while `run` runs (also after an abort, with the run's own ctx), refused `released` once it settled. A
	// persisted job's change may be `deferred` (D8): adopted and published at once, stored within `WithinMs` (1,000 ms by
	// default) or earlier; a crash loses it. In memory the option changes nothing. It blocks.
	Change(fn func(draft *delta.Object) (any, error), ctx context.Context, options ChangeOptions) (any, error)
}

// jobOwner is where a job's handle runs its work: its attempt, as the job sees it.
type jobOwner struct {
	host *chordHost
	// caller is whom runs act for: the object's principal (remote) or the host's (local), from this host.
	caller caller
	// context is the object's context, without an abort signal: runs, state changes and finishes run under it.
	context context.Context
	// signal is aborted when the attempt is sealed: a finish's backoff waits under it.
	signal context.Context
	// token is the attempt's seal token: its starts and finishes are transactions under it.
	token     *documents.SealToken
	documents *documents.Documents
	// admit adds running work (an invocation and its finish, a deferred finish) to the attempt's drain: cleanup (its
	// bookkeeping, may be nil) runs once, when the work settles (the returned settle is called with its error) or when
	// the attempt ends with it still running.
	admit func(cleanup func()) (settle func(err error))
}

// finishAttempts is the failed finishes in a row after which a run is blocked.
const finishAttempts = 5

type jobState int

const (
	// jobDeclared until the attempt is live (starts are refused).
	jobDeclared jobState = iota
	jobLive
	// jobSealed once it is sealed.
	jobSealed
)

// jobSections runs a job's sections as the host's: the delivery scheduler is held for the outermost one.
type jobSections struct {
	sectionLoop
	host *chordHost
}

// Run implements sectionLoop.
func (sections jobSections) Run(work func()) { sections.host.enter(work) }

// job is a job as one attempt declares it: its implementation and the invocations and finishes running on the attempt.
// Starts are taken while the attempt is live; the object's records keep the runs.
type job struct {
	records        *jobRecords
	implementation JobImplementation
	owner          jobOwner
	state          jobState
	// invocations are the runs whose `run` runs on this attempt.
	invocations *runSet
	// sealed is the seal's error, once sealed: a reservation committing after it cuts its run off with it.
	sealed *data.ChordError
	// finishing are the runs whose finish runs on this attempt.
	finishing *runSet
}

var (
	_ JobHandle           = (*job)(nil)
	_ documents.JobHandle = (*job)(nil)
)

func newJob(records *jobRecords, implementation JobImplementation, owner jobOwner) *job {
	return &job{records: records, implementation: implementation, owner: owner, invocations: newRunSet(), finishing: newRunSet()}
}

// Running implements JobHandle: its value is the list of entries `jobRun.entryFor` builds.
func (handle *job) Running() state.SourceHandle { return handle.records.running }

// Start implements JobHandle.
func (handle *job) Start(args any, ctx context.Context, options StartOptions) (Run, error) {
	owner := handle.owner
	run, err := owner.documents.Tx(func(tx *documents.Transaction) (any, error) {
		var staged Run
		var err error
		owner.documents.Do(func() { staged, err = handle.Stage(tx, args, ctx, options) })
		return staged, err
	}, owner.token, ctx)
	if err != nil {
		return nil, err
	}
	return run.(Run), nil
}

// Outcome implements JobHandle.
func (handle *job) Outcome(id string, ctx context.Context) (Outcome, error) {
	return handle.records.outcome(id, ctx)
}

// Abort implements JobHandle.
func (handle *job) Abort(id string, ctx context.Context) (AbortResult, error) {
	return handle.records.abort(id, ctx)
}

// load loads a persisted job's runs, once per object at its first activation (before it goes live). It blocks.
func (handle *job) load(ctx context.Context) error { return handle.records.load(ctx) }

// writeAtClose is the close writer of this job's runs, after the attempt's drain (slice 6b §4.2). It blocks.
func (handle *job) writeAtClose(ctx context.Context) { handle.records.writeAtClose(ctx) }

// live is: the attempt went live: starts are taken, and the records' waiting runs are invoked or finished here.
func (handle *job) live() {
	if handle.state != jobDeclared {
		return
	}
	handle.state = jobLive
	handle.records.attach(handle)
}

// Stage implements documents.JobHandle: it stages a run in tx (`start`, `tx.start`), in order: the args checked, dedupe
// by key (runs started earlier in tx included), the concurrency check, then the record and the state document, created
// in the commit. The run whose finish tx is counts for neither: `finish` may start the same job again, with the same key
// too. It returns the run's handle at once; a start that only dedupes stages nothing. A section.
func (handle *job) Stage(tx *documents.Transaction, args any, ctx context.Context, options documents.StartOptions) (documents.Run, error) {
	// Declaration policy: a handle belongs to its object (types cannot say whose transaction this is).
	if tx.Documents() != handle.owner.documents {
		return nil, fmt.Errorf("%s: a transaction starts only its own object's jobs", handle.owner.documents.Label())
	}
	if handle.state == jobSealed {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: handle.records.name() + ": its attempt ended"}, nil)
	}
	if handle.state == jobDeclared {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: handle.records.name() + ": its object is not live yet"}, nil)
	}
	return handle.records.stage(tx, args, ctx, options)
}

// seal is: the attempt is sealed with err: starts are refused, running invocations are cut off (their signals fire, they
// end `aborted` with reason `restart` and finish on the next live attempt), and the runs finishing here wait for it.
func (handle *job) seal(err *data.ChordError) {
	if handle.state == jobSealed {
		return
	}
	handle.state = jobSealed
	handle.sealed = err
	handle.records.detach(handle, handle.finishing.snapshot())
	handle.finishing.clear()
	for _, run := range handle.invocations.snapshot() {
		handle.records.cut(run, err)
	}
	handle.invocations.clear()
}

// invoke runs `run` for a committed run (with its signal aborted already after an early abort); a persisted run's
// reservation commits first (`running`: a crash from then on counts, D6). The reservation, the invocation and the run's
// finish are one piece of work in the drain, admitted before plugin code runs: `run` may end its own object before its
// first await.
func (handle *job) invoke(run *jobRun) {
	current := run.begin()
	if !handle.records.persists() || run.phase == phaseRunning {
		handle.start(run, current)
		return
	}
	owner, records := handle.owner, handle.records
	settle := owner.admit(nil)
	records.loop.Go(func() {
		_, err := owner.documents.Tx(func(tx *documents.Transaction) (any, error) {
			var err error
			owner.documents.Do(func() { err = records.reserve(tx, run) })
			return nil, err
		}, owner.token, owner.context)
		records.loop.Run(func() {
			switch {
			case err == nil && handle.state != jobSealed:
				handle.start(run, current)
			case err == nil:
				records.cut(run, handle.sealed)
				// No `run` started: nothing else ends it, and an abort joined to it returns.
				current.ended()
			default:
				current.settle()
				current.ended()
				// Refused because this attempt was sealed (at admission, or queued past the seal): the run is pending
				// still, for the next live attempt (this one is detached already). Any other failure (the store closed or
				// failed, the object ended) is not retried here: retrying on this attempt would only fail again.
				if handle.state == jobSealed {
					records.requeue(run)
				}
			}
		})
		settle(err)
	})
}

// start starts `current` invocation of `run` on this attempt: its ctx and its settling act for this invocation only.
func (handle *job) start(run *jobRun, current *invocation) {
	owner, records := handle.owner, handle.records
	ctx := &jobContext{
		callContext: newCallContext(chordctx.WithAbortSignal(owner.context, current.signal), owner.caller, owner.host),
		jobID:       run.id,
		state:       handle.stateOf(run, current),
	}
	settle := owner.admit(ctx.settle)
	handle.invocations.add(run)
	records.loop.Go(func() {
		result, err := handle.implementation.Run(run.args, ctx)
		records.loop.Run(func() {
			// The fence closes as `run` settles: a change made later is refused.
			current.settle()
			ended := jobEnded{kind: OutcomeDone, result: result}
			if err != nil {
				ended = handle.failed(run, current, err)
			}
			handle.settled(run, current, ctx, ended)
			current.ended()
		})
		settle(nil)
	})
}

// finish runs the finish of `run`, whose outcome is recorded: `finish` with the cleanup in one transaction under the
// attempt's token, retried with backoff after an error (reported), blocked after five in a row. The attempt's seal stops
// it without a report or a count; the next live attempt finishes the run.
func (handle *job) finish(run *jobRun) {
	settle := handle.owner.admit(nil)
	handle.records.loop.Go(func() {
		handle.finishRun(run)
		settle(nil)
	})
}

// failed is how an erroring `run` ended: `aborted` after its invocation's signal fired, otherwise `threw` (reported).
func (handle *job) failed(run *jobRun, current *invocation, err error) jobEnded {
	if current.signal.Err() != nil {
		return aborted
	}
	handle.records.report(HostError{Kind: ErrJobFailed, ID: run.id, Error: err})
	return jobEnded{kind: OutcomeFailed, err: JobError{Kind: JobThrew, Error: thrown(err)}}
}

// thrown is err as a run's outcome keeps it: a ChordError or a PluginError as it is, anything else a PluginError
// `internal` with its message.
func thrown(err error) error {
	if chordErr, ok := data.IsChordError(err); ok {
		return chordErr
	}
	if plugin, ok := errors.AsType[*data.PluginError](err); ok {
		return plugin
	}
	return data.NewPluginError("internal", err.Error())
}

// settled is: `current` invocation of `run` settled: its `ctx.Get` references go; its outcome is recorded unless the
// attempt's seal cut it off (which recorded its own, or stopped the run for a later invocation), then the run finishes
// here while this attempt is live.
func (handle *job) settled(run *jobRun, current *invocation, ctx *jobContext, ended jobEnded) {
	ctx.settle()
	if current.token.Sealed() || run.invocation != current {
		return
	}
	handle.invocations.delete(run)
	handle.records.settled(run, ended)
}

// finishRun is `#finish`: it blocks until the run is finished, blocked or the attempt is sealed.
func (handle *job) finishRun(run *jobRun) {
	owner, records := handle.owner, handle.records
	records.loop.Run(func() { handle.finishing.add(run) })
	defer records.loop.Run(func() { handle.finishing.delete(run) })
	var delay time.Duration
	for {
		_, err := owner.documents.Tx(func(tx *documents.Transaction) (any, error) {
			var err error
			var outcome AnyOutcome
			owner.documents.Do(func() {
				var each *staging
				if each, err = records.staging(tx); err != nil {
					return
				}
				each.finishing = run
				outcome = *run.outcome
			})
			if err != nil {
				return nil, err
			}
			if handle.implementation.Finish != nil {
				if err := handle.implementation.Finish(outcome, tx); err != nil {
					return nil, err
				}
			}
			owner.documents.Do(func() { err = records.change(tx, recordChange{kind: changeDelete, run: run}) })
			return nil, err
		}, owner.token, owner.context)
		if err == nil {
			return
		}
		var sealed bool
		var failures int
		records.loop.Run(func() {
			if sealed = handle.state == jobSealed; sealed {
				return
			}
			records.report(HostError{Kind: ErrJobFinishFailed, ID: run.id, Error: err})
			// The count is the run's (stored for a persisted job): failures count across a crash.
			failures = run.finishFailures + 1
		})
		if sealed {
			return
		}
		if failures >= finishAttempts {
			records.record(run, recordChange{kind: changeBlock, run: run, reason: BlockedFinishFailed})
			return
		}
		records.record(run, recordChange{kind: changeUpdate, run: run, fields: runFields{finishFailures: &failures}})
		delay = nextDelay(delay)
		if sleepOn(records.loop, delay, chordctx.WithAbortSignal(owner.context, owner.signal)) != nil {
			return
		}
	}
}

// runState is `ctx.state` of `current` invocation of `run`, when the job has a state: changes under its token while it
// runs.
type runState struct {
	handle  *job
	run     *jobRun
	current *invocation
}

// stateOf is `ctx.state` of `current` invocation of `run`, nil when the job has no state.
func (handle *job) stateOf(run *jobRun, current *invocation) RunState {
	if handle.records.states == nil {
		return nil
	}
	return &runState{handle: handle, run: run, current: current}
}

func (view *runState) refused() error {
	return data.NewChordError(data.ChordErrorInit{
		Code: data.CodeReleased, Reason: data.ReasonSettled,
		Message: fmt.Sprintf("%s: run %s settled; its state is closed", view.handle.records.name(), view.run.id),
	}, nil)
}

// Value implements RunState.
func (view *runState) Value() (*delta.JsonObject, error) {
	var value *delta.JsonObject
	view.handle.records.loop.Run(func() { value = view.handle.records.stateOf(view.run.id) })
	if value == nil {
		return nil, view.refused()
	}
	return value, nil
}

// Change implements RunState.
func (view *runState) Change(fn func(draft *delta.Object) (any, error), ctx context.Context, options ChangeOptions) (any, error) {
	handle, run, current := view.handle, view.run, view.current
	records, owner := handle.records, handle.owner
	var refused bool
	var persistence documents.CommitPersistence
	var checkErr error
	records.loop.Run(func() {
		if refused = current.state != invocationRunning; refused {
			return
		}
		if persistence, checkErr = checkedPersistence(options.Persistence, records.name()); checkErr != nil {
			return
		}
		// A change resetting the restart count is stored at once: the reset must be durable (D8).
		if run.restarts != 0 {
			persistence = documents.Immediate
		}
	})
	if refused {
		return nil, view.refused()
	}
	if checkErr != nil {
		return nil, checkErr
	}
	// Never the abort signal: a run records its progress also after an abort.
	unaborted := chordctx.WithoutAbortSignal(ctx)
	return owner.documents.Tx(func(tx *documents.Transaction) (any, error) {
		draft, err := tx.DraftSlot(records.states, run.id)
		if err != nil {
			return nil, err
		}
		value, err := fn(draft)
		if err != nil {
			return nil, err
		}
		owner.documents.Do(func() { err = records.progressed(tx, run) })
		return value, err
	}, current.token, unaborted, persistence)
}

// checkedPersistence is a state change's `persistence` (a trust boundary: plugin code): `immediate` when omitted; a
// deferred one's `WithinMs` a number from 0 (`+Inf` waits for the others), `invalid/args` otherwise.
func checkedPersistence(persistence *documents.CommitPersistence, what string) (documents.CommitPersistence, error) {
	if persistence == nil || persistence.Kind == documents.CommitImmediate {
		return documents.Immediate, nil
	}
	within := persistence.WithinMs
	if persistence.Kind != documents.CommitDeferred || (within != nil && !(*within >= 0 && !math.IsNaN(*within))) {
		return documents.CommitPersistence{}, data.NewChordError(data.ChordErrorInit{
			Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: what + ": persistence is immediate or deferred with withinMs >= 0",
		}, nil)
	}
	return *persistence, nil
}

// jobContext is a run's ctx: the handler's ctx of its object, its id and, with a state, its state document.
type jobContext struct {
	*callContext
	jobID string
	state RunState
}

var _ JobCtx = (*jobContext)(nil)

// JobID implements JobCtx.
func (ctx *jobContext) JobID() string { return ctx.jobID }

// State implements JobCtx.
func (ctx *jobContext) State() RunState { return ctx.state }
