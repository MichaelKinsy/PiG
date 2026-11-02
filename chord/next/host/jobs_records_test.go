package host

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/jobs/records.test.ts: a job's records on its object (slice 6a §1.2, §15 rule 5): runs
// across attempts, cut off by an attempt's seal, finished on the next live attempt, and the keep-alive of runs waiting
// for one. Go forms: jobs_job_test.go.
//
// pi: packages/chord/src/next/jobs/records.ts

var jobWork = mustDefineJob(DefineJob("app.work", JobOptions{
	Version: 1, Args: contracts.Obj(contracts.Prop("text", contracts.Str())), Result: contracts.Str(), State: jobProgress,
}))

// workingRunner is Pi's `working()`: a restartable object whose every attempt declares `Work`, whose `run` waits for its
// abort, and whose `finish` is finish(attempt, outcome).
type workingRunner struct {
	*restartableRunner
	mu   sync.Mutex
	jobs []JobHandle
}

func (w *workingRunner) Job(index int) JobHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	if index < 0 {
		index += len(w.jobs)
	}
	return w.jobs[index]
}

func working(t *testing.T, finish func(attempt int, outcome Outcome) error, started chan<- struct{}) *workingRunner {
	t.Helper()
	w := &workingRunner{}
	w.restartableRunner = restartable(t, func(attempt int, self Self) {
		handle := self.Job(jobWork, JobImplementation{
			Run: func(args any, ctx JobCtx) (any, error) {
				if err := setOutput(ctx, textOf(args)+" started", ChangeOptions{}); err != nil {
					return nil, err
				}
				if started != nil && attempt == 1 {
					select {
					case started <- struct{}{}:
					default:
					}
				}
				untilDone(ctx)
				return textOf(args), nil
			},
			Finish: func(outcome Outcome, _ documents.Tx) error { return finish(attempt, outcome) },
		})
		w.mu.Lock()
		w.jobs = append(w.jobs, handle)
		w.mu.Unlock()
	}, loadedOptions{CloseTimeout: new(50 * time.Millisecond)})
	return w
}

// records.test.ts › runs across attempts › a run cut off by its attempt's seal ends aborted (restart) and is finished on the next live attempt
func TestRecordsARunCutOffByItsAttemptsSealEndsAbortedRestartAndIsFinishedOnTheNextLiveAttempt(t *testing.T) {
	var mu sync.Mutex
	var finished []string
	started := make(chan struct{}, 1)
	w := working(t, func(attempt int, outcome Outcome) error {
		mu.Lock()
		defer mu.Unlock()
		finished = append(finished, fmt.Sprintf("%d: %s", attempt, outcome.Kind))
		return nil
	}, started)
	run := jobStart(t, w.Job(0), textArgs("a"), background)
	<-started
	w.Fail()
	// No live attempt: the run waits, finishing.
	eventually(t, "the run to wait finishing", func() bool {
		entries := jobRunning(t, w.Job(0))
		return len(entries) == 1 && phases(entries)[0] == "finishing"
	})
	mu.Lock()
	if len(finished) != 0 {
		t.Fatalf("finished = %v before the retry", finished)
	}
	mu.Unlock()
	w.Retry()
	outcome := runOutcome(t, run)
	if outcome.ID != run.ID() || textOf(outcome.Args) != "a" || outputOf(outcome.State) != "a started" ||
		outcome.Kind != OutcomeFailed || outcome.Error.Kind != JobAborted || outcome.Error.Reason != AbortedByRestart {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	if len(finished) != 1 || finished[0] != "2: failed" {
		t.Fatalf("finished = %v", finished)
	}
	mu.Unlock()
	eventually(t, "the second attempt's list to be empty", func() bool { return len(jobRunning(t, w.Job(1))) == 0 })
}

// records.test.ts › runs across attempts › refuses the cut run's changes made while it stops; its settling records nothing more
func TestRecordsRefusesTheCutRunsChangesMadeWhileItStopsItsSettlingRecordsNothingMore(t *testing.T) {
	late := make(chan error, 1)
	started := newJobGate()
	var mu sync.Mutex
	var jobs []JobHandle
	runner := restartable(t, func(_ int, self Self) {
		handle := self.Job(jobWork, JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			started.Open()
			untilDone(ctx)
			// Accepted after an abort (§15), refused once the attempt's seal cut the run off.
			_, err := ctx.State().Change(background, func(draft *delta.Object) (any, error) { return nil, draft.Set("output", "late") }, ChangeOptions{})
			select {
			case late <- err:
			default:
			}
			return "done", nil
		}})
		mu.Lock()
		jobs = append(jobs, handle)
		mu.Unlock()
	}, loadedOptions{})
	mu.Lock()
	first := jobs[0]
	mu.Unlock()
	run := jobStart(t, first, textArgs("a"), background)
	started.Wait()
	runner.Restart()
	outcome := runOutcome(t, run)
	expectChordError(t, <-late, data.CodeReleased, "")
	if outcome.Kind != OutcomeFailed || outcome.Error.Reason != AbortedByRestart || outputOf(outcome.State) != "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	second := jobs[1]
	mu.Unlock()
	eventually(t, "the second attempt's list to be empty", func() bool { return len(jobRunning(t, second)) == 0 })
}

