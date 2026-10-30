package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/jobs/job.test.ts (runs, finishes, `running`, keep-alive, the object's end, GC); the
// start cases are in jobs_job_test.go. The Go forms are described there.

func exitResult(code int) *delta.JsonObject { return jsonObject("exit", float64(code)) }

func setOutput(ctx JobCtx, output string, options ChangeOptions) error {
	_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("output", output) }, ctx, options)
	return err
}

// job.test.ts › self.job › is declared once per job name, only while the factory runs
func TestSelfJobIsDeclaredOncePerJobNameOnlyWhileTheFactoryRuns(t *testing.T) {
	var later Self
	host, errs := jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			later = self
			self.Job(jobEcho, JobImplementation{Run: echoRun()})
			// Declared twice: Pi throws, so does Self.Job (the factory's panic is the activation's error).
			self.Job(jobEcho, JobImplementation{Run: echoRun()})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, jobHostOptions{})
	_, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	if err == nil || !strings.Contains(err.Error(), `job "app.echo" is declared twice`) {
		t.Fatalf("Get = %v, want the declared-twice error", err)
	}
	if kinds := kindsOf(errs.All()); len(kinds) != 1 || kinds[0] != "activation_failed" {
		t.Fatalf("errors = %v", kinds)
	}
	if got := panicMessage(func() {
		later.Job(jobShell, JobImplementation{Run: func(any, JobCtx) (any, error) { return exitResult(0), nil }})
	}); got != "app.item(1): self.job is valid only in the factory" {
		t.Fatalf("a job declared after the factory panicked with %q", got)
	}
}

// job.test.ts › run › gets a ctx with the run's id, the host's principal for a local object, and no state without one
func TestRunGetsACtxWithTheRunsIDTheHostsPrincipalForALocalObjectAndNoStateWithoutOne(t *testing.T) {
	type seen struct {
		id        string
		principal data.Principal
		hasState  bool
	}
	got := make(chan seen, 1)
	var job JobHandle
	host := newJobHost(t, HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}})
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(jobStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
				got <- seen{id: ctx.JobID(), principal: ctx.Principal(), hasState: ctx.State() != nil}
				return textOf(args), nil
			}})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	run := jobStart(t, job, textArgs("a"), background)
	runOutcome(t, run)
	result := <-got
	if result.id != run.ID() || principalKind(result.principal) != "server" || result.hasState {
		t.Fatalf("ctx = %+v", result)
	}
	must(t, host.Close(background))
}

// job.test.ts › run › reads and changes its state document; `running` carries it
func TestRunReadsAndChangesItsStateDocumentRunningCarriesIt(t *testing.T) {
	g := newGate()
	var mu sync.Mutex
	var values []string
	changed := newGate()
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			read := func() string {
				value, err := ctx.State().Value()
				must(t, err)
				return outputOf(value)
			}
			mu.Lock()
			values = append(values, read())
			mu.Unlock()
			must(t, setOutput(ctx, "half", ChangeOptions{}))
			mu.Lock()
			values = append(values, read())
			mu.Unlock()
			changed.Open()
			g.Wait()
			return exitResult(0), nil
		}}
	}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, commandArgs("a"), background)
	changed.Wait()
	entries := jobRunning(t, job)
	want := jsonObject("id", run.ID(), "version", float64(1), "args", commandArgs("a"), "state", jsonObject("output", "half"), "phase", "running")
	nexttest.RequireJSONEqual(t, entries, []any{want})
	g.Open()
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone || outputOf(outcome.State) != "half" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(values, ",") != ",half" {
		t.Fatalf("values = %q", values)
	}
}

var jobMany = mustDefineJob(DefineJob("app.many", JobOptions{
	Version: 1, Args: contracts.Obj(contracts.Prop("n", contracts.Int())), Result: contracts.Int(), State: jobProgress,
}))

// job.test.ts › run › keeps its state documents apart from the object's own documents of the same definition
func TestRunKeepsItsStateDocumentsApartFromTheObjectsOwnDocumentsOfTheSameDefinition(t *testing.T) {
	g := newGate()
	var started sync.WaitGroup
	started.Add(2)
	var mine *singletonDoc
	fixture := declaredJob(t, jobMany, func(self Self, _ *singletonDoc) JobImplementation {
		mine = stateOf(self, jobProgress)
		return JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
			n := args.(*delta.JsonObject).Value("n").(float64)
			must(t, setOutput(ctx, "run "+string(rune('0'+int(n))), ChangeOptions{}))
			started.Done()
			g.Wait()
			return n, nil
		}}
	}, jobHostOptions{})
	job := fixture.Job
	runs := []Run{
		jobStart(t, job, jsonObject("n", float64(1)), background),
		jobStart(t, job, jsonObject("n", float64(2)), background),
	}
	started.Wait()
	var outputs []any
	for _, entry := range jobRunning(t, job) {
		outputs = append(outputs, outputOf(entry.(*delta.JsonObject).Value("state")))
	}
	nexttest.RequireJSONEqual(t, outputs, []any{"run 1", "run 2"})
	var own *delta.JsonObject
	fixture.Host.(*chordHost).enter(func() { own = mine.Value() })
	nexttest.RequireJSONEqual(t, own, jsonObject("output", ""))
	g.Open()
	var final []any
	for _, run := range runs {
		final = append(final, outputOf(runOutcome(t, run).State))
	}
	nexttest.RequireJSONEqual(t, final, []any{"run 1", "run 2"})
}

