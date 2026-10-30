package host

import (
	"container/list"
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/jobs/records.ts
//
// A job's records on its object (§15, slice 6a §1.2, slice 6b §4): its runs by id and by key, the active count, the run
// state documents, `running` and the runs waiting for a live attempt, across the object's attempts. Records and state
// documents change only in transactions on the object's line (staged changes adopted in the commit step); a persisted
// job's also in its object's store, in the same commits, loaded once per object, and written back stopped by the close
// writer when a clean stop cuts its runs off.
//
// Async contract (DESIGN-go-layout.md section 4): `outcome`, `abort`, `record`, `load`, `writeAtClose` and `flushHeld`
// block (the line, the store, a wait) and enter the loop (`sectionLoop.Run`) for each synchronous part Pi has between
// its awaits; every other member is used inside a section. A Pi `void` call of an async method (`void
// this.#recordOutcome(...)`) is an owned background task, `sectionLoop.Go`; `queueMicrotask` is `sectionLoop.Queue`.
// A `Set` or `Map` whose order Pi iterates keeps its insertion order (`runSet`).

// jobRecordsOwner is what a job's records need of their object.
type jobRecordsOwner struct {
	label string
	host  *chordHost
	// context is the object's context, without an abort signal: outcomes are recorded under it.
	context   context.Context
	documents *documents.Documents
	// store is the host's store and storeOwner the object's owner name in it, when the object may persist (P3); nil
	// store when it may not.
	store      *store.ChordStore
	storeOwner string
	// countRuns counts running and finishing runs: they keep the object alive (`host.activity.jobs`).
	countRuns func(delta int)
	// held is: a held state change must be stored by deadline: the object's one timer flushes it (D8).
	held func(deadline int64)
}

// jobEnded is how a run's invocation ended, before its outcome is recorded.
type jobEnded struct {
	kind   OutcomeKind
	result any
	err    JobError
}

// runFields is what a change sets on a run's record besides its phase transitions below (D6, D7, the finish failure
// count); a nil pointer is a field it does not set.
type runFields struct {
	phase          *Phase
	restarts       *int
	finishFailures *int
	abortMarked    bool
}

// restored is a loaded run and the change its activation writes, if any.
type restored struct {
	run    *jobRun
	change *recordChange
}

type changeKind int

const (
	changeCreate changeKind = iota
	changeUpdate
	changeOutcome
	changeBlock
	changeDelete
	changeDrop
	// changeRestore is a loaded run taken into memory as stored.
	changeRestore
)

// recordChange is a change of the records, staged in a transaction and adopted in its commit step (`restore`: a loaded
// run taken into memory as stored).
type recordChange struct {
	kind    changeKind
	run     *jobRun
	fields  runFields
	outcome AnyOutcome
	reason  BlockedReason
}

// staging is one transaction's changes of one job: the runs it started (dedupe, concurrency) and whose finish it is.
type staging struct {
	records *jobRecords
	started []*jobRun
	changes []recordChange
	// adopts is what the transaction's store writes leave in memory, adopted with the changes.
	adopts    []func()
	finishing *jobRun
}

var _ documents.Staged = (*staging)(nil)

var (
	restart = jobEnded{kind: OutcomeFailed, err: JobError{Kind: JobAborted, Reason: AbortedByRestart}}
	aborted = jobEnded{kind: OutcomeFailed, err: JobError{Kind: JobAborted, Reason: AbortedByAbort}}
)

// restartLimit is how many times in a row a persisted run is cut off before it ends `restart_limit` (D6, Durable's
// limit).
const restartLimit = 5

// AbortResult is what `abort` resolved with.
type AbortResult string

// The results of `abort`.
const (
	// AbortMarked: the run was marked (and its `run` settled).
	AbortMarked AbortResult = "marked"
	// AbortTerminal: no unsettled run has that id.
	AbortTerminal AbortResult = "terminal"
)

// runSet is a set of runs in insertion order (Pi's `Set`).
type runSet struct {
	items map[*jobRun]*list.Element
	order list.List
}

func newRunSet() *runSet { return &runSet{items: map[*jobRun]*list.Element{}} }

func (set *runSet) add(run *jobRun) {
	if _, found := set.items[run]; !found {
		set.items[run] = set.order.PushBack(run)
	}
}

func (set *runSet) delete(run *jobRun) {
	if element, found := set.items[run]; found {
		set.order.Remove(element)
		delete(set.items, run)
	}
}

func (set *runSet) clear() {
	clear(set.items)
	set.order.Init()
}

// snapshot is the runs in insertion order.
func (set *runSet) snapshot() []*jobRun {
	runs := make([]*jobRun, 0, len(set.items))
	for element := set.order.Front(); element != nil; element = element.Next() {
		runs = append(runs, element.Value.(*jobRun))
	}
	return runs
}

// jobRecords are the records of one job name on one object. The live attempt's `job` invokes and finishes its runs;
// between live attempts, committed runs not yet invoked and runs whose outcome is recorded wait for the next one.
type jobRecords struct {
	definition *JobDefinition
	running    *running
	owner      jobRecordsOwner
	loop       sectionLoop
	states     *runStates
	// persisted is the store side of a persisted job's runs; nil in memory.
	persisted *persistedRuns
	runs      map[string]*jobRun
	// order is the runs in the order they were taken in (Pi's Map order).
	order *runSet
	keys  map[string]*jobRun
	// active counts running and finishing runs: concurrency and keep-alive count them.
	active int
	// job is the live attempt's job; nil between live attempts.
	job *job
	// waiting are the runs waiting for a live attempt: to be invoked (`invocation` pending) or finished (phase
	// `finishing`).
	waiting *runSet
	// stopped are the runs a clean stop cut off whose record the close writer has not written `stopped` yet.
	stopped *runSet
	// loaded is whether a persisted job's records were loaded (once per object).
	loaded bool
	// ended is the object's end error, once it ended.
	ended *data.ChordError
}

func newJobRecords(definition *JobDefinition, owner jobRecordsOwner) *jobRecords {
	records := &jobRecords{
		definition: definition, owner: owner, loop: jobSections{owner.host.loop(), owner.host}, runs: map[string]*jobRun{}, order: newRunSet(),
		keys: map[string]*jobRun{}, waiting: newRunSet(), stopped: newRunSet(),
	}
	var states *documents.Persistence
	if definition.Persist != nil {
		states = documents.NewPersistence(owner.store, owner.storeOwner, "chord.job-states", definition.Name)
		records.persisted = newPersistedRuns(records.loop, documents.NewPersistence(owner.store, owner.storeOwner, "chord.jobs", definition.Name), states)
	}
	if definition.State != nil {
		owned := runStatesOwner{
			documents: owner.documents,
			changed:   records.stateChanged,
			held:      owner.held,
		}
		records.states = newRunStates(records.loop, definition.State, owned, states)
	}
	records.running = newRunning(owner.documents.Batch(), records.name())
	return records
}

// name is the job in errors: its object and its name.
func (records *jobRecords) name() string {
	return records.owner.label + " job " + records.definition.Name
}

// outcome blocks until the outcome of the unsettled run `id`; a settled or unknown id returns `gone`.
func (records *jobRecords) outcome(id string, ctx context.Context) (AnyOutcome, error) {
	var run *jobRun
	records.loop.Run(func() { run = records.runs[id] })
	if run == nil {
		return AnyOutcome{}, data.NewChordError(data.ChordErrorInit{Code: data.CodeGone, Message: records.name() + ": no unsettled run " + id}, nil)
	}
	return run.waiters.wait(ctx)
}

// abort aborts run `id` (D7, Durable's `abortTask`): a blocked run is dropped; a finishing one is only marked; any other
// is marked (stored for a persisted job) in a transaction, its signal fired after the commit, outside the batch and the
// line, and the abort joins its invocation. AbortMarked, or AbortTerminal when no unsettled run has that id.
func (records *jobRecords) abort(id string, ctx context.Context) (AbortResult, error) {
	var run *jobRun
	var blocked, marked bool
	records.loop.Run(func() {
		run = records.runs[id]
		if run == nil || records.ended != nil {
			run = nil
			return
		}
		blocked, marked = run.phase == phaseBlocked, run.abortMarked
	})
	if run == nil {
		return AbortTerminal, nil
	}
	if blocked {
		records.record(run, recordChange{kind: changeDrop, run: run})
		return AbortMarked, nil
	}
	if !marked {
		records.record(run, recordChange{kind: changeUpdate, run: run, fields: runFields{abortMarked: true}})
	}
	var finishing bool
	records.loop.Run(func() {
		// A finishing run's outcome stays: it is only marked.
		if finishing = run.phase == phaseFinishing; finishing {
			return
		}
		if run.invocation != nil {
			run.invocation.abort(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: fmt.Sprintf("%s: run %s aborted", records.name(), id)}, nil))
		}
	})
	if finishing {
		return AbortMarked, nil
	}
	return AbortMarked, run.over(ctx)
}

