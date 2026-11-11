package host

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Go-only. Pi's route applies a resolve's result in the continuation of its `await`, which runs before the next operation of
// the workers component's queue (workers.ts `#enqueue`: the reaction that starts it is on the promise the bookkeeping
// reaction settles, one microtask after the route's). A Go resolve returns on a goroutine of its own and the route applies its
// result in a section of the host's loop afterwards, so the order is the app's to take: it releases its next operation with
// parking.AppliedOf(ctx).Then, which the route runs in the section that applied the result, after it did. Without that a Retry
// of the workers component's crash restart (Stop, then Retry) could enter the loop between the start's settling and the
// section applying its failure, find the address `resolving` and do nothing, and the failure the section then recorded stayed
// until the next retry (p2: a crashed session worker never restarted for its holders, e2e TestCodeModeFakeProvider's
// kill-and-reopen case).
func TestRouteRunsWhatTheAppReleasedOnAResolveAfterThatResolvesResultWasApplied(t *testing.T) {
	type seen struct {
		noApplied, inSection, resolving, failed, linked bool
	}
	for _, fail := range []bool{true, false} {
		name := "a link"
		if fail {
			name = "a failure"
		}
		t.Run(name, func(t *testing.T) {
			forEachCodec(t, func(t *testing.T) {
				ran := make(chan seen, 2)
				var server *chordHost
				r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
					applied := parking.AppliedOf(ctx)
					if applied == nil {
						ran <- seen{noApplied: true}
						return resolved{}, errors.New("no applied")
					}
					applied.Then(func() {
						// It runs in the section that applied the result: the address is not resolving any more, and the failure (or the
						// link) the result made is there.
						routed := server.routes["session"].addresses["session|S1"]
						ran <- seen{
							inSection: server.batch.InSection(), resolving: routed.resolving,
							failed: routed.failure != nil, linked: routed.path.link() != nil,
						}
					})
					if fail {
						return resolved{}, errors.New("worker exited")
					}
					return worker(), nil
				}})
				server = r.Server.(*chordHost)
				notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
				must(t, err)
				r.Transported()
				var got seen
				scopeEventually(t, "what the app released on the resolve ran", func() bool {
					select {
					case got = <-ran:
						return true
					default:
						return false
					}
				})
				want := seen{inSection: true, failed: fail, linked: !fail}
				if got != want {
					t.Fatalf("the release ran with %+v, want %+v (in a section, after the result was applied)", got, want)
				}
				if fail {
					wantRouteWaiting(t, notes, "session|S1: worker exited")
				} else {
					wantRouteState(t, notes, RefStateLive)
				}
				if len(ran) != 0 {
					t.Fatalf("what the app released ran %d times, want once", len(ran)+1)
				}
			})
		})
	}
}
