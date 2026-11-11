package host

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Go-only. Pi's `#resolve` applies the result of `options.resolve` in the continuation of its `await`, a microtask: a resolve
// that settles at once (the workers component's rejection of a failed address is `Promise.reject`) is applied before the
// event loop takes anything else, so whatever runs after the section that began it (a Retry that the p2 crash restart makes
// after its Stop) finds the address no longer `resolving` and the failure recorded. The Go route lent that section to Resolve
// and applied the result in a section of its own once the goroutine that ended the section had given the loop up, so another
// goroutine's section could enter between them: a Retry that met the address `resolving` did nothing, and the failure the
// resolve then recorded stayed until the next retry (p2: a worker that crashed was not restarted for its holders when its
// exit was handled before its link's close; e2e TestCodeModeFakeProvider, kill-and-reopen).
//
// The result of a resolve that returned inside the section is therefore a microtask of that section (Loop.Queue, the host's
// Promise reaction), queued where Pi's `await` queues its continuation: after the microtasks Resolve's synchronous part queued,
// before those the section queues afterwards.
func TestRouteAppliesAResolveThatSettledInsideItsSectionAsAMicrotaskOfThatSection(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var retrying atomic.Bool
		var server *chordHost
		var before resolveSeen
		r := relay(t, relayedOptions{Resolve: func(address contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
			if retrying.Load() {
				// Resolve's synchronous part queues a microtask before it returns: Pi's runs before the `await` continuation.
				server.loop().Queue(func() { before = seeResolve(server, address.Key) })
				return resolved{}, errors.New("worker exited")
			}
			return resolved{}, errors.New("no worker")
		}})
		server = r.Server.(*chordHost)
		address := Session.Address(jsonObject("id", "S1"))
		notes, err := r.Server.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		wantRouteWaiting(t, notes, "session|S1: no worker")
		// A section that retries the address, as the workers component's Retry does, with a microtask queued after it.
		var after resolveSeen
		var began bool
		retrying.Store(true)
		server.loop().Run(func() {
			began = r.Route.Retry(address)
			server.loop().Queue(func() { after = seeResolve(server, address.Key) })
		})
		if !began {
			t.Fatal("Retry of a held failed address started no resolve")
		}
		if want := (resolveSeen{resolving: true}); before != want {
			t.Fatalf("a microtask queued by Resolve's synchronous part saw %+v, want %+v (before the result is applied)", before, want)
		}
		if want := (resolveSeen{failed: true}); after != want {
			t.Fatalf("a microtask queued after the Retry saw %+v, want %+v (the result applied: not resolving, the failure kept)", after, want)
		}
		wantRouteWaiting(t, notes, "session|S1: worker exited")
	})
}

// resolveSeen is what a microtask saw of the address of the route.
type resolveSeen struct{ resolving, failed bool }

func seeResolve(h *chordHost, key string) resolveSeen {
	routed := h.routes["session"].addresses[key]
	return resolveSeen{resolving: routed.resolving, failed: routed.failure != nil}
}
