package host

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports packages/chord/test/next/remote/relay.test.ts: relayed holds, with a server routing to raw workers and a raw tab.

// rawRelayHost is a server routing to raw workers, and a raw tab on a link it accepted.
type rawRelayHost struct {
	*relayed
	Workers *rawWorkers
	Raw     *RawPeer
}

// rawRelayOptions is the options of rawRelay: Pi's `{ principal, calls }` and the fake timers' loop.
type rawRelayOptions struct {
	Principal data.Principal
	Calls     int
	Loop      sectionLoop
}

func rawRelay(t testing.TB, options rawRelayOptions) *rawRelayHost {
	t.Helper()
	workers := newRawWorkers(t, rawWorkerOptions{Calls: options.Calls})
	rel := relay(t, relayedOptions{Resolve: workers.Resolve, Loop: options.Loop})
	return &rawRelayHost{relayed: rel, Workers: workers, Raw: rawTab(t, rel.Server, options.Principal, link.PartLimits{})}
}

// rawTab is a raw tab on a new link the server accepts as principal (owner by default).
func rawTab(t testing.TB, server Host, principal data.Principal, limits link.PartLimits) *RawPeer {
	t.Helper()
	a, b := link.MemoryLinkPair()
	if principal == nil {
		principal = ownerPrincipal()
	}
	must(t, server.Accept(a, LinkOptions{Principal: principal, Codec: testCodec(), Limits: limits}, background))
	return newRawPeer(t, b, rawPeerOptions{})
}

// placedHold is a hold of PlacedNotes for session with id hold, naming its address.
func placedHold(t testing.TB, hold int, session string) *link.HoldMessage {
	t.Helper()
	spec := specOf(PlacedNotes)
	return &link.HoldMessage{
		Hold: hold, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
		Params:  mustJSONPart(t, jsonObject("session", session)),
		Address: link.WireAddress{Kind: "session", Params: jsonObject("id", session)},
	}
}

// holdPlaced sends a hold of PlacedNotes for session ("S1" when empty) with id hold, naming its address.
func holdPlaced(t testing.TB, peer *RawPeer, hold int, session ...string) {
	t.Helper()
	name := "S1"
	if len(session) > 0 {
		name = session[0]
	}
	peer.Send(placedHold(t, hold, name))
}

// heldHold is a hold as a raw worker received it: its id and its origin, which every answer names.
type heldHold struct {
	hold   int
	origin link.Optional[int]
}

func heldOf(message link.Message) heldHold {
	held := message.(*link.HoldMessage)
	return heldHold{hold: held.Hold, origin: held.Origin}
}

// nextHeld is the next hold a raw worker received.
func nextHeld(peer *RawPeer) heldHold { return heldOf(peer.Next("hold")) }

// bindPlaced binds held on a raw worker with PlacedNotes's editor (or reader) variant and count.
func bindPlaced(t testing.TB, peer *RawPeer, held heldHold, count float64, variant wireIndexes) {
	t.Helper()
	peer.Send(&link.BoundMessage{Origin: held.origin, Holds: []link.BoundEntry{{
		Hold: held.hold, Variant: variant.Variant,
		Members: []link.BoundMember{
			{Member: variant.State["notes"], Value: link.Text("[]")},
			{Member: variant.State["count"], Value: mustJSONPart(t, count)},
		},
		Failed: []link.FailedMember{},
	}}})
}

// bindEditor binds held with the editor variant and count 0 (`bindPlaced(peer, held)`).
func bindEditor(t testing.TB, peer *RawPeer, held heldHold) {
	t.Helper()
	bindPlaced(t, peer, held, 0, editorWire(t))
}

// countState is a `state` of count for holds (of one origin) on a raw worker.
func countState(t testing.TB, holds []heldHold, count float64) *link.StateMessage {
	t.Helper()
	editor := editorWire(t)
	ops := mustJSONPart(t, []any{[]any{"r", count}})
	entries := make([]link.StateEntry, len(holds))
	for index, held := range holds {
		entries[index] = stateEntry(held.hold, []link.StateMember{{Member: editor.State["count"], Ops: ops}}, nil)
	}
	return &link.StateMessage{Origin: holds[0].origin, Holds: entries}
}

func countKind(peer *RawPeer, kind string) int {
	count := 0
	for _, each := range peer.Kinds() {
		if each == kind {
			count++
		}
	}
	return count
}

// settleRelay is `await transported()` over everything a rawRelay owns.
func settleRelay(r *rawRelayHost) {
	flushRelay(r.relayed, r.Workers, r.Raw.link.(*link.MemoryLink))
}

// flushRelay is `await transported()` over everything a relay with raw workers owns, and the extra ends: the hops of a
// message through the relay (tab, server, worker and back) each need a round.
func flushRelay(rel *relayed, workers *rawWorkers, extra ...*link.MemoryLink) {
	for range 4 {
		rel.Transported()
		transported(extra...)
		for _, peer := range workers.Peers() {
			transported(peer.link.(*link.MemoryLink))
		}
	}
}

func goneMessage(origin link.Optional[int], hold int, message string) *link.GoneMessage {
	return &link.GoneMessage{Origin: origin, Hold: hold, Error: jsonObject("kind", "chord", "error",
		jsonObject("code", "unavailable", "message", message))}
}

