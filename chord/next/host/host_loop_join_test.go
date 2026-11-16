package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/loopseam"
)

// Go-only (no Pi case): the join of the host's loop that deliveredOn calls (loopseam.Quiesce) is what Pi's `delivered()`
// (support.ts:138, `setTimeout(0)`) waits for: every promise continuation whose awaited promise has settled already, and not
// one still pending on something outside the host (a test's gate, a native's work, a timer). Go's `await work()` of blocking
// work is an awaitTask: the loop counts the task from its start to the end of the section that resumes its promise, and not
// while it waits parked.

// joinHost is a host whose loop is the one the join is asked about.
func joinHost(t *testing.T) *chordHost {
	t.Helper()
	h := newHost(t, HostOptions{}).(*chordHost)
	t.Cleanup(func() { closeHosts(t, h) })
	return h
}

// joinedParked is the join over a loop whose only task waits parked for something the test holds: it returns at once. A join
// that counts a parked task waits for it, and the loop's detector ends that wait by a panic after joinStall; the panic is the
// failure of this test, with the detector's first line, instead of a crash of the binary.
func joinedParked(t *testing.T, h *chordHost) {
	t.Helper()
	defer func() {
		if report := recover(); report != nil {
			first, _, _ := strings.Cut(fmt.Sprint(report), "\n")
			t.Fatalf("the join waited for a task that is parked on something the test holds: %s", first)
		}
	}()
	loopseam.Quiesce(h.loop())
}

func TestLoopJoinWaitsForATaskThatEndsLaterAndForTheContinuationOfItsPromise(t *testing.T) {
	h := joinHost(t)
	started, step := make(chan struct{}), make(chan struct{})
	var continued atomic.Bool
	h.enter(func() {
		awaitTask(h, func() (int, error) {
			close(started)
			<-step
			return 1, nil
		}).then(func(int, error) { continued.Store(true) })
	})
	// The task ends when the goroutine below says so, whether that is before or after the join started waiting for it.
	go func() {
		<-started
		close(step)
	}()
	loopseam.Quiesce(h.loop())
	if !continued.Load() {
		t.Fatal("the join returned before the continuation of the task's promise ran")
	}
}

func TestLoopJoinWaitsForTheTasksATaskStartsInTheSectionOfItsContinuation(t *testing.T) {
	h := joinHost(t)
	var second atomic.Bool
	h.enter(func() {
		awaitTask(h, func() (int, error) { return 1, nil }).then(func(int, error) {
			awaitTask(h, func() (int, error) { return 2, nil }).then(func(int, error) { second.Store(true) })
		})
	})
	loopseam.Quiesce(h.loop())
	if !second.Load() {
		t.Fatal("the join returned before the continuation of the task a continuation started")
	}
}

// lendParked runs rest as what a handler does after its first park, lent the section h.enter runs: Pi's async function, whose
// continuation after its first `await` is a microtask (a method handler, `route.resolve`).
func lendParked(h *chordHost, rest func(ctx context.Context)) {
	lendParkedUnder(h, context.Background(), rest)
}

// lendParkedUnder is lendParked for a handler whose context is a child of base (a test aborts it through base).
func lendParkedUnder(h *chordHost, base context.Context, rest func(ctx context.Context)) {
	signal := parking.New()
	ctx := context.WithValue(base, parking.Key, signal)
	h.enter(func() {
		h.loop().Lend(signal, func(bool) {
			parking.Park(ctx)
			rest(ctx)
		})
	})
}

func TestLoopJoinWaitsForWhatIsLeftOfAHandlerThatParked(t *testing.T) {
	h := joinHost(t)
	var rest atomic.Bool
	lendParked(h, func(context.Context) { rest.Store(true) })
	loopseam.Quiesce(h.loop())
	if !rest.Load() {
		t.Fatal("the join returned before the rest of a handler that parked ran")
	}
}

func TestLoopJoinDoesNotWaitForAHandlerParkedOnAGateAndWaitsForItsRestOnceTheGateIsReleased(t *testing.T) {
	h := joinHost(t)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	var rest atomic.Bool
	lendParked(h, func(ctx context.Context) {
		_, _ = chordctx.Receive(ctx, gate)
		rest.Store(true)
	})
	// The handler waits on a gate a test holds: Pi's `await held` pending, which `delivered()` does not wait for.
	joinedParked(t, h)
	if rest.Load() {
		t.Fatal("the handler went on before its gate was released")
	}
	release()
	loopseam.Quiesce(h.loop())
	if !rest.Load() {
		t.Fatal("the join returned before the handler, whose gate was released just before, went on")
	}
}

