package workers

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// heldRejection is a fleet whose S1 crashed and whose route has not handled the rejection of the resolve that follows the crash: the
// resolve's link closed after the handshake, so it already runs when the exit is published; the workers' `start` has read the crash
// (workers_workers.go, `crashed`) and returned it, and the route has not handled it, so its address is still `resolving`. The seam is
// the fleet's `resolved` hook, which wraps the route's `Resolve`.
type heldRejection struct {
	f       *fleet
	control *host.Reference
	// first is the incarnation of the worker that crashed.
	first   string
	release func()
	// resolves is the number of route resolves that returned since the crash.
	resolves func() int
}

func crashedWithItsRejectionHeld(t *testing.T) *heldRejection {
	t.Helper()
	var arm atomic.Bool
	var returned atomic.Int32
	held := make(chan struct{})
	free := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(free) }) }
	f := newFleet(t, fleetOptions{resolved: func(_ contracts.Address, link host.ResolvedLink, err error, ctx context.Context) (host.ResolvedLink, error) {
		returned.Add(1)
		if err != nil && arm.CompareAndSwap(true, false) {
			chordctx.Suspend(ctx)
			close(held)
			<-free
		}
		return link, err
	}})
	control := f.get(fixtureControl, "S1")
	t.Cleanup(control.Release)
	t.Cleanup(release)
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	first := f.stateOf("S1").Incarnation
	base := returned.Load()
	arm.Store(true)
	if _, err := control.Call("crash", 7.0, background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the route's resolve after the crash did not return its rejection")
	}
	if state := f.stateOf("S1"); state == nil || state.Status != WorkerFailed {
		t.Fatalf("state when the rejection is held = %+v, want failed", state)
	}
	return &heldRejection{f: f, control: control, first: first, release: release, resolves: func() int { return int(returned.Load() - base) }}
}

// reported is how many resolve_failed the server's host reported.
func reported(f *fleet) int {
	return len(slices.DeleteFunc(f.hostErrors.all(), func(err host.HostError) bool { return err.Kind != host.ErrResolveFailed }))
}

