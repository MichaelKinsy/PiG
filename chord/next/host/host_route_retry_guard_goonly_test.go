package host

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Go-only: the guards of the route's remembered retry (TestRouteHandleRetryWhileTheResolveRunsThatThenRejectsASupersededFailureResolvesOnceMoreAfterTheRejection):
// it is for the superseded rejection of the resolve it entered during, and for no other. Every rejection here says it is superseded, so
// that only the guard under test can keep the retry from resolving.

// supersededWith is a rejection that says its failure was ended.
func supersededWith(message string) error {
	return supersedingRejection{error: errors.New(message), superseded: func() bool { return true }}
}

// A resolve that succeeds takes the retry with it (the link serves the address: nothing restarts). The address's next rejection, after
// its link dropped, fails its holders and resolves nothing more: only the resolve the drop started ran. A remembered retry that outlived
// the resolve it entered during would resolve after every later rejection, for ever: the third resolve answers with a plain error, which
// ends that, so the count below fails instead of the test running away.
func TestRouteHandleRetryWhileTheResolveRunsThatSucceedsIsNotRememberedForTheRejectionOfALaterResolve(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		var calls atomic.Int32
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			switch calls.Add(1) {
			case 1:
				_, _ = chordctx.Receive(ctx, pending)
				return worker(), nil
			case 2:
				return resolved{}, supersededWith("no worker")
			}
			return resolved{}, errors.New("a third resolve: the retry was remembered for a later rejection")
		}})
		notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		if r.Route.Retry(Session.Address(jsonObject("id", "S1"))) {
			t.Fatal("retry resolved the address that is resolving")
		}
		close(pending)
		r.Transported()
		wantRouteState(t, notes, RefStateLive)
		r.RouteLinks()[0].Close()
		r.Transported()
		wantRouteFailed(t, notes, data.FailureWaiting, data.CodeUnavailable)
		r.Transported()
		if got := calls.Load(); got != 2 {
			t.Fatalf("resolves = %d, want 2 (the first, then the one the dropped link started)", got)
		}
	})
}

// An address suspended after the retry entered stays suspended: the rejection fails its holders `suspended` and nothing resolves.
func TestRouteHandleRetryWhileTheResolveRunsIsNotRememberedOnceTheAddressIsSuspendedAgain(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		var calls atomic.Int32
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, _ func() resolved, ctx context.Context) (resolved, error) {
			calls.Add(1)
			_, _ = chordctx.Receive(ctx, pending)
			return resolved{}, supersededWith("no worker")
		}})
		notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		address := Session.Address(jsonObject("id", "S1"))
		r.Route.Retry(address)
		r.Route.Suspend(address)
		close(pending)
		r.Transported()
		wantRouteWaiting(t, notes, "session|S1: suspended")
		r.Transported()
		if got := calls.Load(); got != 1 {
			t.Fatalf("resolves = %d, want 1 (a suspended address resolves nothing)", got)
		}
	})
}

// An address whose last holder released while the resolve ran is not resolved again for the retry: the rejection forgets it.
func TestRouteHandleRetryWhileTheResolveRunsIsNotRememberedOnceTheLastHolderReleasedAndTheAddressIsForgotten(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		var calls atomic.Int32
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, _ func() resolved, ctx context.Context) (resolved, error) {
			calls.Add(1)
			_, _ = chordctx.Receive(ctx, pending)
			return resolved{}, supersededWith("no worker")
		}})
		notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		r.Route.Retry(Session.Address(jsonObject("id", "S1")))
		notes.Release()
		r.Transported()
		close(pending)
		r.Transported()
		h := r.Server.(*chordHost)
		scopeEventually(t, "the address is forgotten", func() bool {
			var kept int
			h.loop().Run(func() { kept = len(h.routes["session"].addresses) })
			return kept == 0
		})
		if got := calls.Load(); got != 1 {
			t.Fatalf("resolves = %d, want 1 (an address nobody holds resolves nothing)", got)
		}
	})
}
