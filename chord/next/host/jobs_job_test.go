package host

import (
	"context"
	"errors"
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

// Ports packages/chord/test/next/jobs/job.test.ts: jobs in memory (§15): starts (args, dedupe, concurrency, `tx.start`,
// `outcome`, when `run` starts), runs (the ctx, the state document and its fence, throws and aborts), finishes
// (cleanup, retries, blocked, dropped), `running`, keep-alive, the object's end, cost guards and GC.
//
// Go forms (jobs_support_test.go): types are JSON values; a Promise.withResolvers gate is a `gate`; fake timers are
// the host's virtual loop; a throw of plugin code is a returned error; a handler runs on a goroutine of its own (a
// handler blocks as it does in Pi), so a test awaits what the host reaches by itself with `eventually`.
// Type-level checks (`expectTypeOf`, `Outcome<...>["args"]`, `JobCtx<TestPrincipal, undefined>`) are designed out:
// the Go types are not generic (jobs_job.go), so the compiler checks the one shape. A WeakRef check is a
// `weak.Pointer` through nexttest.ExpectCollected.

func hostDelivered(host Host) { host.(*chordHost).scheduler.Wait() }

// stageable is a job handle as a transaction starts it.
func stageable(job JobHandle) documents.JobHandle { return job.(documents.JobHandle) }

func untilDone(ctx context.Context) { UntilAborted(ctx) }

func plainRun(result func(args any) any) func(args any, ctx JobCtx) (any, error) {
	return func(args any, _ JobCtx) (any, error) { return result(args), nil }
}

func echoRun() func(args any, ctx JobCtx) (any, error) {
	return plainRun(func(args any) any { return textOf(args) })
}

func echoJob(t *testing.T, implementation JobImplementation, options jobHostOptions) *jobFixture {
	t.Helper()
	return declaredJob(t, jobEcho, func(Self, *singletonDoc) JobImplementation { return implementation }, options)
}

// job.test.ts › start › checks the args against the definition (invalid/args) before anything is recorded
func TestJobStartChecksTheArgsAgainstTheDefinitionBeforeAnythingIsRecorded(t *testing.T) {
	fixture := echoJob(t, JobImplementation{Run: echoRun()}, jobHostOptions{})
	job := fixture.Job
	for _, wrong := range []any{jsonObject("text", float64(1)), jsonObject("text", "a", "more", true)} {
		_, err := job.Start(wrong, background, StartOptions{})
		expectChordError(t, err, data.CodeInvalid, data.ReasonArgs)
	}
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
}

// job.test.ts › start › compiles the args check once per definition (cost guard)
func TestJobStartCompilesTheArgsCheckOncePerDefinition(t *testing.T) {
	telemetry, counting, counts := meteredHost(t)
	fixture := echoJob(t, JobImplementation{Run: echoRun()}, jobHostOptions{loadedOptions: loadedOptions{Telemetry: telemetry}})
	before := counts()["chord.data.compilations"]
	for _, text := range []string{"a", "b", "c"} {
		runOutcome(t, jobStart(t, fixture.Job, textArgs(text), counting))
	}
	if compiled := counts()["chord.data.compilations"] - before; compiled > 1 {
		t.Fatalf("%v compilations, want at most 1", compiled)
	}
	if validations := counts()["chord.data.validations"]; validations < 3 {
		t.Fatalf("%v validations, want at least 3", validations)
	}
}

// job.test.ts › start › returns the unsettled run with the same key, across transactions, whatever its args
func TestJobStartReturnsTheUnsettledRunWithTheSameKeyAcrossTransactionsWhateverItsArgs(t *testing.T) {
	g := newGate()
	fixture := echoJob(t, JobImplementation{Run: gated(g)}, jobHostOptions{})
	job := fixture.Job
	// Two starts at once: the line serializes them, the second sees the first's record.
	var wg sync.WaitGroup
	runs := make([]Run, 2)
	for i, text := range []string{"a", "other"} {
		wg.Go(func() { runs[i] = jobStart(t, job, textArgs(text), background, "k") })
	}
	wg.Wait()
	first := runs[0]
	if runs[1] != first {
		t.Fatal("the second start returned another run")
	}
	if third := jobStart(t, job, textArgs("third"), background, "k"); third != first {
		t.Fatal("the third start returned another run")
	}
	if entries := jobRunning(t, job); len(entries) != 1 {
		t.Fatalf("running = %v", entries)
	}
	g.Open()
	// Whichever start the line served first made the run; args are not compared.
	if outcome := runOutcome(t, first); outcome.Kind != OutcomeDone || outcome.Result != textOf(outcome.Args) {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Settled: the key names nothing any more (Q9).
	next := jobStart(t, job, textArgs("b"), background, "k")
	if next.ID() == first.ID() {
		t.Fatal("the key still names the settled run")
	}
}

// job.test.ts › start › commits nothing for a start that only dedupes
//
// Pi spies `Batch.prototype.commit`; the Go Batch has no hook, so the test checks what makes the commit empty: a start
// that only dedupes stages nothing in its transaction (`tx.StagedOf`), and no frame of `running` follows it.
func TestJobStartCommitsNothingForAStartThatOnlyDedupes(t *testing.T) {
	g := newGate()
	fixture := echoJob(t, JobImplementation{Run: gated(g)}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, textArgs("a"), background, "k")
	watch, err := job.Running().Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	records := recordsOf(job)
	_, err = fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		again, err := tx.Start(stageable(job), textArgs("a"), StartOptions{Key: new("k")})
		if err != nil {
			return nil, err
		}
		if again != run {
			t.Error("a dedupe start returned another run")
		}
		if staged := tx.StagedOf(records); staged != nil {
			t.Error("a dedupe start staged changes")
		}
		return nil, nil
	}, background)
	must(t, err)
	hostDelivered(fixture.Host)
	if frames.Len() != 0 {
		t.Fatalf("%d frames of running after a dedupe", frames.Len())
	}
	g.Open()
}

