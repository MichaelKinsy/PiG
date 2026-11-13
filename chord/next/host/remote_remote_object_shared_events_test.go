package host

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "events" of 'references sharing one wire hold (decision "References share one wire hold")' of
// packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1): five declarations, seven cases at run
// time (one declaration runs for three terminals).
//
// The order of event listeners is defined in Go as in Pi (hostEvents.flush: entry order, then subscription order): the
// cases compare Pi's lists as they are.

// rsHeard collects what event listeners heard, in order.
type rsHeard struct {
	mu   sync.Mutex
	list []string
}

// listener is a listener recording `tag` + y of every event it gets.
func (h *rsHeard) listener(tag string) func(contracts.EventDelivery[delta.JsonValue]) {
	return func(e contracts.EventDelivery[delta.JsonValue]) {
		h.add(fmt.Sprintf("%s%v", tag, e.Value.(*delta.JsonObject).Value("y")))
	}
}

func (h *rsHeard) add(item string) {
	h.mu.Lock()
	h.list = append(h.list, item)
	h.mu.Unlock()
}

// all is a copy of what was heard so far.
func (h *rsHeard) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.list)
}

// rsPaddle is Pi's `paddle(peer, hold, ...ys)`: one `event` message of Match's paddle for hold with an entry per y.
func rsPaddle(t testing.TB, peer *RawPeer, hold int, ys ...float64) {
	t.Helper()
	events := make([]link.WireEvent, 0, len(ys))
	for _, y := range ys {
		events = append(events, paddleEvent(t, hold, y, 1))
	}
	peer.Send(&link.EventMessage{Events: events})
}

// rsDelivered is Pi's `await delivered()` after an event message: the client handled what the peer sent and its event
// flush ran.
func rsDelivered(t testing.TB, rig *rawProviderHost) {
	t.Helper()
	rhSync(t, rig.Peer)
	flushed(rig.Client)
}

// rsMatchRef holds Match (id 1) on client.
func rsMatchRef(t testing.TB, client Host) *Reference {
	t.Helper()
	return rhHoldRef(t, client, Match, jsonObject("id", float64(1)))
}

// rsMatch is Pi's `match()`: Match bound as a player over a raw provider, and a first reference to it.
func rsMatch(t testing.TB) (rig *rawProviderHost, hold int, a *Reference) {
	t.Helper()
	rig = rhProvider(t)
	a = rsMatchRef(t, rig.Client)
	hold = rig.Peer.Next("hold").(*link.HoldMessage).Hold
	bindMatch(t, rig.Peer, hold, "player")
	waitLive(t, a)
	return rig, hold, a
}

// rsWatchClosed waits until watch ended and returns its end.
func rsWatchClosed(t testing.TB, closed <-chan struct{}) {
	t.Helper()
	rhWaitFor(t, func() bool {
		select {
		case <-closed:
			return true
		default:
			return false
		}
	})
}

