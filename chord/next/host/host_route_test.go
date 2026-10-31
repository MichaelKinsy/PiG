package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/host/route.test.ts: routes (§3) on whole relayed hosts, once per codec as upstream's
// `forEachCodec`. Pi's fake timers are the scopeLoop every host of a case runs on (relayedOptions.Loop): `elapse` moves
// its clock and lets links settle.

// routeElapse is `vi.advanceTimersByTimeAsync(ms)` then `transported()`: the fake clock moves in steps of at most 500 ms
// and the links settle after each, as vitest lets promises (the links' events) run between timers. A Go link delivers its
// events on tasks of its own, which the clock does not wait for.
func routeElapse(r *relayed, timers *scopeLoop, ms int64) {
	for ms > 0 {
		step := min(ms, 500)
		timers.AdvanceAsync(step)
		timers.barrier()
		r.Transported()
		ms -= step
	}
}

// waitServerLinks waits until the server host has n open links (the route's resolve made one): the fake clock must not move
// before, since a link's silence is counted from its creation.
func waitServerLinks(t *testing.T, r *relayed, n int) {
	t.Helper()
	h := r.Server.(*chordHost)
	scopeEventually(t, fmt.Sprintf("the server has %d links", n), func() bool {
		got := 0
		h.loop().Run(func() { got = len(h.links) })
		return got == n
	})
}

// wantRouteWaiting waits until ref is `failed { waiting }` with an `unavailable` error of message.
func wantRouteWaiting(t *testing.T, ref *Reference, message string) {
	t.Helper()
	scopeEventually(t, "the reference failed waiting: "+message, func() bool {
		got := routeState(ref)
		return got.Kind == RefStateFailed && got.Cause.Kind == data.FailureWaiting && got.Cause.Error.Code == data.CodeUnavailable &&
			got.Cause.Error.Message == message
	})
}

func routeState(ref *Reference) *RefState { return ref.State().Value() }

// wantRouteState waits (the routes' work runs on tasks of the host's loop) until ref is in a state of kind.
func wantRouteState(t *testing.T, ref *Reference, kind RefStateKind) {
	t.Helper()
	scopeEventually(t, "the reference is "+string(kind), func() bool { return routeState(ref).Kind == kind })
}

// wantRouteFailed fails the test unless ref is failed with a cause of kind whose error has code.
func wantRouteFailed(t *testing.T, ref *Reference, kind data.FailureKind, code data.ChordErrorCode) {
	t.Helper()
	scopeEventually(t, "the reference failed "+string(kind)+"/"+string(code), func() bool {
		got := routeState(ref)
		return got.Kind == RefStateFailed && got.Cause.Kind == kind && got.Cause.Error.Code == code
	})
}

// wantResolves fails the test unless the keys `resolve` ran for are want, in order.
func wantResolves(t *testing.T, r *relayed, want ...string) {
	t.Helper()
	scopeEventually(t, fmt.Sprintf("resolves are %v (now %v)", want, r.Resolves()), func() bool { return slices.Equal(r.Resolves(), want) })
}

func routeS(session string) *delta.JsonObject { return jsonObject("session", session) }

// closeWatched is a link that tells when it closed (the host's own listener stays).
type closeWatched struct {
	link.Link
	closed atomic.Bool
}

func (c *closeWatched) Close() {
	c.closed.Store(true)
	c.Link.Close()
}

func (c *closeWatched) OnClose(listener func()) {
	c.Link.OnClose(func() {
		c.closed.Store(true)
		listener()
	})
}

// route.test.ts › route admission › runs one resolve per address, shared by the holds that wait for it (race 1).
func TestRouteAdmissionRunsOneResolvePerAddressSharedByTheHoldsThatWaitForItRace1(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{})
		var refs [2]*Reference
		var wg sync.WaitGroup
		for i := range refs {
			wg.Go(func() {
				ref, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
				must(t, err)
				refs[i] = ref
			})
		}
		wg.Wait()
		wantResolves(t, r, "session|S1")
		addNote(t, r.Worker("session|S1"), "S1", "a")
		r.Transported()
		for _, ref := range refs {
			value, err := ref.Member("notes").Value()
			must(t, err)
			nexttest.RequireJSONEqual(t, value, []any{jsonObject("text", "a")})
		}
		_, err := r.Tab.Get(PlacedNotes, routeS("S2"), background, GetOptions{})
		must(t, err)
		wantResolves(t, r, "session|S1", "session|S2")
	})
}