// job.test.ts › run › refuses state changes once run settled (released), and never recreates the retired document
func TestRunRefusesStateChangesOnceRunSettledReleasedAndNeverRecreatesTheRetiredDocument(t *testing.T) {
	var captured JobCtx
	var mu sync.Mutex
	var inFinish struct {
		state any
		err   error
	}
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{
			Run: func(_ any, ctx JobCtx) (any, error) {
				mu.Lock()
				captured = ctx
				mu.Unlock()
				return exitResult(0), nil
			},
			// After run returned, before finish committed: refused; the outcome's state is the state at run's settle.
			Finish: func(outcome Outcome, _ documents.Tx) error {
				_, err := captured.State().Change(func(*delta.Object) (any, error) { return nil, nil }, background, ChangeOptions{})
				mu.Lock()
				inFinish.state, inFinish.err = outcome.State, err
				mu.Unlock()
				return nil
			},
		}
	}, jobHostOptions{})
	job := fixture.Job
	runOutcome(t, jobStart(t, job, commandArgs("a"), background))
	mu.Lock()
	nexttest.RequireJSONEqual(t, inFinish.state, jsonObject("output", ""))
	expectChordError(t, inFinish.err, data.CodeReleased, "")
	ctx := captured
	mu.Unlock()
	_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("output", "late") }, background, ChangeOptions{})
	expectChordError(t, err, data.CodeReleased, "")
	_, err = ctx.State().Value()
	if _, ok := data.IsChordError(err); !ok {
		t.Fatalf("Value() = %v, want a ChordError", err)
	}
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
}

// job.test.ts › run › commits a change still waiting for the line when run settles, before the outcome is recorded
func TestRunCommitsAChangeStillWaitingForTheLineWhenRunSettlesBeforeTheOutcomeIsRecorded(t *testing.T) {
	goRun, line, entered := newGate(), newGate(), newGate()
	returned := make(chan struct{})
	var fixture *jobFixture
	fixture = declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			goRun.Wait()
			// Admitted while run runs; it waits behind the transaction holding the line.
			go func() { _ = setOutput(ctx, "last words", ChangeOptions{}) }()
			eventually(t, "the change to wait for the line", func() bool {
				return recordsOf(fixture.Job).owner.documents.Line().WaitingHolders() == 1
			})
			close(returned)
			return exitResult(0), nil
		}}
	}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, commandArgs("a"), background)
	holding := make(chan error, 1)
	go func() {
		_, err := fixture.Self.Tx(func(*documents.Transaction) (any, error) {
			entered.Open()
			line.Wait()
			return nil, nil
		}, background)
		holding <- err
	}()
	entered.Wait()
	goRun.Open()
	<-returned
	// run settled with its change still waiting: nothing committed, the outcome not recorded yet.
	entries := jobRunning(t, job)
	if len(entries) != 1 || entries[0].(*delta.JsonObject).Value("phase") != "running" || outputOf(entries[0].(*delta.JsonObject).Value("state")) != "" {
		t.Fatalf("running = %v", entries)
	}
	line.Open()
	must(t, <-holding)
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outputOf(outcome.State) != "last words" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// job.test.ts › run › commits changes made after an abort while run stops; a throw then ends aborted, not threw
func TestRunCommitsChangesMadeAfterAnAbortWhileRunStopsAThrowThenEndsAbortedNotThrew(t *testing.T) {
	started := newGate()
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			started.Open()
			untilDone(ctx)
			if err := setOutput(ctx, "stopped cleanly", ChangeOptions{}); err != nil {
				return nil, err
			}
			return nil, errors.New("interrupted")
		}}
	}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, commandArgs("a"), background)
	started.Wait()
	go func() { _, _ = job.Abort(run.ID(), background) }()
	outcome := runOutcome(t, run)
	if outcome.ID != run.ID() || commandOf(outcome.Args) != "a" || outputOf(outcome.State) != "stopped cleanly" ||
		outcome.Kind != OutcomeFailed || outcome.Error.Kind != JobAborted || outcome.Error.Reason != AbortedByAbort {
		t.Fatalf("outcome = %+v", outcome)
	}
	if reports := jobReports(fixture.Errors.All()); len(reports) != 0 {
		t.Fatalf("reports = %v", reports)
	}
}

