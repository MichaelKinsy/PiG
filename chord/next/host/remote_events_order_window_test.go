package host

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Cases beyond Pi's own tests of events and call windows (Pi 8d30e6306), at the edges Pi's tests leave untested: the
// order of events against a call's result on one link (both codecs, and through a relay), the default windows exactly at
// and one past their bound, and event acks that count more than was sent. Each expectation is Pi's behaviour, cited at
// its source line.

// orderVariants is a variant with an event member and a method that emits its argument before it returns it.
func orderEvents() []contracts.RemoteEventEntry {
	return []contracts.RemoteEventEntry{contracts.EventEntry("paddle", Sample)}
}

func orderMethods() []contracts.RemoteMethodEntry {
	return []contracts.RemoteMethodEntry{contracts.Meth("serve", contracts.NewMethod(Sample, contracts.Num()))}
}

// OrderMatch is served by the server, PlacedOrderMatch by its session's worker (reached through the server's relay).
var OrderMatch = mustContract(contracts.Remote("app.order-match", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Events:      orderEvents(),
	Methods:     orderMethods(),
}))

var PlacedOrderMatch = mustContract(contracts.Remote("app.placed-order-match", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, HostAddress: exSessionAddress,
	Events:  orderEvents(),
	Methods: orderMethods(),
}))

// orderProbe is the sources of the served order matches.
type orderProbe struct {
	mu      sync.Mutex
	paddles []contracts.EventSource[delta.JsonValue]
}

func (p *orderProbe) paddle() contracts.EventSource[delta.JsonValue] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paddles[len(p.paddles)-1]
}

// serveOrder serves typ: `serve(sample)` emits the sample on the paddle, then returns its y.
func serveOrder(typ *contracts.Contract, probe *orderProbe) func(host FacetHost) error {
	return func(host FacetHost) error {
		return host.Serve(typ, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.MembersImplementation, error) {
			paddle := self.Events()
			probe.mu.Lock()
			probe.paddles = append(probe.paddles, paddle)
			probe.mu.Unlock()
			return contracts.MembersImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle},
				Methods: map[string]contracts.MethodFunction{"serve": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					if err := paddle.Emit(args); err != nil {
						return nil, err
					}
					return args.(*delta.JsonObject).Value("y"), nil
				}},
			}, nil
		}, ServeOptions{})
	}
}

// orderLog is what a consumer saw, in order: "event <y>" per delivery, "result <y>" per returned call.
type orderLog struct {
	mu   sync.Mutex
	seen []string
}

func (l *orderLog) add(entry string) {
	l.mu.Lock()
	l.seen = append(l.seen, entry)
	l.mu.Unlock()
}

func (l *orderLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seen...)
}

// orderOnAReference subscribes to ref's paddle, calls `serve(1)` (which emits 1 before it answers), then has emit emit
// 2 after the answer: the consumer sees the first event before the call returned and the second after it.
func orderOnAReference(t *testing.T, ref *Reference, emit func(y float64), transported func()) {
	t.Helper()
	log := &orderLog{}
	on(t, ref, "paddle", func(e contracts.EventDelivery[delta.JsonValue]) {
		log.add("event " + formatNumber(e.Value.(*delta.JsonObject).Value("y").(float64)))
	}, background)
	transported()
	result, err := ref.Call("serve", sample(1), background)
	must(t, err)
	// The event emitted before the result was delivered before Call returned: nothing else was awaited in between.
	log.add("result " + formatNumber(result.(float64)))
	emit(2)
	rhWaitFor(t, func() bool {
		transported()
		return len(log.all()) == 3
	})
	requireStrings(t, log.all(), []string{"event 1", "result 1", "event 2"})
}