// stage is `job.stage`'s checks and the staged creation, against the object's runs. A section.
func (records *jobRecords) stage(tx *documents.Transaction, args any, ctx context.Context, options documents.StartOptions) (Run, error) {
	definition := records.definition
	// Plugin code, not type-checked: the args are checked as a method's are (R7).
	validator, err := data.ValidatorFor(definition.Args, ctx)
	if err != nil {
		return nil, err
	}
	if err := data.Check(validator, args, data.ReasonArgs, ctx); err != nil {
		return nil, err
	}
	var staged *staging
	if each, ok := tx.StagedOf(records).(*staging); ok {
		staged = each
	}
	if key := options.Key; key != nil {
		var known *jobRun
		if staged != nil {
			for _, run := range staged.started {
				if run.key != nil && *run.key == *key {
					known = run
					break
				}
			}
		}
		if known == nil {
			known = records.keys[*key]
		}
		if known != nil && (staged == nil || known != staged.finishing) {
			return known.handle, nil
		}
	}
	active := records.active
	if staged != nil {
		active += len(staged.started)
		if staged.finishing != nil {
			active--
		}
	}
	if limit := definition.Concurrency; limit != nil && active >= *limit {
		message := fmt.Sprintf("%s: at its concurrency limit (%d)", records.name(), *limit)
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonBusy, Message: message}, nil)
	}
	persisted := records.persisted != nil
	var trace map[string]string
	if persisted {
		span := chordctx.Telemetry(ctx)
		trace = span.Propagation().Inject(span)
	}
	phase := phaseRunning
	if persisted {
		phase = phasePending
	}
	// Checked above: the args are JSON (the definition refuses bytes).
	run := newJobRun(records.loop, uuidv7(), definition.Version, options.Key, args, phase, trace)
	each, err := records.staging(tx)
	if err != nil {
		return nil, err
	}
	each.started = append(each.started, run)
	if err := records.change(tx, recordChange{kind: changeCreate, run: run}); err != nil {
		return nil, err
	}
	if definition.State != nil {
		initial, err := definition.State.Initial(ctx)
		if err != nil {
			return nil, err
		}
		adopt, err := records.states.stageCreate(run.id, initial, func() *[]store.StagedWrite { return tx.StageStore(records.owner.store) })
		if err != nil {
			return nil, err
		}
		each.adopts = append(each.adopts, adopt)
	}
	return run.handle, nil
}