func TestReferencesSharingOneWireHoldEvents(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › events › a subscription survives its reference's retry alone after too_large/part: the private hold's events reach it
	t.Run("a subscription survives its reference's retry alone after too_large/part: the private hold's events reach it", func(t *testing.T) {
		rig := rawProvider(t, rsPart64())
		a1 := rsMatchRef(t, rig.Client)
		a2 := rsMatchRef(t, rig.Client)
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		bindMatch(t, rig.Peer, hold, "player")
		waitLive(t, a1)
		heard := &rsHeard{}
		on(t, a1, "paddle", heard.listener("a1:"), background)
		on(t, a2, "paddle", heard.listener("a2:"), background)
		rig.Peer.Send(rsReplace(t, hold, wireNames(t, Match, "player").State["score"], strings.Repeat("x", 100)))
		rhSync(t, rig.Peer)
		rig.Peer.Next("release")
		a1.Retry()
		own := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		bindMatch(t, rig.Peer, own, "player")
		waitLive(t, a1)
		rsPaddle(t, rig.Peer, own, 1)
		rsPaddle(t, rig.Peer, hold, 2)
		rsDelivered(t, rig)
		if got, kind := heard.all(), rhRefState(a2); !slices.Equal(got, []string{"a1:1"}) || kind != RefStateFailed {
			t.Fatalf("[heard, a2] = [%v %v], want [[a1:1] failed]", got, kind)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › events › <gone | the host's close | revocation> ends every reference sharing the hold once: its watches, event subscriptions and running calls
	//
	// One Pi declaration for three terminals. `once` is a count here: every reference is told its end one time, and no
	// other state on the way (tellings, below).
	for _, terminal := range []string{"gone", "the host's close", "revocation"} {
		t.Run(terminal+" ends every reference sharing the hold once: its watches, event subscriptions and running calls", func(t *testing.T) {
			client := newHost(t, HostOptions{
				RevokedBy: RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
					if principalKind(principal) == "guest" {
						return []delta.JsonValue{principal.Value("session")}, nil
					}
					return nil, nil
				}),
				OnError: func(HostError) {},
			})
			mustLoad(t)(client.Load(nil, background))
			peer := rawParent(t, client, link.PartLimits{})
			// Two references of a guest's scope (revocation ends a revoked principal's, never the host's own).
			var refs []*Reference
			ready, hang := newSignal(), newSignal()
			defer hang.fire()
			scope := rhGo(func() (struct{}, error) {
				_, _ = client.Root(func(ctx Ctx) (any, error) {
					for range 2 {
						ref, err := ctx.Get(Match, jsonObject("id", float64(1)), GetOptions{})
						if err != nil {
							return nil, err
						}
						refs = append(refs, ref)
					}
					ready.fire()
					hang.await(t, ctx)
					return nil, nil
				}, background, RootOptions{Principal: guestPrincipal("S1")})
				return struct{}{}, nil
			})
			hold := peer.Next("hold").(*link.HoldMessage).Hold
			bindMatch(t, peer, hold, "player")
			<-ready
			var states []*statesCollector
			var stateWatches []chord.NativeWatchHandle[*RefState]
			var watches []chord.WatchHandle[delta.JsonValue]
			for _, ref := range refs {
				watch, err := ref.State().Watch(background)
				must(t, err)
				stateWatches = append(stateWatches, watch)
				states = append(states, statesOf(t, watch))
			}
			for _, ref := range refs {
				watches = append(watches, rhWatch(t, ref.Member("score")))
			}
			heard := &rsHeard{}
			for index, ref := range refs {
				on(t, ref, "paddle", heard.listener(fmt.Sprintf("%d:", index)), background)
			}
			// tellings counts, per reference, what its binding is told from here on: every state it is moved to, and its
			// end. Pi's `once`: one each.
			tellings := make([]int, len(refs))
			hostTurn(client, func() {
				for index, ref := range refs {
					ref.binding.until(func(*RefState) decision {
						tellings[index]++
						return waitDecision
					}, background).then(func(struct{}, error) { tellings[index]++ })
				}
			})
			told := func() []int {
				var got []int
				hostTurn(client, func() { got = slices.Clone(tellings) })
				return got
			}
			// `until` decides once at once, on the state now.
			if got := told(); !slices.Equal(got, []int{1, 1}) {
				t.Fatalf("tellings before the terminal = %v, want [1 1] (the state now)", got)
			}
			var calls []*parkedCallResult
			for _, ref := range refs {
				calls = append(calls, startCall(t, ref, "bump", nil, background))
			}
			peer.Next("call")
			peer.Next("call")
			rsPaddle(t, peer, hold, 1)
			rhSync(t, peer)
			flushed(client)
			if got := heard.all(); !slices.Equal(got, []string{"0:1", "1:1"}) {
				t.Fatalf("heard = %v, want [0:1 1:1]", got)
			}
			code := data.CodeReleased
			switch terminal {
			case "gone":
				ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: "ended", Message: "ended"}, nil)
				peer.Send(&link.GoneMessage{Hold: hold, Error: rhWireError(ended)})
				rhSync(t, peer)
			case "revocation":
				must(t, client.Revoke("S1", background))
				code = data.CodeForbidden
			default:
				must(t, client.Close(background))
			}
			// Ended once each: no other state on the way, their state watches ended with it.
			for index, ref := range refs {
				if kind := rhRefState(ref); kind != RefStateEnded {
					t.Fatalf("reference %d = %v, want ended", index, kind)
				}
				rsWatchClosed(t, stateWatches[index].Closed())
				if got := states[index].All(); len(got) != 0 {
					t.Fatalf("states of reference %d on the way = %v, want none", index, got)
				}
			}
			if terminal != "the host's close" {
				// The end is the one telling after the state at the start: 2 each. (A closed host runs no section.)
				if got := told(); !slices.Equal(got, []int{2, 2}) {
					t.Fatalf("tellings = %v, want [2 2]: the state at the start and the end, once", got)
				}
			}
			for index, watch := range watches {
				rsWatchClosed(t, watch.Closed())
				if end := watch.End(); end.Kind != chord.WatchEnded {
					t.Fatalf("the score watch of reference %d ended %v, want ended", index, end.Kind)
				}
			}
			// The host's close closes its links first: running calls see the link drop.
			called := code
			if terminal == "the host's close" {
				called = data.CodeUnavailable
			}
			for index, call := range calls {
				_, err := call.wait(background)
				if got := chordOf(t, err).Code; got != called {
					t.Fatalf("call %d rejected %v, want %v", index, got, called)
				}
			}
			// Their subscriptions ended: a new one is refused, and nothing more reaches the old listeners.
			_, err := refs[0].Events("paddle").On(func(contracts.EventDelivery[delta.JsonValue]) {}, background)
			_ = requireChordError(t, err, code, "")
			if terminal == "the host's close" {
				// Pi's `if (!peer.closed)`: the closed host closed its link; the peer has nothing to send on it.
				rhWaitFor(t, peer.Closed)
			} else {
				rsPaddle(t, peer, hold, 2)
				rhSync(t, peer)
				flushed(client)
			}
			if got := heard.all(); !slices.Equal(got, []string{"0:1", "1:1"}) {
				t.Fatalf("heard after the end = %v, want [0:1 1:1]", got)
			}
			if terminal != "the host's close" {
				must(t, client.Close(background))
			}
			_, _ = scope.wait()
		})
	}

	// remote-object.test.ts: references sharing one wire hold › events › two references subscribed to one member each get every event once; stopping one leaves the other's
	t.Run("two references subscribed to one member each get every event once; stopping one leaves the other's", func(t *testing.T) {
		rig, hold, a := rsMatch(t)
		b := rsMatchRef(t, rig.Client)
		seen := &rsHeard{}
		stopA := on(t, a, "paddle", seen.listener("a"), background)
		on(t, b, "paddle", seen.listener("b"), background)
		rsPaddle(t, rig.Peer, hold, 1)
		rsDelivered(t, rig)
		stopA.Stop()
		rsPaddle(t, rig.Peer, hold, 2)
		rsDelivered(t, rig)
		if got := seen.all(); !slices.Equal(got, []string{"a1", "b1", "b2"}) {
			t.Fatalf("seen = %v, want [a1 b1 b2]", got)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › events › an event in transit when a reference joins reaches it; one queued for the host's flush before it bound does not
	t.Run("an event in transit when a reference joins reaches it; one queued for the host's flush before it bound does not", func(t *testing.T) {
		rig, hold, a := rsMatch(t)
		seen := &rsHeard{}
		// In transit: sent before B joins, delivered after (one turn of the client, as Pi's synchronous lines).
		hostTurn(rig.Client, func() {
			rsPaddle(t, rig.Peer, hold, 1)
			b := rsMatchRef(t, rig.Client)
			on(t, b, "paddle", seen.listener("b"), background)
		})
		rsDelivered(t, rig)
		if got := seen.all(); !slices.Equal(got, []string{"b1"}) {
			t.Fatalf("seen = %v, want [b1]", got)
		}
		// Queued for the flush: C joins in A's listener of the first of two entries; it gets neither.
		var c *Reference
		on(t, a, "paddle", func(e contracts.EventDelivery[delta.JsonValue]) {
			if e.Value.(*delta.JsonObject).Value("y") != float64(2) || c != nil {
				return
			}
			c = rsMatchRef(t, rig.Client)
			on(t, c, "paddle", seen.listener("c"), background)
		}, background)
		rsPaddle(t, rig.Peer, hold, 2, 3)
		rsDelivered(t, rig)
		rsPaddle(t, rig.Peer, hold, 4)
		rsDelivered(t, rig)
		if got := seen.all(); !slices.Equal(got, []string{"b1", "b2", "b3", "b4", "c4"}) {
			t.Fatalf("seen = %v, want [b1 b2 b3 b4 c4]", got)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › events › a binding's event cutoff stays when another binding joins
	t.Run("a binding's event cutoff stays when another binding joins", func(t *testing.T) {
		rig, hold, a := rsMatch(t)
		seen := &rsHeard{}
		on(t, a, "paddle", seen.listener(""), background)
		// One turn, as Pi's synchronous `paddle(peer, hold, 1); client.hold(...)`.
		hostTurn(rig.Client, func() {
			rsPaddle(t, rig.Peer, hold, 1)
			rsMatchRef(t, rig.Client)
		})
		rsDelivered(t, rig)
		if got := seen.all(); !slices.Equal(got, []string{"1"}) {
			t.Fatalf("seen = %v, want [1]", got)
		}
	})
}