// route.test.ts › route admission › authorizes every hold with its principal: a refusal is broken forbidden, other
// principals still resolve (race 4).
func TestRouteAdmissionAuthorizesEveryHoldWithItsPrincipalARefusalIsBrokenForbiddenOtherPrincipalsStillResolveRace4(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		authorize := func(principal data.Principal, address contracts.Address) bool {
			return principalKind(principal) == "owner" ||
				principalKind(principal) == "guest" && address.Key == "session|"+principal.Value("session").(string)
		}
		guest := relay(t, relayedOptions{Tab: guestPrincipal("S1"), Authorize: authorize})
		_, err := guest.Tab.Get(PlacedNotes, routeS("S2"), background, GetOptions{})
		nexttest.RequireJSONEqual(t, rejection(t, err), map[string]any{"code": "forbidden", "message": "session|S2: not authorized"})
		wantResolves(t, guest)
		reader, err := guest.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		if variant, _, _ := reader.Variant(); variant != "reader" {
			t.Fatalf("variant = %q, want reader", variant)
		}
		owner := relay(t, relayedOptions{Authorize: authorize})
		editor, err := owner.Tab.Get(PlacedNotes, routeS("S2"), background, GetOptions{})
		must(t, err)
		if variant, _, _ := editor.Variant(); variant != "editor" {
			t.Fatalf("variant = %q, want editor", variant)
		}
	})
}

// route.test.ts › route admission › ref.retry() holds a refused hold again: authorize admitting it later, over a link and
// on this host (audit).
func TestRouteAdmissionRefRetryHoldsARefusedHoldAgainAuthorizeAdmittingItLaterOverALinkAndOnThisHostAudit(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var admit atomic.Bool
		r := relay(t, relayedOptions{Authorize: func(data.Principal, contracts.Address) bool { return admit.Load() }})
		overLink, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		here, err := r.Server.Hold(PlacedNotes, routeS("S2"), background, GetOptions{})
		must(t, err)
		r.Transported()
		for _, ref := range []*Reference{overLink, here} {
			wantRouteFailed(t, ref, data.FailureBroken, data.CodeForbidden)
		}
		admit.Store(true)
		overLink.Retry()
		here.Retry()
		r.Transported()
		wantRouteState(t, overLink, RefStateLive)
		wantRouteState(t, here, RefStateLive)
	})
}

// route.test.ts › route admission › ref.retry() of a reference refused on this host after host.close ends it released,
// nothing resolved (audit).
func TestRouteAdmissionRefRetryOfAReferenceRefusedOnThisHostAfterHostCloseEndsItReleasedNothingResolvedAudit(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var admit atomic.Bool
		r := relay(t, relayedOptions{Authorize: func(data.Principal, contracts.Address) bool { return admit.Load() }})
		ref, err := r.Server.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		if got := routeState(ref); got.Kind != RefStateFailed || got.Cause.Error.Code != data.CodeForbidden {
			t.Fatalf("state = %+v, want failed forbidden", got)
		}
		must(t, r.Server.Close(background))
		admit.Store(true)
		ref.Retry()
		r.Transported()
		got := routeState(ref)
		if got.Kind != RefStateEnded || got.Error.Code != data.CodeReleased || got.Error.Reason != data.ReasonClosed {
			t.Fatalf("state = %+v, want ended released/closed", got)
		}
		wantResolves(t, r)
	})
}

// route.test.ts › route admission › fails a hold broken with what authorize throws.
func TestRouteAdmissionFailsAHoldBrokenWithWhatAuthorizeThrows(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{AuthorizeErr: func(data.Principal, contracts.Address) (bool, error) {
			return false, errors.New("authorize threw")
		}})
		_, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		if err == nil || err.Error() != "app.placed-notes: authorize threw" {
			t.Fatalf("get = %v, want app.placed-notes: authorize threw", err)
		}
	})
}

