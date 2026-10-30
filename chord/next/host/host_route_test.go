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

// elapse advances the fake clock by ms, then lets links and promises settle.
func routeElapse(r *relayed, timers *scopeLoop, ms int64) {
	timers.AdvanceAsync(ms)
	timers.barrier()
	r.Transported()
}

// waitBackoff waits until the server's route has a backoff window for key (its link closed: the close is an event of a
// task of the link, which Pi's `transported()` settles and the fake clock does not wait for).
func waitBackoff(t *testing.T, r *relayed, key string) {
	t.Helper()
	h := r.Server.(*chordHost)
	scopeEventually(t, "the route has a backoff for "+key, func() bool {
		waiting := false
		h.loop().Run(func() { waiting = h.destination.routes["session"].backoff.waiting(key) })
		return waiting
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
			wg.Add(1)
			go func() {
				defer wg.Done()
				ref, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
				must(t, err)
				refs[i] = ref
			}()
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

// route.test.ts › resolve failures and re-resolves › a rejected resolve leaves the holds loading, is reported, and
// resolves again after the address's backoff (race 2).
func TestResolveFailuresAndReResolvesARejectedResolveLeavesTheHoldsLoadingIsReportedAndResolvesAgainAfterTheAddressesBackoffRace2(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var failures atomic.Int32
		failures.Store(2)
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(_ contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if failures.Add(-1) >= 0 {
				return resolved{}, errors.New("no worker yet")
			}
			return worker(), nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		resolveFailed := func(want int) {
			t.Helper()
			scopeEventually(t, "the server reported the resolve failures", func() bool { return len(r.Errors.Server.All()) == want })
			errs := r.Errors.Server.All()
			for _, err := range errs {
				if err.Kind != ErrResolveFailed || err.Address != "session|S1" {
					t.Fatalf("error = %+v, want resolve_failed session|S1", err)
				}
			}
		}
		resolveFailed(1)
		routeElapse(r, timers, 999)
		resolveFailed(1)
		routeElapse(r, timers, 1)
		// The second failure doubles the window.
		resolveFailed(2)
		routeElapse(r, timers, 1999)
		wantRouteState(t, notes, RefStateLoading)
		routeElapse(r, timers, 1)
		wantResolves(t, r, "session|S1")
		wantRouteState(t, notes, RefStateLive)
	})
}

// route.test.ts › resolve failures and re-resolves › a returned link that is closed already starts the backoff.
func TestResolveFailuresAndReResolvesAReturnedLinkThatIsClosedAlreadyStartsTheBackoff(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var closed atomic.Bool
		closed.Store(true)
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(address contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if !closed.Load() {
				return worker(), nil
			}
			closed.Store(false)
			a, b := link.MemoryLinkPair()
			b.Close()
			transported(a, b)
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1000)
		wantResolves(t, r, "session|S1")
		wantRouteState(t, notes, RefStateLive)
	})
}

// route.test.ts › resolve failures and re-resolves › a dropped link resolves again while held: references go loading, then
// live on a fresh worker (race 6).
func TestResolveFailuresAndReResolvesADroppedLinkResolvesAgainWhileHeldReferencesGoLoadingThenLiveOnAFreshWorkerRace6(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		r := relay(t, relayedOptions{Loop: timers})
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		first := r.Worker("session|S1")
		r.RouteLinks()[0].Close()
		r.Transported()
		wantRouteState(t, notes, RefStateLoading)
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1000)
		wantResolves(t, r, "session|S1", "session|S1")
		if r.Worker("session|S1") == first {
			t.Fatalf("the worker is the first one")
		}
		wantRouteState(t, notes, RefStateLive)
		addNote(t, r.Worker("session|S1"), "S1", "fresh")
		r.Transported()
		value, err := notes.Member("notes").Value()
		must(t, err)
		nexttest.RequireJSONEqual(t, value, []any{jsonObject("text", "fresh")})
	})
}

