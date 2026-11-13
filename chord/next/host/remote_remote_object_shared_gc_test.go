package host

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "GC" of 'references sharing one wire hold (decision "References share one wire hold")' of
// packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).
//
// Go forms: Pi's `instancesOf(RemoteObject)` and `instancesOf(Path)` (weak references to every instance made) are weak
// pointers to the remote object and the path of the references a case makes; Pi's `new WeakRef(reference)` is a weak
// pointer to the reference's binding (what its object, its link's call records and its siblings could keep; the
// `*Reference` is a handle on it); `resolveWatchable(member)` is the member's source. Collection is checked by
// nexttest.ExpectCollected, whose bound is its own.

// rsSourceOf is the source behind member of ref (Pi's `resolveWatchable(ref.member)`).
func rsSourceOf(t testing.TB, ref *Reference, member string) *referenceSource {
	t.Helper()
	var source *referenceSource
	var found bool
	ref.binding.loop.Run(func() { source, found = ref.binding.sources.get(member) })
	if !found {
		t.Fatalf("the reference has no source of %q", member)
	}
	return source
}

func TestReferencesSharingOneWireHoldGC(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › GC › no remote object stays after its last release while the path lives; a released reference kept by the app keeps none
	t.Run("no remote object stays after its last release while the path lives; a released reference kept by the app keeps none", func(t *testing.T) {
		hosts := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
		var kept []*Reference
		object := func() weak.Pointer[remoteObject] {
			a, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
			must(t, err)
			b, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
			must(t, err)
			// Pi's `expect(refs).toHaveLength(1)`: the two references made one remote object.
			if rhRemoteObjectOf(a) != rhRemoteObjectOf(b) {
				t.Fatalf("the two references made two remote objects, want one")
			}
			made := weak.Make(rhRemoteObjectOf(a))
			a.Release()
			b.Release()
			settleLinked(hosts)
			kept = []*Reference{a, b}
			return made
		}()
		nexttest.ExpectCollected(t, object)
		runtime.KeepAlive(kept)
		runtime.KeepAlive(hosts)
	})

	// remote-object.test.ts: references sharing one wire hold › GC › a released reference (its calls cancelled, its watches) is not kept by a live sibling, its object or its link
	t.Run("a released reference (its calls cancelled, its watches) is not kept by a live sibling, its object or its link", func(t *testing.T) {
		var probe *notesProbe
		hosts := linked(t, func(h FacetHost) error {
			made, err := serveNotes(h, nil)
			probe = made
			return err
		}, linkedOptions{})
		b, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
		must(t, err)
		reference, source := func() (weak.Pointer[binding], weak.Pointer[referenceSource]) {
			a, err := hosts.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
			must(t, err)
			call := startCall(t, a, "wait", nil, background)
			rhWatch(t, a.Member("count")).Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
			probe.AwaitWaits(t, 1)
			settleLinked(hosts)
			reference, source := weak.Make(a.binding), weak.Make(rsSourceOf(t, a, "count"))
			a.Release()
			if _, err := call.wait(background); err == nil {
				t.Fatalf("the released reference's call resolved, want a rejection")
			}
			return reference, source
		}()
		settleLinked(hosts)
		nexttest.ExpectCollected(t, reference)
		nexttest.ExpectCollected(t, source)
		if kind := rhRefState(b); kind != RefStateLive {
			t.Fatalf("the sibling = %v, want live", kind)
		}
		runtime.KeepAlive(probe)
		runtime.KeepAlive(hosts)
	})

	// remote-object.test.ts: references sharing one wire hold › GC › a released joiner with queued calls and unseen members is kept by no queue, call record or sibling
	t.Run("a released joiner with queued calls and unseen members is kept by no queue, call record or sibling", func(t *testing.T) {
		client := newHost(t, HostOptions{OnError: func(HostError) {}})
		mustLoad(t)(client.Load(nil, background))
		near, far := link.MemoryLinkPair()
		must(t, client.Connect(near, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		peer := newRawPeer(t, far, rawPeerOptions{Calls: 1})
		peer.Next("hello")
		b := rhHoldRef(t, client, rsPanel, jsonObject())
		hold := peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, peer, hold, "wide", map[string]float64{"x": 7, "y": 1})
		waitLive(t, b)
		rsFailX(t, peer, hold)
		rhSync(t, peer)
		// B's call takes the window's one slot: A's calls wait in its queue.
		sibling := startCall(t, b, "poke", "B1", background)
		reference, source := func() (weak.Pointer[binding], weak.Pointer[referenceSource]) {
			a := rhHoldRef(t, client, rsPanel, jsonObject())
			rsRejects(t, "a.x", a, "x", "x threw", true)
			queued := []*parkedCallResult{startCall(t, a, "poke", "A1", background), startCall(t, a, "poke", "A2", background)}
			reference, source := weak.Make(a.binding), weak.Make(rsSourceOf(t, a, "x"))
			a.Release()
			for index, call := range queued {
				if _, err := call.wait(background); err == nil {
					t.Fatalf("queued call %d of the released joiner resolved, want a rejection", index)
				}
			}
			return reference, source
		}()
		rhSync(t, peer)
		nexttest.ExpectCollected(t, reference)
		nexttest.ExpectCollected(t, source)
		call := peer.Next("call").(*link.CallMessage).Call
		peer.Send(sharedResult(t, call, 1))
		value, err := sibling.wait(background)
		must(t, err)
		rhSync(t, peer)
		if kind, calls := rhRefState(b), takenOf(peer, "call"); value != float64(1) || kind != RefStateLive || calls != 0 {
			t.Fatalf("[sibling, b, calls] = [%v %v %d], want [1 live 0]", value, kind, calls)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › GC › after a retry alone, the old object keeps neither the moved binding nor the private object's forked replicas
	t.Run("after a retry alone, the old object keeps neither the moved binding nor the private object's forked replicas", func(t *testing.T) {
		rig := rawProvider(t, rsPart64())
		kept := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		reference, object, forked := func() (weak.Pointer[binding], weak.Pointer[remoteObject], weak.Pointer[replica]) {
			moved := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			hold := bindNext(t, rig.Peer)
			waitLive(t, moved)
			rig.Peer.Send(rsReplace(t, hold, editorWire(t).State["notes"], []any{jsonObject("text", strings.Repeat("x", 100))}))
			rhSync(t, rig.Peer)
			rig.Peer.Next("release")
			moved.Retry()
			bindHold(t, rig.Peer, rig.Peer.Next("hold").(*link.HoldMessage).Hold)
			waitLive(t, moved)
			var own *remoteObject
			var count *replica
			hostTurn(rig.Client, func() {
				own = rhRemoteObjectOf(moved)
				count = own.bound.replicas.byName["count"]
			})
			if own == rhRemoteObjectOf(kept) || count == nil {
				t.Fatalf("the retried reference has no private object with a count replica")
			}
			reference, object, forked := weak.Make(moved.binding), weak.Make(own), weak.Make(count)
			moved.Release()
			return reference, object, forked
		}()
		rhSync(t, rig.Peer)
		nexttest.ExpectCollected(t, reference)
		nexttest.ExpectCollected(t, object)
		nexttest.ExpectCollected(t, forked)
		if state := rhState(kept); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken {
			t.Fatalf("the kept reference = %s, want failed broken", rhJSON(t, state))
		}
		runtime.KeepAlive(rig)
	})

	// remote-object.test.ts: references sharing one wire hold › GC › a route path dropped after its last holder takes its sharing table
	t.Run("a route path dropped after its last holder takes its sharing table", func(t *testing.T) {
		rel := relay(t, relayedOptions{})
		made := func() weak.Pointer[path] {
			notes, err := rel.Server.Get(background, PlacedNotes, rhNotes("S1"), GetOptions{})
			must(t, err)
			var made weak.Pointer[path]
			hostTurn(rel.Server, func() { made = weak.Make(rhRemoteObjectOf(notes).path) })
			notes.Release()
			rel.Transported()
			return made
		}()
		for _, each := range rel.RouteLinks() {
			each.Close()
		}
		rel.Transported()
		nexttest.ExpectCollected(t, made)
		runtime.KeepAlive(rel)
	})
}
