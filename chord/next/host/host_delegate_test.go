package host

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest/contractstest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Own tests of Host.Delegate (D603; no Pi counterpart: Pi's server serves its plugins in its own host, the Go kit's plugins run in a
// Node subprocess behind the server host). A host with a delegate serves the union of its types and the delegate's: remote types
// placed on it that it does not serve itself go over the delegate, as a route's holds do; what it serves stays, whatever the state of
// the object; nothing changes for a host without one.

// delegateType is `remote(name, { params: {}, hostAddress: () => Server(), variants: { v: { state: { n: int() } } } })`.
func delegateType(t testing.TB, name string) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: contractstest.ServerAddress,
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// delegateServes serves the remote type contract with its variant v, member n constant n.
func delegateServes(contract *contracts.Contract, n float64) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(n)}}}, nil
		}, ServeOptions{})
	})
}

// delegateRig is a server host with a delegate, the delegate host (both at the address `server`) and a tab connected to the server.
type delegateRig struct {
	server, delegate, tab Host
	resolves              atomic.Int32
	// allow is whether the delegate's Resolve gives its link (the delegate "up").
	allow atomic.Bool
	A, B  *contracts.Contract
	C     *contracts.Contract
}

func newDelegateRig(t *testing.T) *delegateRig { return newDelegateRigOf(t, true) }

// newDelegateRigOf is the rig with a delegate that has the address `server` (the plugins' host) or none (a relay).
func newDelegateRigOf(t *testing.T, addressed bool) *delegateRig {
	t.Helper()
	r := &delegateRig{A: delegateType(t, "app.a"), B: delegateType(t, "app.b"), C: delegateType(t, "app.c")}
	r.allow.Store(true)
	r.server = newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	delegateOptions := HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}}
	if addressed {
		delegateOptions.Address = new(Server.Address(nil))
	}
	r.delegate = newHost(t, delegateOptions)
	r.tab = newHost(t, HostOptions{OnError: func(HostError) {}})
	mustRoute(t)(r.server.Delegate(Server, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(_ contracts.Address, ctx context.Context) (ResolvedLink, error) {
			r.resolves.Add(1)
			if !r.allow.Load() {
				return ResolvedLink{}, context.Canceled
			}
			a, b := link.MemoryLinkPair()
			if err := r.delegate.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background); err != nil {
				return ResolvedLink{}, err
			}
			return ResolvedLink{Link: a, Principal: serverPrincipal(), Trusted: true, Codec: testCodec()}, nil
		},
	}, background))
	mustLoad(t)(r.server.Load([]HostFacet{{Name: "@test/a", Facet: delegateServes(r.A, 1.0)}}, background))
	if addressed {
		mustLoad(t)(r.delegate.Load([]HostFacet{{Name: "@test/b", Facet: delegateServes(r.B, 2.0)}}, background))
	} else {
		mustLoad(t)(r.delegate.Load(nil, background))
	}
	mustLoad(t)(r.tab.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, r.server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, r.tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	return r
}

func (r *delegateRig) hold(t *testing.T, host Host, typ *contracts.Contract) *Reference {
	t.Helper()
	ref, err := host.Hold(background, typ, delta.NewJsonObject(0), GetOptions{})
	must(t, err)
	t.Cleanup(ref.Release)
	return ref
}

func wantMember(t *testing.T, ref *Reference, want float64) {
	t.Helper()
	scopeEventually(t, "the member n is bound", func() bool {
		value, err := ref.Member("n").Value()
		return err == nil && value == want
	})
}

func TestDelegateServesTheUnionOfTheHostsTypesAndTheDelegatesAHitGoesOverTheDelegateAMissStaysHere(t *testing.T) {
	r := newDelegateRig(t)
	// A tab's holds: the type the server serves stays on it, the delegate's goes over the delegate, one neither serves waits
	// `unavailable` (not served here), as on Pi's one host.
	a, b, c := r.hold(t, r.tab, r.A), r.hold(t, r.tab, r.B), r.hold(t, r.tab, r.C)
	wantMember(t, a, 1)
	wantMember(t, b, 2)
	wantRouteWaiting(t, c, "app.c is not served here")
	if got := r.resolves.Load(); got != 1 {
		t.Fatalf("resolves = %d, want 1 (the types the server serves never reach the delegate)", got)
	}
	// A reference made on the server itself goes the same ways.
	wantMember(t, r.hold(t, r.server, r.A), 1)
	wantMember(t, r.hold(t, r.server, r.B), 2)
	// A call reaches the delegate's object and the server's own.
	got, err := r.server.Get(background, r.B, delta.NewJsonObject(0), GetOptions{})
	must(t, err)
	t.Cleanup(got.Release)
	if value, err := got.Member("n").Value(); err != nil || value != 2.0 {
		t.Fatalf("n = %v, %v", value, err)
	}
}