// route.test.ts › route admission › fails a hold broken invalid/host_address for an address whose params do not fit the
// kind.
func TestRouteAdmissionFailsAHoldBrokenInvalidHostAddressForAnAddressWhoseParamsDoNotFitTheKind(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		misplaced := mustContract(contracts.Remote("app.misplaced", contracts.RemoteDeclaration{
			Version: 1,
			Params:  contracts.Params{contracts.Prop("n", contracts.Int())},
			// An address of the right kind with params of the wrong type, as a buggy `hostAddress` could compute.
			HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
				return contracts.Address{Kind: "session", Params: jsonObject("id", params.Value("n")), Key: ""}, nil
			},
			Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
				State: []contracts.Property{contracts.Prop("n", contracts.Int())},
			})},
		}))
		r := relay(t, relayedOptions{})
		_, err := r.Tab.Get(misplaced, jsonObject("n", float64(1)), background, GetOptions{})
		chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
		if !ok || chordErr.Reason != data.ReasonHostAddress {
			t.Fatalf("get = %v, want invalid/host_address", err)
		}
		wantResolves(t, r)
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a rejected resolve fails its holders waiting
// with its error, is reported, and runs no timer (race 2).
func TestResolveFailuresAndReResolvesARejectedResolveFailsItsHoldersWaitingWithItsErrorIsReportedAndRunsNoTimerRace2(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var attempts atomic.Int32
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(contracts.Address, func() resolved, context.Context) (resolved, error) {
			attempts.Add(1)
			return resolved{}, errors.New("no worker")
		}})
		// One relayed from the tab, one the server's own.
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		own, err := r.Server.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		for _, ref := range []*Reference{notes, own} {
			wantRouteWaiting(t, ref, "session|S1: no worker")
		}
		scopeEventually(t, "the server reported the resolve failure", func() bool { return len(r.Errors.Server.All()) == 1 })
		errs := r.Errors.Server.All()
		if errs[0].Kind != ErrResolveFailed || errs[0].Address != "session|S1" {
			t.Fatalf("error = %+v, want resolve_failed session|S1", errs[0])
		}
		routeElapse(r, timers, 120_000)
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a new hold resolves a failed address again;
// the earlier holders bind on its link without a re-hold (Q1).
func TestResolveFailuresAndReResolvesANewHoldResolvesAFailedAddressAgainTheEarlierHoldersBindOnItsLinkWithoutAReHoldQ1(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var fail atomic.Bool
		fail.Store(true)
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if fail.Load() {
				return resolved{}, errors.New("no worker")
			}
			return worker(), nil
		}})
		first, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteFailed(t, first, data.FailureWaiting, data.CodeUnavailable)
		fail.Store(false)
		second, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantResolves(t, r, "session|S1")
		wantRouteState(t, first, RefStateLive)
		wantRouteState(t, second, RefStateLive)
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › RouteHandle.retry resolves a failed address
// now; a retry of an address nobody holds keeps nothing.
func TestResolveFailuresAndReResolvesRouteHandleRetryResolvesAFailedAddressNowARetryOfAnAddressNobodyHoldsKeepsNothing(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var fail atomic.Bool
		fail.Store(true)
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if fail.Load() {
				return resolved{}, errors.New("no worker")
			}
			return worker(), nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteFailed(t, notes, data.FailureWaiting, data.CodeUnavailable)
		fail.Store(false)
		r.Route.Retry(Session.Address(jsonObject("id", "S1")))
		r.Route.Retry(Session.Address(jsonObject("id", "S9")))
		r.Transported()
		wantRouteState(t, notes, RefStateLive)
		wantResolves(t, r, "session|S1")
		h := r.Server.(*chordHost)
		var kept int
		h.loop().Run(func() { kept = len(h.routes["session"].addresses) })
		if kept != 1 {
			t.Fatalf("route addresses = %d, want 1 (S9 keeps nothing)", kept)
		}
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a returned link closed already fails the
// holders.
func TestResolveFailuresAndReResolvesAReturnedLinkClosedAlreadyFailsTheHolders(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{Resolve: func(address contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
			a, b := link.MemoryLinkPair()
			b.Close()
			transported(a, b)
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteWaiting(t, notes, "session|S1: the link closed before its handshake")
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a link closing before its handshake fails its
// holders: no re-resolve loop over a worker dying at start.
func TestResolveFailuresAndReResolvesALinkClosingBeforeItsHandshakeFailsItsHoldersNoReResolveLoopOverAWorkerDyingAtStart(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var attempts atomic.Int32
		var made atomic.Bool
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(address contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
			attempts.Add(1)
			a, b := link.MemoryLinkPair()
			timers.AfterFunc(10*time.Millisecond, b.Close)
			made.Store(true)
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		scopeEventually(t, "the link was made", made.Load)
		routeElapse(r, timers, 10)
		wantRouteFailed(t, notes, data.FailureWaiting, data.CodeUnavailable)
		routeElapse(r, timers, 120_000)
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a dropped link that completed its handshake
// resolves again at once, once (race 6).
func TestResolveFailuresAndReResolvesADroppedLinkThatCompletedItsHandshakeResolvesAgainAtOnceOnceRace6(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		r := relay(t, relayedOptions{Loop: timers})
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		first := r.Worker("session|S1")
		r.RouteLinks()[0].Close()
		r.Transported()
		wantResolves(t, r, "session|S1", "session|S1")
		scopeEventually(t, "a fresh worker", func() bool { return r.Worker("session|S1") != first })
		wantRouteState(t, notes, RefStateLive)
		addNote(t, r.Worker("session|S1"), "S1", "fresh")
		r.Transported()
		value, err := notes.Member("notes").Value()
		must(t, err)
		nexttest.RequireJSONEqual(t, value, []any{jsonObject("text", "fresh")})
	})
}

// route.test.ts › resolve failures and re-resolves (no timer, slice 10 R2) › a link whose peer never greets closes after 30 s
// of silence and fails its holders.
func TestResolveFailuresAndReResolvesALinkWhosePeerNeverGreetsClosesAfter30sOfSilenceAndFailsItsHolders(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// The heartbeat's timer too (Pi: `toFake` adds setInterval), before the hosts start it.
		timers := newScopeLoop().fakeIntervals()
		var attempts atomic.Int32
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(address contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
			attempts.Add(1)
			// Its other end is never read: no hello, no close.
			a, _ := link.MemoryLinkPair()
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		waitServerLinks(t, r, 2)
		routeElapse(r, timers, 29_000)
		wantRouteState(t, notes, RefStateLoading)
		// The heartbeat checks every 1.5 s: the silent link closes within it.
		routeElapse(r, timers, 2_500)
		wantRouteFailed(t, notes, data.FailureWaiting, data.CodeUnavailable)
		routeElapse(r, timers, 120_000)
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})
}

// route.test.ts › resolve failures and re-resolves › an address without holds is not resolved again after its link drops.
func TestResolveFailuresAndReResolvesAnAddressWithoutHoldsIsNotResolvedAgainAfterItsLinkDrops(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		r := relay(t, relayedOptions{Loop: timers})
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		notes.Release()
		r.Transported()
		r.RouteLinks()[0].Close()
		routeElapse(r, timers, 60_000)
		wantResolves(t, r, "session|S1")
	})
}

// route.test.ts › resolve failures and re-resolves › a resolve whose holds were all released meanwhile finishes and its
// link is kept (race 3).
func TestResolveFailuresAndReResolvesAResolveWhoseHoldsWereAllReleasedMeanwhileFinishesAndItsLinkIsKeptRace3(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		finish := make(chan struct{})
		var watchedLink atomic.Pointer[closeWatched]
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(_ contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			<-finish
			made := worker()
			watching := &closeWatched{Link: made.Link}
			watchedLink.Store(watching)
			made.Link = watching
			return made, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		notes.Release()
		r.Transported()
		close(finish)
		r.Transported()
		wantResolves(t, r, "session|S1")
		routeElapse(r, timers, 60_000)
		scopeEventually(t, "the resolve returned its link", func() bool { return watchedLink.Load() != nil })
		if watchedLink.Load().closed.Load() {
			t.Fatalf("the kept link closed")
		}
		// The kept link serves the next hold without a resolve.
		_, err = r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		wantResolves(t, r, "session|S1")
		must(t, r.Server.Close(background))
		if !watchedLink.Load().closed.Load() {
			t.Fatalf("the link is open after host.close")
		}
	})
}

