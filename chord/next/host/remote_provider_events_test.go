package host

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/remote/provider.ts

// Ports the events section of packages/chord/test/remote/provider.test.ts (describe 'events at the provider (decision
// "Events: fire-and-forget members")'). Pi's single JS turn is a host section here (hostTurn): what a test emits and
// sets "in one turn" runs in one.

// providerTickSource is a host-lifetime event source, and providerTickView a remote view re-exposing it.
var providerTickSource = mustContract(contracts.Local("app.tick-source", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(Sample))},
}))

var providerTickView = mustContract(contracts.Remote("app.tick-view", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		Events: []contracts.Property{contracts.Prop("ticks", Sample)},
	})},
}))

// matchEvents are the event members' wire indexes of Match's player variant (sorted).
var matchEvents = map[string]int{"ball": 0, "paddle": 1}

// matchServerRaw is a server serving Match and a raw consumer, with the server's options.
type matchServerRaw struct {
	*rawLinkedHost
	Probe *matchProbe
}

func matchServer(t testing.TB, options rawLinkedOptions) *matchServerRaw {
	t.Helper()
	probe := &matchProbe{}
	linkedHost := rawLinked(t, func(host FacetHost) error { return serveMatch(host, probe) }, options)
	return &matchServerRaw{rawLinkedHost: linkedHost, Probe: probe}
}

// provHoldMatch holds Match {id: 1} as id (of origin when given).
func provHoldMatch(t testing.TB, peer *RawPeer, id int, origin ...int) {
	t.Helper()
	provHold(t, peer, id, specOf(Match), jsonObject("id", float64(1)), origin...)
}

// takenEvent is one `event` message a raw peer received, each entry with its value's `y` parsed.
type takenEvent struct {
	Origin link.Optional[int]
	Events []takenEntry
}

type takenEntry struct {
	Hold, Member, At int
	Y                any
}

// takeEvents is the `event` messages a raw peer received (taken).
func takeEvents(peer *RawPeer) []takenEvent {
	peer.t.Helper()
	peer.mu.Lock()
	var taken []*link.EventMessage
	rest := peer.received[:0:0]
	for _, message := range peer.received {
		if event, ok := message.(*link.EventMessage); ok {
			taken = append(taken, event)
		} else {
			rest = append(rest, message)
		}
	}
	peer.received = rest
	peer.mu.Unlock()
	var out []takenEvent
	for _, message := range taken {
		each := takenEvent{Origin: message.Origin, Events: []takenEntry{}}
		for _, entry := range message.Events {
			each.Events = append(each.Events, takenEntry{
				Hold: entry.Hold, Member: entry.Member, At: entry.At,
				Y: partValue(peer.t, entry.Value).(*delta.JsonObject).Value("y"),
			})
		}
		out = append(out, each)
	}
	return out
}

func eventHolds(taken []takenEvent) [][]int {
	out := [][]int{}
	for _, each := range taken {
		holds := []int{}
		for _, entry := range each.Events {
			holds = append(holds, entry.Hold)
		}
		out = append(out, holds)
	}
	return out
}

func eventYs(taken []takenEvent) [][]any {
	out := [][]any{}
	for _, each := range taken {
		ys := []any{}
		for _, entry := range each.Events {
			ys = append(ys, entry.Y)
		}
		out = append(out, ys)
	}
	return out
}

// kindsAfterHello are the kinds of the messages a raw peer received after its hello, without acks of its own.
func kindsAfterHello(peer *RawPeer) []string {
	return slices.DeleteFunc(peer.Kinds(), func(kind string) bool { return kind == "hello" })
}

func forgetReceived(peer *RawPeer) {
	peer.mu.Lock()
	peer.received = nil
	peer.mu.Unlock()
}

func requireEqual(t testing.TB, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func unavailable() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil)
}

// sentEventsOf collects, weakly, the provider sides of the host's links (origin) once.
func providerSidesOf(host Host) []weak.Pointer[providerSide] {
	var refs []weak.Pointer[providerSide]
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, each := range h.links {
			for _, key := range each.providers.keys() {
				side, _ := each.providers.get(key)
				refs = append(refs, weak.Make(side))
			}
		}
	})
	return refs
}