// job.test.ts › run › keeps a result returned after an abort
func TestRunKeepsAResultReturnedAfterAnAbort(t *testing.T) {
	started := newGate()
	fixture := echoJob(t, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
		started.Open()
		untilDone(ctx)
		return textOf(args), nil
	}}, jobHostOptions{})
	run := jobStart(t, fixture.Job, textArgs("kept"), background)
	started.Wait()
	go func() { _, _ = fixture.Job.Abort(run.ID(), background) }()
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "kept" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// job.test.ts › run › ends threw when run throws, reported as job_failed; a plain error becomes PluginError internal
func TestRunEndsThrewWhenRunThrowsReportedAsJobFailedAPlainErrorBecomesPluginErrorInternal(t *testing.T) {
	plugin := data.NewPluginError("quota", "too many")
	fixture := echoJob(t, JobImplementation{Run: func(args any, _ JobCtx) (any, error) {
		if textOf(args) == "plugin" {
			return nil, plugin
		}
		return nil, errors.New("plain")
	}}, jobHostOptions{})
	job := fixture.Job
	first := runOutcome(t, jobStart(t, job, textArgs("plugin"), background))
	if first.Kind != OutcomeFailed || first.Error.Kind != JobThrew || first.Error.Error != error(plugin) {
		t.Fatalf("first = %+v", first)
	}
	second := runOutcome(t, jobStart(t, job, textArgs("plain"), background))
	if second.Kind != OutcomeFailed || second.Error.Kind != JobThrew {
		t.Fatalf("second = %+v", second)
	}
	thrown, ok := second.Error.Error.(*data.PluginError)
	if !ok || thrown.Code != "internal" || thrown.Message != "plain" {
		t.Fatalf("thrown = %#v", second.Error.Error)
	}
	reports := jobReports(fixture.Errors.All())
	if len(reports) != 2 || reports[0] != "job_failed: too many" || reports[1] != "job_failed: plain" {
		t.Fatalf("reports = %v", reports)
	}
	reported := fixture.Errors.All()[0]
	if reported.Kind != ErrJobFailed || reported.Object != "app.store" || reported.Job != "app.echo" || reported.ID != first.ID {
		t.Fatalf("reported = %+v", reported)
	}
}

// job.test.ts › run › releases its ctx.get references when it settles
func TestRunReleasesItsCtxGetReferencesWhenItSettles(t *testing.T) {
	other := mustContract(contracts.Local("app.other", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("log", contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Str()))))},
	}))
	var job JobHandle
	var host Host
	inside := make(chan int, 1)
	host, _ = jobsLoaded(t, func(h FacetHost) error {
		if err := h.Serve(other, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(jobStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
				if _, err := ctx.Get(other, jsonObject("id", float64(1)), GetOptions{}); err != nil {
					return nil, err
				}
				inside <- referenceCount(t, host, other)
				return textOf(args), nil
			}})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, jobHostOptions{})
	runOutcome(t, jobStart(t, job, textArgs("a"), background))
	if count := <-inside; count != 1 {
		t.Fatalf("references inside run = %d, want 1", count)
	}
	eventually(t, "the reference to be released", func() bool { return referenceCount(t, host, other) == 0 })
}

// job.test.ts › run › that ignores its signal at the object's end is drained up to closeTimeout; its later changes are refused
func TestRunThatIgnoresItsSignalAtTheObjectsEndIsDrainedUpToCloseTimeoutItsLaterChangesAreRefused(t *testing.T) {
	g := newGate()
	late := make(chan error, 1)
	started := newGate()
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			started.Open()
			g.Wait()
			late <- setOutput(ctx, "too late", ChangeOptions{})
			return exitResult(0), nil
		}}
	}, jobHostOptions{loadedOptions: loadedOptions{CloseTimeout: new(20 * time.Millisecond)}})
	jobStart(t, fixture.Job, commandArgs("a"), background)
	started.Wait()
	must(t, fixture.Host.Close(background))
	errs := fixture.Errors.All()
	if len(errs) != 1 || errs[0].Kind != ErrDrainTimeout || errs[0].Object != "app.store" {
		t.Fatalf("errors = %+v", errs)
	}
	g.Open()
	expectChordError(t, <-late, data.CodeReleased, "")
}

// job.test.ts › run › may start the same job again from run (recursion): a new run
func TestRunMayStartTheSameJobAgainFromRunRecursionANewRun(t *testing.T) {
	var mu sync.Mutex
	ids := map[string]bool{}
	var job JobHandle
	fixture := echoJob(t, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
		mu.Lock()
		ids[ctx.JobID()] = true
		mu.Unlock()
		if text := textOf(args); len(text) < 3 {
			if _, err := job.Start(textArgs(text+"+"), ctx, StartOptions{}); err != nil {
				return nil, err
			}
		}
		return textOf(args), nil
	}}, jobHostOptions{})
	job = fixture.Job
	runOutcome(t, jobStart(t, job, textArgs("a"), background))
	eventually(t, "the three runs", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ids) == 3 && len(jobRunning(t, job)) == 0
	})
}

