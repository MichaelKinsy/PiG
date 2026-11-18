package workers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Pi's behavior, pinned (no Pi test): a retry that enters while a start is in flight, and the start then fails on its own (the app's
// `start` throws), is gone with that start.
//
//	workers.ts:391-397 `retry` clears the address's failure (none yet), refreshes the state and calls `route.retry`;
//	route.ts:158-164 `retry` clears its own failure and calls #resolveIfHeld, which returns at :237 because `routed.resolving`;
//	workers.ts:502 the start throws `this.#fail(...)` (:747): the failure is recorded, `worker_failed` reported once;
//	route.ts:265-273 the rejection is reported (`resolve_failed`, once) and #settle (:255) fails the holders with it.
//
// So Pi has one resolve, one `resolve_failed` report, one start and the address failed. The retry is not remembered for this rejection:
// it is the start's own failure, not a failure the retry ended (the one case in which the route resolves again after a rejection is a
// rejection that says its failure was ended, workers_retry_resolving_test.go). The route's `resolve` count is read from the fleet's
// `resolved` hook, and the section that handled the rejection is over when the hold's state is read: a resolve it started has returned.
func TestARetryDuringAStartThatThenFailsOnItsOwnIsOneResolveOneReportAndTheAddressStaysFailed(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var resolves atomic.Int32
	f := newFleet(t, fleetOptions{
		start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
			once.Do(func() { close(entered) })
			<-release
			return nil, errors.New("the app's start failed")
		},
		resolved: func(_ contracts.Address, link host.ResolvedLink, err error, _ context.Context) (host.ResolvedLink, error) {
			resolves.Add(1)
			return link, err
		},
	})
	reference := f.hold(fixtureControl, "S1")
	defer reference.Release()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the app's start did not run")
	}
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	close(release)
	until(t, func() bool { return reported(f) >= 1 })
	// The section that handled the rejection has ended: a resolve it started has returned.
	reference.State().Value()
	if got := resolves.Load(); got != 1 {
		t.Fatalf("route resolves = %d, want 1 (the retry was not remembered: the start failed on its own)", got)
	}
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	if got := reported(f); got != 1 {
		t.Fatalf("resolve_failed reports = %d, want 1", got)
	}
	if got := f.starts.all(); len(got) != 1 {
		t.Fatalf("starts = %v, want one", got)
	}
	failures := 0
	for _, err := range f.errors.all() {
		if _, ok := err.(WorkerFailedError); ok {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("worker_failed reports = %d, want 1", failures)
	}
}
