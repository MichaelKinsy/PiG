package workers

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/src/node/workers/workers.ts
// Ports packages/chord/test/node/workers/workers.test.ts describe `a worker's exit (decision "A worker's exit is published after its
// link's work ended")`. Pi's cases wait 200 ms and then assert that something did not happen; here the wait is for the component to
// have handled the worker's exit (`handled`: its exit handler ran, and what it waits for, it is waiting for), after which the
// assertion holds or not, with no time in it.

// reportingFleet is a fleet whose server serves `reporting` over a held store (Pi's `memoryStore()` and `holdNextCommit`), and the
// hold function.
func reportingFleet(t *testing.T, log *reportLog, onError func(err WorkersError)) (*fleet, func() *heldCommit, func() int64) {
	t.Helper()
	chordStore, hold := heldStore(t)
	f := newFleet(t, fleetOptions{
		store:   chordStore,
		facets:  []host.HostFacet{reporting(log, reportOptions{persist: true})},
		onError: onError,
	})
	return f, hold, chordStore.LastSeq
}

func TestAWorkersExitAWorkerThatReportsAndExitsInTheSameTurnItsLinkIsClosedBeforeItsExitIsPublishedNoTransactionOfItsReportsCommits(t *testing.T) {
	published := make(chan struct{})
	var once atomic.Bool
	log := &reportLog{}
	f := newFleet(t, fleetOptions{
		// Each report's transaction runs right after the exit was published.
		facets: []host.HostFacet{reporting(log, reportOptions{before: published})},
		onError: func(err WorkersError) {
			if _, exited := err.(WorkerExitedError); exited && !once.Swap(true) {
				close(published)
			}
		},
	})
	// A test that fails before the exit was published must not leave a report waiting for it.
	t.Cleanup(func() {
		if !once.Swap(true) {
			close(published)
		}
	})
	// The channel outlives the worker: nothing but the component closes its link at the exit.
	f.behaviours.set("S1", workerBehaviour{HoldsChannel: true})
	journal := f.journalOf()
	defer journal.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	// One report whose handler waits when the worker exits, one sent in the turn of the exit.
	reportsCall(t, control, "report", "waiting")
	until(t, func() bool { return slices.Contains(log.received.all(), "waiting") })
	go func() { _, _ = control.Call("reportAndExit", "last", background) }()
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	// The exit is published: no frame is handled from now on, so what was received is all there will be; each of those ended.
	until(t, func() bool { return len(log.outcomes.all()) == len(log.received.all()) })
	// `last` was handled before the exit and aborted with it, or never handled.
	var outcomes []string
	for _, outcome := range log.outcomes.all() {
		if outcome != "last unavailable" {
			outcomes = append(outcomes, outcome)
		}
	}
	if !slices.Equal(outcomes, []string{"waiting unavailable"}) {
		t.Fatalf("outcomes = %q, want [waiting unavailable] (and maybe last unavailable)", log.outcomes.all())
	}
	if lines := linesOf(t, journal); len(lines) != 0 {
		t.Fatalf("journal = %q, want none", lines)
	}
}

func TestAWorkersExitACommitAReportQueuedInTheStoreBeforeTheExitLandsBeforeTheExitIsPublishedUntilThenTheStateShowsTheWorkerAndNothingStarts(t *testing.T) {
	log := &reportLog{}
	var lastSeq func() int64
	var atExit atomic.Int64
	f, hold, seq := reportingFleet(t, log, func(err WorkersError) {
		if _, exited := err.(WorkerExitedError); exited {
			atExit.Store(lastSeq())
		}
	})
	lastSeq = seq
	journal := f.journalOf()
	defer journal.Release()
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	running := f.stateOf("S1")
	child := f.childOf("S1")
	held := hold()
	reportsCall(t, control, "report", "queued")
	<-held.Entered()
	queued := lastSeq()
	if _, err := control.Call("crash", 7.0, background); err != nil {
		t.Fatal(err)
	}
	// Its link is closed at its exit: the holder sees that at once.
	handled(t, child)
	until(t, func() bool { return refKind(notes) != host.RefStateLive })
	if got := f.stateOf("S1"); !reflect.DeepEqual(got, running) {
		t.Fatalf("state = %+v, want the running worker %+v", got, running)
	}
	if errs := f.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if starts := f.starts.all(); !slices.Equal(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %q", starts)
	}
	held.Release()
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	// The commit the report queued had landed when the exit was published (Pi reads the journal there).
	if got := atExit.Load(); got <= queued {
		t.Fatalf("the store's last seq at the exit = %d, want after the held commit (%d)", got, queued)
	}
	if lines := linesOf(t, journal); !slices.Equal(lines, []string{"queued"}) {
		t.Fatalf("journal = %q", lines)
	}
	if starts := f.starts.all(); !slices.Equal(starts, []string{"session|S1"}) {
		t.Fatalf("starts after the exit = %q", starts)
	}
}