// An event emitted before a call's result is delivered before that result, and one emitted after it after it: one link
// keeps the order of what its provider sent (Pi remote/provider.ts: `sendEvents` sends at the flush, a microtask after
// the emit, :381-390; a call's reply is sent when its handler settled, later in the same turn's microtasks).
func TestEventsKeepTheirOrderAgainstACallsResultOnOneLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		t.Run("on the wire: event, result, event", func(t *testing.T) {
			probe := &orderProbe{}
			s := rawLinked(t, serveOrder(OrderMatch, probe), rawLinkedOptions{})
			provHold(t, s.Peer, 0, specOf(OrderMatch), jsonObject())
			s.Peer.Next("bound")
			provCall(t, s.Peer, 0, 0, wireNames(t, OrderMatch, "player").Methods["serve"], sample(1))
			rhWaitFor(t, func() bool { return countMessages[*link.ResultMessage](s.Peer) == 1 })
			hostTurn(s.Server, func() { must(t, probe.paddle().Emit(sample(2))) })
			settle(s.Peer)
			var order []string
			for _, message := range s.Peer.Received() {
				switch message := message.(type) {
				case *link.EventMessage:
					for _, entry := range message.Events {
						order = append(order, "event "+formatNumber(partValue(t, entry.Value).(*delta.JsonObject).Value("y").(float64)))
					}
				case *link.ResultMessage:
					order = append(order, "result "+formatNumber(partValue(t, message.Value).(float64)))
				}
			}
			requireStrings(t, order, []string{"event 1", "result 1", "event 2"})
		})

		t.Run("at a consumer host: the listener runs before the call returns", func(t *testing.T) {
			probe := &orderProbe{}
			l := linked(t, serveOrder(OrderMatch, probe), linkedOptions{})
			ref, err := l.Client.Get(background, OrderMatch, jsonObject(), GetOptions{})
			must(t, err)
			orderOnAReference(t, ref, func(y float64) {
				hostTurn(l.Server, func() { must(t, probe.paddle().Emit(sample(y))) })
			}, func() { settleLinked(l) })
		})

		t.Run("through a relay: a worker's event and result reach the tab in the worker's order", func(t *testing.T) {
			probe := &orderProbe{}
			rig := relay(t, relayedOptions{Worker: serveOrder(PlacedOrderMatch, probe)})
			ref, err := rig.Tab.Get(background, PlacedOrderMatch, jsonObject("session", "S1"), GetOptions{})
			must(t, err)
			orderOnAReference(t, ref, func(y float64) {
				hostTurn(rig.Worker("session|S1"), func() { must(t, probe.paddle().Emit(sample(y))) })
			}, rig.Transported)
		})
	})
}

// eventCounts are the recorder's counters by name, `.reason` appended when a measurement has one.
func eventCounts(recorder *telemetry.InMemoryTelemetryContext) map[string]float64 {
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

// emitTurns emits y from..to on the match's paddle, each in a turn of its own (one `event` message per turn).
func emitTurns(t testing.TB, s *matchServerRaw, from, to int) {
	t.Helper()
	for y := from; y <= to; y++ {
		hostTurn(s.Server, func() { must(t, s.Probe.paddle(0).Emit(sample(float64(y)))) })
		settle(s.Peer)
	}
}

// The default event window (Pi host/host.ts:654, `options.eventWindow ?? 32`) at its bound: the 32nd unacknowledged
// `event` message is sent, the 33rd is dropped at the provider and counted `chord.events.dropped{reason=window}`
// (Pi remote/provider.ts:369-372: `if (this.#eventsInFlight >= eventWindow) { ...add(1, { reason: "window" }); return; }`):
// never blocked, never refused with an error, never queued. One ack frees one slot.
func TestTheDefaultEventWindowSendsThe32ndUnacknowledgedMessageAndDropsThe33rd(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		recorder := telemetry.NewInMemoryTelemetryContext()
		s := matchServer(t, rawLinkedOptions{Telemetry: recorder})
		provHoldMatch(t, s.Peer, 0)
		s.Peer.Next("bound")
		emitTurns(t, s, 1, 32)
		if got := len(takeEvents(s.Peer)); got != 32 {
			t.Fatalf("event messages with 32 outstanding = %d, want 32 (the window is 32)", got)
		}
		if got := eventCounts(recorder)["chord.events.dropped.window"]; got != 0 {
			t.Fatalf("dropped at 32 outstanding = %v, want 0", got)
		}
		emitTurns(t, s, 33, 33)
		if got := takeEvents(s.Peer); len(got) != 0 {
			t.Fatalf("the 33rd outstanding event was sent: %v, want it dropped", eventYs(got))
		}
		counts := eventCounts(recorder)
		for name, want := range map[string]float64{
			"chord.events.emitted": 33, "chord.events.sent": 32, "chord.events.dropped.window": 1, "chord.events.encodes": 32,
		} {
			if counts[name] != want {
				t.Fatalf("%s = %v, want %v (all %v)", name, counts[name], want, counts)
			}
		}
		if s.Peer.Closed() || s.Errors.Len() != 0 {
			t.Fatalf("closed = %v, errors = %v, want the link open and no error: a full window only drops", s.Peer.Closed(), s.Errors.All())
		}
		// One ack frees one slot: the next emit is sent, the one after it dropped again.
		s.Peer.Send(&link.AckMessage{Events: link.Some(1)})
		settle(s.Peer)
		emitTurns(t, s, 34, 35)
		requireEqual(t, "ys after one ack", eventYs(takeEvents(s.Peer)), [][]any{{34.0}})
	})
}