// change stages change in tx: in the job's staging, adopted in the commit step, and for a persisted job the run's record
// written with it (rule 8: serialized now), and for a settled run its state document retired. A section.
func (records *jobRecords) change(tx *documents.Transaction, change recordChange) error {
	each, err := records.staging(tx)
	if err != nil {
		return err
	}
	each.changes = append(each.changes, change)
	run := change.run
	retires := change.kind == changeDelete || change.kind == changeDrop
	if retires && records.states != nil && records.states.trackers[run.id] != nil {
		if _, err := tx.RetireSlot(records.states, run.id); err != nil {
			return err
		}
	}
	persisted := records.persisted
	if persisted == nil {
		return nil
	}
	writes := tx.StageStore(records.owner.store)
	if retires {
		each.adopts = append(each.adopts, persisted.stageRetire(run.id, writes))
		return nil
	}
	adopt, err := persisted.stage(run.id, storedRun(run, records.definition.Fingerprint, recordFields(change)), writes)
	if err != nil {
		return err
	}
	each.adopts = append(each.adopts, adopt)
	return nil
}

// staging is this job's changes in tx, made on the first. A section.
func (records *jobRecords) staging(tx *documents.Transaction) (*staging, error) {
	made, err := tx.Stage(records, func() documents.Staged { return &staging{records: records} })
	if err != nil {
		return nil, err
	}
	return made.(*staging), nil
}

// Adopt implements documents.Staged: adopted in the commit step.
func (each *staging) Adopt() error {
	records := each.records
	// A step admitted before the object ended adopts after it: the records are not changed any more.
	if records.ended != nil {
		return nil
	}
	// What is stored first: a new run's state document exists when its `running` entry is made.
	for _, adopt := range each.adopts {
		adopt()
	}
	for _, change := range each.changes {
		records.adopt(change)
	}
	return nil
}