func TestAWorkersExitAStopResolvesOnceTheExitWasPublishedAfterTheCommitsItsWorkerQueued(t *testing.T) {
	log := &reportLog{}
	f, hold, lastSeq := reportingFleet(t, log, nil)
	journal := f.journalOf()
	defer journal.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	child := f.childOf("S1")
	held := hold()
	reportsCall(t, control, "report", "queued")
	<-held.Entered()
	queued := lastSeq()
	// What the stop's caller saw at the instant its stop resolved, read by the stop's own goroutine right after the stop returned (an
	// event of the stop's end, not a poll): the state's entry and the store's last seq. The held commit landed, then the exit was
	// published (the state's value is rewritten in the section that closes `gone`), then the stop resolved. A watcher's frame
	// reaches it on its own goroutine, after the stop's caller may have run, so it does not say which came first.
	type resolution struct {
		err   error
		state *WorkerState
		seq   int64
	}
	result := make(chan resolution, 1)
	go func() {
		err := f.workers.Stop(session("S1"), background)
		result <- resolution{err: err, state: f.stateOf("S1"), seq: lastSeq()}
	}()
	handled(t, child)
	select {
	case <-result:
		t.Fatal("the stop resolved before the exit was published")
	default:
	}
	if got := f.stateOf("S1"); got == nil || got.Status != WorkerStopping {
		t.Fatalf("state = %+v, want stopping", got)
	}
	held.Release()
	resolved := <-result
	if resolved.err != nil {
		t.Fatal(resolved.err)
	}
	if resolved.state != nil {
		t.Fatalf("state when the stop resolved = %+v, want none: the exit is published before the stop resolves", resolved.state)
	}
	if resolved.seq <= queued {
		t.Fatalf("the store's last seq when the stop resolved = %d, want after the held commit (%d)", resolved.seq, queued)
	}
	if lines := linesOf(t, journal); !slices.Equal(lines, []string{"queued"}) {
		t.Fatalf("journal = %q", lines)
	}
}

// Go-only (no Pi case): Pi's `close` stops each address and awaits `gone` (workers.ts `close`, #stop), which resolves after the
// store settled: a close during the wait is bounded by its caller's context (`awaitWithContext(exited, context)`), and the exit is
// published when the commits settled, as no failure (the component is closing: #left). The wait is the store's own, no bound of ours.
func TestAWorkersExitCloseDuringTheSettleWaitReturnsWithItsContextAndTheExitIsPublishedOnceTheCommitSettled(t *testing.T) {
	log := &reportLog{}
	f, hold, _ := reportingFleet(t, log, nil)
	journal := f.journalOf()
	defer journal.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	child := f.childOf("S1")
	held := hold()
	reportsCall(t, control, "report", "queued")
	<-held.Entered()
	if _, err := control.Call("crash", 7.0, background); err != nil {
		t.Fatal(err)
	}
	handled(t, child)
	ended, cancel := context.WithCancelCause(background)
	cancelled := context.Canceled
	cancel(cancelled)
	if err := f.workers.Close(ended); !errors.Is(err, cancelled) {
		t.Fatalf("Close = %v, want its context's cause: it waits for the exit to be published", err)
	}
	if got := f.stateOf("S1"); got == nil {
		t.Fatal("the exit was published before the commit settled")
	}
	held.Release()
	until(t, func() bool { return f.stateOf("S1") == nil })
	if errs := f.kinds(); len(errs) != 0 {
		t.Fatalf("errors = %q, want none: a closing component publishes no failure", errs)
	}
	if lines := linesOf(t, journal); !slices.Equal(lines, []string{"queued"}) {
		t.Fatalf("journal = %q", lines)
	}
}