// job.test.ts › start › returns the same run for a key started twice in one transaction
func TestJobStartReturnsTheSameRunForAKeyStartedTwiceInOneTransaction(t *testing.T) {
	fixture := echoJob(t, JobImplementation{Run: echoRun()}, jobHostOptions{})
	job := fixture.Job
	runs, err := fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		one, err := tx.Start(stageable(job), textArgs("a"), StartOptions{Key: new("k")})
		if err != nil {
			return nil, err
		}
		two, err := tx.Start(stageable(job), textArgs("b"), StartOptions{Key: new("k")})
		return [2]Run{one, two}, err
	}, background)
	must(t, err)
	pair := runs.([2]Run)
	if pair[1] != pair[0] {
		t.Fatal("the second start in the transaction returned another run")
	}
	if outcome := runOutcome(t, pair[0]); textOf(outcome.Args) != "a" {
		t.Fatalf("args = %v", outcome.Args)
	}
}

// job.test.ts › start › at the concurrency limit › rejects unavailable/busy: running and finishing runs count
func TestJobStartAtTheConcurrencyLimitRejectsUnavailableBusyRunningAndFinishingRunsCount(t *testing.T) {
	loop := newVirtualLoop()
	runGate := newGate()
	var finishes int
	var mu sync.Mutex
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{
			Run: func(any, JobCtx) (any, error) {
				runGate.Wait()
				return jsonObject("exit", float64(0)), nil
			},
			Finish: func(Outcome, documents.Tx) error {
				// The first finish fails: the run stays finishing during the backoff, the line free.
				mu.Lock()
				defer mu.Unlock()
				finishes++
				if finishes == 1 {
					return errors.New("once")
				}
				return nil
			},
		}
	}, jobHostOptions{Loop: loop})
	job := fixture.Job
	run := jobStart(t, job, commandArgs("a"), background)
	_, err := job.Start(commandArgs("b"), background, StartOptions{})
	expectChordError(t, err, data.CodeUnavailable, data.ReasonBusy)
	runGate.Open()
	eventually(t, "the run to be finishing", func() bool {
		entries := jobRunning(t, job)
		return len(entries) == 1 && phases(entries)[0] == "finishing"
	})
	_, err = job.Start(commandArgs("b"), background, StartOptions{})
	expectChordError(t, err, data.CodeUnavailable, data.ReasonBusy)
	eventually(t, "the backoff timer", func() bool { return loop.fake.TimerCount() == 1 })
	loop.fake.Advance(1_000)
	runOutcome(t, run)
	runOutcome(t, jobStart(t, job, commandArgs("c"), background))
}

// job.test.ts › start › counts runs started earlier in the same transaction
func TestJobStartCountsRunsStartedEarlierInTheSameTransaction(t *testing.T) {
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(any, JobCtx) (any, error) { return jsonObject("exit", float64(0)), nil }}
	}, jobHostOptions{})
	job := fixture.Job
	_, err := fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		if _, err := tx.Start(stageable(job), commandArgs("a")); err != nil {
			return nil, err
		}
		_, err := tx.Start(stageable(job), commandArgs("b"))
		return nil, err
	}, background)
	expectChordError(t, err, data.CodeUnavailable, data.ReasonBusy)
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
}