// records.test.ts › runs across attempts › a finish sealed mid-way is not counted as a failure and runs again on the next live attempt
func TestRecordsAFinishSealedMidWayIsNotCountedAsAFailureAndRunsAgainOnTheNextLiveAttempt(t *testing.T) {
	g := newJobGate()
	var mu sync.Mutex
	var attempts []int
	started := make(chan struct{}, 1)
	w := working(t, func(attempt int, _ Outcome) error {
		mu.Lock()
		attempts = append(attempts, attempt)
		mu.Unlock()
		if attempt == 1 {
			g.Wait()
		}
		return nil
	}, started)
	run := jobStart(t, w.Job(0), textArgs("a"), background)
	<-started
	go func() { _, _ = w.Job(0).Abort(run.ID(), background) }()
	eventually(t, "the first finish", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(attempts) == 1
	})
	w.Restart()
	g.Open()
	// `run` returns its result after the abort: a success.
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "a" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("finishes by attempt = %v", attempts)
	}
	mu.Unlock()
	// No job_finish_failed: the stuck finish only outlived the drain.
	if kinds := kindsOf(w.Errors.All()); len(kinds) != 2 || kinds[0] != "object_failed" || kinds[1] != "drain_timeout" {
		t.Fatalf("errors = %v", kinds)
	}
}

// records.test.ts › keep-alive › a run waiting for a live attempt keeps a failed object no longer: it ends with its last reference
func TestRecordsKeepAliveARunWaitingForALiveAttemptKeepsAFailedObjectNoLongerItEndsWithItsLastReference(t *testing.T) {
	started := make(chan struct{}, 1)
	w := working(t, func(int, Outcome) error { return nil }, started)
	run := jobStart(t, w.Job(0), textArgs("a"), background)
	<-started
	w.Fail()
	w.Reference.Release()
	expectChordError(t, runOutcomeErr(t, run, background), data.CodeReleased, "")
}

var jobSaved = mustDefineState(documents.DefineState("app.saved", documents.StateOptions{
	Schema:         contracts.Obj(contracts.Prop("n", contracts.Int())),
	Version:        1,
	Initial:        func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	Persist:        true,
	CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
}))

