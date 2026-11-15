package host

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/remote/path.ts

// Ports packages/chord/test/remote/path.test.ts.

// recordingHolder records where it was held and what it was told, as `name@link` and `name dropped`: the path calls only
// attach and dropped (Pi's test fakes the holder the same way); a gated path also tells it `withheld` and `admitted`.
type recordingHolder struct {
	name  string
	log   *[]string
	names map[*hostLink]string
}

func (*recordingHolder) kind() string                  { return "recording" }
func (*recordingHolder) span() telemetry.TelemetrySpan { return nil }
func (*recordingHolder) principal() data.Principal     { return nil }
func (h *recordingHolder) attach(l *hostLink) {
	*h.log = append(*h.log, h.name+"@"+h.names[l])
}
func (h *recordingHolder) dropped(*data.ChordError) { *h.log = append(*h.log, h.name+" dropped") }
func (h *recordingHolder) unreachable(err *data.ChordError) {
	*h.log = append(*h.log, h.name+" unreachable: "+err.Message)
}
func (h *recordingHolder) withheld(cause data.FailureCause) {
	*h.log = append(*h.log, h.name+" "+string(cause.Kind))
}
func (h *recordingHolder) admitted() { *h.log = append(*h.log, h.name+" admitted") }

// pathRig names the links its holders are held on: the path only passes a link on.
type pathRig struct {
	log   []string
	names map[*hostLink]string
}

func newPathRig() *pathRig { return &pathRig{names: map[*hostLink]string{}} }

func (r *pathRig) holder(name string) *recordingHolder {
	return &recordingHolder{name: name, log: &r.log, names: r.names}
}

func (r *pathRig) linkOf(name string) *hostLink {
	l := &hostLink{}
	r.names[l] = name
	return l
}

func expectLog(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// path.test.ts: a path › asks for a link when it gets a holder without one; holds every holder on each link it gets
func TestPathAsksForALinkAndHoldsEveryHolderOnEachLink(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func(pathEvent) { changes++ }, nil, nil)
	p.attach(rig.holder("a"))
	p.attach(rig.holder("b"))
	if changes != 2 {
		t.Fatalf("changes = %d, want 2", changes)
	}
	expectLog(t, rig.log)
	p.connected(rig.linkOf("1"))
	expectLog(t, rig.log, "a@1", "b@1")
	p.attach(rig.holder("c"))
	if last := rig.log[len(rig.log)-1]; last != "c@1" {
		t.Fatalf("last = %q, want c@1", last)
	}
	if changes != 2 {
		t.Fatalf("changes = %d, want 2", changes)
	}
}

// path.test.ts: a path › tells every holder when its link drops, then asks for the next; a stale link's drop is ignored
func TestPathTellsHoldersWhenItsLinkDrops(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func(pathEvent) { changes++ }, nil, nil)
	first, second := rig.linkOf("1"), rig.linkOf("2")
	p.connected(first)
	p.attach(rig.holder("a"))
	p.disconnected(first)
	expectLog(t, rig.log, "a@1", "a dropped")
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.link() != nil {
		t.Fatal("link() is set after its drop")
	}
	p.connected(second)
	p.disconnected(first)
	expectLog(t, rig.log, "a@1", "a dropped", "a@2")
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.link() != second {
		t.Fatal("link() is not the second link")
	}
}

// path.test.ts: a path › reports its last holder's detach: no later link holds it
func TestPathReportsItsLastHoldersDetach(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func(pathEvent) { changes++ }, nil, nil)
	a, b := rig.holder("a"), rig.holder("b")
	p.connected(rig.linkOf("1"))
	p.attach(a)
	p.attach(b)
	p.detach(a)
	if changes != 0 {
		t.Fatalf("changes = %d, want 0", changes)
	}
	p.detach(b)
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.held() {
		t.Fatal("held() after the last detach")
	}
	p.connected(rig.linkOf("2"))
	expectLog(t, rig.log, "a@1", "b@1")
}

