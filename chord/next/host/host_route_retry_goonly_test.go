package host

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Go-only: Pi's `retry` (route.ts:158-164) clears the address's failure and calls #resolveIfHeld, which does nothing while the address's
// resolve runs (`routed.resolving`, :237). Pi's resolve that is about to reject and the route's handling of that rejection are a chain
// of microtasks, so no retry can enter between them from a timer or I/O; a Go resolve returns to a section of its own, and the retry
// can enter before it. The retry that entered while the resolve ran is then not lost: the resolve's rejection is handled as usual (its
// holders fail with it, it is reported), and the address resolves once more at once, as a retry after the rejection does.
func TestRouteHandleRetryWhileTheResolveRunsThatThenRejectsResolvesOnceMoreAfterTheRejection(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		pending := make(chan struct{})
		var calls atomic.Int32
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			if calls.Add(1) == 1 {
				_, _ = chordctx.Receive(ctx, pending)
				return resolved{}, errors.New("no worker")
			}
			return worker(), nil
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
		if got := calls.Load(); got != 2 {
			t.Fatalf("resolves = %d, want 2 (the rejected one, then the retry's)", got)
		}
	})
}