// records.test.ts › the object's end during an admitted commit › a run started in a commit held in the SQL while the host closes ends with the object; no run is left counted
func TestRecordsTheObjectsEndDuringAnAdmittedCommitARunStartedInACommitHeldInTheSQLWhileTheHostClosesEndsWithTheObjectNoRunIsLeftCounted(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	var saved *singletonDoc
	var job JobHandle
	runner := storeRestartable(t, func(_ int, self StoreSelf) {
		handle := self.Job(jobWork, JobImplementation{
			Run: func(args any, ctx JobCtx) (any, error) {
				untilDone(ctx)
				return "", nil
			},
			Finish: func(Outcome, documents.Tx) error { return nil },
		})
		doc := stateOf(self, jobSaved)
		mu.Lock()
		saved, job = doc, handle
		mu.Unlock()
	}, loadedOptions{Store: st})
	mu.Lock()
	doc, handle := saved, job
	mu.Unlock()
	// The next commit's step waits inside the backend transaction, after its admission.
	gate := holdNextCommit()
	type started struct {
		run Run
		err error
	}
	startedRun := make(chan started, 1)
	go func() {
		got, err := runner.Attempts.at(0).Tx(func(tx documents.Tx) (any, error) {
			draft, err := tx.Draft(doc.s)
			if err != nil {
				return nil, err
			}
			if err := draft.Set("n", float64(1)); err != nil {
				return nil, err
			}
			return tx.Start(stageable(handle), textArgs("a"))
		}, background)
		if err != nil {
			startedRun <- started{err: err}
			return
		}
		startedRun <- started{run: got.(Run)}
	}()
	<-gate.Entered()
	closed := make(chan error, 1)
	go func() { closed <- runner.Host.Close(background) }()
	nexttest.Flushed()
	select {
	case err := <-closed:
		t.Fatalf("the host closed (%v) before the admitted step finished", err)
	default:
	}
	// The object's teardown waits for the admitted step, so the run's record is adopted before the end.
	gate.Release()
	got := <-startedRun
	must(t, got.err)
	must(t, <-closed)
	expectChordError(t, runOutcomeErr(t, got.run, background), data.CodeReleased, "")
	if jobs := activityCount(t, runner.Host, "jobs"); jobs != 0 {
		t.Fatalf("jobs = %d", jobs)
	}
	if seq := st.LastSeq(); seq != 1 {
		t.Fatalf("lastSeq = %d, want 1", seq)
	}
}

// jobsRunner is the jobs object of the reload tests: `start` starts a run of `text`, `running` lists them.
var jobsRunner = mustContract(contracts.Local("app.runner", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("start", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Str())),
		contracts.Mem("running", contracts.Arr(contracts.Unknown())),
	},
}))

// jobWorkV2 is version 2 of `Work`: its args gained a priority, its result is an object; v1 runs map forwards.
var jobWorkV2 = mustDefineJob(DefineJob("app.work", JobOptions{
	Version: 2,
	Args:    contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("priority", contracts.Int())),
	Result:  contracts.Obj(contracts.Prop("text", contracts.Str())),
	State:   jobProgress,
	Accepts: JobAccepts{1: {
		Args: func(old any) (any, error) {
			return jsonObject("text", old.(*delta.JsonObject).Value("text"), "priority", float64(0)), nil
		},
		Result: func(old any) (any, error) { return jsonObject("text", old), nil },
	}},
}))

// jobWorkV2Strict is `WorkV2` without an adapter for version 1.
var jobWorkV2Strict = mustDefineJob(DefineJob("app.work", JobOptions{
	Version: 2,
	Args:    contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("priority", contracts.Int())),
	Result:  contracts.Obj(contracts.Prop("text", contracts.Str())),
	State:   jobProgress,
}))

type jobFinished struct {
	code    string
	outcome Outcome
}

// finishedLog is what the finishes of the reload tests recorded.
type finishedLog struct {
	mu   sync.Mutex
	list []jobFinished
}

func (log *finishedLog) push(code string, outcome Outcome) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.list = append(log.list, jobFinished{code, outcome})
}

func (log *finishedLog) all() []jobFinished {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]jobFinished(nil), log.list...)
}

// finishesAfterATurn waits for the first finish, lets one more turn of the host pass and returns every finish then: Pi's
// `await flushed(); expect(finished).toEqual([one entry])`, where a second, spurious finish would fail the comparison.
func finishesAfterATurn(t testing.TB, log *finishedLog) []jobFinished {
	t.Helper()
	eventually(t, "its finish to run on the new code", func() bool { return len(log.all()) >= 1 })
	nexttest.Flushed()
	got := log.all()
	if len(got) != 1 {
		t.Fatalf("finished = %+v, want exactly one finish", got)
	}
	return got
}

// runnerOptions are the options of `runner`: the gates its runs wait for (the next queued one each) and whether the object
// opens a native (a reload restarts it).
type runnerOptions struct {
	gates  []*jobGate
	native bool
}