// job.test.ts › finish › gets each outcome kind; the record, key and state document go in its commit, atomically with its writes
func TestFinishGetsEachOutcomeKindTheRecordKeyAndStateDocumentGoInItsCommitAtomicallyWithItsWrites(t *testing.T) {
	var mu sync.Mutex
	var outcomes []string
	fixture := declaredJob(t, jobShell, func(_ Self, log *singletonDoc) JobImplementation {
		return JobImplementation{
			Run: func(args any, ctx JobCtx) (any, error) {
				command := commandOf(args)
				if err := setOutput(ctx, "out "+command, ChangeOptions{}); err != nil {
					return nil, err
				}
				if command == "fail" {
					return nil, errors.New("no")
				}
				return exitResult(0), nil
			},
			Finish: func(outcome Outcome, tx documents.Tx) error {
				mu.Lock()
				outcomes = append(outcomes, string(outcome.Kind)+" "+outputOf(outcome.State))
				mu.Unlock()
				return appendLog(tx, log, commandOf(outcome.Args))
			},
		}
	}, jobHostOptions{})
	job := fixture.Job
	// The log entry and the removed entry arrive in one commit: whenever the log's watch sees an entry, the run is gone.
	watch, err := fixture.Log.s.Watch(background)
	must(t, err)
	var seenMu sync.Mutex
	var seen [][2]int
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			list := call.Value.(*delta.JsonObject).Value("list").([]any)
			running := len(jobRunning(t, job))
			seenMu.Lock()
			seen = append(seen, [2]int{len(list), running})
			seenMu.Unlock()
		}
		return nil
	})
	runOutcome(t, jobStart(t, job, commandArgs("ok"), background, "k"))
	runOutcome(t, jobStart(t, job, commandArgs("fail"), background, "k"))
	hostDelivered(fixture.Host)
	mu.Lock()
	if strings.Join(outcomes, "|") != "done out ok|failed out fail" {
		t.Fatalf("outcomes = %v", outcomes)
	}
	mu.Unlock()
	eventually(t, "the log's frames", func() bool {
		seenMu.Lock()
		defer seenMu.Unlock()
		return len(seen) >= 2
	})
	seenMu.Lock()
	defer seenMu.Unlock()
	// The first frame is the watch's current value: its list may be empty when the frame precedes the first commit.
	if got := seen[len(seen)-2:]; got[0] != [2]int{1, 0} || got[1] != [2]int{2, 0} {
		t.Fatalf("seen = %v", seen)
	}
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
	_, _ = watch.Stop()
}

// job.test.ts › finish › only cleans up without a finish
func TestFinishOnlyCleansUpWithoutAFinish(t *testing.T) {
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(any, JobCtx) (any, error) { return exitResult(3), nil }}
	}, jobHostOptions{})
	run := jobStart(t, fixture.Job, commandArgs("a"), background, "k")
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, exitResult(3))
	if entries := jobRunning(t, fixture.Job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
}

// advanceFinishBackoff waits for the finish's backoff timer and advances the virtual clock by delay.
func advanceBackoff(t *testing.T, loop *virtualLoop, delay int64) {
	t.Helper()
	eventually(t, "the backoff timer", func() bool { return loop.fake.TimerCount() == 1 })
	loop.fake.Advance(delay)
}

