package workers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Go-only (no Pi case): the stated difference of the route's remembered retry. A retry that enters while a start is in flight and the
// start then fails on its own (the app's `start` throws) finds Pi's address failed until another retry. Here the route remembers the
// retry for the rejection of the resolve it entered during, so it handles the rejection as usual (reports it, fails the holders) and
// then resolves the address once more. The address has its new failure by then, so that resolve rejects at once: no fork, the app's
// `start` ran once, `worker_failed` is reported once, the address is failed, and the route reports the rejection twice.
func TestARetryDuringAStartThatThenFailsOnItsOwnResolvesTheAddressOnceMoreAndStartsNothing(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		once.Do(func() { close(entered) })
		<-release
		return nil, errors.New("the app's start failed")
	}})
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
	rejections := func() int {
		count := 0
		for _, err := range f.hostErrors.all() {
			if err.Kind == host.ErrResolveFailed {
				count++
			}
		}
		return count
	}
	until(t, func() bool { return rejections() == 2 })
	if state := f.stateOf("S1"); state == nil || state.Status != WorkerFailed {
		t.Fatalf("state = %+v, want failed", state)
	}
	if got := f.starts.all(); len(got) != 1 {
		t.Fatalf("starts = %v, want one (the retry started nothing more)", got)
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