// runner is a facet serving `Runner` with `job` (labelled `code` in what its finish records into `finished`); its `run`
// waits for the next queued gate; with `native` the object opens a native.
func runner(code string, job *JobDefinition, finished *finishedLog, options runnerOptions) Facet {
	var mu sync.Mutex
	gates := options.gates
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(jobsRunner, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			if options.native {
				self.Open(func(context.Context) (any, error) { return code, nil },
					func(any, NativeCloseReason, context.Context) error { return nil })
			}
			if job == nil {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"running": Expose(state.NewConstant([]any{}))},
					Methods: map[string]contracts.MethodFunction{
						"start": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "", nil },
					},
				}}, nil
			}
			handle := self.Job(job, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					var next *jobGate
					mu.Lock()
					if len(gates) > 0 {
						next, gates = gates[0], gates[1:]
					}
					mu.Unlock()
					if next != nil {
						select {
						case <-next.Done():
						case <-ctx.Done():
						}
					}
					if ctx.Err() != nil {
						return nil, context.Cause(ctx)
					}
					text := textOf(args) + " by " + code
					if job.Version == 1 {
						return text, nil
					}
					return jsonObject("text", text), nil
				},
				Finish: func(outcome Outcome, _ documents.Tx) error {
					finished.push(code, outcome)
					return nil
				},
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"running": Expose(handle.Running())},
				Methods: map[string]contracts.MethodFunction{
					"start": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						text := args.(*delta.JsonObject).Value("text")
						started := jsonObject("text", text)
						if job.Version != 1 {
							started = jsonObject("text", text, "priority", float64(1))
						}
						run, err := handle.Start(started, ctx, StartOptions{})
						if err != nil {
							return nil, err
						}
						return run.ID(), nil
					},
				},
			}}, nil
		}, ServeOptions{})
	})
}

// runnerRunning is `jobs.running.value`.
func runnerRunning(t testing.TB, jobs *Reference) []any {
	t.Helper()
	value, err := jobs.Member("running").Value()
	must(t, err)
	return value.([]any)
}

// startedBy is `jobs.start({ text }, context)`: the new run's id.
func startedBy(t testing.TB, jobs *Reference, text string) string {
	t.Helper()
	id, err := jobs.Call("start", jsonObject("text", text), background)
	must(t, err)
	return id.(string)
}

// entryMatches is whether the `running` entries are exactly entries as `toMatchObject` reads them: the same number, each
// of the listed fields equal.
func entryMatches(got []any, want ...map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for index, fields := range want {
		entry, _ := got[index].(*delta.JsonObject)
		if entry == nil {
			return false
		}
		for key, value := range fields {
			if !nexttest.JSONEqual(entry.Value(key), value) {
				return false
			}
		}
	}
	return true
}

func outcomeIs(outcome Outcome, kind OutcomeKind, result any) bool {
	return outcome.Kind == kind && nexttest.JSONEqual(outcome.Result, result)
}

// records.test.ts › jobs across a reload (slice 7b §8) › an older run finishes on its old code across a swap; its finish runs on the new code, mapped forwards once
func TestRecordsJobsAcrossAReloadAnOlderRunFinishesOnItsOldCodeAcrossASwapItsFinishRunsOnTheNewCodeMappedForwardsOnce(t *testing.T) {
	finished := &finishedLog{}
	g := newJobGate()
	host := newJobHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: runner("v1", jobWork, finished, runnerOptions{gates: []*jobGate{g}})}}, background))
	jobs, err := host.Get(background, jobsRunner, jsonObject(), GetOptions{})
	must(t, err)
	startedBy(t, jobs, "a")
	nexttest.Flushed()
	jobsReloaded(t, host, "@test/app", runner("v2", jobWorkV2, finished, runnerOptions{}))
	nexttest.Flushed()
	// Listed with its args mapped forwards.
	eventually(t, "the old run listed with its args mapped forwards", func() bool {
		return entryMatches(runnerRunning(t, jobs), map[string]any{
			"version": float64(1), "args": jsonObject("text", "a", "priority", float64(0)), "phase": "running",
		})
	})
	g.Open()
	got := finishesAfterATurn(t, finished)
	if got[0].code != "v2" || got[0].outcome.Kind != OutcomeDone ||
		!nexttest.JSONEqual(got[0].outcome.Args, jsonObject("text", "a", "priority", float64(0))) ||
		!nexttest.JSONEqual(got[0].outcome.Result, jsonObject("text", "a by v1")) {
		t.Fatalf("finished = %+v", got)
	}
	must(t, host.Close(background))
}