// restartsOnceAfterARejectionTheRouteHeld crashes S1 and holds the rejection of the route's automatic resolve between the workers'
// answer and the route's handling of it, then calls again, as an observer of `state` that saw `failed` does.
//
// The contract is Pi's, in either order its one chain of microtasks can give an observer (workers.ts #start :486-487, route.ts #resolve
// :265-273): a retry before the read makes the in-flight start go on, a retry after the route handled the rejection makes the route
// resolve; both start the worker once more, once, running another incarnation. The retry that entered in between cleared the failure the
// rejection reports (Retry and Rollout clear it before they call the route), so the rejection says it is superseded and the route
// resolves once more after handling it.
func restartsOnceAfterARejectionTheRouteHeld(t *testing.T, again func(f *fleet)) {
	t.Helper()
	h := crashedWithItsRejectionHeld(t)
	again(h.f)
	h.release()
	// The route handled the rejection (it reports it); the start the retry asked for follows it.
	until(t, func() bool { return reported(h.f) >= 1 })
	until(t, func() bool { state := h.f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	if got := h.f.starts.all(); len(got) != 2 {
		t.Fatalf("starts = %v, want two (the first and the one after the crash)", got)
	}
	if second := h.f.stateOf("S1").Incarnation; second == "" || second == h.first {
		t.Fatalf("incarnation after the restart = %q, want another than %q", second, h.first)
	}
}

// Go-only (no Pi case): a retry that enters between the start's read of the crash and the route's handling of its rejection (a Go
// goroutine hop is a turn there; Pi's chain of microtasks has none an observer's timer or I/O can use) starts the worker again, once.
func TestARetryThatEntersAfterTheStartReadTheCrashAndBeforeTheRouteHandledItsRejectionStartsTheWorkerAgainOnce(t *testing.T) {
	restartsOnceAfterARejectionTheRouteHeld(t, func(f *fleet) {
		if err := f.workers.Retry(session("S1"), background); err != nil {
			t.Fatal(err)
		}
	})
}

// Go-only (no Pi case): a rollout also clears the failures and calls the route's `retry` for them (workers.ts #rollout :829-832), so
// one that enters in the same gap starts the crashed worker again, once; it lists no host for it (the address's start had settled
// already, and a rollout lists the start its address has, :833-835).
func TestARolloutThatEntersAfterTheStartReadTheCrashAndBeforeTheRouteHandledItsRejectionStartsTheWorkerAgainOnce(t *testing.T) {
	restartsOnceAfterARejectionTheRouteHeld(t, func(f *fleet) {
		scripts, err := f.plugins.Scripts("worker", background)
		if err != nil {
			t.Fatal(err)
		}
		hosts, err := f.workers.Rollout(scripts, background)
		if err != nil {
			t.Fatal(err)
		}
		if len(hosts) != 0 {
			t.Fatalf("rollout hosts = %v, want none (no worker runs, and the address's start had settled)", hosts)
		}
	})
}

// Go-only (no Pi case): the other read of the recorded failure. A new hold of an address that is failed is a new request: the route
// resolves, and the workers' `resolve` rejects at once with the failure (workers.ts :370). A retry that enters between that answer and
// the route's handling of it has ended the failure the rejection reports, as in the crash's case, and starts the worker once.
func TestARetryThatEntersAfterAFailedAddressAnsweredANewHoldAndBeforeTheRouteHandledItsRejectionStartsTheWorkerAgainOnce(t *testing.T) {
	var arm atomic.Bool
	held := make(chan struct{})
	free := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(free) }) }
	f := newFleet(t, fleetOptions{resolved: func(_ contracts.Address, link host.ResolvedLink, err error, ctx context.Context) (host.ResolvedLink, error) {
		if err != nil && arm.CompareAndSwap(true, false) {
			chordctx.Suspend(ctx)
			close(held)
			<-free
		}
		return link, err
	}})
	t.Cleanup(release)
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	first := f.stateOf("S1").Incarnation
	if _, err := control.Call("crash", 7.0, background); err != nil {
		t.Fatal(err)
	}
	// The crash's own rejection is handled (reported), and its section has ended: the address is failed and resolves nothing.
	until(t, func() bool { return reported(f) >= 1 })
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	control.State().Value()
	arm.Store(true)
	second := f.hold(fixtureControl, "S1")
	defer second.Release()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the route's resolve for the new hold did not return its rejection")
	}
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	release()
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	if got := f.starts.all(); len(got) != 2 {
		t.Fatalf("starts = %v, want two (the first and the one after the crash)", got)
	}
	if again := f.stateOf("S1").Incarnation; again == "" || again == first {
		t.Fatalf("incarnation after the restart = %q, want another than %q", again, first)
	}
}

// Go-only (no Pi case): the rejection is superseded only when the failure it reports was ended. A call to the route's own `retry` that
// the workers did not make ends no failure of theirs: the address's failure is still the crash, a resolve for it would reject at once
// with it again, and Pi's guard (route.ts:237) drops the retry that entered while the resolve ran. Nothing resolves again, nothing
// starts, the address stays failed.
func TestACallToTheRoutesRetryThatEndsNoFailureOfTheWorkersIsNotFollowedByTheRejectionItEntersBefore(t *testing.T) {
	h := crashedWithItsRejectionHeld(t)
	if h.f.workers.route.Retry(session("S1")) {
		t.Fatal("retry resolved the address that is resolving")
	}
	h.release()
	until(t, func() bool { return reported(h.f) >= 1 })
	// The section that handled the rejection has ended: a resolve the route started in it has returned by now.
	h.control.State().Value()
	if got := h.resolves(); got != 1 {
		t.Fatalf("route resolves since the crash = %d, want 1 (the failure was not ended: nothing resolves for the retry)", got)
	}
	if state := h.f.stateOf("S1"); state == nil || state.Status != WorkerFailed {
		t.Fatalf("state = %+v, want failed", state)
	}
	if got := h.f.starts.all(); len(got) != 1 {
		t.Fatalf("starts = %v, want one (the first)", got)
	}
}