// job.test.ts › finish › throwing › is retried after 1 s doubling, each failure reported; a later success finishes the run
func TestFinishThrowingIsRetriedAfter1SDoublingEachFailureReportedALaterSuccessFinishesTheRun(t *testing.T) {
	loop := newVirtualLoop()
	var mu sync.Mutex
	var at []int64
	fixture := echoJob(t, JobImplementation{
		Run: echoRun(),
		Finish: func(Outcome, documents.Tx) error {
			mu.Lock()
			defer mu.Unlock()
			at = append(at, loop.fake.Now())
			if len(at) < 4 {
				return errors.New("finish " + string(rune('0'+len(at))))
			}
			return nil
		},
	}, jobHostOptions{Loop: loop})
	run := jobStart(t, fixture.Job, textArgs("a"), background)
	for _, delay := range []int64{1_000, 2_000, 4_000} {
		advanceBackoff(t, loop, delay)
	}
	if kind := runOutcome(t, run).Kind; kind != OutcomeDone {
		t.Fatalf("kind = %s", kind)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(at) != 4 || at[0] != 0 || at[1] != 1_000 || at[2] != 3_000 || at[3] != 7_000 {
		t.Fatalf("finishes at %v, want [0 1000 3000 7000]", at)
	}
	want := []string{"job_finish_failed: finish 1", "job_finish_failed: finish 2", "job_finish_failed: finish 3"}
	if got := jobReports(fixture.Errors.All()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("reports = %v", got)
	}
}

// job.test.ts › finish › throwing › blocks the run after five failures in a row: outcome, state and key kept, not counted; abort drops it
func TestFinishThrowingBlocksTheRunAfterFiveFailuresInARowOutcomeStateAndKeyKeptNotCountedAbortDropsIt(t *testing.T) {
	loop := newVirtualLoop()
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{
			Run: func(_ any, ctx JobCtx) (any, error) {
				if err := setOutput(ctx, "kept", ChangeOptions{}); err != nil {
					return nil, err
				}
				return exitResult(0), nil
			},
			Finish: func(Outcome, documents.Tx) error { return errors.New("always") },
		}
	}, jobHostOptions{Loop: loop})
	job := fixture.Job
	run := jobStart(t, job, commandArgs("a"), background, "k")
	waitingCtx, cancel := chordctx.WithCancel(background)
	waiter := make(chan error, 1)
	go func() { waiter <- runOutcomeErr(t, run, waitingCtx) }()
	dropped := make(chan error, 1)
	go func() { dropped <- runOutcomeErr(t, run, background) }()
	for _, delay := range []int64{1_000, 2_000, 4_000, 8_000} {
		advanceBackoff(t, loop, delay)
	}
	eventually(t, "five reports and the block", func() bool {
		entries := jobRunning(t, job)
		return len(jobReports(fixture.Errors.All())) == 5 && len(entries) == 1 && phases(entries)[0] == "blocked"
	})
	nexttest.RequireJSONEqual(t, jobRunning(t, job), []any{jsonObject("id", run.ID(), "version", float64(1), "phase", "blocked", "reason", "finish_failed")})
	// Its waiters keep waiting; their own context ends a wait.
	cancel(errors.New("gave up"))
	if err := <-waiter; err == nil || err.Error() != "gave up" {
		t.Fatalf("a cancelled waiter = %v", err)
	}
	// Dedupe returns it; a different key starts a new run (blocked runs do not count for concurrency).
	if again := jobStart(t, job, commandArgs("b"), background, "k"); again != run {
		t.Fatal("dedupe did not return the blocked run")
	}
	other := jobStart(t, job, commandArgs("c"), background, "other")
	eventually(t, "the other run to be finishing", func() bool {
		entries := jobRunning(t, job)
		return len(entries) == 2 && phases(entries)[0] == "blocked" && phases(entries)[1] == "finishing"
	})
	go func() { _, _ = job.Abort(run.ID(), background) }()
	expectChordError(t, <-dropped, data.CodeAborted, "")
	eventually(t, "the blocked run to be dropped", func() bool {
		entries := jobRunning(t, job)
		return len(entries) == 1 && entries[0].(*delta.JsonObject).Value("id") == other.ID()
	})
	var drops []HostError
	for _, err := range fixture.Errors.All() {
		if err.Kind == ErrJobDropped {
			drops = append(drops, err)
		}
	}
	if len(drops) != 1 || drops[0].Object != "app.store" || drops[0].Job != "app.shell" || drops[0].ID != run.ID() {
		t.Fatalf("drops = %+v", drops)
	}
}

// job.test.ts › finish › backing off › leaves no timer behind when its object ends during the backoff
func TestFinishBackingOffLeavesNoTimerBehindWhenItsObjectEndsDuringTheBackoff(t *testing.T) {
	loop := newVirtualLoop()
	fixture := echoJob(t, JobImplementation{Run: echoRun(), Finish: func(Outcome, documents.Tx) error { return errors.New("stuck") }}, jobHostOptions{Loop: loop})
	run := jobStart(t, fixture.Job, textArgs("a"), background)
	rejected := make(chan error, 1)
	go func() { rejected <- runOutcomeErr(t, run, background) }()
	eventually(t, "the backoff", func() bool { return fixture.Errors.Len() == 1 && loop.fake.TimerCount() == 1 })
	must(t, fixture.Host.Close(background))
	if timers := loop.fake.TimerCount(); timers != 0 {
		t.Fatalf("%d timers after close", timers)
	}
	loop.fake.Advance(60_000)
	if n := fixture.Errors.Len(); n != 1 {
		t.Fatalf("%d errors", n)
	}
	expectChordError(t, <-rejected, data.CodeReleased, "")
}

// job.test.ts › finish › ignores an abort while it runs
func TestFinishIgnoresAnAbortWhileItRuns(t *testing.T) {
	g, entered := newGate(), newGate()
	fixture := echoJob(t, JobImplementation{Run: echoRun(), Finish: func(Outcome, documents.Tx) error {
		entered.Open()
		g.Wait()
		return nil
	}}, jobHostOptions{})
	run := jobStart(t, fixture.Job, textArgs("a"), background)
	entered.Wait()
	go func() { _, _ = fixture.Job.Abort(run.ID(), background) }()
	g.Open()
	if kind := runOutcome(t, run).Kind; kind != OutcomeDone {
		t.Fatalf("kind = %s", kind)
	}
}

