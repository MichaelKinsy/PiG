package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// rigHolder is a hold over a path (Pi's `Upstream`): the route never inspects one.
type rigHolder struct{ id int }

// linkOf is the path's current link, read in a section.
func (r *routeRig) linkOf(path *rigPath) routeLink {
	var current routeLink
	r.within(func() { current = path.Link() })
	return current
}

// attach holds a new holder over path, in a section.
func (r *routeRig) attach(path *rigPath) *rigHolder {
	holder := &rigHolder{}
	r.within(func() { path.attach(holder) })
	return holder
}

// connected waits until the address's resolve was handled and its path has a link.
func (r *routeRig) connected(path *rigPath, attempts int) routeLink {
	r.t.Helper()
	r.waitAttempts(attempts)
	r.settled(path)
	r.loop.barrier()
	current := r.linkOf(path)
	if current == nil {
		r.t.Fatal("the path has no link after its resolve")
	}
	return current
}

// failedLink waits until the address's resolve was handled.
func (r *routeRig) handled(path *rigPath, attempts int) {
	r.t.Helper()
	r.waitAttempts(attempts)
	r.settled(path)
	r.loop.barrier()
}

func (r *routeRig) pathAs(id string, principal data.Principal) (routePath, error) {
	var made routePath
	var err error
	r.within(func() { made, err = r.route.path(delta.JsonObjectOf("id", id), principal, chordctx.Background()) })
	return made, err
}

func (r *routeRig) closedLink(made routeLink) (known bool) {
	r.within(func() { known = r.route.closed(made) })
	return known
}

func (r *routeRig) opened(made routeLink) { r.within(func() { r.route.opened(made) }) }

func (r *routeRig) keysAttempted() []string { return r.attemptKeys() }

func routeWantKeys(t testing.TB, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") || len(got) != len(want) {
		t.Fatalf("resolves = %v, want %v", got, want)
	}
}

func TestRouteAdmissionRunsOneResolvePerAddressSharedByTheHoldsThatWaitForItRace1(t *testing.T) {
	rig := newRouteRig(t)
	one, two := rig.path("S1"), rig.path("S1")
	if one != two {
		t.Fatal("the route made two paths for one address")
	}
	rig.within(func() {
		one.attach(&rigHolder{id: 1})
		two.attach(&rigHolder{id: 2})
	})
	made := rig.connected(one, 1)
	routeWantKeys(t, rig.keysAttempted(), "session|S1")
	rig.within(func() {
		if len(one.attached) != 2 || one.attached[0] != made || one.attached[1] != made {
			t.Fatalf("holders attached on %v, want both on the link", one.attached)
		}
	})
	// Another address resolves of its own.
	other := rig.path("S2")
	rig.attach(other)
	rig.connected(other, 2)
	routeWantKeys(t, rig.keysAttempted(), "session|S1", "session|S2")
}

func TestRouteAdmissionAuthorizesEveryHoldWithItsPrincipalARefusalIsBrokenForbiddenOtherPrincipalsStillResolveRace4(t *testing.T) {
	rig := newRouteRig(t)
	rig.authorize = func(principal data.Principal, address contracts.Address) (bool, error) {
		kind := principal.Value("kind")
		return kind == "owner" || (kind == "guest" && address.Key == "session|"+principal.Value("session").(string)), nil
	}
	guest := delta.JsonObjectOf("kind", "guest", "session", "S1")
	_, err := rig.pathAs("S2", guest)
	wantScopeError(t, err, data.CodeForbidden, "", "session|S2: not authorized")
	routeWantKeys(t, rig.keysAttempted())
	allowed, err := rig.pathAs("S1", guest)
	if err != nil {
		t.Fatal(err)
	}
	rig.within(func() { allowed.(*rigPath).attach(&rigHolder{}) })
	rig.connected(allowed.(*rigPath), 1)
	owner, err := rig.pathAs("S2", delta.JsonObjectOf("kind", "owner"))
	if err != nil {
		t.Fatal(err)
	}
	rig.within(func() { owner.(*rigPath).attach(&rigHolder{}) })
	rig.connected(owner.(*rigPath), 2)
}