func TestRelayIDsAndOrigins(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		add, wait := editor.Methods["add"], editor.Methods["wait"]

		// Ports relay.test.ts "ids and origins › keep the downstream ids upstream, under the downstream link's origin; answers come back by it".
		t.Run("keep the downstream ids upstream, under the downstream link's origin; answers come back by it", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 7)
			worker := r.Workers.At(t, 0)
			message := worker.Next("hold").(*link.HoldMessage)
			if message.Hold != 7 || message.Origin != link.Some(1) {
				t.Fatalf("worker hold = %d origin %+v, want 7 under 1", message.Hold, message.Origin)
			}
			held := heldOf(message)
			bindPlaced(t, worker, held, 3, editor)
			bound := r.Raw.Next("bound").(*link.BoundMessage)
			if bound.Origin.Ok || bound.Holds[0].Hold != 7 || bound.Holds[0].Variant != editor.Variant {
				t.Fatalf("bound = %+v, want hold 7 of the editor variant without origin", bound)
			}
			provCall(t, r.Raw, 5, 7, add, jsonObject("text", "x"))
			// Calls are forwarded at once, with the same id.
			forwarded := worker.Next("call").(*link.CallMessage)
			if forwarded.Origin != link.Some(1) || forwarded.Call != 5 || forwarded.Hold != 7 || forwarded.Method != add {
				t.Fatalf("forwarded call = %+v, want call 5 on hold 7 method %d under 1", forwarded, add)
			}
			worker.Send(&link.ResultMessage{Origin: link.Some(1), Call: 5, Value: link.Text("1")})
			result := r.Raw.Next("result").(*link.ResultMessage)
			if result.Call != 5 || result.Origin.Ok {
				t.Fatalf("result = %+v, want call 5 without origin", result)
			}
			r.Raw.Send(&link.RetryMessage{Hold: 7})
			retry := worker.Next("retry").(*link.RetryMessage)
			if retry.Origin != link.Some(1) || retry.Hold != 7 || retry.Operation != "" || retry.Through != nil {
				t.Fatalf("forwarded retry = %+v, want a bare retry of hold 7 under 1", retry)
			}
		})

		// Ports "give each downstream link its own origin, never reused".
		t.Run("give each downstream link its own origin, never reused", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 0)
			worker := r.Workers.At(t, 0)
			one := nextHeld(worker)
			other := rawTab(t, r.Server, nil, link.PartLimits{})
			holdPlaced(t, other, 0)
			two := nextHeld(worker)
			if one.origin != link.Some(1) || two.origin != link.Some(2) {
				t.Fatalf("origins = %+v and %+v, want 1 and 2", one.origin, two.origin)
			}
			other.Close()
			settleRelay(r)
			holdPlaced(t, rawTab(t, r.Server, nil, link.PartLimits{}), 0)
			if third := nextHeld(worker); third.origin != link.Some(3) {
				t.Fatalf("third origin = %+v, want 3", third.origin)
			}
		})

		// Ports "drop entries, notices and replies for holds of the origin relayed over another link".
		t.Run("drop entries, notices and replies for holds of the origin relayed over another link", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 0, "S1")
			// Pi calls `resolve` for an address synchronously when its first hold arrives, so its workers come in the order of the
			// holds. Here each address resolves on an owned task of its own (`Resolve` blocks): the second hold waits for the first
			// worker, whose index is then the hold's.
			one := r.Workers.At(t, 0)
			x := nextHeld(one)
			holdPlaced(t, r.Raw, 1, "S2")
			two := r.Workers.At(t, 1)
			y := nextHeld(two)
			if x.origin != link.Some(1) || y.origin != link.Some(1) {
				t.Fatalf("origins = %+v and %+v, want 1 and 1", x.origin, y.origin)
			}
			bindEditor(t, one, x)
			r.Raw.Next("bound")
			provCall(t, r.Raw, 0, 0, wait, nil)
			one.Next("call")
			// Worker two speaks for worker one's hold of the same tab origin.
			two.Send(countState(t, []heldHold{x}, 9))
			two.Send(&link.LoadingMessage{Origin: link.Some(1), Hold: 0})
			two.Send(&link.ResultMessage{Origin: link.Some(1), Call: 0})
			two.Send(goneMessage(link.Some(1), 0, "forged"))
			bindEditor(t, two, x)
			settleRelay(r)
			requireStrings(t, r.Raw.Kinds(), []string{"hello"})
			// Its windowed messages, left empty, are acknowledged to it at once.
			requireStrings(t, repeatKind(countKind(two, "ack"), "ack"), []string{"ack", "ack"})
			// A message with its own hold and the other's: only its own entry is forwarded.
			bindEditor(t, two, y)
			r.Raw.Next("bound")
			both := &link.StateMessage{Origin: link.Some(1), Holds: append(countState(t, []heldHold{x}, 9).Holds, countState(t, []heldHold{y}, 3).Holds...)}
			two.Send(both)
			state := r.Raw.Next("state").(*link.StateMessage)
			if len(state.Holds) != 1 || state.Holds[0].Hold != 1 {
				t.Fatalf("forwarded state holds = %+v, want [1]", state.Holds)
			}
			one.Send(&link.ResultMessage{Origin: link.Some(1), Call: 0})
			if result := r.Raw.Next("result").(*link.ResultMessage); result.Call != 0 {
				t.Fatalf("result call = %d, want 0", result.Call)
			}
		})

		// Ports "ignore answers for an origin from a link it relayed nothing to".
		t.Run("ignore answers for an origin from a link it relayed nothing to", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 0)
			worker := r.Workers.At(t, 0)
			held := nextHeld(worker)
			// Another tab speaks for the first tab's origin on its own link.
			other := rawTab(t, r.Server, nil, link.PartLimits{})
			settleRelay(r)
			bindEditor(t, other, held)
			settleRelay(r)
			requireStrings(t, r.Raw.Kinds(), []string{"hello"})
			bindEditor(t, worker, held)
			r.Raw.Next("bound")
		})
	})
}

func repeatKind(count int, kind string) []string {
	kinds := make([]string, count)
	for index := range kinds {
		kinds[index] = kind
	}
	return kinds
}

// unparsablePart is a part of a message that is no JSON, which travels where nothing parses it: a relay, or the
// worker's raw peer. The text codec cannot carry it; it carries a corrupt deflated part instead.
func unparsablePart() link.Part {
	if testCodec() == link.TextCodec {
		return link.NewDeflatedPart([]byte{1, 2, 3})
	}
	return link.Bytes([]byte("{not json"))
}