// path.test.ts: a path › says what changed: a holder attached without a link, a drop, the last release; fails its holders waiting
func TestPathSaysWhatChangedAHolderAttachedWithoutALinkADropTheLastReleaseFailsItsHoldersWaiting(t *testing.T) {
	rig := newPathRig()
	var events []pathEventKind
	p := newPath(func(event pathEvent) { events = append(events, event.kind) }, nil, nil)
	a := rig.holder("a")
	p.attach(a)
	p.unreachable(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "no worker"}, nil))
	first := rig.linkOf("1")
	p.connected(first)
	p.disconnected(first)
	p.detach(a)
	if !slices.Equal(events, []pathEventKind{pathAttached, pathDropped, pathReleased}) {
		t.Fatalf("events = %v, want attached, dropped, released", events)
	}
	expectLog(t, rig.log, "a unreachable: no worker", "a@1", "a dropped")
}

// path.test.ts: a path › counts a join to a held holder as a request without a link (a failed route resolves again),
// none with one (Pi main 47e9d74f9)
func TestPathCountsAJoinToAHeldHolderAsARequestWithoutALinkNoneWithOne(t *testing.T) {
	rig := newPathRig()
	var events []pathEventKind
	p := newPath(func(event pathEvent) { events = append(events, event.kind) }, nil, nil)
	a := rig.holder("a")
	p.attach(a)
	p.requested(a)
	if !slices.Equal(events, []pathEventKind{pathAttached, pathAttached}) {
		t.Fatalf("events = %v, want attached, attached", events)
	}
	p.connected(rig.linkOf("1"))
	p.requested(a)
	if !slices.Equal(events, []pathEventKind{pathAttached, pathAttached}) {
		t.Fatalf("events with a link = %v, want attached, attached", events)
	}
}

// path.test.ts: a path › tells whether its route suspended it; a path without a route never is (Pi main 47e9d74f9)
func TestPathTellsWhetherItsRouteSuspendedItAPathWithoutARouteNeverIs(t *testing.T) {
	suspended := false
	if newPath(func(pathEvent) {}, nil, nil).suspended() {
		t.Fatal("a path without a route is suspended")
	}
	routed := newPath(func(pathEvent) {}, func() bool { return suspended }, nil)
	if routed.suspended() {
		t.Fatal("suspended() before its route suspended it")
	}
	suspended = true
	if !routed.suspended() {
		t.Fatal("suspended() is false after its route suspended it")
	}
}

// gatedRig is a gated path over real links: Pi's test fakes a link with a `fence` the test runs by hand; here the link is
// a host's own, its fence is the `ping` its raw peer receives, and a `pong` the peer sends runs it. Everything touching the
// path runs inside a section of the host (a fence runs in one, on the link's delivery goroutine).
type gatedRig struct {
	*pathRig
	t        *testing.T
	host     *chordHost
	peers    map[*hostLink]*RawPeer
	admitted map[string]bool
	events   []string
	path     *path
}

// gatedPathRig makes a rig whose gate admits the holders named in admitted.
func gatedPathRig(t *testing.T, admitted ...string) *gatedRig {
	t.Helper()
	g := &gatedRig{pathRig: newPathRig(), t: t, peers: map[*hostLink]*RawPeer{}, admitted: map[string]bool{}}
	g.host = newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
	for _, name := range admitted {
		g.admitted[name] = true
	}
	g.path = newPath(func(event pathEvent) {
		g.events = append(g.events, [...]string{pathAttached: "attached", pathDropped: "dropped", pathReleased: "released"}[event.kind])
	}, nil, g)
	return g
}

// in runs f in a section of the host.
func (g *gatedRig) in(f func()) {
	g.t.Helper()
	g.host.loop().Run(f)
}