func TestRouteAdmissionFailsAHoldBrokenWithWhatAuthorizeThrows(t *testing.T) {
	rig := newRouteRig(t)
	threw := errors.New("authorize threw")
	rig.authorize = func(data.Principal, contracts.Address) (bool, error) { return false, threw }
	if _, err := rig.pathAs("S1", delta.JsonObjectOf("kind", "owner")); err != threw {
		t.Fatalf("err = %v, want what authorize returned", err)
	}
	routeWantKeys(t, rig.keysAttempted())
}

func TestRouteAdmissionFailsAHoldBrokenInvalidHostAddressForAnAddressWhoseParamsDoNotFitTheKind(t *testing.T) {
	rig := newRouteRig(t)
	var err error
	// An address of the right kind with params of the wrong type, as a buggy `hostAddress` could compute.
	rig.within(func() {
		_, err = rig.route.path(delta.JsonObjectOf("id", 1.0), delta.JsonObjectOf("kind", "owner"), chordctx.Background())
	})
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonHostAddress {
		t.Fatalf("err = %v, want invalid/host_address", err)
	}
	routeWantKeys(t, rig.keysAttempted())
}

// Mirrors the doc comment of `path`: a hold without a principal is refused `forbidden` before `authorize` runs.
func TestRouteAdmissionRefusesAHoldWithoutAPrincipalWithoutAskingAuthorize(t *testing.T) {
	rig := newRouteRig(t)
	asked := false
	rig.authorize = func(data.Principal, contracts.Address) (bool, error) {
		asked = true
		return true, nil
	}
	_, err := rig.pathAs("S1", nil)
	wantScopeError(t, err, data.CodeForbidden, "", "session|S1: not authorized")
	if asked {
		t.Fatal("authorize ran for a hold without a principal")
	}
}

func TestResolveFailuresAndReResolvesARejectedResolveLeavesTheHoldsLoadingIsReportedAndResolvesAgainAfterTheAddressesBackoffRace2(t *testing.T) {
	rig := newRouteRig(t)
	failures := 2
	rig.resolve = func(address contracts.Address, ctx context.Context) (ResolvedLink, error) {
		if failures > 0 {
			failures--
			return ResolvedLink{}, errors.New("no worker yet")
		}
		return rig.worker(address, ctx)
	}
	path := rig.path("S1")
	rig.attach(path)
	rig.handled(path, 1)
	if rig.linkOf(path) != nil {
		t.Fatal("the path has a link after a failed resolve")
	}
	reports := rig.host.reported()
	if len(reports) != 1 || reports[0].Kind != ErrResolveFailed || reports[0].Address != "session|S1" {
		t.Fatalf("reports = %+v", reports)
	}
	rig.elapse(999)
	if got := len(rig.host.reported()); got != 1 {
		t.Fatalf("reports after 999 ms = %d, want 1", got)
	}
	rig.elapse(1)
	rig.handled(path, 2)
	// The second failure doubles the window.
	if got := len(rig.host.reported()); got != 2 {
		t.Fatalf("reports after the second failure = %d, want 2", got)
	}
	rig.elapse(1999)
	if rig.linkOf(path) != nil || len(rig.keysAttempted()) != 2 {
		t.Fatalf("resolved before the window ended: %v", rig.keysAttempted())
	}
	rig.elapse(1)
	rig.connected(path, 3)
}

func TestResolveFailuresAndReResolvesAReturnedLinkThatIsClosedAlreadyStartsTheBackoff(t *testing.T) {
	rig := newRouteRig(t)
	closed := true
	rig.resolve = func(address contracts.Address, ctx context.Context) (ResolvedLink, error) {
		if !closed {
			return rig.worker(address, ctx)
		}
		closed = false
		dead := &rigLink{}
		dead.Close()
		return ResolvedLink{Link: dead, Principal: delta.JsonObjectOf("kind", "worker")}, nil
	}
	path := rig.path("S1")
	rig.attach(path)
	rig.handled(path, 1)
	if rig.linkOf(path) != nil {
		t.Fatal("a closed link was connected")
	}
	rig.elapse(1000)
	rig.connected(path, 2)
}