// Committed implements documents.Staged: the actions after the commit.
func (each *staging) Committed() {
	records := each.records
	ended := records.ended
	if ended == nil {
		for _, change := range each.changes {
			records.committed(change)
		}
		return
	}
	// Its runs end as the object's other runs did.
	for _, change := range each.changes {
		if change.kind == changeCreate {
			change.run.reject(ended)
		}
	}
}

// Discarded implements documents.Staged.
func (each *staging) Discarded() {
	for _, change := range each.changes {
		if change.kind == changeCreate {
			change.run.reject(each.records.discarded())
		}
	}
}

// load is a persisted job's runs, loaded once per object at its first activation (slice 6b §4.2), again only after a load
// failed (design §9): each record read; a record of another version or fingerprint blocked (`version`) as stored, its
// state document left undecoded; the others' state documents read and decoded, a run cut off while `running` counted
// (`restarts + 1`; at the limit it ends `restart_limit`, reported `job_interrupted`), `stopped` and `pending` ones made
// `pending` again. The runs and their activation changes are adopted in one commit, all before anything runs; a failure
// leaves nothing loaded. It blocks (the store's reads, the commit).
func (records *jobRecords) load(ctx context.Context) error {
	persisted := records.persisted
	var done bool
	records.loop.Run(func() { done = persisted == nil || records.loaded })
	if done {
		return nil
	}
	var all []restored
	fail := func(err error) error {
		records.loop.Run(func() {
			persisted.drop()
			for _, each := range all {
				if records.states != nil {
					records.states.Retire(each.run.id)
				}
			}
		})
		return err
	}
	loaded, err := persisted.load(ctx)
	if err != nil {
		return fail(err)
	}
	for _, record := range loaded {
		each, err := records.restored(record, ctx)
		if err != nil {
			return fail(err)
		}
		all = append(all, each)
	}
	if len(all) > 0 {
		documentsOf := records.owner.documents
		if _, err := documentsOf.Tx(func(tx *documents.Transaction) (any, error) {
			var err error
			documentsOf.Do(func() { err = records.restoreAll(tx, all) })
			return nil, err
		}, documentsOf.Own(), ctx); err != nil {
			return fail(err)
		}
	}
	records.loop.Run(func() {
		records.loaded = true
		for _, each := range all {
			if each.change != nil && each.change.kind == changeOutcome && each.change.outcome.Kind == OutcomeFailed && each.change.outcome.Error.Kind == JobRestartLimit {
				records.report(HostError{Kind: ErrJobInterrupted, ID: each.run.id, Restarts: each.change.outcome.Error.Restarts})
			}
		}
		for _, each := range all {
			records.need(each.run)
		}
	})
	return nil
}

// restoreAll stages the loaded runs in tx: each restored, then the activation changes (adopted after every restore).
func (records *jobRecords) restoreAll(tx *documents.Transaction, all []restored) error {
	each, err := records.staging(tx)
	if err != nil {
		return err
	}
	for _, loaded := range all {
		each.changes = append(each.changes, recordChange{kind: changeRestore, run: loaded.run})
	}
	for _, loaded := range all {
		if loaded.change != nil {
			if err := records.change(tx, *loaded.change); err != nil {
				return err
			}
		}
	}
	return nil
}

// attach is: `job`, of the attempt that went live, invokes and finishes the runs from now on; the waiting ones first.
func (records *jobRecords) attach(attached *job) {
	if records.ended != nil {
		return
	}
	records.job = attached
	waiting := records.waiting.snapshot()
	records.waiting.clear()
	for _, run := range waiting {
		records.dispatch(run, attached)
	}
}

// detach is: `job`'s attempt was sealed: its running invocations are cut off (`cut`), the runs it was finishing wait for
// the next live attempt.
func (records *jobRecords) detach(detached *job, finishing []*jobRun) {
	if records.job == detached {
		records.job = nil
	}
	for _, run := range finishing {
		records.need(run)
	}
}

// requeue is: a run whose reservation its sealed attempt never committed waits for the next live attempt (dispatched
// now when one is attached already: the sealed attempt was detached at its seal).
func (records *jobRecords) requeue(run *jobRun) { records.need(run) }