// job.test.ts › start › lets finish chain the same job at concurrency 1: its own run is not counted
func TestJobStartLetsFinishChainTheSameJobAtConcurrency1ItsOwnRunIsNotCounted(t *testing.T) {
	var mu sync.Mutex
	var done []string
	var job JobHandle
	fixture := declaredJob(t, jobShell, func(_ Self, log *singletonDoc) JobImplementation {
		return JobImplementation{
			Run: func(args any, _ JobCtx) (any, error) {
				return jsonObject("exit", float64(len(commandOf(args)))), nil
			},
			Finish: func(outcome Outcome, tx documents.Tx) error {
				command := commandOf(outcome.Args)
				mu.Lock()
				done = append(done, command)
				mu.Unlock()
				if err := appendLog(tx, log, command); err != nil {
					return err
				}
				if len(command) < 3 {
					_, err := tx.Start(stageable(job), commandArgs(command+"x"))
					return err
				}
				return nil
			},
		}
	}, jobHostOptions{})
	job = fixture.Job
	runOutcome(t, jobStart(t, job, commandArgs("a"), background))
	eventually(t, "the chain to finish", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(done) == 3 && len(jobRunning(t, job)) == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(done, ",") != "a,ax,axx" {
		t.Fatalf("finished = %v", done)
	}
}

// job.test.ts › start › lets finish start the same job again with the same key: a new run, not the finishing one
func TestJobStartLetsFinishStartTheSameJobAgainWithTheSameKeyANewRunNotTheFinishingOne(t *testing.T) {
	g := newGate()
	var successor Run
	var job JobHandle
	var mu sync.Mutex
	fixture := echoJob(t, JobImplementation{
		Run: func(args any, _ JobCtx) (any, error) {
			if textOf(args) == "again" {
				g.Wait()
			}
			return textOf(args), nil
		},
		Finish: func(outcome Outcome, tx documents.Tx) error {
			if textOf(outcome.Args) == "first" {
				run, err := tx.Start(stageable(job), textArgs("again"), StartOptions{Key: new("k")})
				mu.Lock()
				successor = run
				mu.Unlock()
				return err
			}
			return nil
		},
	}, jobHostOptions{})
	job = fixture.Job
	first := jobStart(t, job, textArgs("first"), background, "k")
	if outcome := runOutcome(t, first); outcome.Kind != OutcomeDone || outcome.Result != "first" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	next := successor
	mu.Unlock()
	if next.ID() == first.ID() {
		t.Fatal("the successor is the finishing run")
	}
	entries := jobRunning(t, job)
	if len(entries) != 1 {
		t.Fatalf("running = %v", entries)
	}
	entry := entries[0].(*delta.JsonObject)
	if entry.Value("id") != next.ID() || entry.Value("key") != "k" || entry.Value("phase") != "running" {
		t.Fatalf("entry = %v", entry)
	}
	// The key names the successor now.
	if again := jobStart(t, job, textArgs("other"), background, "k"); again != next {
		t.Fatal("the key does not name the successor")
	}
	g.Open()
	if outcome := runOutcome(t, next); outcome.Kind != OutcomeDone || outcome.Result != "again" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// job.test.ts › start › tx.start commits with the transaction's other writes, both or neither
func TestJobStartTxStartCommitsWithTheTransactionsOtherWritesBothOrNeither(t *testing.T) {
	fixture := echoJob(t, JobImplementation{Run: echoRun()}, jobHostOptions{})
	job, log := fixture.Job, fixture.Log
	started, err := fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		if err := appendLog(tx, log, "requested"); err != nil {
			return nil, err
		}
		return tx.Start(stageable(job), textArgs("a"))
	}, background)
	must(t, err)
	nexttest.RequireJSONEqual(t, fixture.List(), []any{"requested"})
	if kind := runOutcome(t, started.(Run)).Kind; kind != OutcomeDone {
		t.Fatalf("kind = %s", kind)
	}
	var discarded Run
	_, err = fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		if err := appendLog(tx, log, "lost"); err != nil {
			return nil, err
		}
		run, err := tx.Start(stageable(job), textArgs("b"))
		discarded = run
		if err != nil {
			return nil, err
		}
		return nil, errors.New("changed my mind")
	}, background)
	if err == nil || err.Error() != "changed my mind" {
		t.Fatalf("err = %v", err)
	}
	nexttest.RequireJSONEqual(t, fixture.List(), []any{"requested"})
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
	expectChordError(t, runOutcomeErr(t, discarded, background), data.CodeReleased, "")
	_, err = job.Outcome(discarded.ID(), background)
	expectChordError(t, err, data.CodeGone, "")
}

