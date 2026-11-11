package host

import (
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/host-link.ts

// Ports the describe 'call windows by trust (decision "Call windows only for untrusted links")' of
// packages/chord/test/remote/host-link.test.ts.

// helloOf is the `hello` a raw peer received.
func helloOf(t testing.TB, peer *RawPeer) *link.HelloMessage {
	t.Helper()
	var hello *link.HelloMessage
	rhWaitFor(t, func() bool {
		for _, message := range peer.Received() {
			if found, ok := message.(*link.HelloMessage); ok {
				hello = found
			}
		}
		return hello != nil
	})
	return hello
}

func intp(n int) *int { return &n }

// host-link.test.ts: call windows by trust › announce none on a link given as trusted, the host's callWindow otherwise: accept and connect
func TestHostLinkAnnouncesNoCallWindowOnATrustedLinkAndTheHostsOtherwise(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		for _, parent := range []bool{false, true} {
			trusted := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{Parent: parent, Trusted: true, CallWindow: intp(8)})
			rhWaitFor(t, func() bool { return len(trusted.Peer.Kinds()) > 0 })
			rhSync(t, trusted.Peer)
			if got := helloOf(t, trusted.Peer).Calls; got != 0 {
				t.Fatalf("parent=%v: a trusted link announced calls %d, want none", parent, got)
			}
			untrusted := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{Parent: parent, CallWindow: intp(8)})
			rhSync(t, untrusted.Peer)
			if got := helloOf(t, untrusted.Peer).Calls; got != 8 {
				t.Fatalf("parent=%v: an untrusted link announced calls %d, want 8", parent, got)
			}
		}
	})
}

// host-link.test.ts: call windows by trust › announce none on a route's link its resolve gave as trusted, the host's callWindow otherwise
func TestHostLinkAnnouncesNoCallWindowOnARoutesTrustedLinkAndTheHostsOtherwise(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		for _, trusted := range []bool{true, false} {
			workers := newRawWorkers(t, rawWorkerOptions{Trusted: trusted})
			rig := relay(t, relayedOptions{Resolve: workers.Resolve})
			go func() { _, _ = rig.Tab.Get(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{}) }()
			worker := rhFirstPeer(t, workers)
			worker.Next("hold")
			announced := helloOf(t, worker)
			if trusted && announced.Calls != 0 {
				t.Fatalf("a trusted route link announced calls %d, want none", announced.Calls)
			}
			if !trusted && announced.Calls != 10_000 {
				t.Fatalf("an untrusted route link announced calls %d, want 10000", announced.Calls)
			}
		}
	})
}

// host-link.test.ts: call windows by trust › asymmetric trust: a client trusting its server keeps within the server's window, the server announcing 2
func TestHostLinkAClientTrustingItsServerKeepsWithinTheServersWindow(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		errs := &hostErrors{}
		var probe *notesProbe
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), CallWindow: intp(2), OnError: errs.push})
		mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
			var err error
			probe, err = serveNotes(host, nil)
			return err
		})}}, background))
		client := newHost(t, HostOptions{OnError: errs.push})
		mustLoad(t)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec(), Trusted: true}, background))
		notes, err := client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		first, cancel := chordctx.WithCancel(background)
		startCall(t, notes, "wait", nil, first)
		for range 2 {
			startCall(t, notes, "wait", nil, background)
		}
		probe.AwaitWaits(t, 2)
		rhSettleLinks(a, b)
		if got := len(probe.Waits()); got != 2 {
			t.Fatalf("the server started %d waits, want 2", got)
		}
		cancel(errors.New("enough"))
		probe.AwaitWaits(t, 3)
		rhSettleLinks(a, b)
		if got := len(probe.Waits()); got != 3 {
			t.Fatalf("the server started %d waits, want 3", got)
		}
		if errs.Len() != 0 {
			t.Fatalf("errors = %v", errs.All())
		}
	})
}