func TestLoopJoinDoesNotWaitForAHandlerInUntilAbortedAndWaitsForItsRestOnceItIsAborted(t *testing.T) {
	h := joinHost(t)
	base, abort := context.WithCancel(context.Background())
	t.Cleanup(abort)
	var rest atomic.Bool
	lendParkedUnder(h, base, func(ctx context.Context) {
		UntilAborted(ctx)
		rest.Store(true)
	})
	// Waiting for its abort is what the handler does until then: Pi's pending `await untilAborted(ctx)`, which `delivered()` does
	// not wait for.
	joinedParked(t, h)
	if rest.Load() {
		t.Fatal("the handler went on before its abort")
	}
	abort()
	loopseam.Quiesce(h.loop())
	if !rest.Load() {
		t.Fatal("the join returned before the handler, whose context was aborted just before, went on")
	}
}

func TestLoopJoinDoesNotWaitForAHandlerInSleepAndItResumesAtTheDeadlineAndAtAnAbort(t *testing.T) {
	h := joinHost(t)
	t.Run("deadline", func(t *testing.T) {
		slept := make(chan error, 1)
		lendParked(h, func(ctx context.Context) { slept <- sleepOn(h.loop(), 20*time.Millisecond, ctx) })
		// The handler is counted until it parks in the sleep; the timer's section then wakes it, and the test waits for that.
		loopseam.Quiesce(h.loop())
		if err := <-slept; err != nil {
			t.Fatalf("a sleep that reached its deadline returned %v", err)
		}
	})
	t.Run("abort", func(t *testing.T) {
		base, abort := context.WithCancel(context.Background())
		t.Cleanup(abort)
		slept := make(chan error, 1)
		lendParkedUnder(h, base, func(ctx context.Context) { slept <- sleepOn(h.loop(), time.Hour, ctx) })
		// An hour is a wait for something the loop does not run: the join does not wait for it.
		joinedParked(t, h)
		select {
		case err := <-slept:
			t.Fatalf("a sleep of an hour returned %v before its abort", err)
		default:
		}
		abort()
		loopseam.Quiesce(h.loop())
		if err := <-slept; !errors.Is(err, context.Canceled) {
			t.Fatalf("a sleep aborted returned %v, want its abort's cause", err)
		}
	})
}