// job.test.ts › finish › does not run when its object ends while the outcome and finish wait for the line: no call, no report
func TestFinishDoesNotRunWhenItsObjectEndsWhileTheOutcomeAndFinishWaitForTheLineNoCallNoReport(t *testing.T) {
	goRun, line, entered := newGate(), newGate(), newGate()
	var mu sync.Mutex
	var finished []string
	fixture := echoJob(t, JobImplementation{
		Run: func(args any, _ JobCtx) (any, error) {
			goRun.Wait()
			return textOf(args), nil
		},
		Finish: func(outcome Outcome, _ documents.Tx) error {
			mu.Lock()
			defer mu.Unlock()
			finished = append(finished, textOf(outcome.Args))
			return nil
		},
	}, jobHostOptions{})
	run := jobStart(t, fixture.Job, textArgs("a"), background)
	waiting := make(chan error, 1)
	go func() { waiting <- runOutcomeErr(t, run, background) }()
	holding := make(chan error, 1)
	go func() {
		_, err := fixture.Self.Tx(func(*documents.Transaction) (any, error) {
			entered.Open()
			line.Wait()
			return nil, nil
		}, background)
		holding <- err
	}()
	entered.Wait()
	// run settles while the line is held: its outcome and finish queue behind it.
	goRun.Open()
	eventually(t, "the outcome to wait for the line", func() bool {
		return recordsOf(fixture.Job).owner.documents.Line().WaitingHolders() == 1
	})
	closing := make(chan error, 1)
	go func() { closing <- fixture.Host.Close(background) }()
	line.Open()
	<-holding
	must(t, <-closing)
	mu.Lock()
	defer mu.Unlock()
	if len(finished) != 0 || fixture.Errors.Len() != 0 {
		t.Fatalf("finished %v, errors %v", finished, fixture.Errors.All())
	}
	expectChordError(t, <-waiting, data.CodeReleased, "")
}

// job.test.ts › finish › is drained at the object's end when it runs: its commit is refused, nothing is reported
func TestFinishIsDrainedAtTheObjectsEndWhenItRunsItsCommitIsRefusedNothingIsReported(t *testing.T) {
	g, entered := newGate(), newGate()
	var mu sync.Mutex
	var events []string
	note := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	fixture := echoJob(t, JobImplementation{Run: echoRun(), Finish: func(Outcome, documents.Tx) error {
		entered.Open()
		g.Wait()
		note("finish returned")
		return nil
	}}, jobHostOptions{})
	jobStart(t, fixture.Job, textArgs("a"), background)
	entered.Wait()
	closed := make(chan struct{})
	go func() {
		must(t, fixture.Host.Close(background))
		note("closed")
		close(closed)
	}()
	nexttest.Flushed()
	mu.Lock()
	if len(events) != 0 {
		t.Fatalf("events before the drain ended = %v", events)
	}
	mu.Unlock()
	g.Open()
	<-closed
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(events, ",") != "finish returned,closed" {
		t.Fatalf("events = %v", events)
	}
	if fixture.Errors.Len() != 0 {
		t.Fatalf("errors = %v", fixture.Errors.All())
	}
}

// job.test.ts › running › has one entry per unsettled run, in start order, tagged by phase; a key when given
func TestRunningHasOneEntryPerUnsettledRunInStartOrderTaggedByPhaseAKeyWhenGiven(t *testing.T) {
	gates := []*gate{newGate(), newGate()}
	finishing := newGate()
	fixture := echoJob(t, JobImplementation{
		Run: func(args any, _ JobCtx) (any, error) {
			gates[int(textOf(args)[0]-'0')].Wait()
			return textOf(args), nil
		},
		Finish: func(Outcome, documents.Tx) error {
			finishing.Wait()
			return nil
		},
	}, jobHostOptions{})
	job := fixture.Job
	zero := jobStart(t, job, textArgs("0"), background, "z")
	one := jobStart(t, job, textArgs("1"), background)
	gates[0].Open()
	eventually(t, "the first run to be finishing", func() bool { return phases(jobRunning(t, job))[0] == "finishing" })
	want := []any{
		jsonObject("id", zero.ID(), "version", float64(1), "key", "z", "args", textArgs("0"), "phase", "finishing"),
		jsonObject("id", one.ID(), "version", float64(1), "args", textArgs("1"), "phase", "running"),
	}
	nexttest.RequireJSONEqual(t, jobRunning(t, job), want)
	finishing.Open()
	gates[1].Open()
	runOutcome(t, one)
	eventually(t, "the runs to be gone", func() bool { return len(jobRunning(t, job)) == 0 })
}

