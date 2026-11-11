package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Go-only. Pi's `#enqueue` runs the next operation of an address from the reaction on the promise its bookkeeping reaction
// settles, one microtask after the route's `await` of a start: the route applied the start's result before the next operation
// (a Stop, and the Retry after it) begins. Here the route applies the result in a section after the goroutine of `resolve`
// returned, so the start releases the next operation in that section (parking.Applied), not when it settles: a Retry that came
// between the two found the address `resolving`, did nothing, and the failure the section then recorded was never retried
// (p2: a crashed session worker was not restarted for its holders).
func TestResolveReleasesTheNextOperationInTheSectionThatAppliesTheStartsResult(t *testing.T) {
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		return nil, errors.New("no start data")
	}})
	address := fixtureSession.Address(jsonObject("id", "S1"))
	applied := parking.NewApplied()
	if _, err := f.workers.resolve(address, parking.WithApplied(background, applied)); err == nil {
		t.Fatal("the start with no data resolved a link")
	}
	// The start settled and its bookkeeping ran: the operation's goroutine ends after it handed the release to the route.
	var next chan struct{}
	f.workers.loop.Run(func() { next = f.workers.addresses[address.Key].queue })
	if err := f.workers.tasks.wait(background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-next:
		t.Fatal("the next operation was released before the route applied the start's result")
	default:
	}
	applied.Apply()
	select {
	case <-next:
	default:
		t.Fatal("the next operation was not released by the section that applied the start's result")
	}
}

// A resolve no route made (no parking.Applied on its ctx) has no continuation to wait for: the next operation is released
// when the start settles.
func TestResolveReleasesTheNextOperationWhenTheStartSettlesWithoutARouteThatApplies(t *testing.T) {
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		return nil, errors.New("no start data")
	}})
	address := fixtureSession.Address(jsonObject("id", "S1"))
	if _, err := f.workers.resolve(address, background); err == nil {
		t.Fatal("the start with no data resolved a link")
	}
	var next chan struct{}
	f.workers.loop.Run(func() { next = f.workers.addresses[address.Key].queue })
	if err := f.workers.tasks.wait(background); err != nil {
		t.Fatal(err)
	}
	select {
	case <-next:
	default:
		t.Fatal("the next operation was not released when the start settled")
	}
}
