package host

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/jobs/records.test.ts: a job's records on its object (slice 6a §1.2, §15 rule 5): runs
// across attempts, cut off by an attempt's seal, finished on the next live attempt, and the keep-alive of runs waiting
// for one. Go forms: jobs_job_test.go.

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
	started := newGate()
	var mu sync.Mutex
	var jobs []JobHandle
	runner := restartable(t, func(_ int, self Self) {
		handle := self.Job(jobWork, JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
			started.Open()
			untilDone(ctx)
			// Accepted after an abort (§15), refused once the attempt's seal cut the run off.
			_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("output", "late") }, background, ChangeOptions{})
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
	g := newGate()
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
		got, err := runner.Attempts.at(0).Tx(func(tx *documents.Transaction) (any, error) {
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