// job.test.ts › running › replaces only the entry whose state changed, with ops under its entry (cost guard: no walk of other runs)
func TestRunningReplacesOnlyTheEntryWhoseStateChangedWithOpsUnderItsEntry(t *testing.T) {
	g := newGate()
	var mu sync.Mutex
	contexts := map[int]JobCtx{}
	fixture := declaredJob(t, jobMany, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
			n := args.(*delta.JsonObject).Value("n").(float64)
			mu.Lock()
			contexts[int(n)] = ctx
			mu.Unlock()
			g.Wait()
			return n, nil
		}}
	}, jobHostOptions{})
	job := fixture.Job
	for n := range 100 {
		jobStart(t, job, jsonObject("n", float64(n)), background)
	}
	eventually(t, "100 runs", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(contexts) == 100
	})
	before := jobRunning(t, job)
	watch, err := job.Running().Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	mu.Lock()
	ctx := contexts[42]
	mu.Unlock()
	must(t, setOutput(ctx, "x", ChangeOptions{}))
	hostDelivered(fixture.Host)
	after := jobRunning(t, job)
	changed := 0
	for index := range after {
		if after[index] != before[index] {
			changed++
		}
	}
	if changed != 1 || outputOf(after[42].(*delta.JsonObject).Value("state")) != "x" {
		t.Fatalf("%d entries replaced; entry 42 = %v", changed, after[42])
	}
	got := frames.Items()
	if len(got) != 1 {
		t.Fatalf("%d frames", len(got))
	}
	nexttest.RequireJSONEqual(t, opsAsAny(got[0].Ops), []any{[]any{"a", []any{42, "state", "output"}, "x"}})
	g.Open()
}

// job.test.ts › running › does no validation, walk or compilation for a state change (cost guard)
func TestRunningDoesNoValidationWalkOrCompilationForAStateChange(t *testing.T) {
	telemetry, counting, counts := meteredHost(t)
	g := newGate()
	ctxs := make(chan JobCtx, 1)
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			ctxs <- ctx
			g.Wait()
			return exitResult(0), nil
		}}
	}, jobHostOptions{loadedOptions: loadedOptions{Telemetry: telemetry, Context: counting}})
	job := fixture.Job
	jobStart(t, job, commandArgs("a"), counting)
	ctx := <-ctxs
	watch, err := job.Running().Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	dataCounts := func() map[string]float64 {
		out := map[string]float64{}
		for name, value := range counts() {
			if strings.HasPrefix(name, "chord.data.") {
				out[name] = value
			}
		}
		return out
	}
	before := dataCounts()
	must(t, setOutput(ctx, strings.Repeat("x", 10_000), ChangeOptions{}))
	hostDelivered(fixture.Host)
	nexttest.RequireJSONEqual(t, dataCounts(), before)
	if frames.Len() != 1 {
		t.Fatalf("%d frames", frames.Len())
	}
	g.Open()
}

// job.test.ts › running › frames record changes as splices and sets of the list
func TestRunningFramesRecordChangesAsSplicesAndSetsOfTheList(t *testing.T) {
	g := newGate()
	fixture := echoJob(t, JobImplementation{Run: gated(g)}, jobHostOptions{})
	job := fixture.Job
	watch, err := job.Running().Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	run := jobStart(t, job, textArgs("a"), background)
	hostDelivered(fixture.Host)
	g.Open()
	runOutcome(t, run)
	eventually(t, "three frames", func() bool { hostDelivered(fixture.Host); return frames.Len() == 3 })
	entry := jsonObject("id", run.ID(), "version", float64(1), "args", textArgs("a"), "phase", "running")
	finishing := jsonObject("id", run.ID(), "version", float64(1), "args", textArgs("a"), "phase", "finishing")
	want := [][]any{
		{[]any{"p", []any{}, 0, 0, []any{entry}}},
		{[]any{"s", []any{0}, finishing}},
		{[]any{"p", []any{}, 0, 1, []any{}}},
	}
	for i, frame := range frames.Items() {
		nexttest.RequireJSONEqual(t, opsAsAny(frame.Ops), want[i])
	}
}

// job.test.ts › keep-alive › keeps an object without holders while runs run, and ends it after the last one and its linger
func TestKeepAliveKeepsAnObjectWithoutHoldersWhileRunsRunAndEndsItAfterTheLastOneAndItsLinger(t *testing.T) {
	g := newGate()
	var job JobHandle
	ended := make(chan struct{})
	var host Host
	host, _ = jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{Run: gated(g)})
			self.OnLive(func(ctx Ctx) *Handled {
				return Awaiting(ctx.(*callContext).host.loop(), func() error {
					UntilAborted(ctx)
					close(ended)
					return nil
				})
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{Linger: new(20 * time.Millisecond)})
	}, jobHostOptions{})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	jobStart(t, job, textArgs("a"), background)
	if jobs := activityCount(t, host, "jobs"); jobs != 1 {
		t.Fatalf("jobs = %d", jobs)
	}
	if refs := referenceCount(t, host, jobItemType); refs != 1 {
		t.Fatalf("references = %d", refs)
	}
	item.Release()
	// The linger passes (real time, 2x its length) while the run keeps the object.
	time.Sleep(40 * time.Millisecond)
	select {
	case <-ended:
		t.Fatal("the object ended while a run ran")
	default:
	}
	if refs := referenceCount(t, host, jobItemType); refs != 0 {
		t.Fatalf("references = %d", refs)
	}
	if jobs, lingering := activityCount(t, host, "jobs"), activityCount(t, host, "lingering"); jobs != 1 || lingering != 0 {
		t.Fatalf("activity = %d jobs, %d lingering", jobs, lingering)
	}
	g.Open()
	eventually(t, "the object to linger", func() bool {
		return activityCount(t, host, "jobs") == 0 && activityCount(t, host, "lingering") == 1
	})
	<-ended
	eventually(t, "the linger to end", func() bool {
		return activityCount(t, host, "jobs") == 0 && activityCount(t, host, "lingering") == 0
	})
}

