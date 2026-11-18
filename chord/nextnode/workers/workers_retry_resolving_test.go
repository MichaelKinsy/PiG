package workers

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// restartsOnceAfterARejectionTheRouteHeld crashes S1 and holds the rejection of the route's automatic resolve (its link closed after the
// handshake, so it already runs when the exit is published) between the workers' answer and the route's handling of it, then calls
// again, as an observer of `state` that saw `failed` does. The workers' `start` has read the crash by then (workers_workers.go,
// `crashed`) and returned it; the route has not handled it, so its address still `resolving`.
//
// The contract is Pi's, in either order its one chain of microtasks can give an observer (workers.ts #start :485-487, route.ts #resolve
// :265-273): a retry before the read makes the in-flight start go on, a retry after the route handled the rejection makes the route
// resolve; both start the worker once more, once, running another incarnation. The seam is the fleet's `resolved` hook, which wraps
// the route's `Resolve`.
func restartsOnceAfterARejectionTheRouteHeld(t *testing.T, again func(f *fleet)) {
	t.Helper()
	var arm atomic.Bool
	held := make(chan struct{})
	release := make(chan struct{})
	f := newFleet(t, fleetOptions{resolved: func(_ contracts.Address, link host.ResolvedLink, err error, ctx context.Context) (host.ResolvedLink, error) {
		if err != nil && arm.CompareAndSwap(true, false) {
			chordctx.Suspend(ctx)
			close(held)
			<-release
		}
		return link, err
	}})
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	first := f.stateOf("S1").Incarnation
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
	again(f)
	close(release)
	// The route handled the rejection (it reports it); the start the retry asked for follows it.
	until(t, func() bool {
		return slices.ContainsFunc(f.hostErrors.all(), func(err host.HostError) bool { return err.Kind == host.ErrResolveFailed })
	})
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	if got := f.starts.all(); len(got) != 2 {
		t.Fatalf("starts = %v, want two (the first and the one after the crash)", got)
	}
	if second := f.stateOf("S1").Incarnation; second == "" || second == first {
		t.Fatalf("incarnation after the restart = %q, want another than %q", second, first)
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