// route.test.ts › RouteHandle.suspend (R1, Q3) › resolves nothing while held: the holders fail waiting suspended once the
// link is gone; retry lifts it.
func TestRouteHandleSuspendResolvesNothingWhileHeldTheHoldersFailWaitingSuspendedOnceTheLinkIsGoneRetryLiftsIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{})
		address := Session.Address(jsonObject("id", "S1"))
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Route.Suspend(address)
		wantRouteState(t, notes, RefStateLive)
		r.RouteLinks()[0].Close()
		r.Transported()
		wantRouteWaiting(t, notes, "session|S1: suspended")
		another, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteFailed(t, another, data.FailureWaiting, data.CodeUnavailable)
		wantResolves(t, r, "session|S1")
		r.Route.Retry(address)
		r.Transported()
		wantResolves(t, r, "session|S1", "session|S1")
		wantRouteState(t, notes, RefStateLive)
		wantRouteState(t, another, RefStateLive)
	})
}

// route.test.ts › RouteHandle.suspend (R1, Q3) › goes with the last holder: a later hold resolves on demand; an unheld
// address is never suspended.
func TestRouteHandleSuspendGoesWithTheLastHolderALaterHoldResolvesOnDemandAnUnheldAddressIsNeverSuspended(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{})
		address := Session.Address(jsonObject("id", "S1"))
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Route.Suspend(address)
		r.RouteLinks()[0].Close()
		r.Transported()
		notes.Release()
		r.Transported()
		r.Route.Suspend(address)
		_, err = r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		wantResolves(t, r, "session|S1", "session|S1")
	})
}

