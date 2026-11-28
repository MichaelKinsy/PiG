package host

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "too_large/part and the retry alone" of 'references sharing one wire hold (decision "References
// share one wire hold")' of packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

// rsPlacedThrows is Pi's `throws.now`: while set, rsPlaced's `hostAddress` fails.
var rsPlacedThrows atomic.Bool

// rsPlaced is Pi's `Placed`: a remote type whose `hostAddress` throws while rsPlacedThrows is set.
var rsPlaced = mustContract(contracts.Remote("app.placed", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
		if rsPlacedThrows.Load() {
			return contracts.Address{}, errors.New("no address")
		}
		return Server.Address(nil), nil
	},
	State: []contracts.RemoteStateEntry{contracts.StateEntry("text", contracts.Str())},
}))

// rsBindText is Pi's `bindText`: it binds hold of rsPlaced with text.
func rsBindText(t testing.TB, peer *RawPeer, hold int, text string) {
	t.Helper()
	names := wireNames(t, rsPlaced, "v")
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
		Hold: hold, Granted: names.Granted, Failed: []link.FailedMember{},
		Members: []link.BoundMember{{Member: names.State["text"], Value: mustJSONPart(t, text)}},
	}}})
}

// rsPart64 is Pi's `{ limits: { part: 64 } }`.
func rsPart64() rawLinkedOptions {
	part := 64.0
	return rawLinkedOptions{Limits: link.PartLimits{Part: &part}}
}

// rsReplace is a `state` message replacing member (a wire index) of hold by value.
func rsReplace(t testing.TB, hold, member int, value any) *link.StateMessage {
	t.Helper()
	return rhStateEntry(hold, []link.StateMember{{Member: member, Ops: mustJSONPart(t, []any{[]any{"r", value}})}}, nil, nil)
}

// rsTooLarge fails the test unless ref is failed broken with `too_large`.
func rsTooLarge(t testing.TB, what string, ref *Reference) {
	t.Helper()
	state := rhState(ref)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.Cause.Error.Code != data.CodeTooLarge {
		t.Fatalf("%s = %s, want failed broken too_large", what, rhJSON(t, state))
	}
}

func TestReferencesSharingOneWireHoldRetryAlone(t *testing.T) {
	placed := func(t *testing.T, rig *rawProviderHost) *Reference {
		t.Helper()
		return rhHoldRef(t, rig.Client, rsPlaced, jsonObject())
	}
	text := wireNames(t, rsPlaced, "v").State["text"]

	// remote-object.test.ts: references sharing one wire hold › too_large/part and the retry alone › fails every binding broken; a new reference holds anew; a retry moves only its binding, keeping its last value
	t.Run("fails every binding broken; a new reference holds anew; a retry moves only its binding, keeping its last value", func(t *testing.T) {
		rsPlacedThrows.Store(false)
		rig := rawProvider(t, rsPart64())
		a1 := placed(t, rig)
		a2 := placed(t, rig)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindText(t, rig.Peer, hold, "short")
		waitLive(t, a2)
		watch := nexttest.ChangesOf(rhWatch(t, a1.Member("text")))
		// A state part past the link's limit: the shared object fails, every binding with it.
		rig.Peer.Send(rsReplace(t, hold, text, strings.Repeat("x", 100)))
		rhSync(t, rig.Peer)
		if released := rig.Peer.Next("release").(*link.ReleaseMessage).Hold; released != hold {
			t.Fatalf("released %d, want %d", released, hold)
		}
		rsTooLarge(t, "a1", a1)
		rsTooLarge(t, "a2", a2)
		// C makes a new hold, owning the new entry.
		c := placed(t, rig)
		held := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		// A1 alone moves to a private hold, its watch and last value its own.
		a1.Retry()
		retried := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		if retried == held {
			t.Fatalf("the retry's hold = %d, the new entry's own", retried)
		}
		if a, b, last := rhRefState(a1), rhRefState(a2), memberValue(t, a1, "text"); a != RefStateLoading || b != RefStateFailed || last != "short" {
			t.Fatalf("after the retry = [%v %v %v], want [loading failed short]", a, b, last)
		}
		rsBindText(t, rig.Peer, retried, "again")
		waitLive(t, a1)
		rhSync(t, rig.Peer)
		rhWaitCalls(t, watch, 2)
		values := nexttest.ValuesOf(watch.Items())
		failed, is := values[0].(nexttest.FailedValue)
		if len(values) != 2 || !is || !strings.Contains(failed.Failed, "over the wire limit") || values[1] != "again" {
			t.Fatalf("a1's watch = %v, want [{failed: ...over the wire limit...} again]", values)
		}
		// D joins C's entry, not A1's private object.
		rsBindText(t, rig.Peer, held, "c")
		waitLive(t, c)
		d := placed(t, rig)
		kind, value := rhRefState(d), memberValue(t, d, "text")
		rhSync(t, rig.Peer)
		if holds := takenOf(rig.Peer, "hold"); kind != RefStateLive || value != "c" || holds != 0 {
			t.Fatalf("d = [%v %v %d holds], want [live c 0]", kind, value, holds)
		}
		// The old object's last binding leaves: nothing more is sent for it.
		a2.Release()
		c.Release()
		rhSync(t, rig.Peer)
		if releases := takenOf(rig.Peer, "release"); releases != 0 {
			t.Fatalf("releases after a2 and c let go = %d, want 0", releases)
		}
		d.Release()
		a1.Release()
		rhSync(t, rig.Peer)
		released := []int{}
		for _, message := range rig.Peer.Received() {
			if release, is := message.(*link.ReleaseMessage); is {
				released = append(released, release.Hold)
			}
		}
		slices.Sort(released)
		if want := rhSorted([]int{held, retried}); !slices.Equal(released, want) {
			t.Fatalf("released holds = %v, want %v", released, want)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › too_large/part and the retry alone › a retry whose placement throws fails only that binding; the old object's last binding retrying ends it
	t.Run("a retry whose placement throws fails only that binding; the old object's last binding retrying ends it", func(t *testing.T) {
		rig := rawProvider(t, rsPart64())
		rsPlacedThrows.Store(false)
		defer rsPlacedThrows.Store(false)
		a1 := placed(t, rig)
		a2 := placed(t, rig)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindText(t, rig.Peer, hold, strings.Repeat("x", 100))
		rhSync(t, rig.Peer)
		rig.Peer.Next("release")
		rsPlacedThrows.Store(true)
		a1.Retry()
		state := rhState(a1)
		if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || !strings.Contains(state.Cause.Error.Message, "no address") {
			t.Fatalf("a1 = %s, want failed broken with no address", rhJSON(t, state))
		}
		if other := rhState(a2); other.Kind != RefStateFailed || other.Cause.Error.Code != data.CodeTooLarge {
			t.Fatalf("a2 = %s, want failed too_large", rhJSON(t, other))
		}
		rsPlacedThrows.Store(false)
		// A2, the old object's last binding, retries: the old object ends, A2 holds on its own.
		a2.Retry()
		again := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindText(t, rig.Peer, again, "ok")
		waitLive(t, a2)
		a1.Retry()
		if next := rig.Peer.Next("hold").(*link.HoldMessage).Hold; next != again+1 {
			t.Fatalf("a1's hold after its second retry = %d, want %d", next, again+1)
		}
	})
}
