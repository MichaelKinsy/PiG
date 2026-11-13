package host

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "seen members" of 'references sharing one wire hold (decision "References share one wire hold")' of
// packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

// rsSetX is Pi's `setX(peer, hold, x)`: a `state` message setting `x` of rsPanel's hold (bound `wide`).
func rsSetX(t testing.TB, peer *RawPeer, hold int, x float64) {
	t.Helper()
	peer.Send(rsReplace(t, hold, wireNames(t, rsPanel, "wide").State["x"], x))
}

// rsFailX is Pi's `setX(peer, hold, undefined)`: a `state` message failing `x` of rsPanel's hold with `x threw`.
func rsFailX(t testing.TB, peer *RawPeer, hold int) {
	t.Helper()
	failed := []link.FailedMember{{Member: wireNames(t, rsPanel, "wide").State["x"], Error: rhWireError(data.NewPluginError("derive", "x threw"))}}
	peer.Send(rhStateEntry(hold, nil, failed, nil))
}

// rsOpened is Pi's `opened(member)`: what a watch opened now on member gives: its value (the last good one when failed)
// and "", or nil and the rejection's message.
func rsOpened(t testing.TB, member *ReferenceMember) (value any, rejected string) {
	t.Helper()
	watch, err := member.Watch(background)
	if err != nil {
		return nil, err.Error()
	}
	value = watch.Value()
	_, err = watch.Stop()
	must(t, err)
	return value, ""
}

// rsOpens fails the test unless a watch opened now on member of ref gives want.
func rsOpens(t testing.TB, what string, ref *Reference, member string, want float64) {
	t.Helper()
	if value, rejected := rsOpened(t, ref.Member(member)); rejected != "" || value != want {
		t.Fatalf("%s opened %v (rejected %q), want %v", what, value, rejected, want)
	}
}

// rsRejects fails the test unless a watch opened now on member of ref is rejected with a message containing want (the
// whole message when exact).
func rsRejects(t testing.TB, what string, ref *Reference, member, want string, exact bool) {
	t.Helper()
	value, rejected := rsOpened(t, ref.Member(member))
	if rejected == "" || (exact && rejected != want) || !strings.Contains(rejected, want) {
		t.Fatalf("%s opened %v (rejected %q), want the rejection %q", what, value, rejected, want)
	}
}

func TestReferencesSharingOneWireHoldSeenMembers(t *testing.T) {
	panel := func(t *testing.T, client Host) *Reference {
		t.Helper()
		return rhHoldRef(t, client, rsPanel, jsonObject())
	}

	// remote-object.test.ts: references sharing one wire hold › seen members › a join to a live object with a failed member: its watch rejects with the error, no last value, until a value arrives
	t.Run("a join to a live object with a failed member: its watch rejects with the error, no last value, until a value arrives", func(t *testing.T) {
		rig := rhProvider(t)
		a := panel(t, rig.Client)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 7, "y": 1})
		waitLive(t, a)
		rsFailX(t, rig.Peer, hold)
		rhSync(t, rig.Peer)
		b := panel(t, rig.Client)
		rsOpens(t, "a.x", a, "x", 7)
		rsRejects(t, "b.x", b, "x", "x threw", true)
		rsSetX(t, rig.Peer, hold, 8)
		rhSync(t, rig.Peer)
		rsOpens(t, "b.x", b, "x", 8)
	})

	// remote-object.test.ts: references sharing one wire hold › seen members › a member missing from the bound variant at the join: likewise, also across variants (missing, present, missing)
	t.Run("a member missing from the bound variant at the join: likewise, also across variants (missing, present, missing)", func(t *testing.T) {
		rig := rhProvider(t)
		a := panel(t, rig.Client)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 1, "y": 2})
		waitLive(t, a)
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		rsBindPanel(t, rig.Peer, hold, "narrow", map[string]float64{"x": 3})
		rhSync(t, rig.Peer)
		b := panel(t, rig.Client)
		rsOpens(t, "a.y", a, "y", 2)
		rsRejects(t, "b.y", b, "y", `has no state member "y"`, false)
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 4, "y": 5})
		rhSync(t, rig.Peer)
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		rsBindPanel(t, rig.Peer, hold, "narrow", map[string]float64{"x": 6})
		rhSync(t, rig.Peer)
		// Seen while present: its own last value now.
		rsOpens(t, "b.y", b, "y", 5)
	})

	// remote-object.test.ts: references sharing one wire hold › seen members › a join while loading sees nothing of before: the reconnect's snapshot with x failed rejects its watch; the old one starts at 7
	t.Run("a join while loading sees nothing of before: the reconnect's snapshot with x failed rejects its watch; the old one starts at 7", func(t *testing.T) {
		rig := rhProvider(t)
		a := panel(t, rig.Client)
		rsBindPanel(t, rig.Peer, rig.Peer.Next("hold").(*link.HoldMessage).Hold, "wide", map[string]float64{"x": 7, "y": 1})
		waitLive(t, a)
		if x := memberValue(t, a, "x"); x != float64(7) {
			t.Fatalf("a.x = %v, want 7", x)
		}
		rig.Peer.Close()
		// Pi's `await transported()`: the client handled the drop.
		rhParentDown(t, rig.Client)
		b := panel(t, rig.Client)
		next := rawParent(t, rig.Client, link.PartLimits{})
		rsBindPanel(t, next, next.Next("hold").(*link.HoldMessage).Hold, "wide", map[string]float64{"y": 2})
		waitLive(t, b)
		rsOpens(t, "a.x", a, "x", 7)
		rsRejects(t, "b.x", b, "x", "member threw", true)
	})

	// remote-object.test.ts: references sharing one wire hold › seen members › failed, then good, then failed again before the joiner's first read: the joiner keeps that value as its own
	t.Run("failed, then good, then failed again before the joiner's first read: the joiner keeps that value as its own", func(t *testing.T) {
		rig := rhProvider(t)
		a := panel(t, rig.Client)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 7, "y": 1})
		waitLive(t, a)
		rsFailX(t, rig.Peer, hold)
		rhSync(t, rig.Peer)
		b := panel(t, rig.Client)
		rsSetX(t, rig.Peer, hold, 9)
		rsFailX(t, rig.Peer, hold)
		rhSync(t, rig.Peer)
		rsOpens(t, "b.x", b, "x", 9)
	})

	// remote-object.test.ts: references sharing one wire hold › seen members › unseen members and own last values survive the retry alone after too_large/part
	t.Run("unseen members and own last values survive the retry alone after too_large/part", func(t *testing.T) {
		rig := rawProvider(t, rsPart64())
		a := panel(t, rig.Client)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 7, "y": 1})
		waitLive(t, a)
		rsFailX(t, rig.Peer, hold)
		rhSync(t, rig.Peer)
		b := panel(t, rig.Client)
		rsOpens(t, "b.y", b, "y", 1)
		rig.Peer.Send(rsReplace(t, hold, wireNames(t, rsPanel, "wide").State["y"], strings.Repeat("y", 100)))
		rhSync(t, rig.Peer)
		rig.Peer.Next("release")
		b.Retry()
		own := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, own, "wide", map[string]float64{"y": 2})
		waitLive(t, b)
		// x: still unseen (failed at the join, no value since); y: its own.
		rsRejects(t, "b.x", b, "x", "member threw", true)
		rsSetX(t, rig.Peer, own, 3)
		rsFailX(t, rig.Peer, own)
		rhSync(t, rig.Peer)
		rsOpens(t, "b.x", b, "x", 3)
	})
}