// reserve records `run` running before its `run` is invoked (a persisted run's reservation: a crash after it counts).
func (records *jobRecords) reserve(tx *documents.Transaction, run *jobRun) error {
	phase := phaseRunning
	return records.change(tx, recordChange{kind: changeUpdate, run: run, fields: runFields{phase: &phase}})
}

// progressed is: a state change of a run with restarts counted resets them in its commit (progress, D6).
func (records *jobRecords) progressed(tx *documents.Transaction, run *jobRun) error {
	if run.restarts == 0 {
		return nil
	}
	zero := 0
	return records.change(tx, recordChange{kind: changeUpdate, run: run, fields: runFields{restarts: &zero}})
}

// settled is: a run's invocation settled: its outcome is recorded (phase `finishing`) in a transaction of the object
// behind the state changes admitted before, so `outcome.State` has them; then it is finished on the live attempt.
func (records *jobRecords) settled(run *jobRun, ended jobEnded) {
	records.loop.Go(func() { records.recordOutcome(run, ended) })
}

// cut is: a running run whose attempt was sealed: its signal fires with err, its invocation is fenced (later state
// changes refused, the open one discarded, its settling ignored). In memory it ends `failed { aborted, reason:
// "restart" }`, finished on the next live attempt; persisted it is stopped (R3), written back by the close writer and
// resumed by the next live attempt as after a clean close.
func (records *jobRecords) cut(run *jobRun, err *data.ChordError) {
	// Only an invoked run is cut: its current invocation is the one the seal stops.
	current := run.invocation
	current.settle()
	records.owner.documents.Fence(current.token)
	current.abort(err)
	if records.persisted != nil {
		run.phase = phaseStopped
		// Resumed per the job's `resume` (an `abandon` job's run ends `failed { aborted, restart }`).
		run.resuming = true
		records.stopped.add(run)
		if records.ended == nil {
			records.need(run)
		}
		return
	}
	if records.ended == nil {
		records.loop.Go(func() { records.recordOutcome(run, restart) })
	}
}

// record records change (a run's finish failure, its block, its drop) in a transaction of the object, if `run` is still
// one of its runs there. The object's end ends it. It blocks.
func (records *jobRecords) record(run *jobRun, change recordChange) {
	documentsOf := records.owner.documents
	// The object ended: its runs went with it.
	_, _ = documentsOf.Tx(func(tx *documents.Transaction) (any, error) {
		var err error
		documentsOf.Do(func() {
			if records.runs[run.id] == run {
				err = records.change(tx, change)
			}
		})
		return nil, err
	}, documentsOf.Own(), records.owner.context)
}

// writeAtClose is the close writer (slice 6b §4.2, Durable's `writeAtClose`): one commit past the seal writing the runs
// a clean stop cut off `stopped` and every held state change, straight to the store (the object's transactions are
// refused by now). Its failure is the store's; the records stay as they were (a crash counts them). It blocks.
func (records *jobRecords) writeAtClose(ctx context.Context) {
	persisted := records.persisted
	if persisted == nil {
		return
	}
	var writes []store.StagedWrite
	var adopts []func()
	records.loop.Run(func() {
		for _, run := range records.stopped.snapshot() {
			adopt, err := persisted.stage(run.id, storedRun(run, records.definition.Fingerprint, storedFields{}), &writes)
			if err == nil {
				adopts = append(adopts, adopt)
			}
		}
		records.stopped.clear()
		for _, id := range records.heldIds() {
			if adopt, err := records.states.stageHeld(id, &writes); err == nil && adopt != nil {
				adopts = append(adopts, adopt)
			}
		}
	})
	if len(writes) == 0 {
		return
	}
	fence := store.CommitFence{
		Admit: func() bool { return true },
		Adopt: func(int64, []int64) error {
			records.loop.Run(func() {
				for _, adopt := range adopts {
					adopt()
				}
			})
			return nil
		},
	}
	// The store failed or refused it (reported as the store's): a crash loses as much.
	_, _ = records.owner.store.Commit(writes, fence, chordctx.WithoutAbortSignal(ctx))
}

// heldIds are the runs holding state changes not stored yet.
func (records *jobRecords) heldIds() []string {
	if records.states == nil {
		return nil
	}
	return records.states.heldIds()
}