func TestProviderEvents(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "send a turn's events as one event message per link and origin, after the state published before the flush".
		t.Run("send a turn's events as one event message per link and origin, after the state published before the flush", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{})
			provHoldMatch(t, s.Peer, 0)
			provHoldMatch(t, s.Peer, 1)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			// Pi's `await peer.next` lets the peer's acks reach the host: both window slots (two `bound`) are free again before
			// the turn, so the state goes out at once and not behind the window.
			settle(s.Peer)
			forgetReceived(s.Peer)
			before := int(time.Now().UnixMilli())
			hostTurn(s.Server, func() {
				must(t, s.Probe.paddle(0).Emit(sample(1)))
				must(t, s.Probe.score(0).Set(1.0))
				must(t, s.Probe.paddle(0).Emit(sample(2)))
			})
			settle(s.Peer)
			requireEqual(t, "kinds", kindsAfterHello(s.Peer), []string{"state", "event"})
			taken := takeEvents(s.Peer)
			if len(taken) != 1 {
				t.Fatalf("event messages = %d, want 1", len(taken))
			}
			type entry struct {
				hold, member int
				y            any
			}
			var got []entry
			for _, each := range taken[0].Events {
				got = append(got, entry{each.Hold, each.Member, each.Y})
				if each.At < before {
					t.Fatalf("at = %d, want >= %d", each.At, before)
				}
			}
			p := matchEvents["paddle"]
			requireEqual(t, "entries", got, []entry{{0, p, 1.0}, {1, p, 1.0}, {0, p, 2.0}, {1, p, 2.0}})
		})

		// No Pi test. Pi's provider.ts (8d30e6306) sends a flush's events by `sendEvents` (lines 382-389) after only the event
		// window (`#eventsInFlight`, queueEvent 367-380; "Separate from the state window and its fence", 677), never looking
		// at the state window or its waiting holds (`#free`, 734): with the state window full the state waits for a slot and
		// the event message overtakes it. The same order here.
		t.Run("send a flush's events before the state held back by a full state window, which goes when a slot frees", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{NoAck: true})
			provHoldMatch(t, s.Peer, 0)
			provHoldMatch(t, s.Peer, 1)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			settle(s.Peer)
			forgetReceived(s.Peer)
			hostTurn(s.Server, func() {
				must(t, s.Probe.paddle(0).Emit(sample(1)))
				must(t, s.Probe.score(0).Set(1.0))
			})
			settle(s.Peer)
			requireEqual(t, "kinds with the window full", kindsAfterHello(s.Peer), []string{"event"})
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			requireEqual(t, "kinds after one ack", kindsAfterHello(s.Peer), []string{"event", "state"})
		})

		// Ports "send one message per origin, each with its origin".
		t.Run("send one message per origin, each with its origin", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{Parent: true})
			provHoldMatch(t, s.Peer, 0)
			provHoldMatch(t, s.Peer, 0, 3)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(1))) })
			settle(s.Peer)
			var got [][2]int
			for _, each := range takeEvents(s.Peer) {
				origin := -1
				if each.Origin.Ok {
					origin = each.Origin.Value
				}
				got = append(got, [2]int{origin, len(each.Events)})
			}
			requireEqual(t, "origins", got, [][2]int{{-1, 1}, {3, 1}})
		})

		// Ports "send nothing to a variant without the member, and none before a bound the state window holds back".
		t.Run("send nothing to a variant without the member, and none before a bound the state window holds back", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{NoAck: true, Child: ownerPrincipal()})
			provHoldMatch(t, s.Peer, 0)
			s.Peer.Next("bound")
			// The window's two slots: the bound and a state, unacknowledged; a new hold's bound waits.
			hostTurn(s.Server, func() { must(t, s.Probe.score(0).Set(1.0)) })
			s.Peer.Next("state")
			provHoldMatch(t, s.Peer, 1)
			settle(s.Peer)
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(1))) })
			settle(s.Peer)
			requireEqual(t, "holds", eventHolds(takeEvents(s.Peer)), [][]int{{0}})
			s.Peer.Ack(link.Optional[int]{})
			s.Peer.Next("bound")
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(2))) })
			settle(s.Peer)
			requireEqual(t, "holds", eventHolds(takeEvents(s.Peer)), [][]int{{0, 1}})
		})

		// Ports "send nothing for an emit its object fails after in the same turn: no event to a binding not shown live".
		t.Run("send nothing for an emit its object fails after in the same turn: no event to a binding not shown live", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{})
			provHoldMatch(t, s.Peer, 0)
			s.Peer.Next("bound")
			hostTurn(s.Server, func() {
				must(t, s.Probe.paddle(0).Emit(sample(1)))
				failLive(s.Probe.self(0), unavailable())
			})
			s.Peer.Next("failed")
			settle(s.Peer)
			if got := takeEvents(s.Peer); len(got) != 0 {
				t.Fatalf("events = %v, want none", got)
			}
		})

		// Ports "send nothing re-exposed: an emit its source fails after in the same turn reaches no hold of the view".
		t.Run("send nothing re-exposed: an emit its source fails after in the same turn reaches no hold of the view", func(t *testing.T) {
			var mu sync.Mutex
			var sources []contracts.EventSource[delta.JsonValue]
			var selves []Self
			s := rawLinked(t, func(host FacetHost) error {
				if err := host.Serve(providerTickSource, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					ticks := self.Events()
					mu.Lock()
					sources = append(sources, ticks)
					selves = append(selves, self)
					mu.Unlock()
					return contracts.Implementation{Members: contracts.VariantImplementation{
						Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
					}}, nil
				}, ServeOptions{}); err != nil {
					return err
				}
				return host.Serve(providerTickView, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": self.Get(providerTickSource, jsonObject()).Events("ticks")},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(providerTickView), jsonObject())
			s.Peer.Next("bound")
			mu.Lock()
			source, self := sources[0], selves[0]
			mu.Unlock()
			hostTurn(s.Server, func() { must(t, source.Emit(sample(1))) })
			settle(s.Peer)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{1.0}})
			hostTurn(s.Server, func() {
				must(t, source.Emit(sample(2)))
				failLive(self, unavailable())
			})
			settle(s.Peer)
			if got := takeEvents(s.Peer); len(got) != 0 {
				t.Fatalf("events = %v, want none", got)
			}
		})

		// Ports "keep state and event credits apart: events flow while the state window is full, and the reverse".
		t.Run("keep state and event credits apart: events flow while the state window is full, and the reverse", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{NoAck: true, EventWindow: intp(1)})
			provHoldMatch(t, s.Peer, 0)
			s.Peer.Next("bound")
			hostTurn(s.Server, func() { must(t, s.Probe.score(0).Set(1.0)) })
			s.Peer.Next("state")
			hostTurn(s.Server, func() { must(t, s.Probe.score(0).Set(2.0)) })
			// State waits for a slot; an event goes.
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(1))) })
			settle(s.Peer)
			if got := len(takeEvents(s.Peer)); got != 1 {
				t.Fatalf("events = %d, want 1", got)
			}
			states := func() int { return countKinds(kindsAfterHello(s.Peer), "state") }
			if states() != 0 {
				t.Fatalf("states = %d, want none yet", states())
			}
			// The event window is full now; state acks open the state window only.
			s.Peer.Ack(link.Optional[int]{})
			s.Peer.Ack(link.Optional[int]{})
			settle(s.Peer)
			if states() != 1 {
				t.Fatalf("states = %d, want 1", states())
			}
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(2))) })
			settle(s.Peer)
			if got := takeEvents(s.Peer); len(got) != 0 {
				t.Fatalf("events = %v, want none", got)
			}
			// An event ack frees the event window, not a state slot.
			s.Peer.Send(&link.AckMessage{Events: link.Some(1)})
			settle(s.Peer)
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(3))) })
			settle(s.Peer)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{3.0}})
		})

		// Ports "drop a flush's events while the event window is full, counted, and send again once acknowledged".
		t.Run("drop a flush's events while the event window is full, counted, and send again once acknowledged", func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			s := matchServer(t, rawLinkedOptions{EventWindow: intp(2), Telemetry: recorder})
			provHoldMatch(t, s.Peer, 0)
			s.Peer.Next("bound")
			counts := func() map[string]float64 {
				result := map[string]float64{}
				for _, measurement := range recorder.GetMetrics() {
					key := measurement.Name
					if reason, ok := measurement.Attributes.Get("reason"); ok {
						key += "." + fmt.Sprint(reason)
					}
					result[key] += measurement.Value
				}
				return result
			}
			for _, y := range []float64{1, 2, 3} {
				hostTurn(s.Server, func() {
					must(t, s.Probe.paddle(0).Emit(sample(y)))
					must(t, s.Probe.paddle(0).Emit(sample(y*10)))
				})
				settle(s.Peer)
			}
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{1.0, 10.0}, {2.0, 20.0}})
			// The dropped round encoded nothing: the window is decided before a value is encoded.
			got := counts()
			for name, want := range map[string]float64{
				"chord.events.emitted": 6, "chord.events.sent": 4, "chord.events.dropped.window": 2, "chord.events.encodes": 4,
			} {
				if got[name] != want {
					t.Fatalf("%s = %v, want %v (all %v)", name, got[name], want, got)
				}
			}
			s.Peer.Send(&link.AckMessage{Events: link.Some(2)})
			settle(s.Peer)
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(4))) })
			settle(s.Peer)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{4.0}})
		})

		// Ports "encode each value once per representation: 1 and 10 holders, one more for a mapped version; never walked or copied".
		t.Run("encode each value once per representation: 1 and 10 holders, one more for a mapped version; never walked or copied", func(t *testing.T) {
			meter := nexttest.Metered(t)
			var mu sync.Mutex
			var paddles []contracts.EventSource[delta.JsonValue]
			s := rawLinked(t, func(host FacetHost) error {
				return host.Serve(MatchV2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					paddle := self.Events()
					mu.Lock()
					paddles = append(paddles, paddle)
					mu.Unlock()
					return contracts.Implementation{Variant: "player", Members: contracts.VariantImplementation{
						State:   map[string]chord.Watchable[delta.JsonValue]{"score": self.Derive([]any{self.Writable(0.0)}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
						Events:  map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle, "ball": self.Events()},
						Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
					}}, nil
				}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
					VariantEvents: []contracts.Entry[[]contracts.Entry[contracts.EventMapping]]{{Name: "player", Value: []contracts.Entry[contracts.EventMapping]{{
						Name: "paddle", Value: contracts.EventMapping{Func: func(current delta.JsonValue) (delta.JsonValue, error) {
							return jsonObject("y", current.(*delta.JsonObject).Value("y")), nil
						}},
					}}}},
				}}}})
			}, rawLinkedOptions{Telemetry: meter.Telemetry})
			encodes := func(y float64) float64 {
				before := meter.Counts()["chord.events.encodes"]
				mu.Lock()
				paddle := paddles[0]
				mu.Unlock()
				hostTurn(s.Server, func() {
					must(t, paddle.Emit(jsonObject("y", y, "t", 0.0)))
				})
				settle(s.Peer)
				return meter.Counts()["chord.events.encodes"] - before
			}
			provHold(t, s.Peer, 0, specOf(MatchV2), jsonObject("id", float64(1)))
			s.Peer.Next("bound")
			if got := encodes(1); got != 1 {
				t.Fatalf("encodes = %v, want 1", got)
			}
			// Bindings waiting for a slot go out together: the holds' entries, not their messages, are counted.
			bound := func() int {
				count := 0
				for _, message := range s.Peer.Received() {
					if b, ok := message.(*link.BoundMessage); ok {
						count += len(b.Holds)
					}
				}
				return count
			}
			for id := 1; id < 10; id++ {
				provHold(t, s.Peer, id, specOf(MatchV2), jsonObject("id", float64(1)))
			}
			settle(s.Peer)
			if bound() != 9 {
				t.Fatalf("bound = %d, want 9", bound())
			}
			if got := encodes(2); got != 1 {
				t.Fatalf("encodes = %v, want 1", got)
			}
			// An older consumer: its mapped values are a representation of their own, encoded once for all its holds.
			provHoldMatch(t, s.Peer, 10)
			provHoldMatch(t, s.Peer, 11)
			settle(s.Peer)
			if bound() != 11 {
				t.Fatalf("bound = %d, want 11", bound())
			}
			takeEvents(s.Peer)
			if got := encodes(3); got != 2 {
				t.Fatalf("encodes = %v, want 2", got)
			}
			taken := takeEvents(s.Peer)
			var older []any
			for _, entry := range taken[0].Events {
				if entry.Hold >= 10 {
					older = append(older, entry.Y)
				}
			}
			requireEqual(t, "older holds' ys", older, []any{3.0, 3.0})
		})

		// Ports "retire a provider side's record only once its event tickets drained (GC)".
		t.Run("retire a provider side's record only once its event tickets drained (GC)", func(t *testing.T) {
			s := matchServer(t, rawLinkedOptions{})
			provHoldMatch(t, s.Peer, 0)
			provHoldMatch(t, s.Peer, 1)
			s.Peer.Next("bound")
			s.Peer.Next("bound")
			hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(1))) })
			settle(s.Peer)
			if got := len(takeEvents(s.Peer)); got != 1 {
				t.Fatalf("events = %d, want 1", got)
			}
			kept := providerSidesOf(s.Server)
			// The test keeps no attempt (its source): an ended attempt kept by a test keeps its object's past.
			s.Probe.mu.Lock()
			s.Probe.paddles, s.Probe.scores, s.Probe.selves = nil, nil, nil
			s.Probe.mu.Unlock()
			s.Peer.Send(&link.ReleaseMessage{Hold: 0})
			s.Peer.Send(&link.ReleaseMessage{Hold: 1})
			settle(s.Peer)
			// Nothing held, its event message unacknowledged: the side stays until its ack.
			if !slices.ContainsFunc(kept, func(ref weak.Pointer[providerSide]) bool { return ref.Value() != nil }) {
				t.Fatal("the provider side went before its event ack")
			}
			s.Peer.Send(&link.AckMessage{Events: link.Some(1)})
			settle(s.Peer)
			nexttest.ExpectCollected(t, kept...)
			if s.Peer.Closed() {
				t.Fatal("the link closed")
			}
		})
	})
}

func countKinds(kinds []string, kind string) int {
	count := 0
	for _, each := range kinds {
		if each == kind {
			count++
		}
	}
	return count
}