// job.test.ts › start › gives a short run's handle its outcome however late the caller waits
func TestJobStartGivesAShortRunsHandleItsOutcomeHoweverLateTheCallerWaits(t *testing.T) {
	fixture := echoJob(t, JobImplementation{Run: echoRun()}, jobHostOptions{})
	run := jobStart(t, fixture.Job, textArgs("quick"), background)
	eventually(t, "the run to settle", func() bool { return len(jobRunning(t, fixture.Job)) == 0 })
	outcome := runOutcome(t, run)
	if outcome.ID != run.ID() || textOf(outcome.Args) != "quick" || outcome.Kind != OutcomeDone || outcome.Result != "quick" || outcome.State != nil {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// job.test.ts › start › waits by id for unsettled runs only: settled and unknown ids reject gone
func TestJobStartWaitsByIDForUnsettledRunsOnlySettledAndUnknownIDsRejectGone(t *testing.T) {
	g := newGate()
	fixture := echoJob(t, JobImplementation{Run: gated(g)}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, textArgs("a"), background)
	type result struct {
		outcome Outcome
		err     error
	}
	waiting := make(chan result, 1)
	go func() {
		outcome, err := job.Outcome(run.ID(), background)
		waiting <- result{outcome, err}
	}()
	g.Open()
	got := <-waiting
	must(t, got.err)
	if got.outcome.Kind != OutcomeDone {
		t.Fatalf("kind = %s", got.outcome.Kind)
	}
	for _, id := range []string{run.ID(), "nope"} {
		_, err := job.Outcome(id, background)
		expectChordError(t, err, data.CodeGone, "")
	}
}

// job.test.ts › start › starts run after the commit, outside the batch, once the transaction settled
func TestJobStartStartsRunAfterTheCommitOutsideTheBatchOnceTheTransactionSettled(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	note := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, entry)
	}
	var fixture *jobFixture
	fixture = declaredJob(t, jobEcho, func(self Self, log *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(args any, _ JobCtx) (any, error) {
			note("run sees " + joinStrings(fixture.List()))
			// Outside the batch and the line: a transaction of its own works.
			_, err := self.Tx(func(tx *documents.Transaction) (any, error) {
				return nil, appendLog(tx, log, "from run "+textOf(args))
			}, background)
			return textOf(args), err
		}}
	}, jobHostOptions{})
	job, log := fixture.Job, fixture.Log
	started, err := fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		run, err := tx.Start(stageable(job), textArgs("a"))
		if err != nil {
			return nil, err
		}
		if err := appendLog(tx, log, "written with the start"); err != nil {
			return nil, err
		}
		note("transaction done")
		return run, nil
	}, background)
	must(t, err)
	runOutcome(t, started.(Run))
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(seen, "|"); got != "transaction done|run sees written with the start" {
		t.Fatalf("seen = %q", got)
	}
	nexttest.RequireJSONEqual(t, fixture.List(), []any{"written with the start", "from run a"})
}

func joinStrings(items []any) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = item.(string)
	}
	return strings.Join(parts, ",")
}