// route.test.ts › host.close during a resolve (race 7) › aborts the resolve's context and closes a link returned afterwards.
func TestHostCloseDuringAResolveRace7AbortsTheResolvesContextAndClosesALinkReturnedAfterwards(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		finish := make(chan struct{})
		var routing atomic.Value
		var watchedLink atomic.Pointer[closeWatched]
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			routing.Store(ctx)
			<-finish
			made := worker()
			watching := &closeWatched{Link: made.Link}
			watchedLink.Store(watching)
			made.Link = watching
			return made, nil
		}})
		_, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		scopeEventually(t, "the resolve ran", func() bool { return routing.Load() != nil })
		ctx := routing.Load().(context.Context)
		if ctx.Err() != nil {
			t.Fatalf("the resolve's context is aborted before the close")
		}
		must(t, r.Server.Close(background))
		if ctx.Err() == nil {
			t.Fatalf("the resolve's context is not aborted by host.close")
		}
		close(finish)
		r.Transported()
		scopeEventually(t, "the late link was closed", func() bool {
			watching := watchedLink.Load()
			return watching != nil && watching.closed.Load()
		})
	})
}

// route.test.ts › declaring routes › is done before load, once per kind.
func TestDeclaringRoutesIsDoneBeforeLoadOncePerKind(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{OnError: func(HostError) {}})
		options := RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(_ contracts.Address, ctx context.Context) (ResolvedLink, error) {
				<-ctx.Done()
				return ResolvedLink{}, context.Cause(ctx)
			},
		}
		must(t, host.Route(Session, options, background))
		if err := host.Route(Session, options, background); err == nil || err.Error() != "route: session is routed already" {
			t.Fatalf("second route = %v, want route: session is routed already", err)
		}
		must(t, host.Load(nil, background))
		if err := host.Route(mustKind(contracts.NewHostKind("other")), options, background); err == nil || err.Error() != "route: routes are declared before load" {
			t.Fatalf("route after load = %v, want route: routes are declared before load", err)
		}
	})
}