// records.test.ts › jobs across a reload (slice 7b §8) › a run without an adapter is blocked (version) until a reload brings matching code
func TestRecordsJobsAcrossAReloadARunWithoutAnAdapterIsBlockedVersionUntilAReloadBringsMatchingCode(t *testing.T) {
	finished := &finishedLog{}
	g := newJobGate()
	host := newJobHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: runner("v1", jobWork, finished, runnerOptions{gates: []*jobGate{g}})}}, background))
	jobs, err := host.Get(background, jobsRunner, jsonObject(), GetOptions{})
	must(t, err)
	id := startedBy(t, jobs, "a")
	nexttest.Flushed()
	jobsReloaded(t, host, "@test/app", runner("v2", jobWorkV2Strict, finished, runnerOptions{}))
	g.Open()
	eventually(t, "the run to be blocked for its version", func() bool {
		return entryMatches(runnerRunning(t, jobs), map[string]any{"id": id, "version": float64(1), "phase": "blocked", "reason": "version"})
	})
	nexttest.Flushed()
	if len(finished.all()) != 0 {
		t.Fatalf("finished = %+v", finished.all())
	}
	entry := runnerRunning(t, jobs)[0].(*delta.JsonObject)
	if entry.Len() != 4 {
		t.Fatalf("a blocked entry has only id, version, phase and reason: %v", entry)
	}
	jobsReloaded(t, host, "@test/app", runner("v1b", jobWork, finished, runnerOptions{}))
	got := finishesAfterATurn(t, finished)
	if got[0].code != "v1b" || !outcomeIs(got[0].outcome, OutcomeDone, "a by v1") {
		t.Fatalf("finished = %+v", got)
	}
	eventually(t, "the list to be empty", func() bool { return len(runnerRunning(t, jobs)) == 0 })
	must(t, host.Close(background))
}

// records.test.ts › jobs across a reload (slice 7b §8) › a native restart aborts running in-memory runs (reload); their finish runs on the new code
func TestRecordsJobsAcrossAReloadANativeRestartAbortsRunningInMemoryRunsReloadTheirFinishRunsOnTheNewCode(t *testing.T) {
	finished := &finishedLog{}
	host := newJobHost(t, HostOptions{OnError: func(HostError) {}})
	g := newJobGate()
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: runner("v1", jobWork, finished, runnerOptions{gates: []*jobGate{g}, native: true})}}, background))
	jobs, err := host.Get(background, jobsRunner, jsonObject(), GetOptions{})
	must(t, err)
	startedBy(t, jobs, "a")
	nexttest.Flushed()
	jobsReloaded(t, host, "@test/app", runner("v2", jobWork, finished, runnerOptions{native: true}))
	got := finishesAfterATurn(t, finished)
	if got[0].code != "v2" || got[0].outcome.Kind != OutcomeFailed ||
		got[0].outcome.Error.Kind != JobAborted || got[0].outcome.Error.Reason != AbortedByReload {
		t.Fatalf("finished = %+v", got)
	}
	must(t, host.Close(background))
}

// records.test.ts › jobs across a reload (slice 7b §8) › a job the new code no longer declares blocks its runs (version); declared again, they go on
func TestRecordsJobsAcrossAReloadAJobTheNewCodeNoLongerDeclaresBlocksItsRunsVersionDeclaredAgainTheyGoOn(t *testing.T) {
	finished := &finishedLog{}
	g := newJobGate()
	host := newJobHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: runner("v1", jobWork, finished, runnerOptions{gates: []*jobGate{g}})}}, background))
	jobs, err := host.Get(background, jobsRunner, jsonObject(), GetOptions{})
	must(t, err)
	startedBy(t, jobs, "a")
	nexttest.Flushed()
	jobsReloaded(t, host, "@test/app", runner("v2", nil, finished, runnerOptions{}))
	g.Open()
	nexttest.Flushed()
	if len(finished.all()) != 0 {
		t.Fatalf("finished = %+v", finished.all())
	}
	jobsReloaded(t, host, "@test/app", runner("v3", jobWork, finished, runnerOptions{}))
	got := finishesAfterATurn(t, finished)
	if got[0].code != "v3" || !outcomeIs(got[0].outcome, OutcomeDone, "a by v1") {
		t.Fatalf("finished = %+v", got)
	}
	must(t, host.Close(background))
}