func TestResolveFailuresAndReResolvesADroppedLinkResolvesAgainWhileHeldReferencesGoLoadingThenLiveOnAFreshWorkerRace6(t *testing.T) {
	rig := newRouteRig(t)
	path := rig.path("S1")
	rig.attach(path)
	first := rig.connected(path, 1)
	if !rig.closedLink(first) {
		t.Fatal("the route did not know its own link")
	}
	rig.within(func() {
		if path.dropped != 1 || path.link != nil {
			t.Fatalf("after the drop: dropped %d holders, link %v", path.dropped, path.link)
		}
	})
	rig.elapse(1000)
	second := rig.connected(path, 2)
	if second == first {
		t.Fatal("the path got its old link again")
	}
	routeWantKeys(t, rig.keysAttempted(), "session|S1", "session|S1")
	if rig.closedLink(first) {
		t.Fatal("a link that is closed already is closed again")
	}
}

func TestResolveFailuresAndReResolvesOnlyALinkThatCompletedItsHandshakeResetsTheBackoff(t *testing.T) {
	rig := newRouteRig(t)
	path := rig.path("S1")
	rig.attach(path)
	first := rig.connected(path, 1)
	// The peer's handshake completed.
	rig.opened(first)
	rig.closedLink(first)
	rig.elapse(1000)
	silent := rig.connected(path, 2)
	// The silent link closed before its handshake: the window doubles.
	rig.elapse(10)
	rig.closedLink(silent)
	rig.elapse(1999)
	if len(rig.keysAttempted()) != 2 {
		t.Fatalf("resolved before the doubled window ended: %v", rig.keysAttempted())
	}
	rig.elapse(1)
	third := rig.connected(path, 3)
	// That link greeted: the next drop starts again at 1 s.
	rig.opened(third)
	rig.closedLink(third)
	rig.elapse(1000)
	rig.connected(path, 4)
}

func TestResolveFailuresAndReResolvesAnAddressWithoutHoldsIsNotResolvedAgainAfterItsLinkDrops(t *testing.T) {
	rig := newRouteRig(t)
	path := rig.path("S1")
	holder := rig.attach(path)
	made := rig.connected(path, 1)
	rig.within(func() { path.detach(holder) })
	rig.closedLink(made)
	rig.elapse(60_000)
	routeWantKeys(t, rig.keysAttempted(), "session|S1")
	rig.within(func() {
		if len(rig.route.addresses) != 0 || len(rig.route.keys) != 0 {
			t.Fatalf("the route keeps the address %v", rig.route.keys)
		}
	})
	if got := rig.loop.TimerCount(); got != 0 {
		t.Fatalf("timers left = %d, want none", got)
	}
}

func TestResolveFailuresAndReResolvesAResolveWhoseHoldsWereAllReleasedMeanwhileFinishesAndItsLinkIsKeptRace3(t *testing.T) {
	rig := newRouteRig(t)
	gate := make(chan struct{})
	rig.resolve = func(address contracts.Address, ctx context.Context) (ResolvedLink, error) {
		<-gate
		return rig.worker(address, ctx)
	}
	path := rig.path("S1")
	holder := rig.attach(path)
	rig.waitAttempts(1)
	rig.within(func() { path.detach(holder) })
	close(gate)
	made := rig.connected(path, 1)
	rig.elapse(60_000)
	if made.(*rigHostLink).raw.isClosed() {
		t.Fatal("the kept link was closed")
	}
	// The kept link serves the next hold without a resolve.
	rig.attach(path)
	rig.within(func() {
		if path.attached[len(path.attached)-1] != made {
			t.Fatalf("the next hold is not held on the kept link: %v", path.attached)
		}
	})
	routeWantKeys(t, rig.keysAttempted(), "session|S1")
}