// joined, left, unlinked are the gate: a holder joined is forwarded at once when its name is admitted; a holder that left
// is logged; the path losing its link is an event.
func (g *gatedRig) joined(holder upstream) bool { return g.admitted[holder.(*recordingHolder).name] }
func (g *gatedRig) left(holder upstream) {
	g.log = append(g.log, holder.(*recordingHolder).name+" left")
}
func (g *gatedRig) unlinked() { g.events = append(g.events, "unlinked") }

// newLink is a link of the host with a raw peer behind it, named for the holders' log.
func (g *gatedRig) newLink(name string) *hostLink {
	g.t.Helper()
	a, b := link.MemoryLinkPair()
	must(g.t, g.host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(g.t, b, rawPeerOptions{})
	peer.Next("hello")
	var made *hostLink
	g.in(func() {
		for _, l := range g.host.links {
			if _, known := g.names[l]; !known {
				made = l
			}
		}
		g.names[made] = name
	})
	g.peers[made] = peer
	return made
}

// pings are the pings the peer of l received so far and not answered (each is a fence the path made on it).
func (g *gatedRig) pings(l *hostLink) int {
	g.t.Helper()
	rhSync(g.t, g.peers[l])
	return countMessages[*link.PingMessage](g.peers[l])
}

// pong answers every ping the peer received so far, then waits for the host to have handled the answers.
func (g *gatedRig) pong(l *hostLink) {
	g.t.Helper()
	peer := g.peers[l]
	for range g.pings(l) {
		peer.Next("ping")
		peer.Send(&link.PongMessage{})
	}
	rhSync(g.t, peer)
}

// snapshot is the log and the events so far.
func (g *gatedRig) snapshot() (log, events []string) {
	g.in(func() { log, events = slices.Clone(g.log), slices.Clone(g.events) })
	return log, events
}

func expectInts(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", what, got, want)
	}
}

var deniedCause = data.FailureCause{Kind: data.FailureDenied, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: "no"}, nil)}

// path.test.ts: a gated path (slice 14 §2.3) › holds only what its gate admits on its links, and wants a link only for those
func TestGatedPathHoldsOnlyWhatItsGateAdmitsOnItsLinksAndWantsALinkOnlyForThose(t *testing.T) {
	g := gatedPathRig(t, "a")
	a, b := g.holder("a"), g.holder("b")
	first := g.newLink("1")
	g.in(func() {
		g.path.attach(b)
		// Withheld: no link is asked for it.
		if len(g.events) != 0 || !g.path.held() || g.path.wanted() {
			t.Errorf("events %v held %v wanted %v, want none, true, false", g.events, g.path.held(), g.path.wanted())
		}
		g.path.attach(a)
		if !slices.Equal(g.events, []string{"attached"}) || !g.path.wanted() {
			t.Errorf("events %v wanted %v, want [attached], true", g.events, g.path.wanted())
		}
		g.path.connected(first)
		expectLog(t, g.log, "a@1")
		g.path.forward(b)
		expectLog(t, g.log, "a@1", "b admitted", "b@1")
		g.path.disconnected(g.path.link())
		// Its gate is told after the holders, before the route: it may take back what it cannot forward on a new link.
		if !slices.Equal(g.events, []string{"attached", "unlinked", "dropped"}) {
			t.Errorf("events = %v, want attached, unlinked, dropped", g.events)
		}
		g.path.unreachable(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "down"}, nil))
		expectLog(t, g.log[3:], "b dropped", "a dropped", "b unreachable: down", "a unreachable: down")
		g.path.detach(a)
		if last := g.log[len(g.log)-1]; last != "a left" {
			t.Errorf("last = %q, want a left", last)
		}
	})
}