func TestLoopJoinDoesNotWaitForATaskParkedOnAGateAndWaitsForItsContinuationOnceTheWaitIsOver(t *testing.T) {
	// Each wait a task can park in, and each way it ends: the gate the test released just before the next join (Pi's
	// `resolve()` queues the continuation at once: nothing may be left for the goroutine to be scheduled), the context
	// cancelled, the work that settles.
	type wait struct {
		name string
		// park is what the task does: wait for gate (or ctx's end), which the test ends with end.
		park func(ctx context.Context, gate chan struct{}, settled chan chordctx.Settled[int])
		end  func(gate chan struct{}, settled chan chordctx.Settled[int], cancel context.CancelFunc)
	}
	waits := []wait{
		{
			"Receive released",
			func(ctx context.Context, gate chan struct{}, _ chan chordctx.Settled[int]) {
				_, _ = chordctx.Receive(ctx, gate)
			},
			func(gate chan struct{}, _ chan chordctx.Settled[int], _ context.CancelFunc) { close(gate) },
		},
		{
			"Receive of a value",
			func(ctx context.Context, gate chan struct{}, _ chan chordctx.Settled[int]) {
				_, _ = chordctx.Receive(ctx, gate)
			},
			func(gate chan struct{}, _ chan chordctx.Settled[int], _ context.CancelFunc) { gate <- struct{}{} },
		},
		{
			"Receive cancelled",
			func(ctx context.Context, gate chan struct{}, _ chan chordctx.Settled[int]) {
				_, _ = chordctx.Receive(ctx, gate)
			},
			func(_ chan struct{}, _ chan chordctx.Settled[int], cancel context.CancelFunc) { cancel() },
		},
		{
			"Await settled",
			func(ctx context.Context, _ chan struct{}, settled chan chordctx.Settled[int]) {
				_, _ = chordctx.Await(ctx, settled)
			},
			func(_ chan struct{}, settled chan chordctx.Settled[int], _ context.CancelFunc) {
				settled <- chordctx.Settled[int]{Value: 1}
			},
		},
		{
			"Idle until the gate closed",
			func(_ context.Context, gate chan struct{}, _ chan chordctx.Settled[int]) {
				resume := parking.Idle(func() bool {
					select {
					case <-gate:
						return true
					default:
						return false
					}
				})
				defer resume()
				<-gate
			},
			func(gate chan struct{}, _ chan chordctx.Settled[int], _ context.CancelFunc) { close(gate) },
		},
	}
	for _, w := range waits {
		t.Run(w.name, func(t *testing.T) {
			h := joinHost(t)
			gate, settled := make(chan struct{}), make(chan chordctx.Settled[int])
			ctx, cancel := context.WithCancel(background)
			defer cancel()
			t.Cleanup(func() {
				select {
				case <-gate:
				default:
					close(gate)
				}
			})
			var continued atomic.Bool
			h.enter(func() {
				awaitTask(h, func() (int, error) {
					w.park(ctx, gate, settled)
					return 1, nil
				}).then(func(int, error) { continued.Store(true) })
			})
			// The task parks (or the join waits until it does, it cannot end before the wait is over): the join returns with
			// the task unfinished.
			joinedParked(t, h)
			if continued.Load() {
				t.Fatal("the task went past its wait")
			}
			w.end(gate, settled, cancel)
			loopseam.Quiesce(h.loop())
			if !continued.Load() {
				t.Fatal("the join after the wait ended returned before the task's continuation ran")
			}
		})
	}
}

func TestLoopJoinReportsATaskThatBlocksOnSomethingTheLoopDoesNotKnow(t *testing.T) {
	loop := NewLoop()
	loop.stall = joinStallForTest
	release := make(chan struct{})
	loop.task(blocksRaw, func() { blocksRaw(release) })
	report := func() (text any) {
		defer func() { text = recover() }()
		loopseam.Quiesce(loop)
		return nil
	}()
	close(release)
	loop.Wait()
	text, _ := report.(string)
	for _, want := range []string{"join waited", "1 running", "blocksRaw", "chordctx.Receive"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
}

// joinStallForTest is the bound of the detector test: a task that blocks raw is reported after it (never waited for).
const joinStallForTest = 50 * time.Millisecond

// blocksRaw is a task's work that waits on a plain channel: a misuse of the host's contract.
func blocksRaw(release chan struct{}) { <-release }

// The wait of a task for the holder of an object's line before it (`await documents.settled()` of the teardown, which a commit a
// test holds keeps waiting) is a park of its own, not a wait a join sees as work: Pi's `setTimeout(0)` does not wait for a promise
// pending on the line.
func TestLoopJoinDoesNotWaitForATaskParkedOnTheLineAndWaitsForItsContinuationOnceTheHolderReleased(t *testing.T) {
	h := joinHost(t)
	h.loop().(*Loop).stall = joinStallForTest
	line := documents.NewLine()
	hold := line.Acquire(background)
	t.Cleanup(hold)
	var continued atomic.Bool
	h.enter(func() {
		awaitTask(h, func() (int, error) {
			line.Acquire(background)()
			return 1, nil
		}).then(func(int, error) { continued.Store(true) })
	})
	joinedParked(t, h)
	if continued.Load() {
		t.Fatal("the task went past the line's holder")
	}
	hold()
	loopseam.Quiesce(h.loop())
	if !continued.Load() {
		t.Fatal("the join after the holder released returned before the task's continuation ran")
	}
}

// A persisted run's reservation (the store write of its `running` phase) and the invocation after it are tasks of the host's
// loop, counted by the join: Pi's `await delivered()` runs after the microtask chain of the reservation (`await commit()`
// then `invoke`), the chain a persisted-run test reads the record after. The reservation here waits for a commit queue the
// test holds, which is a wait on the store the loop cannot say it waits for: the join reports it as the stall it is (the
// task is named), where a join that does not count the task returns at once with the reservation in flight. A job's tasks
// start on the loop the records hold, a wrapper of the host's loop (jobSections): the count is the host's.
func TestLoopJoinCountsAPersistedRunsReservation(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	jobs := opened(t, st, func(any, JobCtx) (any, error) { return countResult(1), nil }, nil)
	h := jobs.Host.(*chordHost)
	h.loop().(*Loop).stall = joinStallForTest
	gate := holdNextCommit()
	started := make(chan Run, 1)
	go func() { started <- jobStart(t, jobs.Imports, pathArgs("p"), background) }()
	<-gate.Entered()
	release := heldCommitQueue(t, st, gate)
	run := <-started
	report := func() (text any) {
		defer func() { text = recover() }()
		loopseam.Quiesce(h.loop())
		return nil
	}()
	text, _ := report.(string)
	for _, want := range []string{"join waited", "1 running", "(*job).invoke"} {
		if !strings.Contains(text, want) {
			t.Errorf("the join returned with the reservation in flight, or its report lacks %q:\n%.600s", want, text)
		}
	}
	release()
	deliveredOn(jobs.Host)
	jobs.mu.Lock()
	finished := len(jobs.finished)
	jobs.mu.Unlock()
	if finished != 1 {
		t.Fatalf("the join returned before the run was reserved, invoked and finished: %d finished, record = %v", finished, record(t, st, run.ID()))
	}
}

const stalledJoinChild = "CHORD_HOST_STALLED_JOIN_CHILD"

// A test that holds a gate on purpose has deferred calls that wait for it (closeHosts behind a held commit), and a panic of the
// test's own goroutine is printed only after they returned: the report of a stalled join must reach the output, and end the
// binary, though they never do. The test runs the binary again (the child below) with a task that blocks on a bare channel and a
// deferred call that never returns.
func TestDeliveredOnReportsAStalledTaskAtOnceThoughTheTestsDeferredCallNeverReturns(t *testing.T) {
	if os.Getenv(stalledJoinChild) != "" {
		stalledJoinTest(t)
		return
	}
	// A time bound as a fault detector only: the child ends by itself within the join's bound.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=45s")
	child.Env = append(os.Environ(), stalledJoinChild+"=1")
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("the binary did not fail: %v", err)
	}
	for _, want := range []string{"panic: chord: the host loop's join waited", "1 running", "blocksRaw", "chordctx.Receive"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("the output lacks %q:\n%s", want, output)
		}
	}
}