// flushHeld stores every held state change whose deadline passed (`now`), in one transaction of the object (D8). It
// blocks.
func (records *jobRecords) flushHeld(now int64) {
	states := records.states
	var holding bool
	records.loop.Run(func() { holding = states != nil && len(states.heldIds()) > 0 })
	if !holding {
		return
	}
	documentsOf := records.owner.documents
	// The object is ending: its close writer stores what is held.
	_, _ = documentsOf.Tx(func(tx *documents.Transaction) (any, error) {
		var err error
		documentsOf.Do(func() {
			each, stagingErr := records.staging(tx)
			if stagingErr != nil {
				err = stagingErr
				return
			}
			for _, id := range states.heldIds() {
				if states.deadline(id) > now {
					continue
				}
				adopt, stageErr := states.stageHeld(id, tx.StageStore(records.owner.store))
				if stageErr != nil {
					err = stageErr
					return
				}
				if adopt != nil {
					each.adopts = append(each.adopts, adopt)
				}
			}
		})
		return nil, err
	}, documentsOf.Own(), records.owner.context)
}

// nextDeadline is the earliest deadline of a held state change (math.MaxInt64: none).
func (records *jobRecords) nextDeadline() int64 {
	states := records.states
	if states == nil {
		return noDeadline
	}
	earliest := noDeadline
	for _, id := range states.heldIds() {
		earliest = min(earliest, states.deadline(id))
	}
	return earliest
}

// stateOf is the value of run `id`'s state document, nil without one or once retired.
func (records *jobRecords) stateOf(id string) *delta.JsonObject {
	if records.states == nil {
		return nil
	}
	return records.states.value(id)
}

// stateValue is stateOf as an interface value: nil, not a nil pointer, when absent.
func (records *jobRecords) stateValue(id string) any {
	if value := records.stateOf(id); value != nil {
		return value
	}
	return nil
}

// persists is whether the job persists its runs.
func (records *jobRecords) persists() bool { return records.persisted != nil }

// end is: the object ends: running runs' signals fire, every waiter returns err; blocked in-memory runs are dropped
// (reported), persisted ones keep their records; nothing finishes. The records stay readable until `drop`.
func (records *jobRecords) end(err *data.ChordError) {
	if records.ended != nil {
		return
	}
	records.ended = err
	records.job = nil
	records.waiting.clear()
	for _, run := range records.order.snapshot() {
		if run.phase == phaseBlocked && records.persisted == nil {
			records.report(HostError{Kind: ErrJobDropped, ID: run.id})
		}
		current := run.invocation
		if current != nil {
			current.abort(err)
		}
		run.reject(err)
		if current != nil && current.state != invocationRunning {
			current.ended()
		}
	}
	if records.active > 0 {
		records.owner.countRuns(-records.active)
	}
}

// drop is: its object's documents are dropped: the runs, their state documents and `running`'s watches go.
func (records *jobRecords) drop() {
	clear(records.runs)
	records.order.clear()
	clear(records.keys)
	if records.states != nil {
		records.states.drop()
	}
	if records.persisted != nil {
		records.persisted.drop()
	}
	records.running.end()
}

// report reports a run's failure, drop or interruption to the host.
func (records *jobRecords) report(err HostError) {
	err.Object = records.owner.label
	err.Job = records.definition.Name
	records.owner.host.report(err)
}