func TestDelegateWhatTheDelegatesOwnLinkAsksForAndNothingServesIsNotSentBackToIt(t *testing.T) {
	// A delegate without an address (a relay: its holds for the server's types go to its parent, the server). It holds a type
	// neither host serves: it arrives on the delegate's own link, so the server answers `unavailable` itself instead of
	// delegating the hold back (a loop between the two hosts).
	r := newDelegateRigOf(t, false)
	// The delegate's link is made by the first hold that goes over it (the relay serves no `app.b`).
	r.hold(t, r.tab, r.B)
	scopeEventually(t, "the delegate resolved", func() bool { return r.resolves.Load() == 1 })
	c := r.hold(t, r.delegate, r.C)
	wantRouteWaiting(t, c, "app.c is not served here")
	// One the server serves reaches it from the delegate.
	wantMember(t, r.hold(t, r.delegate, r.A), 1)
}

func TestDelegateWhenItIsDownItsTypesWaitAndTheHostsOwnAreUntouched(t *testing.T) {
	r := newDelegateRig(t)
	a, b := r.hold(t, r.tab, r.A), r.hold(t, r.tab, r.B)
	wantMember(t, a, 1)
	wantMember(t, b, 2)
	// The delegate goes away and cannot be resolved again: its holders wait (`failed { waiting }`), the server's own types stay live.
	r.allow.Store(false)
	must(t, r.delegate.Close(background))
	scopeEventually(t, "the delegate's holder left live", func() bool { return routeState(b).Kind != RefStateLive })
	if got := routeState(a).Kind; got != RefStateLive {
		t.Fatalf("the server's own type is %s, want live", got)
	}
	if value, err := a.Member("n").Value(); err != nil || value != 1.0 {
		t.Fatalf("n = %v, %v", value, err)
	}
	if got := routeState(b); got.Kind == RefStateEnded {
		t.Fatalf("the delegate's holder ended: %+v", got)
	}
}

func TestDelegateHoldsKeepTheirOrderAcrossTheDelegatesRestartAndTheServersTypesNeverMove(t *testing.T) {
	r := newDelegateRig(t)
	a, b := r.hold(t, r.tab, r.A), r.hold(t, r.tab, r.B)
	wantMember(t, a, 1)
	wantMember(t, b, 2)
	// The delegate reloads its type (a restart: the object ends and a new one serves the new code); a hold made while it
	// restarts and one made after bind to the new code, none ever failed, and the server's type was not touched.
	var mu sync.Mutex
	var states []RefStateKind
	watch, err := b.State().Watch(background)
	must(t, err)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, call.Value.Kind)
		return nil
	})
	report, err := r.delegate.Reload([]HostFacet{{Name: "@test/b", Facet: delegateServes(r.B, 3.0)}}, background, ReloadOptions{})
	must(t, err)
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	during := r.hold(t, r.tab, r.B)
	wantMember(t, during, 3)
	wantMember(t, b, 3)
	wantMember(t, r.hold(t, r.tab, r.B), 3)
	mu.Lock()
	defer mu.Unlock()
	for _, state := range states {
		if state == RefStateFailed || state == RefStateEnded {
			t.Fatalf("the holder's states = %v, want no failure", states)
		}
	}
	if got := routeState(a).Kind; got != RefStateLive {
		t.Fatalf("the server's own type is %s, want live", got)
	}
	if got := r.resolves.Load(); got != 1 {
		t.Fatalf("resolves = %d, want 1: a restart of the delegate's objects keeps its link", got)
	}
}

func TestDelegateIsRefusedForAKindThatIsNotTheHostsAddressAfterLoadAndTwice(t *testing.T) {
	routeOptions := RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
			return ResolvedLink{}, context.Canceled
		},
	}
	withAddress := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	if _, err := withAddress.Delegate(Session, routeOptions, background); err == nil || err.Error() != "delegate: session is not this host's address kind" {
		t.Fatalf("Delegate(session) = %v", err)
	}
	if _, err := withAddress.Delegate(Server, routeOptions, background); err != nil {
		t.Fatal(err)
	}
	if _, err := withAddress.Delegate(Server, routeOptions, background); err == nil || err.Error() != "delegate: server is routed already" {
		t.Fatalf("Delegate twice = %v", err)
	}
	mustLoad(t)(withAddress.Load(nil, background))
	loaded := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	mustLoad(t)(loaded.Load(nil, background))
	if _, err := loaded.Delegate(Server, routeOptions, background); err == nil || err.Error() != "delegate: routes are declared before load" {
		t.Fatalf("Delegate after load = %v", err)
	}
	// A host without an address has no kind to delegate.
	anonymous := newHost(t, HostOptions{OnError: func(HostError) {}})
	if _, err := anonymous.Delegate(Server, routeOptions, background); err == nil || err.Error() != "delegate: server is not this host's address kind" {
		t.Fatalf("Delegate without an address = %v", err)
	}
}

func TestDestinationDelegatesOnlyTheRemoteTypesTheHostDoesNotServe(t *testing.T) {
	served := map[string]bool{"app.a": true}
	d := newDestination(new(Server.Address(nil)), map[string]*route{}, newPath(func(pathEvent) {}), func(name string) bool { return served[name] })
	if d.delegates("app.b", nil) {
		t.Fatal("a host without a delegate delegates")
	}
	d.delegating = true
	if d.delegates("app.a", nil) || !d.delegates("app.b", nil) {
		t.Fatal("the delegate takes what the host serves, or not what it does not")
	}
}