// route.test.ts › a route's context › is the parent of its resolves and of the work arriving on its links.
func TestARoutesContextIsTheParentOfItsResolvesAndOfTheWorkArrivingOnItsLinks(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		app := chordctx.NewKey[string]("app")
		who := mustContract(contracts.Remote("app.who", contracts.RemoteDeclaration{
			Version:     1,
			Params:      contracts.Params{},
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
			Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
				Methods: []contracts.MethodEntry{contracts.Meth("app", contracts.NewMethod(contracts.TSchema{}, contracts.Str()))},
			})},
		}))
		var mu sync.Mutex
		var resolvedBy []string
		var worker Host
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
		must(t, server.Route(Session, RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(address contracts.Address, routing context.Context) (ResolvedLink, error) {
				value, _ := chordctx.Value(routing, app)
				mu.Lock()
				resolvedBy = append(resolvedBy, value)
				mu.Unlock()
				a, b := link.MemoryLinkPair()
				made := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal("S1"), OnError: func(HostError) {}})
				if err := made.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background); err != nil {
					return ResolvedLink{}, err
				}
				err := made.Load([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
					_, err := serveNotes(h, PlacedNotes)
					return err
				})}}, background)
				mu.Lock()
				worker = made
				mu.Unlock()
				return ResolvedLink{Link: a, Principal: workerPrincipal("S1"), Codec: testCodec()}, err
			},
		}, chordctx.WithValue(background, app, "the app's")))
		must(t, server.Load([]HostFacet{{Name: "@test/server", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(who, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
					Methods: map[string]contracts.MethodFunction{
						"app": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							if value, ok := chordctx.Value(ctx, app); ok {
								return value, nil
							}
							return "none", nil
						},
					},
				}}, nil
			}, ServeOptions{})
		})}}, background))
		_, err := server.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		// The worker is made by the resolve, on a task of the route.
		for {
			mu.Lock()
			ready := worker != nil
			mu.Unlock()
			if ready {
				break
			}
			deliveredOn(server)
		}
		// The worker's call reaches the server over the route link: its handler runs under the route's context.
		held, err := worker.Get(who, jsonObject(), background, GetOptions{})
		must(t, err)
		got, err := held.Call("app", nil, background)
		must(t, err)
		if got != "the app's" {
			t.Fatalf("app = %v, want the app's", got)
		}
		mu.Lock()
		if !slices.Equal(resolvedBy, []string{"the app's"}) {
			t.Fatalf("resolved by %v, want [the app's]", resolvedBy)
		}
		mu.Unlock()
	})
}

// route.test.ts › GC › a failed or suspended address keeps no route state once its holders are released.
func TestRouteGCAFailedOrSuspendedAddressKeepsNoRouteStateOnceItsHoldersAreReleased(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{Resolve: func(address contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if address.Params.Value("id") == "failed" {
				return resolved{}, errors.New("no worker")
			}
			return worker(), nil
		}})
		failed, err := r.Tab.Hold(PlacedNotes, routeS("failed"), background, GetOptions{})
		must(t, err)
		held, err := r.Tab.Get(PlacedNotes, routeS("held"), background, GetOptions{})
		must(t, err)
		r.Route.Suspend(Session.Address(jsonObject("id", "held")))
		r.RouteLinks()[0].Close()
		r.Transported()
		failed.Release()
		held.Release()
		r.Transported()
		h := r.Server.(*chordHost)
		scopeEventually(t, "the route keeps no address", func() bool {
			kept := -1
			h.loop().Run(func() { kept = len(h.routes["session"].addresses) + len(h.routes["session"].keys) + len(h.routes["session"].links) })
			return kept == 0
		})
	})
}

// route.test.ts › GC › a closed route link and its released references keep no route state.
func TestRouteGCAClosedRouteLinkAndItsReleasedReferencesKeepNoRouteState(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var references []weak.Pointer[Reference]
		var workers []weak.Pointer[chordHost]
		var links []weak.Pointer[link.MemoryLink]
		var tab Host
		func() {
			r := relay(t, relayedOptions{})
			tab = r.Tab
			notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
			must(t, err)
			references = append(references, weak.Make(notes))
			workers = append(workers, weak.Make(r.Worker("session|S1").(*chordHost)))
			routeLinks := r.RouteLinks()
			links = append(links, weak.Make(routeLinks[0]))
			notes.Release()
			r.Transported()
			routeLinks[0].Close()
			r.Workers.Clear()
			r.mu.Lock()
			r.routeLinks = nil
			r.mu.Unlock()
			r.Transported()
		}()
		expectCollectedEventually(t, references...)
		expectCollectedEventually(t, workers...)
		expectCollectedEventually(t, links...)
		if tab == nil {
			t.Fatal("the tab is gone")
		}
	})
}

// route.test.ts › GC › holds released while their resolve never settles are not kept.
func TestRouteGCHoldsReleasedWhileTheirResolveNeverSettlesAreNotKept(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var references []weak.Pointer[Reference]
		var server Host
		func() {
			r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, _ func() resolved, ctx context.Context) (resolved, error) {
				<-ctx.Done()
				return resolved{}, context.Cause(ctx)
			}})
			server = r.Server
			notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
			must(t, err)
			r.Transported()
			notes.Release()
			r.Transported()
			references = append(references, weak.Make(notes))
		}()
		expectCollectedEventually(t, references...)
		if server == nil {
			t.Fatal("the server is gone")
		}
	})
}