// unparsablePlainPart is unparsablePart for what a child link never sends deflated (params and arguments): in the text
// codec, which cannot carry bytes that are no JSON, it is plain JSON and proves nothing there.
func unparsablePlainPart(plain string) link.Part {
	if testCodec() == link.TextCodec {
		return link.Text(plain)
	}
	return link.Bytes([]byte("{not json"))
}

func TestRelayPartsAreNeverParsed(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		// Ports "parts at a relay › are never parsed: params, snapshots, ops, arguments and results travel as they came".
		// Pi counts the calls of link/values.ts's parsePart and parsePayload through a module mock; Go cannot patch a
		// package function, so the property is checked by parts a parse would refuse (no JSON, a corrupt deflate): a
		// relay that parsed or inflated one would close the link with a protocol error instead of forwarding it.
		t.Run("are never parsed: params, snapshots, ops, arguments and results travel as they came", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			settleRelay(r)
			unparsed := unparsablePart()
			parent := r.Raw
			message := placedHold(t, 0, "S1")
			// Params travel as they came: a part no parse could read.
			message.Params = unparsablePlainPart(`{"session":"S1"}`)
			parent.Send(message)
			worker := r.Workers.At(t, 0)
			held := heldOf(worker.Next("hold"))
			worker.Send(&link.BoundMessage{Origin: held.origin, Holds: []link.BoundEntry{{
				Hold: held.hold, Variant: editor.Variant, Failed: []link.FailedMember{},
				Members: []link.BoundMember{{Member: editor.State["notes"], Value: unparsablePart()}, {Member: editor.State["count"], Value: mustJSONPart(t, 1.0)}},
			}}})
			bound := parent.Next("bound").(*link.BoundMessage).Holds[0]
			if !reflectEqualPart(bound.Members[0].Value, unparsed) {
				t.Fatalf("snapshot part = %v, want it as it came", bound.Members[0].Value)
			}
			worker.Send(&link.StateMessage{Origin: held.origin, Holds: []link.StateEntry{
				stateEntry(held.hold, []link.StateMember{{Member: editor.State["count"], Ops: unparsablePart()}}, nil),
			}})
			state := parent.Next("state").(*link.StateMessage).Holds[0]
			if !reflectEqualPart(state.Members[0].Ops, unparsed) {
				t.Fatalf("ops part = %v, want it as it came", state.Members[0].Ops)
			}
			args := unparsablePlainPart(`{"text":"x"}`)
			parent.Send(&link.CallMessage{Call: 0, Hold: 0, Method: editor.Methods["add"], Args: args})
			call := worker.Next("call").(*link.CallMessage)
			if !reflectEqualPart(call.Args, args) {
				t.Fatalf("args part = %v, want it as it came", call.Args)
			}
			worker.Send(&link.ResultMessage{Origin: held.origin, Call: 0, Value: unparsablePart()})
			result := parent.Next("result").(*link.ResultMessage)
			if !reflectEqualPart(result.Value, unparsed) {
				t.Fatalf("result part = %v, want it as it came", result.Value)
			}
			settleRelay(r)
			if parent.Closed() || worker.Closed() {
				t.Fatal("a link closed: the relay parsed a part")
			}
			if r.Errors.Server.Len() != 0 {
				t.Fatalf("server errors = %v, want none", r.Errors.Server.All())
			}
		})
	})
}

// reflectEqualPart reports whether two parts are the same bytes.
func reflectEqualPart(a, b link.Part) bool {
	switch typed := a.(type) {
	case link.Bytes:
		other, ok := b.(link.Bytes)
		return ok && slices.Equal(typed, other)
	case *link.DeflatedPart:
		other, ok := b.(*link.DeflatedPart)
		return ok && slices.Equal(typed.Bytes, other.Bytes)
	case link.Text:
		other, ok := b.(link.Text)
		return ok && typed == other
	}
	return false
}

// pausableLink is a link end whose incoming messages can be held back (a slow tab): paused, they wait; resumed, they
// arrive in order. kinds lists what arrived, decoded with the test codec.
type pausableLink struct {
	link.Link
	mu       sync.Mutex
	paused   bool
	held     [][]byte
	kinds    []string
	listener func(message []byte)
}

func newPausableLink(end link.Link) *pausableLink {
	p := &pausableLink{Link: end}
	end.OnMessage(func(message []byte) {
		kind := "?"
		if decoded, err := testCodec().Decode(message); err == nil {
			kind = decoded.Kind()
		}
		p.mu.Lock()
		p.kinds = append(p.kinds, kind)
		if p.paused || p.listener == nil {
			// Before the host listens, messages wait as a memory link's do.
			p.held = append(p.held, message)
			p.mu.Unlock()
			return
		}
		listener := p.listener
		p.mu.Unlock()
		listener(message)
	})
	return p
}

func (p *pausableLink) OnMessage(listener func(message []byte)) {
	p.mu.Lock()
	p.listener = listener
	var early [][]byte
	if !p.paused {
		early, p.held = p.held, nil
	}
	p.mu.Unlock()
	for _, message := range early {
		listener(message)
	}
}

func (p *pausableLink) pause() {
	p.mu.Lock()
	p.paused = true
	p.mu.Unlock()
}

func (p *pausableLink) resume() {
	p.mu.Lock()
	p.paused = false
	held := p.held
	p.held = nil
	listener := p.listener
	p.mu.Unlock()
	for _, message := range held {
		listener(message)
	}
}

func (p *pausableLink) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.kinds...)
}

func (p *pausableLink) forget() {
	p.mu.Lock()
	p.kinds = nil
	p.mu.Unlock()
}