func TestHostCloseDuringAResolveRace7AbortsTheResolvesContextAndClosesALinkReturnedAfterwards(t *testing.T) {
	rig := newRouteRig(t)
	gate := make(chan struct{})
	seen := make(chan context.Context, 1)
	raw := &rigLink{}
	rig.resolve = func(_ contracts.Address, ctx context.Context) (ResolvedLink, error) {
		seen <- ctx
		<-gate
		return ResolvedLink{Link: raw, Principal: delta.JsonObjectOf("kind", "worker")}, nil
	}
	path := rig.path("S1")
	rig.attach(path)
	routing := <-seen
	if routing.Err() != nil {
		t.Fatal("the resolve's context was aborted before the host closed")
	}
	// The host closes: it aborts the route's context, marks itself closing and closes the route.
	rig.cancel(scopeReason("host closed"))
	rig.host.setClosing()
	var holders []routedHolder
	rig.within(func() { holders = rig.route.close() })
	if routing.Err() == nil {
		t.Fatal("the resolve's context was not aborted at the host's close")
	}
	if len(holders) != 1 {
		t.Fatalf("close returned %d holders, want the one hold", len(holders))
	}
	close(gate)
	scopeEventually(t, "the link returned after the close closed", raw.isClosed)
	rig.settled(path)
	if len(rig.host.links()) != 0 || len(rig.host.reported()) != 0 {
		t.Fatalf("the route made a host link %d or reported %v after the host closed", len(rig.host.links()), rig.host.reported())
	}
}

// Mirrors the doc comment of `resolve`: it gets the route's own context, the options of what it returned reach the host's
// routeLink, and a peer that greeted first has its backoff cleared at once.
func TestRouteResolvePassesTheRoutesContextAndTheResolvedLinksOptionsAndClearsTheBackoffOfAGreetedPeer(t *testing.T) {
	rig := newRouteRig(t)
	seen := make(chan context.Context, 1)
	limits := &link.PartLimits{}
	rig.host.greeted = true
	rig.resolve = func(address contracts.Address, ctx context.Context) (ResolvedLink, error) {
		seen <- ctx
		return ResolvedLink{Link: &rigLink{}, Principal: delta.JsonObjectOf("kind", "worker"), Trusted: true, Limits: limits}, nil
	}
	path := rig.path("S1")
	rig.attach(path)
	made := rig.connected(path, 1)
	if got := <-seen; got != rig.context {
		t.Fatal("resolve did not get the route's context")
	}
	options := rig.host.options[0]
	if !options.trusted || options.limits != limits || options.principal.Value("kind") != "worker" {
		t.Fatalf("routeLink options = %+v", options)
	}
	// Greeted at once: its close is a first failure again (1 s), not a doubled one.
	rig.closedLink(made)
	rig.elapse(1000)
	rig.connected(path, 2)
}

// Mirrors the doc comments of `holders` and `close`: every holder of every path in address order, linked or not, and a
// close forgets the backoff windows.
func TestRouteCloseReturnsEveryHolderOfEveryPathAndForgetsTheBackoff(t *testing.T) {
	rig := newRouteRig(t)
	rig.resolve = func(contracts.Address, context.Context) (ResolvedLink, error) {
		return ResolvedLink{}, errors.New("no worker")
	}
	one, two := rig.path("S1"), rig.path("S2")
	h1 := rig.attach(one)
	h2 := rig.attach(two)
	h3 := rig.attach(one)
	rig.handled(one, 1)
	rig.handled(two, 2)
	if got := rig.loop.TimerCount(); got != 2 {
		t.Fatalf("backoff windows = %d, want one per address", got)
	}
	var holders []routedHolder
	rig.within(func() { holders = rig.route.close() })
	if len(holders) != 3 || holders[0] != routedHolder(h1) || holders[1] != routedHolder(h3) || holders[2] != routedHolder(h2) {
		t.Fatalf("close returned %v, want S1's holders then S2's", holders)
	}
	if got := rig.loop.TimerCount(); got != 0 {
		t.Fatalf("timers left after close = %d", got)
	}
}

func TestNewRouteRefusesAKindNotDeclaredByHostKind(t *testing.T) {
	loop := newScopeLoop()
	_, err := newRoute(&contracts.HostKind{Kind: "session"}, RouteOptions{}, &rigHost{loopOf: loop}, chordctx.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "session: not a declared host kind") {
		t.Fatalf("err = %v", err)
	}
}