// job.test.ts › keep-alive › keeps an object whose last run's finish chains another job: the chained run runs, then the object ends
func TestKeepAliveKeepsAnObjectWhoseLastRunsFinishChainsAnotherJobTheChainedRunRunsThenTheObjectEnds(t *testing.T) {
	a := mustDefineJob(DefineJob("app.a", JobOptions{
		Version: 1, Args: contracts.Obj(contracts.Prop("text", contracts.Str())), Result: contracts.Str(),
	}))
	var mu sync.Mutex
	var events []string
	note := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	var handleA JobHandle
	var chained Run
	var host Host
	host, _ = jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			self.Open(
				func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { note("object ended"); return nil },
			)
			b := self.Job(jobEcho, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
				timerWait(host.(*chordHost).loop(), 10*time.Millisecond)
				note("b ran " + textOf(args))
				return textOf(args), nil
			}})
			handleA = self.Job(a, JobImplementation{
				Run: func(args any, _ JobCtx) (any, error) { return textOf(args), nil },
				Finish: func(_ Outcome, tx documents.Tx) error {
					run, err := tx.Start(stageable(b), textArgs("chained"))
					mu.Lock()
					chained = run
					mu.Unlock()
					return err
				},
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, jobHostOptions{})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	jobStart(t, handleA, textArgs("x"), background)
	item.Release()
	eventually(t, "the chain and the object's end", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 2
	})
	mu.Lock()
	run := chained
	got := strings.Join(events, ",")
	mu.Unlock()
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "chained" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got != "b ran chained,object ended" {
		t.Fatalf("events = %q", got)
	}
	if jobs := activityCount(t, host, "jobs"); jobs != 0 {
		t.Fatalf("jobs = %d", jobs)
	}
}

// job.test.ts › keep-alive › drops blocked runs, reported, when the object ends after its last run
func TestKeepAliveDropsBlockedRunsReportedWhenTheObjectEndsAfterItsLastRun(t *testing.T) {
	loop := newVirtualLoop()
	var job JobHandle
	var host Host
	var errs *hostErrors
	host, errs = jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{
				Run:    echoRun(),
				Finish: func(Outcome, documents.Tx) error { return errors.New("stuck") },
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, jobHostOptions{Loop: loop})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	run := jobStart(t, job, textArgs("a"), background)
	item.Release()
	for _, delay := range []int64{1_000, 2_000, 4_000, 8_000} {
		advanceBackoff(t, loop, delay)
	}
	eventually(t, "the drop", func() bool { return errs.Len() == 6 })
	want := []string{"job_finish_failed", "job_finish_failed", "job_finish_failed", "job_finish_failed", "job_finish_failed", "job_dropped"}
	if got := kindsOf(errs.All()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("errors = %v", got)
	}
	expectChordError(t, runOutcomeErr(t, run, background), data.CodeReleased, "")
	if jobs := activityCount(t, host, "jobs"); jobs != 0 {
		t.Fatalf("jobs = %d", jobs)
	}
}

// job.test.ts › keep-alive › keeps a lingering object when a run starts during its linger
func TestKeepAliveKeepsALingeringObjectWhenARunStartsDuringItsLinger(t *testing.T) {
	g := newGate()
	var job JobHandle
	host, _ := jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{Run: gated(g)})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{Linger: new(20 * time.Millisecond)})
	}, jobHostOptions{})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	item.Release()
	if lingering := activityCount(t, host, "lingering"); lingering != 1 {
		t.Fatalf("lingering = %d", lingering)
	}
	run := jobStart(t, job, textArgs("a"), background)
	if jobs, lingering := activityCount(t, host, "jobs"), activityCount(t, host, "lingering"); jobs != 1 || lingering != 0 {
		t.Fatalf("activity = %d jobs, %d lingering", jobs, lingering)
	}
	// The linger's length passes (real time) while the run keeps the object.
	time.Sleep(40 * time.Millisecond)
	g.Open()
	if kind := runOutcome(t, run).Kind; kind != OutcomeDone {
		t.Fatalf("kind = %s", kind)
	}
}

// panicMessage is what work panics with, "" when it does not.
func panicMessage(work func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	work()
	return ""
}
