package host

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "the accepted differences" of 'references sharing one wire hold (decision "References share one
// wire hold")' of packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

func TestReferencesSharingOneWireHoldAcceptedDifferences(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › the accepted differences › presence and activity at the provider: one entry and count per hold; a non-final release changes neither
	t.Run("presence and activity at the provider: one entry and count per hold; a non-final release changes neither", func(t *testing.T) {
		hosts := linked(t, func(host FacetHost) error { _, err := serveNotes(host, nil); return err }, linkedOptions{})
		shown := func(what string, want float64) {
			t.Helper()
			settleLinked(hosts)
			listed := float64(len(sourceValue(t, hosts.Server, hosts.Server.References(Notes)).([]any)))
			if references := rhActivityReferences(t, hosts.Server); listed != want || references != want {
				t.Fatalf("%s: [listed, references] = [%v %v], want [%v %v]", what, listed, references, want, want)
			}
		}
		get := func(session string) *Reference {
			t.Helper()
			ref, err := hosts.Client.Get(background, Notes, rhNotes(session), GetOptions{})
			must(t, err)
			return ref
		}
		a := get("S1")
		b := get("S1")
		other := get("S2")
		shown("two references of S1 and one of S2", 2)
		a.Release()
		shown("after a non-final release", 2)
		b.Release()
		other.Release()
		shown("after the last releases", 0)
	})

	// remote-object.test.ts: references sharing one wire hold › the accepted differences › a commit in transit: a joiner first reads the replica, then gets the commit
	t.Run("a commit in transit: a joiner first reads the replica, then gets the commit", func(t *testing.T) {
		rig := rhProvider(t)
		a := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer, jsonObject("text", "a"))
		waitLive(t, a)
		var b *Reference
		var first delta.JsonValue
		// One turn of the client, as Pi's synchronous `peer.send(...); client.hold(...); b.count.value`: the commit, sent
		// before the join, cannot be handled before the turn ends.
		hostTurn(rig.Client, func() {
			rig.Peer.Send(rsCount(t, hold, 2))
			b = rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
			first = memberValue(t, b, "count")
		})
		if first != float64(1) {
			t.Fatalf("the joiner's first read = %v, want 1 (the replica)", first)
		}
		rhSync(t, rig.Peer)
		if count := memberValue(t, b, "count"); count != float64(2) {
			t.Fatalf("the joiner's count after the commit = %v, want 2", count)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › the accepted differences › part limits apply to received parts: a value grown past the limit by deltas does not fail a joiner; an oversized call result fails only its call
	t.Run("part limits apply to received parts: a value grown past the limit by deltas does not fail a joiner; an oversized call result fails only its call", func(t *testing.T) {
		rig := rawProvider(t, rsPart64())
		a := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer)
		waitLive(t, a)
		for n := range 6 {
			ops := mustJSONPart(t, []any{[]any{"p", []any{}, float64(n), 0.0, []any{jsonObject("text", "0123456789")}}})
			rig.Peer.Send(rhStateEntry(hold, []link.StateMember{{Member: editorWire(t).State["notes"], Ops: ops}}, nil, nil))
		}
		rhSync(t, rig.Peer)
		if size := len(rhJSON(t, memberValue(t, a, "notes"))); size <= 64 {
			t.Fatalf("the notes' JSON is %d bytes, want more than 64", size)
		}
		b := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		if state := rhState(b); state.Kind != RefStateLive || state.Cause.Kind != "" || state.RetryAt != nil {
			t.Fatalf("the joiner = %s, want {kind: live}", rhJSON(t, state))
		}
		calling := startCall(t, b, "add", jsonObject("text", "x"), background)
		call := rig.Peer.Next("call").(*link.CallMessage).Call
		rig.Peer.Send(&link.ResultMessage{Call: call, Value: mustJSONPart(t, strings.Repeat("y", 100))})
		_, err := calling.wait(background)
		_ = requireChordError(t, err, data.CodeTooLarge, "")
		if kinds := []RefStateKind{rhRefState(a), rhRefState(b)}; kinds[0] != RefStateLive || kinds[1] != RefStateLive {
			t.Fatalf("states = %v, want [live live]", kinds)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › the accepted differences › watch callbacks of one shared commit run in subscription order, not hold order (accepted difference 9)
	//
	// Pi expects the order ["b", "a"]. Go delivers each watch on its own task; Pi's subscription order between two
	// references is not defined here. The case keeps Pi's inputs and asserts what Go defines of that result: the one
	// commit reaches b's callback and a's callback, each exactly once.
	// TestRemoteObjectWatchesGetTheHoldsCommitsInCommitOrder pins the order Go does define.
	t.Run("watch callbacks of one shared commit run in subscription order, not hold order (accepted difference 9)", func(t *testing.T) {
		rig := rhProvider(t)
		a := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		b := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer)
		waitLive(t, b)
		var mu sync.Mutex
		var order []string
		calls := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(order) }
		start := func(ref *Reference, name string) {
			rhWatch(t, ref.Member("count")).Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return nil
			})
		}
		// B subscribes first, though A holds first.
		start(b, "b")
		start(a, "a")
		rig.Peer.Send(rsCount(t, hold, 4))
		rhSync(t, rig.Peer)
		rhWaitFor(t, func() bool { return len(calls()) >= 2 })
		// A commit after it: were one of the two called twice for the first, its third call would be in before these.
		rig.Peer.Send(rsCount(t, hold, 5))
		rhSync(t, rig.Peer)
		rhWaitFor(t, func() bool { return len(calls()) >= 4 })
		got := calls()
		first, second := slices.Clone(got[:2]), slices.Clone(got[2:])
		slices.Sort(first)
		slices.Sort(second)
		if want := []string{"a", "b"}; len(got) != 4 || !slices.Equal(first, want) || !slices.Equal(second, want) {
			t.Fatalf("callbacks = %v, want b and a once per commit", got)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › the accepted differences › a joining getter makes no hold and no window turn: it resolves with the replica at once
	t.Run("a joining getter makes no hold and no window turn: it resolves with the replica at once", func(t *testing.T) {
		rig := rhProvider(t)
		getting := asyncGet(rig.Client, Notes, rhNotes("S1"), background, GetOptions{})
		bindNext(t, rig.Peer, jsonObject("text", "a"))
		a := awaitReference(t, getting)
		b, err := rig.Client.Get(background, Notes, rhNotes("S1"), GetOptions{})
		must(t, err)
		count := memberValue(t, b, "count")
		rhSync(t, rig.Peer)
		if holds := takenOf(rig.Peer, "hold"); count != float64(1) || holds != 0 {
			t.Fatalf("[b.count, holds] = [%v %d], want [1 0]", count, holds)
		}
		if !rsSame(memberValue(t, a, "notes"), memberValue(t, b, "notes")) {
			t.Fatalf("the two references' notes are two values, want the one shared value")
		}
	})
}

// PiG's own: the order Go defines where Pi's "watch callbacks of one shared commit run in subscription order" defines
// one between two references: each reference's watch gets the hold's commits in commit order, whichever reference's
// callback runs first.
func TestRemoteObjectWatchesGetTheHoldsCommitsInCommitOrder(t *testing.T) {
	rig := rhProvider(t)
	a := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
	b := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
	hold := bindNext(t, rig.Peer)
	waitLive(t, b)
	frames := []*nexttest.Collector[nexttest.Frame[delta.JsonValue]]{
		nexttest.FramesOf(rhWatch(t, b.Member("count"))), nexttest.FramesOf(rhWatch(t, a.Member("count"))),
	}
	rig.Peer.Send(rsCount(t, hold, 4))
	rig.Peer.Send(rsCount(t, hold, 5))
	rhSync(t, rig.Peer)
	rhWaitFor(t, func() bool { return frames[0].Len() >= 2 && frames[1].Len() >= 2 })
	for index, each := range frames {
		if got := valuesOfFrames(each.Items()); !slices.Equal(got, []any{float64(4), float64(5)}) {
			t.Fatalf("watch %d got %v, want [4 5]", index, got)
		}
	}
}