// stalledJoinTest is the child: a task blocks on a bare channel, the join stalls, and the deferred call hangs.
func stalledJoinTest(t *testing.T) {
	h := joinHost(t)
	h.loop().(*Loop).stall = joinStallForTest
	release := make(chan struct{})
	h.loop().(*Loop).task(blocksRaw, func() { blocksRaw(release) })
	forever := make(chan struct{})
	defer func() { <-forever }()
	deliveredOn(h)
	t.Error("deliveredOn returned though a task blocks on a bare channel")
}

// Go-only (no Pi case): the count's channel is closed when the count falls to zero, and the next rise makes a new one; what was
// closed is taken out of its owner, so a second release of it (a second path to zero) never closes it again.
func TestQuietChannelIsClosedOnce(t *testing.T) {
	loop := NewLoop()
	loop.mu.Lock()
	loop.hopStartLocked()
	first := loop.quiet
	loop.hopStartLocked()
	loop.hopEndLocked()
	select {
	case <-first:
		t.Fatal("the count's channel was closed while a hop is still running")
	default:
	}
	loop.hopEndLocked()
	select {
	case <-first:
	default:
		t.Fatal("the count's channel was not closed when the count fell to zero")
	}
	if loop.quiet != nil {
		t.Fatal("the closed channel is still the loop's")
	}
	loop.hopStartLocked()
	second := loop.quiet
	loop.mu.Unlock()
	if second == nil || second == first {
		t.Fatal("the next rise from zero did not make a new channel")
	}
	select {
	case <-second:
		t.Fatal("the new channel is closed while a hop is running")
	default:
	}

	// A second release of the same channel: no panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a second release of the count's channel panicked: %v", r)
		}
	}()
	owner := make(chan struct{})
	releaseQuiet(&owner)
	releaseQuiet(&owner)
	var pace pacer
	pace.runningLocked()
	pace.quietLocked()
	releaseQuiet(&pace.quiet)
}
