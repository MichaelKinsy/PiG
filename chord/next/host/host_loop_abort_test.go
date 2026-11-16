package host

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/internal/loopseam"
)

// Go-only (no Pi case): onAbort is the abort listener of a waiter that enters the loop (host_loop_abort.go). Pi's listener runs
// inside `abort()` (context/index.ts:138-153); what the host ports of it is the order against the section that cancels and the
// join: the listener's work is queued at the cancel, and a test's `delivered()` waits for it.

var errAborted = errors.New("aborted by the test")

func TestOnAbortFromOutsideASectionIsAHopTheJoinWaitsFor(t *testing.T) {
	h := joinHost(t)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	var ran atomic.Bool
	onAbort(h.loop(), ctx, func() { ran.Store(true) })
	cancel(errAborted)
	loopseam.Quiesce(h.loop())
	if !ran.Load() {
		t.Fatal("the join returned before the work an abort queued ran")
	}
}

func TestOnAbortInASectionQueuesItsWorkAfterTheSection(t *testing.T) {
	h := joinHost(t)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	var order []string
	h.enter(func() {
		onAbort(h.loop(), ctx, func() { order = append(order, "abort") })
		cancel(errAborted)
		order = append(order, "section")
	})
	if got := order; len(got) != 2 || got[0] != "section" || got[1] != "abort" {
		t.Fatalf("order = %v, want the section's own work first, then the work the abort queued, before the section's caller returns", got)
	}
}

func TestOnAbortOnAnAbortedContextQueuesAtOnce(t *testing.T) {
	h := joinHost(t)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	cancel(errAborted)
	var ran atomic.Bool
	onAbort(h.loop(), ctx, func() { ran.Store(true) })
	loopseam.Quiesce(h.loop())
	if !ran.Load() {
		t.Fatal("the listener of an aborted context did not run")
	}
}

func TestOnAbortStopKeepsItsWorkFromBeingQueued(t *testing.T) {
	h := joinHost(t)
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	var ran atomic.Bool
	stop := onAbort(h.loop(), ctx, func() { ran.Store(true) })
	if !stop() {
		t.Fatal("stop reported the listener already ran")
	}
	cancel(errAborted)
	loopseam.Quiesce(h.loop())
	if ran.Load() {
		t.Fatal("a stopped listener ran")
	}
}

func TestOnAbortListensToASourceOfAnotherPackageToo(t *testing.T) {
	h := joinHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	onAbort(h.loop(), ctx, func() { close(ran) })
	cancel()
	// A context of the standard library calls its listeners on a goroutine of its own: the work arrives through the loop once that
	// goroutine queued it, and the test waits for the work itself.
	<-ran
}

func TestOnAbortNowRunsInsideTheCancelThatAbortsTheContext(t *testing.T) {
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	var ran atomic.Bool
	onAbortNow(ctx, func() { ran.Store(true) })
	cancel(errAborted)
	if !ran.Load() {
		t.Fatal("the cancel returned before the listener ran: Pi's listener runs inside abort()")
	}
}

func TestOnAbortNowRunsOnceAndNotAfterItsStop(t *testing.T) {
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	var runs atomic.Int32
	stopped := onAbortNow(ctx, func() { runs.Add(1) })
	cancelled, cancelAgain := chordctx.WithCancel(chordctx.Background())
	stop := onAbortNow(cancelled, func() { runs.Add(10) })
	if !stop() {
		t.Fatal("stop reported the listener already ran")
	}
	cancelAgain(errAborted)
	cancel(errAborted)
	cancel(errAborted)
	if got := runs.Load(); got != 1 {
		t.Fatalf("listeners ran %d (units: 1 = the live one, 10 = the stopped one), want 1", got)
	}
	if stopped() {
		t.Fatal("stop after the listener ran reported that it kept it from running")
	}
}

func TestALinkedAbortFollowsItsSourcesInsideTheirCancel(t *testing.T) {
	for _, name := range []string{"the caller", "the signal"} {
		caller, abortCaller := chordctx.WithCancel(chordctx.Background())
		signal, abortSignal := chordctx.WithCancel(chordctx.Background())
		linked := withLinkedAbortSignal(signal, caller)
		abort := abortCaller
		if name == "the signal" {
			abort = abortSignal
		}
		abort(errAborted)
		if cause := context.Cause(linked.context); !errors.Is(cause, errAborted) {
			t.Fatalf("after the abort of %s the linked context's cause is %v, want %v at once (AbortSignal.any aborts in the same step)", name, cause, errAborted)
		}
		linked.release()
	}
}

// heapInUse is the live heap after two collections.
func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// A registration is held by every source of its context until its stop: a waiter that ends (it completed, or was abandoned
// by its owner) calls stop, and a long-lived context keeps nothing of 20,000 of them (a leaked one is about 300 bytes).
func TestOnAbortStoppedRegistrationsLeaveNothingOnALongLivedContext(t *testing.T) {
	h := joinHost(t)
	long, cancel := chordctx.WithCancel(chordctx.Background())
	defer cancel(errAborted)
	register := func(n int) {
		for range n {
			stop := onAbort(h.loop(), long, func() {})
			if !stop() {
				t.Fatal("stop reported the listener already ran")
			}
		}
	}
	register(1000)
	before := heapInUse()
	register(20000)
	if grown := int64(heapInUse()) - int64(before); grown > 2<<20 {
		t.Fatalf("20,000 registrations, each stopped, grew the heap by %d bytes (limit 2 MiB): a stopped listener stays on its context", grown)
	}
}