// restored is the run a loaded record stands for and the change its activation writes (D6): a record of another version
// or fingerprint blocked as stored (its state document only found, never decoded: slice 7 maps it); otherwise its state
// document read and decoded (migrated), pinned while it is unsettled, a crash counted, at the limit its outcome, marked
// resuming unless blocked or finishing. It blocks (the store's reads).
func (records *jobRecords) restored(loaded loadedRecord, ctx context.Context) (restored, error) {
	id, record := loaded.id, loaded.record
	definition := records.definition
	run := newJobRun(records.loop, id, record.Version, record.Key, record.Args, record.Phase, record.Trace)
	run.restarts = record.Restarts
	run.finishFailures = record.FinishFailures
	run.abortMarked = record.Abort
	if record.Version != definition.Version || record.Fingerprint != definition.Fingerprint {
		run.phase = phaseBlocked
		reason := BlockedVersion
		run.blocked = &reason
		// What is stored of its state (also when this version declares none), so an abort drops it with the record.
		if err := records.persisted.findBlockedState(id, ctx); err != nil {
			return restored{}, err
		}
		return restored{run: run}, nil
	}
	states := records.states
	if states != nil {
		document, err := states.persistence.Read(id, ctx)
		if err != nil {
			return restored{}, err
		}
		if document != nil {
			decoded, err := states.persistence.Decode(document, states.definition.Current(), ctx)
			if err != nil {
				return restored{}, err
			}
			records.loop.Run(func() { states.create(id, decoded.Value, &decoded.Stored) })
		}
	}
	var failure error
	var each restored
	records.loop.Run(func() {
		if record.outcome != nil {
			outcome, err := outcomeOf(*record.outcome, id, record.Args, records.stateValue(id))
			if err != nil {
				failure = err
				return
			}
			run.outcome = &outcome
		}
		run.blocked = record.Blocked
		switch record.Phase {
		case phaseRunning:
			restarts := record.Restarts + 1
			run.resuming = true
			if restarts < restartLimit {
				pending := phasePending
				each = restored{run: run, change: &recordChange{kind: changeUpdate, run: run, fields: runFields{phase: &pending, restarts: &restarts}}}
				return
			}
			run.restarts = restarts
			outcome := records.outcomeFor(run, jobEnded{kind: OutcomeFailed, err: JobError{Kind: JobRestartLimit, Restarts: restarts}})
			each = restored{run: run, change: &recordChange{kind: changeOutcome, run: run, outcome: outcome}}
		case phaseStopped:
			run.resuming = true
			pending := phasePending
			each = restored{run: run, change: &recordChange{kind: changeUpdate, run: run, fields: runFields{phase: &pending}}}
		case phasePending:
			run.resuming = true
			each = restored{run: run}
		default:
			each = restored{run: run}
		}
	})
	return each, failure
}

// recordOutcome records the outcome of `run` (with what its state holds, D8), then has it finished on the live attempt.
// It blocks (the transaction).
func (records *jobRecords) recordOutcome(run *jobRun, ended jobEnded) {
	documentsOf := records.owner.documents
	_, err := documentsOf.Tx(func(tx *documents.Transaction) (any, error) {
		var err error
		documentsOf.Do(func() {
			outcome := records.outcomeFor(run, ended)
			if err = records.change(tx, recordChange{kind: changeOutcome, run: run, outcome: outcome}); err != nil {
				return
			}
			// What its state holds is stored before the outcome is recorded (D8).
			if records.persisted == nil || records.states == nil {
				return
			}
			adopt, stageErr := records.states.stageHeld(run.id, tx.StageStore(records.owner.store))
			if stageErr != nil {
				err = stageErr
				return
			}
			if adopt != nil {
				each, stagingErr := records.staging(tx)
				if stagingErr != nil {
					err = stagingErr
					return
				}
				each.adopts = append(each.adopts, adopt)
			}
		})
		return nil, err
	}, documentsOf.Own(), records.owner.context)
	records.loop.Run(func() {
		// A run ending without an invocation (marked aborted, abandoned, cut off in memory) has none to wait for.
		if run.invocation != nil {
			run.invocation.ended()
		}
		// The object ended: nothing finishes.
		if err == nil {
			records.need(run)
		}
	})
}

// outcomeFor is `run`'s outcome for ended, with its args and its state as it is now.
func (records *jobRecords) outcomeFor(run *jobRun, ended jobEnded) AnyOutcome {
	return AnyOutcome{ID: run.id, Args: run.args, State: records.stateValue(run.id), Kind: ended.kind, Result: ended.result, Error: ended.err}
}

// need is: `run` needs a live attempt (to be invoked or finished): now, or once one attaches.
func (records *jobRecords) need(run *jobRun) {
	if records.ended != nil || records.runs[run.id] != run {
		return
	}
	if records.job == nil {
		records.waiting.add(run)
		return
	}
	records.dispatch(run, records.job)
}

// dispatch invokes or finishes `run` on `job`, as its record stands: a finishing run is finished; a run not invoked yet
// is invoked, unless it is marked aborted (it ends `aborted` without `run`) or resumes abandoned (`failed { aborted,
// reason: "restart" }`).
func (records *jobRecords) dispatch(run *jobRun, target *job) {
	if records.runs[run.id] != run {
		return
	}
	if run.phase == phaseFinishing {
		target.finish(run)
		return
	}
	if run.phase == phaseBlocked || run.invoked() {
		return
	}
	if run.abortMarked {
		records.loop.Go(func() { records.recordOutcome(run, aborted) })
		return
	}
	if run.resuming && records.definition.Persist != nil && records.definition.Persist.Resume == ResumeAbandon {
		records.loop.Go(func() { records.recordOutcome(run, restart) })
		return
	}
	run.resuming = false
	target.invoke(run)
}