func TestRelayWindow(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {

		// Ports "the window through a relay › forwards every bound and state at once and relays each ack to its worker".
		t.Run("forwards every bound and state at once and relays each ack to its worker", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{NoAck: true})
			rel := relay(t, relayedOptions{Resolve: workers.Resolve})
			a, b := link.MemoryLinkPair()
			must(t, rel.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			tab := newRawPeer(t, b, rawPeerOptions{NoAck: true})
			holdPlaced(t, tab, 0)
			worker := workers.At(t, 0)
			held := nextHeld(worker)
			bindEditor(t, worker, held)
			for _, count := range []float64{1, 2, 3} {
				worker.Send(countState(t, []heldHold{held}, count))
			}
			flushRelay(rel, workers, a, b)
			// The relay holds nothing back: the worker's window for this origin is the limit.
			requireStrings(t, tab.Kinds(), []string{"hello", "bound", "state", "state", "state"})
			if acks := countKind(worker, "ack"); acks != 0 {
				t.Fatalf("worker acks = %d, want 0", acks)
			}
			tab.Ack(link.Optional[int]{})
			tab.Ack(link.Optional[int]{})
			flushRelay(rel, workers, a, b)
			var acks []*link.AckMessage
			for _, message := range worker.Received() {
				if ack, ok := message.(*link.AckMessage); ok {
					acks = append(acks, ack)
				}
			}
			if len(acks) != 2 || acks[0].Origin != held.origin || acks[1].Origin != held.origin {
				t.Fatalf("worker acks = %+v, want two for origin %+v", acks, held.origin)
			}
		})

		// Ports "acknowledges to the right worker in order: two workers sharing one tab".
		t.Run("acknowledges to the right worker in order: two workers sharing one tab", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{NoAck: true})
			rel := relay(t, relayedOptions{Resolve: workers.Resolve})
			a, b := link.MemoryLinkPair()
			must(t, rel.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			tab := newRawPeer(t, b, rawPeerOptions{NoAck: true})
			flush := func() { flushRelay(rel, workers, a, b) }
			holdPlaced(t, tab, 0, "S1")
			one := workers.At(t, 0)
			x := nextHeld(one)
			holdPlaced(t, tab, 1, "S2")
			two := workers.At(t, 1)
			y := nextHeld(two)
			bindEditor(t, one, x)
			flush()
			bindEditor(t, two, y)
			flush()
			tab.Ack(link.Optional[int]{})
			flush()
			if got := countKind(one, "ack"); got != 1 {
				t.Fatalf("first worker acks = %d, want 1", got)
			}
			if got := countKind(two, "ack"); got != 0 {
				t.Fatalf("second worker acks = %d, want 0", got)
			}
			tab.Ack(link.Optional[int]{})
			flush()
			if got := countKind(two, "ack"); got != 1 {
				t.Fatalf("second worker acks = %d, want 1", got)
			}
		})

		// Ports "never tears a commit: a slow tab's states fold at the worker, a derive over two holds sees both at one moment (race 21)".
		t.Run("never tears a commit: a slow tab's states fold at the worker, a derive over two holds sees both at one moment (race 21)", func(t *testing.T) {
			Pair := mustContract(contracts.Local("app.pair", contracts.LocalDeclaration{
				Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("pair", contracts.Str())},
			}))
			rel := relay(t, relayedOptions{})
			seen := newEventLog()
			tab := newHost(t, HostOptions{OnError: func(HostError) {}})
			a, b := link.MemoryLinkPair()
			must(t, rel.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			slow := newPausableLink(b)
			must(t, tab.Connect(slow, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			must(t, tab.Load([]HostFacet{{Name: "@test/pair", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				return h.Serve(Pair, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					one := self.Get(PlacedNotes, jsonObject("session", "S1"))
					two := self.Get(PlacedNotes, jsonObject("session", "S1"))
					return contracts.Implementation{Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"pair": self.Derive([]any{one.Member("count"), two.Member("count")}, func(values []any) (delta.JsonValue, error) {
								pair := formatNumber(values[0].(float64)) + "/" + formatNumber(values[1].(float64))
								seen.add(pair)
								return pair, nil
							}),
						},
					}}, nil
				}, ServeOptions{})
			})}}, background))
			view, err := tab.Get(Pair, jsonObject(), background, GetOptions{})
			must(t, err)
			watch, err := view.Member("pair").Watch(background)
			must(t, err)
			watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
			worker := rel.Worker(Session.Address(jsonObject("id", "S1")).Key)
			slow.pause()
			slow.forget()
			for _, text := range []string{"a", "b", "c", "d", "e"} {
				addNote(t, worker, "S1", text)
			}
			flush := func() {
				flushOwn(rel)
				transported(a, b)
			}
			flush()
			// Two states in the tab origin's window at the worker, the other commits folded there.
			requireStrings(t, slow.seen(), []string{"state", "state"})
			slow.resume()
			flush()
			flush()
			nexttest.RequireJSONEqual(t, memberValue(t, view, "pair"), "5/5")
			requireStrings(t, slow.seen(), []string{"state", "state", "state"})
			for _, pair := range seen.all() {
				if len(pair) < 3 || pair[:1] != pair[2:] {
					t.Fatalf("a derive saw half a commit: %s", pair)
				}
			}
			if rel.Errors.Server.Len() != 0 {
				t.Fatalf("server errors = %v, want none", rel.Errors.Server.All())
			}
		})
	})
}

