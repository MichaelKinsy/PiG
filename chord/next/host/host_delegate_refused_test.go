package host

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// D605 claim 5: the app kit's delegate lets every principal through (`Authorize` is always true) and trusts its link (`Trusted: true`).
// Nothing in the host's own gate then stands between a hold and the delegate but what the host's tables say. These cases pin what is
// still refused with exactly those options: a type nobody serves, a kind that is not the host's address kind, and a second delegate; and
// that a refused `Delegate` changes nothing about the delegate the host does have.
func TestDelegateWithAuthorizeAlwaysTrueAndATrustedLinkStillRefusesWhatNobodyServesAKindThatIsNotTheAddressKindAndASecondDelegate(t *testing.T) {
	var first, second atomic.Int32
	delegate := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	b := delegateType(t, "app.b")
	c := delegateType(t, "app.c")
	options := func(resolves *atomic.Int32) RouteOptions {
		return RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(_ contracts.Address, ctx context.Context) (ResolvedLink, error) {
				resolves.Add(1)
				a, other := link.MemoryLinkPair()
				if err := delegate.Connect(other, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background); err != nil {
					return ResolvedLink{}, err
				}
				return ResolvedLink{Link: a, Principal: serverPrincipal(), Trusted: true, Codec: testCodec()}, nil
			},
		}
	}
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	mustRoute(t)(server.Delegate(Server, options(&first), background))
	if _, err := server.Delegate(Session, options(&second), background); err == nil || err.Error() != "delegate: session is not this host's address kind" {
		t.Fatalf("Delegate(session) = %v", err)
	}
	if _, err := server.Delegate(Server, options(&second), background); err == nil || err.Error() != "delegate: server is routed already" {
		t.Fatalf("a second Delegate(server) = %v", err)
	}
	mustLoad(t)(server.Load(nil, background))
	mustLoad(t)(delegate.Load([]HostFacet{{Name: "@test/b", Facet: delegateServes(b, 2.0)}}, background))
	tab := newHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(tab.Load(nil, background))
	a, other := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, tab.Connect(other, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))

	hold := func(host Host, typ *contracts.Contract) *Reference {
		ref, err := host.Hold(background, typ, delta.NewJsonObject(0), GetOptions{})
		must(t, err)
		t.Cleanup(ref.Release)
		return ref
	}
	// The one delegate is the first: what it serves arrives, and a type nobody serves is unavailable from the tab and from the host
	// itself, whatever `Authorize` lets through.
	wantMember(t, hold(tab, b), 2)
	wantRouteWaiting(t, hold(tab, c), "app.c is not served here")
	wantRouteWaiting(t, hold(server, c), "app.c is not served here")
	if first.Load() != 1 || second.Load() != 0 {
		t.Fatalf("resolves: first %d, second %d, want 1 and 0 (the refused Delegates added no route)", first.Load(), second.Load())
	}
}