// path.test.ts: a gated path (slice 14 §2.3) › forwards a holder a denial released on the link only behind a fence; one never forwarded needs none
func TestGatedPathForwardsAHolderADenialReleasedOnTheLinkOnlyBehindAFenceOneNeverForwardedNeedsNone(t *testing.T) {
	g := gatedPathRig(t, "a")
	a, b := g.holder("a"), g.holder("b")
	first := g.newLink("1")
	g.in(func() {
		g.path.attach(a)
		g.path.attach(b)
		g.path.connected(first)
		g.path.withhold(a, deniedCause)
		g.path.withhold(b, deniedCause)
		expectLog(t, g.log, "a@1", "a denied", "b denied")
		g.path.forward(b)
		g.path.forward(a)
	})
	// `b` was never on the link: held at once. `a` is told it is admitted, and waits for its fence.
	log, _ := g.snapshot()
	expectLog(t, log[3:], "b admitted", "b@1", "a admitted")
	expectInts(t, "fences", g.pings(first), 1)
	g.pong(first)
	log, _ = g.snapshot()
	expectLog(t, log[6:], "a@1")
	// Denied again and allowed: it was on the link again, so a new fence.
	g.in(func() {
		g.path.withhold(a, deniedCause)
		g.path.forward(a)
	})
	expectInts(t, "fences", g.pings(first), 1)
}

// path.test.ts: a gated path (slice 14 §2.3) › a holder its gate takes back when the link is lost wants no link, and is on none until it is forwarded again
func TestGatedPathAHolderItsGateTakesBackWhenTheLinkIsLostWantsNoLinkAndIsOnNoneUntilItIsForwardedAgain(t *testing.T) {
	g := gatedPathRig(t, "a")
	a := g.holder("a")
	first, second := g.newLink("1"), g.newLink("2")
	g.in(func() {
		g.path.attach(a)
		g.path.connected(first)
		g.path.disconnected(first)
		// What a gate does in `unlinked` when it cannot read an allow.
		g.path.withhold(a, data.FailureCause{Kind: data.FailureWaiting, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "source failed"}, nil)})
		if !g.path.withholds(a) || g.path.wanted() {
			t.Errorf("withholds %v wanted %v, want true, false", g.path.withholds(a), g.path.wanted())
		}
		g.path.connected(second)
		expectLog(t, g.log, "a@1", "a dropped", "a waiting")
		// Never on the second link: forwarded there with no fence.
		g.path.forward(a)
		expectLog(t, g.log[3:], "a admitted", "a@2")
		unlinked := 0
		for _, event := range g.events {
			if event == "unlinked" {
				unlinked++
			}
		}
		expectInts(t, "unlinked", unlinked, 1)
	})
	expectInts(t, "fences", g.pings(second), 0)
}

// path.test.ts: a gated path (slice 14 §2.3) › a denial before the fence ran cancels it; a lost link drops it, and the next link holds what is admitted
func TestGatedPathADenialBeforeTheFenceRanCancelsItALostLinkDropsItAndTheNextLinkHoldsWhatIsAdmitted(t *testing.T) {
	g := gatedPathRig(t, "a")
	a := g.holder("a")
	first, second := g.newLink("1"), g.newLink("2")
	g.in(func() {
		g.path.attach(a)
		g.path.connected(first)
		g.path.withhold(a, deniedCause)
		g.path.forward(a)
		g.path.withhold(a, deniedCause)
	})
	// Two fences were sent: the first cancelled by the second denial, so answering both runs none.
	expectInts(t, "fences", g.pings(first), 1)
	g.pong(first)
	log, _ := g.snapshot()
	expectLog(t, log, "a@1", "a denied", "a admitted", "a denied")
	g.in(func() { g.path.forward(a) })
	expectInts(t, "fences", g.pings(first), 1)
	g.in(func() { g.path.disconnected(first) })
	g.pong(first)
	// Not held on the lost link (its fence was pending): not told of the drop twice, held on the next at once.
	g.in(func() { g.path.connected(second) })
	log, _ = g.snapshot()
	expectLog(t, log, "a@1", "a denied", "a admitted", "a denied", "a admitted", "a dropped", "a@2")
}