func TestRelayUpstreamDrop(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		reader := wireNames(t, PlacedNotes, "reader")

		// Ports "an upstream drop › sends loading downstream, fails relayed calls unavailable, holds again with the same id and forwards the new bound, another variant allowed (race 12)".
		t.Run("sends loading downstream, fails relayed calls unavailable, holds again with the same id and forwards the new bound, another variant allowed (race 12)", func(t *testing.T) {
			loop := newScopeLoop()
			r := rawRelay(t, rawRelayOptions{Loop: loop})
			got := asyncGet(r.Tab, PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			first := r.Workers.At(t, 0)
			held := nextHeld(first)
			bindPlaced(t, first, held, 1, editor)
			notes := awaitReference(t, got)
			if variant, _, _ := notes.Variant(); variant != "editor" {
				t.Fatalf("variant = %q, want editor", variant)
			}
			call := startCall(t, notes, "wait", nil, background)
			first.Next("call")
			first.Close()
			_, err := call.wait(background)
			_ = requireChordError(t, err, data.CodeUnavailable, "")
			settleRelay(r)
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state = %s, want loading", kind)
			}
			loop.AdvanceAsync(1000)
			second := r.Workers.At(t, 1)
			again := nextHeld(second)
			if again.hold != held.hold || again.origin != held.origin {
				t.Fatalf("re-held = %+v, want %+v", again, held)
			}
			bindPlaced(t, second, again, 4, reader)
			settleRelay(r)
			if kind := refStateKind(notes); kind != RefStateLive {
				t.Fatalf("state = %s, want live", kind)
			}
			if variant, _, _ := notes.Variant(); variant != "reader" {
				t.Fatalf("variant = %q, want reader", variant)
			}
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 4.0)
			if r.Errors.Tab.Len() != 0 {
				t.Fatalf("tab errors = %v, want none", r.Errors.Tab.All())
			}
		})

		// Ports "answers a call that crossed the loading notice unavailable (race 13)".
		t.Run("answers a call that crossed the loading notice unavailable (race 13)", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 0)
			worker := r.Workers.At(t, 0)
			bindEditor(t, worker, nextHeld(worker))
			r.Raw.Next("bound")
			worker.Close()
			settleRelay(r)
			provCall(t, r.Raw, 0, 0, editor.Methods["wait"], nil)
			if loading := r.Raw.Next("loading").(*link.LoadingMessage); loading.Hold != 0 || loading.Origin.Ok {
				t.Fatalf("loading = %+v, want hold 0 without origin", loading)
			}
			requireChordWire(t, r.Raw.Next("error").(*link.ErrorMessage).Error, "unavailable", "")
		})
	})
}

func TestRelayParentLinkThatIsDown(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "a parent link that is down (audit) › holds a hold arriving meanwhile on the parent path: it waits loading and binds on the next parent link".
		t.Run("holds a hold arriving meanwhile on the parent path: it waits loading and binds on the next parent link", func(t *testing.T) {
			server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
			must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				_, err := serveNotes(h, nil)
				return err
			})}}, background))
			middle := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}})
			must(t, middle.Load(nil, background))
			// up connects the middle host to the server over a new link; it returns both ends.
			up := func() (*link.MemoryLink, *link.MemoryLink) {
				a, b := link.MemoryLinkPair()
				must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
				must(t, middle.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
				return a, b
			}
			first, firstEnd := up()
			transported(first, firstEnd)
			first.Close()
			transported(first, firstEnd)
			tab := newHost(t, HostOptions{OnError: func(HostError) {}})
			must(t, tab.Load(nil, background))
			a, b := link.MemoryLinkPair()
			must(t, middle.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			notes, err := tab.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			transported(a, b)
			if kind := refStateKind(notes); kind != RefStateLoading {
				t.Fatalf("state = %s, want loading", kind)
			}
			up()
			awaitLive(t, notes)
		})
	})
}

func TestRelayDownstreamRelease(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "a downstream release › before the upstream hold was sent drops it; after, releases it upstream; messages crossing it are dropped (race 14)".
		t.Run("before the upstream hold was sent drops it; after, releases it upstream; messages crossing it are dropped (race 14)", func(t *testing.T) {
			finish := make(chan struct{})
			workers := newRawWorkers(t, rawWorkerOptions{})
			rel := relay(t, relayedOptions{
				Resolve: func(address contracts.Address, made func() resolved, ctx context.Context) (resolved, error) {
					<-finish
					return workers.Resolve(address, made, ctx)
				},
			})
			raw := rawTab(t, rel.Server, nil, link.PartLimits{})
			holdPlaced(t, raw, 0)
			flushRelay(rel, workers)
			settle(raw)
			raw.Send(&link.ReleaseMessage{Hold: 0})
			holdPlaced(t, raw, 1)
			flushRelay(rel, workers)
			settle(raw)
			close(finish)
			worker := workers.At(t, 0)
			sent := worker.Next("hold").(*link.HoldMessage)
			if sent.Hold != 1 {
				t.Fatalf("sent hold = %d, want 1", sent.Hold)
			}
			flushRelay(rel, workers)
			settle(worker)
			requireStrings(t, worker.Kinds(), []string{"hello"})
			raw.Send(&link.ReleaseMessage{Hold: 1})
			if released := worker.Next("release").(*link.ReleaseMessage); released.Origin != sent.Origin || released.Hold != 1 {
				t.Fatalf("upstream release = %+v, want hold 1 under %+v", released, sent.Origin)
			}
			held := heldOf(sent)
			bindEditor(t, worker, held)
			worker.Send(countState(t, []heldHold{held}, 1))
			flushRelay(rel, workers)
			settle(raw)
			settle(worker)
			// The relay no longer relays the hold: dropped there (Q1), each message acknowledged to the worker at once.
			requireStrings(t, raw.Kinds(), []string{"hello"})
			if acks := countKind(worker, "ack"); acks != 2 {
				t.Fatalf("worker acks = %d, want 2", acks)
			}
		})
	})
}

func TestRelayDownstreamDrop(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		wait := editorWire(t).Methods["wait"]
		// Ports "a downstream drop › closes its origin upstream: one closed per worker, no release or cancel (race 15)".
		t.Run("closes its origin upstream: one closed per worker, no release or cancel (race 15)", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			holdPlaced(t, r.Raw, 0, "S1")
			// As in "drop entries, notices and replies": the second hold waits for the first address's worker (Go resolves
			// each address on a task of its own, so the order of the workers is not the order of the holds otherwise).
			one := r.Workers.At(t, 0)
			held := nextHeld(one)
			holdPlaced(t, r.Raw, 1, "S2")
			two := r.Workers.At(t, 1)
			two.Next("hold")
			bindEditor(t, one, held)
			r.Raw.Next("bound")
			provCall(t, r.Raw, 0, 0, wait, nil)
			one.Next("call")
			r.Raw.Close()
			settleRelay(r)
			for _, worker := range []*RawPeer{one, two} {
				var ends []link.Message
				for _, message := range worker.Received() {
					switch message.Kind() {
					case "release", "abort", "closed":
						ends = append(ends, message)
					}
				}
				if len(ends) != 1 {
					t.Fatalf("worker ends = %v, want one closed", ends)
				}
				if closed, ok := ends[0].(*link.ClosedMessage); !ok || closed.Origin != held.origin.Value {
					t.Fatalf("worker end = %+v, want closed of origin %d", ends[0], held.origin.Value)
				}
			}
		})
	})
}