// adopt adopts one record change, inside the commit step: the maps, the active count, `running`. A new run is counted
// for its object here, before the commit's publication (a link closing while it sends may release the object's last
// reference) and before any run of the same commit settles in `committed`.
func (records *jobRecords) adopt(change recordChange) {
	run := change.run
	switch change.kind {
	case changeCreate, changeRestore:
		records.runs[run.id] = run
		records.order.add(run)
		if run.key != nil {
			records.keys[*run.key] = run
		}
		if run.phase != phaseBlocked {
			records.active++
			records.owner.countRuns(1)
		}
		run.entry = run.entryFor(records.stateOf(run.id))
		records.running.add(run.entry)
	case changeUpdate:
		fields := change.fields
		if fields.phase != nil {
			run.phase = *fields.phase
		}
		if fields.restarts != nil {
			run.restarts = *fields.restarts
		}
		if fields.finishFailures != nil {
			run.finishFailures = *fields.finishFailures
		}
		if fields.abortMarked {
			run.abortMarked = true
		}
		records.replaceEntry(run)
	case changeOutcome:
		outcome := change.outcome
		run.outcome = &outcome
		run.phase = phaseFinishing
		records.replaceEntry(run)
	case changeBlock:
		run.phase = phaseBlocked
		reason := change.reason
		run.blocked = &reason
		records.active--
		records.replaceEntry(run)
	case changeDelete, changeDrop:
		delete(records.runs, run.id)
		records.order.delete(run)
		// The finishing run's key may name its successor already, started in the same commit.
		if run.key != nil && records.keys[*run.key] == run {
			delete(records.keys, *run.key)
		}
		if change.kind == changeDelete {
			records.active--
		}
		records.running.remove(run.entry)
		run.entry = nil
	}
}

// committed is the actions after a record change committed.
func (records *jobRecords) committed(change recordChange) {
	run := change.run
	switch change.kind {
	case changeCreate:
		// After the commit, outside the batch, once the transaction settled; on the live attempt, or the next.
		records.loop.Queue(func() { records.need(run) })
	case changeDelete:
		records.owner.countRuns(-1)
		run.resolve(*run.outcome)
	case changeBlock:
		records.owner.countRuns(-1)
	case changeDrop:
		records.report(HostError{Kind: ErrJobDropped, ID: run.id})
		run.reject(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: fmt.Sprintf("%s: run %s dropped", records.name(), run.id)}, nil))
	case changeRestore, changeUpdate, changeOutcome:
	}
}

// stateChanged is: a run's state document changed by ops, inside the commit step: its entry is replaced.
func (records *jobRecords) stateChanged(id string, ops []delta.Op) {
	run := records.runs[id]
	// Its record may not be adopted yet (a start creates both in one commit; its entry takes the state then).
	if run == nil || run.entry == nil {
		return
	}
	before := run.entry
	run.entry = run.entryFor(records.stateOf(id))
	records.running.stateChanged(before, run.entry, ops)
}

// replaceEntry is: a run's record changed, inside the commit step: its entry is replaced.
func (records *jobRecords) replaceEntry(run *jobRun) {
	before := run.entry
	run.entry = run.entryFor(records.stateOf(run.id))
	records.running.replace(before, run.entry)
}

func (records *jobRecords) discarded() error {
	return data.NewChordError(data.ChordErrorInit{
		Code: data.CodeReleased, Reason: data.ReasonDiscarded, Message: records.name() + ": the start's transaction was discarded",
	}, nil)
}

// recordFields is the record fields a change writes, its phase transitions included.
func recordFields(change recordChange) storedFields {
	switch change.kind {
	case changeUpdate:
		return storedFields{phase: change.fields.phase, restarts: change.fields.restarts, finishFailures: change.fields.finishFailures, abortMarked: change.fields.abortMarked}
	case changeOutcome:
		phase := phaseFinishing
		outcome := change.outcome
		return storedFields{phase: &phase, outcome: &outcome}
	case changeBlock:
		phase := phaseBlocked
		reason := change.reason
		return storedFields{phase: &phase, blocked: &reason}
	}
	return storedFields{}
}