// job.test.ts › start › an abort between the commit and the start is marked, then the signal fires; the abort joins run (D7)
func TestJobStartAnAbortBetweenTheCommitAndTheStartIsMarkedThenTheSignalFiresTheAbortJoinsRun(t *testing.T) {
	loop := newVirtualLoop()
	var signalled bool
	var mu sync.Mutex
	runStarted := make(chan struct{})
	fixture := echoJob(t, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
		close(runStarted)
		timerWait(loop, 20*time.Millisecond)
		mu.Lock()
		signalled = ctx.Err() != nil
		mu.Unlock()
		return textOf(args), nil
	}}, jobHostOptions{Loop: loop})
	job := fixture.Job
	watch, err := job.Running().Watch(background)
	must(t, err)
	aborts := make(chan AbortResult, 4)
	var once sync.Once
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		once.Do(func() {
			for _, entry := range call.Value.([]any) {
				id := entry.(*delta.JsonObject).Value("id").(string)
				go func() {
					result, err := job.Abort(id, background)
					must(t, err)
					aborts <- result
				}()
			}
		})
		return nil
	})
	run := jobStart(t, job, textArgs("a"), background)
	<-runStarted
	eventually(t, "the sleeping run", func() bool { return loop.fake.TimerCount() == 1 })
	// The watch's frame of the start's commit reaches the abort before the run wakes.
	eventually(t, "the abort to fire the run's signal", func() bool {
		var fired bool
		recordsOf(job).loop.Run(func() {
			if current := recordsOf(job).runs[run.ID()].invocation; current != nil {
				fired = current.signal.Err() != nil
			}
		})
		return fired
	})
	loop.fake.Advance(20)
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "a" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	if !signalled {
		t.Error("run's signal had not fired")
	}
	mu.Unlock()
	if result := <-aborts; result != AbortMarked {
		t.Fatalf("abort = %s", result)
	}
	if result, err := job.Abort(run.ID(), background); err != nil || result != AbortTerminal {
		t.Fatalf("abort of a settled run = %s, %v", result, err)
	}
	_, _ = watch.Stop()
}

// job.test.ts › start › creates nothing for an aborted caller; a caller aborted after the commit does not cancel the run
func TestJobStartCreatesNothingForAnAbortedCallerACallerAbortedAfterTheCommitDoesNotCancelTheRun(t *testing.T) {
	g := newGate()
	var signalled bool
	var mu sync.Mutex
	fixture := echoJob(t, JobImplementation{Run: func(args any, ctx JobCtx) (any, error) {
		g.Wait()
		mu.Lock()
		signalled = ctx.Err() != nil
		mu.Unlock()
		return textOf(args), nil
	}}, jobHostOptions{})
	job := fixture.Job
	early, cancelEarly := chordctx.WithCancel(background)
	cancelEarly(errors.New("gone already"))
	if _, err := job.Start(textArgs("a"), early, StartOptions{}); err == nil || err.Error() != "gone already" {
		t.Fatalf("an aborted caller's start = %v", err)
	}
	if entries := jobRunning(t, job); len(entries) != 0 {
		t.Fatalf("running = %v", entries)
	}
	caller, cancelCaller := chordctx.WithCancel(background)
	run := jobStart(t, job, textArgs("b"), caller)
	cancelCaller(errors.New("caller left"))
	g.Open()
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "b" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if signalled {
		t.Fatal("the caller's abort cancelled the run")
	}
}

// job.test.ts › start › rolls back the transaction's other writes when the state's initial() fails, naming the definition
func TestJobStartRollsBackTheTransactionsOtherWritesWhenTheStatesInitialFailsNamingTheDefinition(t *testing.T) {
	bad := mustDefineState(documents.DefineState("app.bad-state", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("output", contracts.Str())),
		Version: 1,
		// Plugin code: a wrong initial value.
		Initial: func() *delta.JsonObject { return jsonObject("output", float64(1)) },
	}))
	badJob := mustDefineJob(DefineJob("app.bad", JobOptions{
		Version: 1, Args: contracts.Obj(contracts.Prop("text", contracts.Str())), Result: contracts.Str(), State: bad,
	}))
	fixture := declaredJob(t, badJob, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: echoRun()}
	}, jobHostOptions{})
	job, log := fixture.Job, fixture.Log
	_, err := fixture.Self.Tx(func(tx *documents.Transaction) (any, error) {
		if err := appendLog(tx, log, "lost"); err != nil {
			return nil, err
		}
		_, err := tx.Start(stageable(job), textArgs("a"))
		return nil, err
	}, background)
	if err == nil || err.Error() != "app.bad-state: initial() does not match the schema" {
		t.Fatalf("err = %v", err)
	}
	if len(fixture.List()) != 0 || len(jobRunning(t, job)) != 0 {
		t.Fatalf("log %v, running %v", fixture.List(), jobRunning(t, job))
	}
}

// job.test.ts › start › is refused before its object is live and once it ended
func TestJobStartIsRefusedBeforeItsObjectIsLiveAndOnceItEnded(t *testing.T) {
	var early error
	var job JobHandle
	host, _ := jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			job = self.Job(jobEcho, JobImplementation{Run: echoRun()})
			// The object is not live while its factory runs: a start from the factory is refused.
			_, early = job.Start(textArgs("a"), background, StartOptions{})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, jobHostOptions{})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	expectChordError(t, early, data.CodeNotReady, "")
	item.Release()
}