// The default call window (Pi host/host.ts:653, `options.callWindow ?? 10_000`) on a link not given as trusted: it is
// announced in `hello` (remote/host-link.ts:83), 10,000 concurrent calls run, and the 10,001st closes the link with a
// protocol error (remote/provider.ts:530-532: `if (this.#limit !== undefined && this.#calls.size >= this.#limit)
// throw new Error(`call ${message.call}: more than ${this.#limit} calls`)`). A trusted link announces no window and
// runs 10,001.
func TestTheDefaultCallWindowRuns10000ConcurrentCallsAndClosesTheLinkAtThe10001st(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		wait := editorWire(t).Methods["wait"]
		calls := func(s *notesServerRaw, from, to int) {
			for id := from; id < to; id++ {
				provCall(t, s.Peer, id, 0, wait, nil)
			}
			settle(s.Peer)
		}

		t.Run("a link not given as trusted: 10,000 run, the 10,001st closes it", func(t *testing.T) {
			s := notesServerOf(t, rawLinkedOptions{})
			if got := helloOf(t, s.Peer).Calls; got != 10_000 {
				t.Fatalf("announced %d, want 10000", got)
			}
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			calls(s, 0, 10_000)
			if s.Peer.Closed() {
				t.Fatalf("the link closed within its 10,000 calls: %v", protocolErrors(s.Errors))
			}
			if got := len(s.Probe.AwaitWaits(t, 10_000)); got != 10_000 {
				t.Fatalf("waits = %d, want 10000", got)
			}
			if s.Peer.Closed() || s.Errors.Len() != 0 {
				t.Fatalf("closed = %v, errors = %v, want 10,000 calls running on an open link", s.Peer.Closed(), s.Errors.All())
			}
			calls(s, 10_000, 10_001)
			if !s.Peer.Closed() {
				t.Fatal("the link stayed open at the 10,001st call")
			}
			requireStrings(t, protocolErrors(s.Errors), []string{"call 10000: more than 10000 calls"})
		})

		t.Run("a trusted link: no window announced, 10,001 run", func(t *testing.T) {
			s := notesServerOf(t, rawLinkedOptions{Trusted: true})
			if got := helloOf(t, s.Peer).Calls; got != 0 {
				t.Fatalf("announced %d, want none", got)
			}
			provHold(t, s.Peer, 0, specOf(Notes), jsonObject("session", "S1"))
			s.Peer.Next("bound")
			calls(s, 0, 10_001)
			if got := len(s.Probe.AwaitWaits(t, 10_001)); got != 10_001 {
				t.Fatalf("waits = %d, want 10001", got)
			}
			if s.Peer.Closed() || s.Errors.Len() != 0 {
				t.Fatalf("closed = %v, errors = %v, want the trusted link open", s.Peer.Closed(), s.Errors.All())
			}
		})
	})
}

// An event ack counts consumed `event` messages; it names none. One that counts messages never sent, or counts a
// message a second time, finds no ticket and is ignored (Pi remote/provider.ts:681-684: `const ticket =
// this.#tickets.shift(); if (ticket === undefined) break;`): no protocol error, and the window never grows past the
// host's `eventWindow`.
func TestAnEventAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		open := func(t *testing.T) *matchServerRaw {
			s := matchServer(t, rawLinkedOptions{EventWindow: new(2)})
			provHoldMatch(t, s.Peer, 0)
			s.Peer.Next("bound")
			return s
		}
		healthy := func(t *testing.T, s *matchServerRaw) {
			t.Helper()
			if s.Peer.Closed() || s.Errors.Len() != 0 {
				t.Fatalf("closed = %v, errors = %v, want the link open and no error", s.Peer.Closed(), s.Errors.All())
			}
		}

		t.Run("an ack for five messages when none was sent: the window stays 2", func(t *testing.T) {
			s := open(t)
			s.Peer.Send(&link.AckMessage{Events: link.Some(5)})
			settle(s.Peer)
			healthy(t, s)
			emitTurns(t, s, 1, 4)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{1.0}, {2.0}})
		})

		t.Run("an ack counting more than is outstanding frees only what is outstanding", func(t *testing.T) {
			s := open(t)
			emitTurns(t, s, 1, 1)
			s.Peer.Send(&link.AckMessage{Events: link.Some(3)})
			settle(s.Peer)
			healthy(t, s)
			emitTurns(t, s, 2, 5)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{1.0}, {2.0}, {3.0}})
		})

		t.Run("the same message acknowledged twice frees one slot", func(t *testing.T) {
			s := open(t)
			emitTurns(t, s, 1, 1)
			s.Peer.Send(&link.AckMessage{Events: link.Some(1)})
			s.Peer.Send(&link.AckMessage{Events: link.Some(1)})
			settle(s.Peer)
			healthy(t, s)
			emitTurns(t, s, 2, 5)
			requireEqual(t, "ys", eventYs(takeEvents(s.Peer)), [][]any{{1.0}, {2.0}, {3.0}})
		})
	})
}