// route.test.ts › resolve failures and re-resolves › only a link that completed its handshake resets the backoff.
func TestResolveFailuresAndReResolvesOnlyALinkThatCompletedItsHandshakeResetsTheBackoff(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var silent atomic.Int32
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(address contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if silent.Add(-1) < 0 {
				return worker(), nil
			}
			// A link whose other end never greets and closes.
			a, b := link.MemoryLinkPair()
			timers.AfterFunc(10*time.Millisecond, b.Close)
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		silent.Store(1)
		r.RouteLinks()[0].Close()
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1000)
		// The silent link closed before its handshake: the window doubles.
		routeElapse(r, timers, 10)
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1999)
		wantResolves(t, r, "session|S1")
		routeElapse(r, timers, 1)
		wantResolves(t, r, "session|S1", "session|S1")
		wantRouteState(t, notes, RefStateLive)
		// That link greeted: the next drop starts again at 1 s.
		links := r.RouteLinks()
		links[len(links)-1].Close()
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1000)
		if got := len(r.Resolves()); got != 3 {
			t.Fatalf("resolves = %d, want 3", got)
		}
	})
}

// route.test.ts › resolve failures and re-resolves › a link whose peer never greets closes after 30 s of silence; the
// address backs off, then resolves again while held.
func TestResolveFailuresAndReResolvesALinkWhosePeerNeverGreetsClosesAfter30sOfSilenceTheAddressBacksOffThenResolvesAgainWhileHeld(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		timers := newScopeLoop()
		var silent atomic.Bool
		silent.Store(true)
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(address contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			if !silent.Load() {
				return worker(), nil
			}
			silent.Store(false)
			// Its other end is never read: no hello, no close.
			a, _ := link.MemoryLinkPair()
			return resolved{Link: a, Principal: workerPrincipal(address.Params.Value("id").(string))}, nil
		}})
		notes, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		routeElapse(r, timers, 29_000)
		wantResolves(t, r)
		wantRouteState(t, notes, RefStateLoading)
		// The heartbeat checks every 1.5 s: the silent link closes within it, then its address waits 1 s.
		routeElapse(r, timers, 2_500)
		waitBackoff(t, r, "session|S1")
		routeElapse(r, timers, 1_000)
		wantResolves(t, r, "session|S1")
		r.Transported()
		wantRouteState(t, notes, RefStateLive)
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
		var watched *closeWatched
		r := relay(t, relayedOptions{Loop: timers, Resolve: func(_ contracts.Address, worker func() resolved, _ context.Context) (resolved, error) {
			<-finish
			made := worker()
			watched = &closeWatched{Link: made.Link}
			made.Link = watched
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
		if watched.closed.Load() {
			t.Fatalf("the kept link closed")
		}
		// The kept link serves the next hold without a resolve.
		_, err = r.Tab.Get(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		wantResolves(t, r, "session|S1")
		must(t, r.Server.Close(background))
		if !watched.closed.Load() {
			t.Fatalf("the link is open after host.close")
		}
	})
}

// route.test.ts › host.close during a resolve (race 7) › aborts the resolve's context and closes a link returned afterwards.
func TestHostCloseDuringAResolveRace7AbortsTheResolvesContextAndClosesALinkReturnedAfterwards(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		finish := make(chan struct{})
		var routing atomic.Value
		var watched *closeWatched
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
			routing.Store(ctx)
			<-finish
			made := worker()
			watched = &closeWatched{Link: made.Link}
			made.Link = watched
			return made, nil
		}})
		_, err := r.Tab.Hold(PlacedNotes, routeS("S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
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
		if !watched.closed.Load() {
			t.Fatalf("the link returned after the close stays open")
		}
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
		nexttest.RequireJSONEqual(t, resolvedBy, []any{"the app's"})
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
			routeLinks = nil
			r.Transported()
		}()
		nexttest.ExpectCollected(t, references...)
		nexttest.ExpectCollected(t, workers...)
		nexttest.ExpectCollected(t, links...)
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
		nexttest.ExpectCollected(t, references...)
		if server == nil {
			t.Fatal("the server is gone")
		}
	})
}