// nthTransactionDatabase holds the `n`th backend transaction after `arm` inside it, at its first statement (Go form of
// Pi replacing `store.commit` to hold the second commit's step: `ChordStore.Commit` is a method of a concrete type).
type nthTransactionDatabase struct {
	sqlite.SqliteDatabase
	mu    sync.Mutex
	left  int
	gate  *transactionGate
	armed bool
}

func (db *nthTransactionDatabase) arm(n int) *transactionGate {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.left, db.armed, db.gate = n, true, newTransactionGate()
	return db.gate
}

func (db *nthTransactionDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	var held *transactionGate
	db.mu.Lock()
	if db.armed {
		db.left--
		if db.left == 0 {
			held, db.armed = db.gate, false
		}
	}
	db.mu.Unlock()
	return db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		if held != nil {
			transaction = &heldExecutor{SqliteExecutor: transaction, gate: held}
		}
		return callback(transaction)
	})
}

// records.test.ts › a reservation released after a swap (final review) › starts the old code's run with its own args, not the new version's mapping (Sol)
func TestRecordsAReservationReleasedAfterASwapStartsTheOldCodesRunWithItsOwnArgsNotTheNewVersionsMappingSol(t *testing.T) {
	owner := mustContract(contracts.Local("app.reserve-owner", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	options := func(version int) JobOptions {
		return JobOptions{Version: version, Result: contracts.Int(), Persist: &JobPersistence{Resume: ResumeRerun}}
	}
	v1Options := options(1)
	v1Options.Args = contracts.Obj(contracts.Prop("n", contracts.Int()))
	v1 := mustDefineJob(DefineJob("app.reserve", v1Options))
	v2Options := options(2)
	v2Options.Args = contracts.Obj(contracts.Prop("label", contracts.Str()))
	v2Options.Accepts = JobAccepts{1: {
		Args: func(old any) (any, error) {
			return jsonObject("label", fmt.Sprintf("mapped %v", old.(*delta.JsonObject).Value("n"))), nil
		},
		Result: func(old any) (any, error) { return old, nil },
	}}
	v2 := mustDefineJob(DefineJob("app.reserve", v2Options))
	var db *nthTransactionDatabase
	st := openMemoryStore(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		db = &nthTransactionDatabase{SqliteDatabase: inner}
		return db
	}, nil)
	host := newJobHost(t, HostOptions{Store: st, OnError: func(HostError) {}})
	var mu sync.Mutex
	var seen []any
	var handle JobHandle
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/reserve", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(owner, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle = self.Job(v1, JobImplementation{Run: func(args any, _ JobCtx) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, args.(*delta.JsonObject).Value("n"))
				return float64(42), nil
			}})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})}}, background))
	// The object is live: Pi's `load` resolves with it served; hold a reference so it is.
	reference, err := host.Get(background, owner, jsonObject(), GetOptions{})
	must(t, err)
	defer reference.Release()
	// The run's reservation (the second commit after the start) waits on `gate` until the swap to V2 happened.
	held := db.arm(2)
	run := jobStart(t, handle, jsonObject("n", float64(1)), background)
	<-held.Entered()
	reloaded := make(chan error, 1)
	go func() {
		_, err := host.Reload([]HostFacet{{Name: "@app/reserve", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(owner, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Job(v2, JobImplementation{Run: func(any, JobCtx) (any, error) { return float64(43), nil }})
				return contracts.Implementation{}, nil
			}, ServeOptions{})
		})}}, background, ReloadOptions{})
		reloaded <- err
	}()
	hostDelivered(host)
	nexttest.Flushed()
	held.Release()
	must(t, <-reloaded)
	runOutcome(t, run)
	// V1's handler got V1's args.
	mu.Lock()
	if len(seen) != 1 || seen[0] != float64(1) {
		t.Fatalf("seen = %v", seen)
	}
	mu.Unlock()
	must(t, host.Close(background))
	must(t, st.Close(background))
}