func TestRelayGoneFromUpstream(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "gone from upstream › is forwarded and the relayed hold forgotten (race 16)".
		t.Run("is forwarded and the relayed hold forgotten (race 16)", func(t *testing.T) {
			r := rawRelay(t, rawRelayOptions{})
			got := asyncGet(r.Tab, PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			worker := r.Workers.At(t, 0)
			held := nextHeld(worker)
			bindEditor(t, worker, held)
			notes := awaitReference(t, got)
			watch, err := notes.Member("count").Watch(background)
			must(t, err)
			worker.Send(goneMessage(held.origin, held.hold, "revoked"))
			<-watch.Closed()
			if end := watch.End(); end.Kind != chord.WatchEnded {
				t.Fatalf("watch end = %+v, want ended", end)
			}
			if _, err := notes.Member("count").Value(); err == nil || err.Error() != "revoked" {
				t.Fatalf("count error = %v, want revoked", err)
			}
			// A state crossing the gone reaches the tab, which ignores it; nothing closes.
			worker.Send(countState(t, []heldHold{held}, 9))
			settleRelay(r)
			if worker.Closed() {
				t.Fatal("the worker's link closed")
			}
		})
	})
}

// relayedHoldsOf are the relayed holds a host's links hold now, weakly.
func relayedHoldsOf(host Host) []weak.Pointer[relayedHold] {
	var refs []weak.Pointer[relayedHold]
	chordHostOf := host.(*chordHost)
	chordHostOf.loop().Run(func() {
		for _, each := range chordHostOf.links {
			for _, provider := range each.providers.values() {
				for _, entry := range provider.entries.values() {
					if relayedEntry, ok := entry.(*relayedHold); ok {
						refs = append(refs, weak.Make(relayedEntry))
					}
				}
			}
		}
	})
	return refs
}

func TestRelayGC(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "GC at a relay › a released relayed hold and a closed downstream link keep no relay state".
		t.Run("a released relayed hold and a closed downstream link keep no relay state", func(t *testing.T) {
			var refs []weak.Pointer[relayedHold]
			wait := editorWire(t).Methods["wait"]
			r := rawRelay(t, rawRelayOptions{})
			func() {
				holdPlaced(t, r.Raw, 0)
				holdPlaced(t, r.Raw, 1)
				worker := r.Workers.At(t, 0)
				one := nextHeld(worker)
				bindEditor(t, worker, one)
				r.Raw.Next("bound")
				provCall(t, r.Raw, 0, 0, wait, nil)
				worker.Next("call")
				refs = relayedHoldsOf(r.Server)
				r.Raw.Send(&link.ReleaseMessage{Hold: 0})
				r.Raw.Close()
				settleRelay(r)
			}()
			if len(refs) != 2 {
				t.Fatalf("relayed holds = %d, want 2", len(refs))
			}
			nexttest.ExpectCollected(t, refs...)
			// The server is retained until here.
			_ = r.Server
		})
	})
}

// listeningLink is a link end that records every message it receives, weakly (the Go form of Pi's received buffers). A buffer
// under 16 bytes (an `ack`) comes from the allocator's tiny blocks, which hold several objects and are freed only when every
// one of them is: its weak pointer says nothing of who retains the message, so only larger buffers are recorded.
type listeningLink struct {
	link.Link
	mu      sync.Mutex
	buffers []weak.Pointer[byte]
}

func (l *listeningLink) OnMessage(listener func(message []byte)) {
	l.Link.OnMessage(func(message []byte) {
		if len(message) >= 16 {
			l.mu.Lock()
			l.buffers = append(l.buffers, weak.Make(&message[0]))
			l.mu.Unlock()
		}
		listener(message)
	})
}

func (l *listeningLink) recorded() []weak.Pointer[byte] {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]weak.Pointer[byte](nil), l.buffers...)
}

