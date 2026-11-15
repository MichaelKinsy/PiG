package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
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
			var continued atomic.Bool
			h.enter(func() {
				awaitTask(h, func() (int, error) {
					w.park(ctx, gate, settled)
					return 1, nil
				}).then(func(int, error) { continued.Store(true) })
			})
			// The task parks (or the join waits until it does, it cannot end before the wait is over): the join returns with
			// the task unfinished.
			loopseam.Quiesce(h.loop())
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
	loopseam.Quiesce(h.loop())
	if continued.Load() {
		t.Fatal("the task went past the line's holder")
	}
	hold()
	loopseam.Quiesce(h.loop())
	if !continued.Load() {
		t.Fatal("the join after the holder released returned before the task's continuation ran")
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