func TestRelayReceivedBuffers(t *testing.T) {
	// Ports "received buffers at a relay › are not kept by the parts of a relayed hold after its message was handled".
	t.Run("are not kept by the parts of a relayed hold after its message was handled", func(t *testing.T) {
		workers := newRawWorkers(t, rawWorkerOptions{})
		rel := relay(t, relayedOptions{Resolve: workers.Resolve})
		a, b := link.MemoryLinkPair()
		listening := &listeningLink{Link: a}
		must(t, rel.Server.Accept(listening, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
		tab := newRawPeer(t, b, rawPeerOptions{Codec: link.BinaryCodec})
		holdPlaced(t, tab, 0, "S1")
		worker := workers.At(t, 0)
		bindEditor(t, worker, nextHeld(worker))
		tab.Next("bound")
		buffers := listening.recorded()
		if len(buffers) <= 1 {
			t.Fatalf("recorded buffers = %d, want more than 1", len(buffers))
		}
		nexttest.ExpectCollected(t, buffers...)
		if refs := relayedHoldsOf(rel.Server); len(refs) != 1 || refs[0].Value() == nil {
			t.Fatalf("relayed holds = %d, want the one hold alive", len(refs))
		}
	})
}

func TestRelayMixedCodecs(t *testing.T) {
	// Ports "mixed codecs › relay between a binary tab link and a text route link".
	t.Run("relay between a binary tab link and a text route link", func(t *testing.T) {
		rel := relay(t, relayedOptions{TabCodec: link.BinaryCodec, RouteCodec: link.TextCodec})
		notes, err := rel.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		addNote(t, rel.Worker(Session.Address(jsonObject("id", "S1")).Key), "S1", "é")
		flushOwn(rel)
		nexttest.RequireJSONEqual(t, memberValue(t, notes, "notes"), []any{jsonObject("text", "é")})
		added, err := notes.Call("add", jsonObject("text", "b"), background)
		must(t, err)
		nexttest.RequireJSONEqual(t, added, 2.0)
		cancellable, cancel := chordctx.WithCancel(background)
		waiting := startCall(t, notes, "wait", nil, cancellable)
		cancel(errStop)
		if _, err := waiting.wait(background); err == nil || err.Error() != "stop" {
			t.Fatalf("waiting error = %v, want stop", err)
		}
	})
}

var errStop = &stopError{}

type stopError struct{}

func (*stopError) Error() string { return "stop" }

// countingInflates is a compressor that counts its inflates (nodeCompression underneath).
type countingInflates struct {
	mu       sync.Mutex
	inflates int
}

func (c *countingInflates) Deflate(input []byte) []byte {
	return nextnode.NodeCompression.Deflate(input)
}

func (c *countingInflates) Inflate(input []byte, limit float64) nextnode.Inflated {
	c.mu.Lock()
	c.inflates++
	c.mu.Unlock()
	return nextnode.NodeCompression.Inflate(input, limit)
}

func (c *countingInflates) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflates
}

func loremText() string {
	text := ""
	for range 3000 {
		text += "lorem ipsum "
	}
	return text
}

func TestRelayLimitsAndCompression(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		editor := editorWire(t)
		add := editor.Methods["add"]

		// Ports "limits and compression through a relay › check an argument's size on the admitting link at the relay: too_large/part, nothing forwarded".
		t.Run("check an argument's size on the admitting link at the relay: too_large/part, nothing forwarded", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{})
			rel := relay(t, relayedOptions{Resolve: workers.Resolve})
			raw := rawTab(t, rel.Server, nil, link.PartLimits{Part: new(100.0)})
			holdPlaced(t, raw, 0)
			worker := workers.At(t, 0)
			bindEditor(t, worker, nextHeld(worker))
			raw.Next("bound")
			provCall(t, raw, 0, 0, add, jsonObject("text", string(repeatBytes('x', 200))))
			refused := raw.Next("error").(*link.ErrorMessage)
			if refused.Call != 0 {
				t.Fatalf("error call = %d, want 0", refused.Call)
			}
			requireChordWire(t, refused.Error, "too_large", "part")
			flushRelay(rel, workers)
			settle(raw)
			settle(worker)
			if countKind(worker, "call") != 0 {
				t.Fatal("the relay forwarded the call")
			}
		})

		// Ports "stamp the admitting link's decoded limit on the upstream hold; a tab's own is ignored (race 12)".
		t.Run("stamp the admitting link's decoded limit on the upstream hold; a tab's own is ignored (race 12)", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{})
			rel := relay(t, relayedOptions{Resolve: workers.Resolve})
			raw := rawTab(t, rel.Server, nil, link.PartLimits{Decoded: new(4096.0)})
			message := placedHold(t, 0, "S1")
			message.Limit = link.Some(1_000_000_000)
			raw.Send(message)
			worker := workers.At(t, 0)
			if held := worker.Next("hold").(*link.HoldMessage); held.Limit != link.Some(4096) {
				t.Fatalf("upstream limit = %+v, want 4096", held.Limit)
			}
		})

		// Ports "enforce the stamped limit where the part is decoded: the worker refuses the tab's large argument".
		t.Run("enforce the stamped limit where the part is decoded: the worker refuses the tab's large argument", func(t *testing.T) {
			rel := relay(t, relayedOptions{Trusted: true, Limits: link.PartLimits{Decoded: new(1000.0)}})
			notes, err := rel.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			// Within the tab link's wire limit, past its decoded limit, which the trusted worker link does not have.
			_, err = notes.Call("add", jsonObject("text", string(repeatBytes('x', 2000))), background)
			_ = requireChordError(t, err, data.CodeTooLarge, data.ReasonPart)
			added, err := notes.Call("add", jsonObject("text", "small"), background)
			must(t, err)
			nexttest.RequireJSONEqual(t, added, 1.0)
		})

		// Ports "forward deflated parts as they came: the relay inflates nothing (a counter)".
		t.Run("forward deflated parts as they came: the relay inflates nothing (a counter)", func(t *testing.T) {
			server, tab := &countingInflates{}, &countingInflates{}
			rel := relay(t, relayedOptions{ServerCompression: server, WorkerCompression: nextnode.NodeCompression, TabCompression: tab})
			notes, err := rel.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			text := loremText()
			_, err = notes.Call("add", jsonObject("text", text), background)
			must(t, err)
			flushOwn(rel)
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "notes"), []any{jsonObject("text", text)})
			// The worker's state deflated once at its origin, inflated by its owner, the tab.
			if server.count() != 0 || tab.count() != 1 {
				t.Fatalf("inflates [server tab] = [%d %d], want [0 1]", server.count(), tab.count())
			}
		})

		// Ports "forward deflated parts as they came at a relay without a compressor: nothing inflated there". Go form:
		// Pi stubs the web DecompressionStream the compressor-less relay would inflate with and counts its bytes; here
		// every inflate (synchronous or asynchronous) counts `chord.link.inflates` on the telemetry of the context the
		// links were made with, so one inflate in all (the tab's) proves the relay inflated nothing.
		t.Run("forward deflated parts as they came at a relay without a compressor: nothing inflated there", func(t *testing.T) {
			meter := nexttest.Metered(t)
			tab := &countingInflates{}
			rel := relay(t, relayedOptions{WorkerCompression: nextnode.NodeCompression, TabCompression: tab, Context: meter.Context})
			notes, err := rel.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			_, err = notes.Call("add", jsonObject("text", loremText()), background)
			must(t, err)
			flushOwn(rel)
			if tab.count() != 1 {
				t.Fatalf("tab inflates = %d, want 1", tab.count())
			}
			if inflates := meter.Counts()["chord.link.inflates"]; inflates != 1 {
				t.Fatalf("chord.link.inflates = %v, want 1 (the tab's)", inflates)
			}
		})

		// Ports "forward answers from upstream unchecked: the end that parses a part checks it against its own link".
		t.Run("forward answers from upstream unchecked: the end that parses a part checks it against its own link", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{Limits: link.PartLimits{Part: new(50.0)}})
			rel := relay(t, relayedOptions{Resolve: workers.Resolve})
			getting := asyncGet(rel.Tab, PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			worker := workers.At(t, 0)
			held := nextHeld(worker)
			bindEditor(t, worker, held)
			notes := awaitReference(t, getting)
			// A state part and a result past the route link's wire limit reach the tab, whose link has none that small.
			opList := []any{[]any{"r", 1234567890.0}}
			for range 10 {
				opList = append(opList, []any{"r", 7.0})
			}
			worker.Send(&link.StateMessage{Origin: held.origin, Holds: []link.StateEntry{
				stateEntry(held.hold, []link.StateMember{{Member: editor.State["count"], Ops: mustJSONPart(t, opList)}}, nil),
			}})
			added := startCall(t, notes, "add", jsonObject("text", "x"), background)
			call := worker.Next("call").(*link.CallMessage)
			long := string(repeatBytes('y', 60))
			worker.Send(&link.ResultMessage{Origin: held.origin, Call: call.Call, Value: mustJSONPart(t, long)})
			result, err := added.wait(background)
			must(t, err)
			nexttest.RequireJSONEqual(t, result, long)
			flushRelay(rel, workers)
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 7.0)
			if kind := refStateKind(notes); kind != RefStateLive {
				t.Fatalf("state = %s, want live", kind)
			}
		})

		// Ports "detach a reference failed too_large from its route; retry %i ms after the drop places it anew, a new hold" (it.each over [0, 1200]).
		for _, wait := range []int64{0, 1200} {
			t.Run("detach a reference failed too_large from its route; retry "+formatNumber(float64(wait))+" ms after the drop places it anew, a new hold", func(t *testing.T) {
				workers := newRawWorkers(t, rawWorkerOptions{Limits: link.PartLimits{Part: new(50.0)}})
				rel := relay(t, relayedOptions{Resolve: workers.Resolve})
				notes, err := rel.Server.Hold(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
				must(t, err)
				first := workers.At(t, 0)
				held := nextHeld(first)
				first.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
					Hold: held.hold, Variant: editor.Variant, Failed: []link.FailedMember{},
					Members: boundMembers(t, mustJSONPart(t, []any{jsonObject("text", string(repeatBytes('z', 60)))}), mustJSONPart(t, 1.0)),
				}}})
				if released := first.Next("release").(*link.ReleaseMessage); released.Hold != held.hold {
					t.Fatalf("released hold = %d, want %d", released.Hold, held.hold)
				}
				state := liveStateOf(notes)
				if state.Kind != RefStateFailed || state.Cause.Error.Code != data.CodeTooLarge {
					t.Fatalf("state = %+v, want failed too_large", state)
				}
				// The worker's link drops: nothing holds the address, so nothing resolves it again for this reference.
				first.Close()
				// Pi waits in real time here (the route's backoff window runs on the real clock).
				time.Sleep(time.Duration(wait) * time.Millisecond)
				flushRelay(rel, workers)
				if got := len(workers.Peers()); got != 1 {
					t.Fatalf("workers = %d, want 1", got)
				}
				// The route forgot the address: the retry places the reference anew, resolves it and holds it.
				notes.Retry()
				if kind := refStateKind(notes); kind != RefStateLoading {
					t.Fatalf("state after retry = %s, want loading", kind)
				}
				second := workers.At(t, 1)
				bindPlaced(t, second, nextHeld(second), 2, editor)
				flushRelay(rel, workers)
				settle(second)
				if kind := refStateKind(notes); kind != RefStateLive {
					t.Fatalf("state = %s, want live", kind)
				}
				nexttest.RequireJSONEqual(t, memberValue(t, notes, "count"), 2.0)
			})
		}

		// Ports "let a tab without a compressor inflate the worker's deflated parts asynchronously, in order".
		t.Run("let a tab without a compressor inflate the worker's deflated parts asynchronously, in order", func(t *testing.T) {
			rel := relay(t, relayedOptions{WorkerCompression: nextnode.NodeCompression, ServerCompression: nextnode.NodeCompression})
			notes, err := rel.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			text := loremText()
			for range 3 {
				_, err = notes.Call("add", jsonObject("text", text), background)
				must(t, err)
			}
			counted := make(chan struct{})
			var once sync.Once
			watch, err := notes.Member("count").Watch(background)
			must(t, err)
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				if call.Kind == chord.WatchCallValue && call.Value == 3.0 {
					once.Do(func() { close(counted) })
				}
				return nil
			})
			if value, _ := notes.Member("count").Value(); value != 3.0 {
				<-counted
			}
			nexttest.RequireJSONEqual(t, memberValue(t, notes, "notes"), []any{
				jsonObject("text", text), jsonObject("text", text), jsonObject("text", text),
			})
		})
	})
}

func repeatBytes(char byte, count int) []byte {
	bytes := make([]byte, count)
	for index := range bytes {
		bytes[index] = char
	}
	return bytes
}

// flushOwn is `await transported()` over the links a relay made itself, hop by hop.
func flushOwn(rel *relayed) {
	for range 4 {
		rel.Transported()
	}
}

// awaitLive blocks until ref is live: its state is watched, a state change waking the wait.
func awaitLive(t testing.TB, ref *Reference) {
	t.Helper()
	live := make(chan struct{})
	var once sync.Once
	watch, err := ref.State().Watch(background)
	must(t, err)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind == chord.WatchCallValue && call.Value.Kind == RefStateLive {
			once.Do(func() { close(live) })
		}
		return nil
	})
	if refStateKind(ref) == RefStateLive {
		once.Do(func() { close(live) })
	}
	<-live
	_, _ = watch.Stop()
}
