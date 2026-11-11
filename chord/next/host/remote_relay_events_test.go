package host

import (
	"context"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/relay.ts

// Ports the call-window, event and real-host describes added to packages/chord/test/remote/relay.test.ts by Pi
// 8d30e6306 (decisions "Call windows only for untrusted links" and "Events: fire-and-forget members").

// callIDs are the ids of the calls a raw peer received, in order.
func callIDs(peer *RawPeer) []int {
	ids := []int{}
	for _, message := range peer.Received() {
		if call, ok := message.(*link.CallMessage); ok {
			ids = append(ids, call.Call)
		}
	}
	return ids
}

// answer is a call a raw tab got an answer for (`result` or `error`), with the error's message.
type relayAnswer struct {
	Call    int
	Message string
}

func relayAnswersOf(t testing.TB, peer *RawPeer) []relayAnswer {
	t.Helper()
	answers := []relayAnswer{}
	for _, message := range peer.Received() {
		switch typed := message.(type) {
		case *link.ResultMessage:
			answers = append(answers, relayAnswer{typed.Call, "ok"})
		case *link.ErrorMessage:
			_, _, text := chordWire(t, typed.Error)
			answers = append(answers, relayAnswer{typed.Call, text})
		}
	}
	return answers
}

// wireChord is a chord error on the wire.
func wireChord(code, message string) link.WireError {
	return jsonObject("kind", "chord", "error", jsonObject("code", code, "message", message))
}

// relayBound is a raw tab holding PlacedNotes S1 (hold 0) bound through the relay at a raw worker announcing 2 calls.
type relayBound struct {
	*rawRelayHost
	Worker *RawPeer
	Held   heldHold
}

func boundRelay(t testing.TB, trusted, noAck bool) *relayBound {
	t.Helper()
	r := rawRelay(t, rawRelayOptions{Calls: 2, Trusted: trusted, NoAck: noAck})
	holdPlaced(t, r.Raw, 0)
	worker := r.Workers.At(t, 0)
	held := nextHeld(worker)
	bindEditor(t, worker, held)
	r.Raw.Next("bound")
	if noAck {
		r.Raw.Ack(link.Optional[int]{})
	}
	return &relayBound{rawRelayHost: r, Worker: worker, Held: held}
}

// callFrom sends calls ids of hold from the raw tab.
func (b *relayBound) callFrom(t testing.TB, hold int, ids ...int) {
	t.Helper()
	wait := editorWire(t).Methods["wait"]
	for _, id := range ids {
		provCall(t, b.Raw, id, hold, wait, nil)
	}
}

// secondHold holds PlacedNotes S1 as hold 1 and binds it at the worker.
func (b *relayBound) secondHold(t testing.TB) heldHold {
	t.Helper()
	holdPlaced(t, b.Raw, 1)
	other := nextHeld(b.Worker)
	bindEditor(t, b.Worker, other)
	b.Raw.Next("bound")
	return other
}

func requireInts(t testing.TB, what string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func requireAnswers(t testing.TB, got []relayAnswer, want ...relayAnswer) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("answers = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("answers = %v, want %v", got, want)
		}
	}
}

func countMessages[T link.Message](peer *RawPeer) int {
	count := 0
	for _, message := range peer.Received() {
		if _, ok := message.(T); ok {
			count++
		}
	}
	return count
}

func TestRelayedCallsInTheUpstreamWindow(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "wait per upstream window: the worker's 2 of five calls, the rest in order as answers free slots".
		t.Run("wait per upstream window: the worker's 2 of five calls, the rest in order as answers free slots", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.callFrom(t, 0, 0, 1, 2, 3, 4)
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
			b.Worker.Send(&link.ResultMessage{Origin: b.Held.origin, Call: 0})
			b.Worker.Send(&link.ErrorMessage{Origin: b.Held.origin, Call: 1, Error: wireChord("unavailable", "no")})
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1, 2, 3})
			requireAnswers(t, relayAnswersOf(t, b.Raw), relayAnswer{0, "ok"}, relayAnswer{1, "no"})
			if b.Raw.Closed() {
				t.Fatal("the tab's link closed")
			}
		})

		// Ports "wait per upstream window from a trusted downstream link too: none announced to the tab, 2 upstream".
		t.Run("wait per upstream window from a trusted downstream link too: none announced to the tab, 2 upstream", func(t *testing.T) {
			b := boundRelay(t, true, false)
			if got := helloOf(t, b.Raw).Calls; got != 0 {
				t.Fatalf("announced calls %d, want none", got)
			}
			ids := make([]int, 20)
			for i := range ids {
				ids[i] = i
			}
			b.callFrom(t, 0, ids...)
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
			if b.Raw.Closed() {
				t.Fatal("the tab's link closed")
			}
		})

		// Ports "abort: a waiting call leaves the queue unsent; a forwarded one is aborted upstream and frees its slot".
		t.Run("abort: a waiting call leaves the queue unsent; a forwarded one is aborted upstream and frees its slot", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.callFrom(t, 0, 0, 1, 2, 3)
			settleRelay(b.rawRelayHost)
			b.Raw.Send(&link.AbortMessage{Call: 2})
			b.Raw.Send(&link.AbortMessage{Call: 0})
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1, 3})
			var aborts []*link.AbortMessage
			for _, message := range b.Worker.Received() {
				if abort, ok := message.(*link.AbortMessage); ok {
					aborts = append(aborts, abort)
				}
			}
			if len(aborts) != 1 || aborts[0].Origin != b.Held.origin || aborts[0].Call != 0 {
				t.Fatalf("aborts = %+v, want one of call 0 under the hold's origin", aborts)
			}
		})

		// Ports "release: the hold's waiting calls go unsent, its forwarded ones free their slots for the origin's others".
		t.Run("release: the hold's waiting calls go unsent, its forwarded ones free their slots for the origin's others", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.secondHold(t)
			b.callFrom(t, 0, 0, 1)
			b.callFrom(t, 1, 2)
			b.callFrom(t, 0, 3)
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
			b.Raw.Send(&link.ReleaseMessage{Hold: 0})
			settleRelay(b.rawRelayHost)
			var releases []*link.ReleaseMessage
			var order []string
			for _, message := range b.Worker.Received() {
				switch typed := message.(type) {
				case *link.ReleaseMessage:
					releases = append(releases, typed)
					order = append(order, "release")
				case *link.CallMessage:
					order = append(order, "call "+formatNumber(float64(typed.Call)))
				}
			}
			if len(releases) != 1 || releases[0].Origin != b.Held.origin || releases[0].Hold != 0 {
				t.Fatalf("releases = %+v, want one of hold 0 under the hold's origin", releases)
			}
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1, 2})
			// The release first: the upstream counts the released hold's forwarded calls until it arrives.
			requireEqual(t, "order", order, []string{"call 0", "call 1", "release", "call 2"})
		})

		// Ports "a downstream close: waiting calls never go, the worker gets one closed".
		t.Run("a downstream close: waiting calls never go, the worker gets one closed", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.callFrom(t, 0, 0, 1, 2)
			settleRelay(b.rawRelayHost)
			b.Raw.Close()
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
			var ends []link.Message
			for _, message := range b.Worker.Received() {
				switch message.Kind() {
				case "release", "abort", "closed":
					ends = append(ends, message)
				}
			}
			if len(ends) != 1 {
				t.Fatalf("ends = %v, want one closed", ends)
			}
			if closed, ok := ends[0].(*link.ClosedMessage); !ok || closed.Origin != b.Held.origin.Value {
				t.Fatalf("end = %+v, want closed of the hold's origin", ends[0])
			}
		})

		// Ports "an upstream drop: forwarded and waiting calls fail unavailable, none sent on the next link".
		t.Run("an upstream drop: forwarded and waiting calls fail unavailable, none sent on the next link", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.callFrom(t, 0, 0, 1, 2, 3)
			settleRelay(b.rawRelayHost)
			b.Worker.Close()
			settleRelay(b.rawRelayHost)
			ids := []int{}
			for _, each := range relayAnswersOf(t, b.Raw) {
				ids = append(ids, each.Call)
			}
			requireInts(t, "answered", ids, []int{0, 1, 2, 3})
			if got := countKind(b.Raw, "loading"); got != 1 {
				t.Fatalf("loading messages = %d, want 1", got)
			}
			next := b.Workers.At(t, 1)
			bindEditor(t, next, nextHeld(next))
			b.Raw.Next("bound")
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls on the next link", callIDs(next), []int{})
		})

		// Ports "loading: the waiting calls are rejected unavailable, the forwarded ones kept; a bound of another variant gets none of them".
		t.Run("loading: the waiting calls are rejected unavailable, the forwarded ones kept; a bound of another variant gets none of them", func(t *testing.T) {
			b := boundRelay(t, false, false)
			b.callFrom(t, 0, 0, 1, 2, 3)
			settleRelay(b.rawRelayHost)
			b.Worker.Send(&link.LoadingMessage{Origin: b.Held.origin, Hold: b.Held.hold})
			settleRelay(b.rawRelayHost)
			requireAnswers(t, relayAnswersOf(t, b.Raw), relayAnswer{2, "hold 0: rebinding"}, relayAnswer{3, "hold 0: rebinding"})
			if got := countKind(b.Raw, "loading"); got != 1 {
				t.Fatalf("loading messages = %d, want 1", got)
			}
			bindPlaced(t, b.Worker, b.Held, 0, wireNames(t, PlacedNotes, "reader"))
			b.Worker.Send(&link.ResultMessage{Origin: b.Held.origin, Call: 0})
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
			answers := relayAnswersOf(t, b.Raw)
			if last := answers[len(answers)-1]; last != (relayAnswer{0, "ok"}) {
				t.Fatalf("last answer = %v, want call 0 ok", last)
			}
		})

		// Ports "a call crossing the loading notice, still waiting when the new bound's ack passes: rejected before the ack goes up".
		t.Run("a call crossing the loading notice, still waiting when the new bound's ack passes: rejected before the ack goes up", func(t *testing.T) {
			b := boundRelay(t, false, true)
			b.callFrom(t, 0, 0, 1)
			settleRelay(b.rawRelayHost)
			b.Worker.Send(&link.LoadingMessage{Origin: b.Held.origin, Hold: b.Held.hold})
			settleRelay(b.rawRelayHost)
			// The tab had not learnt of the loading when it called: the old indexes.
			b.callFrom(t, 0, 2)
			settleRelay(b.rawRelayHost)
			bindPlaced(t, b.Worker, b.Held, 0, wireNames(t, PlacedNotes, "reader"))
			b.Raw.Next("bound")
			settleRelay(b.rawRelayHost)
			requireAnswers(t, relayAnswersOf(t, b.Raw))
			acks := countMessages[*link.AckMessage](b.Worker)
			b.Raw.Ack(link.Optional[int]{})
			settleRelay(b.rawRelayHost)
			requireAnswers(t, relayAnswersOf(t, b.Raw), relayAnswer{2, "hold 0: rebound"})
			if got := countMessages[*link.AckMessage](b.Worker); got != acks+1 {
				t.Fatalf("acks at the worker = %d, want %d", got, acks+1)
			}
			// Made with the new binding: it waits and goes as a slot frees.
			b.callFrom(t, 0, 3)
			b.Worker.Send(&link.ResultMessage{Origin: b.Held.origin, Call: 0})
			settleRelay(b.rawRelayHost)
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1, 3})
		})

		// Ports "failed and gone: the waiting calls are rejected with their cause".
		t.Run("failed and gone: the waiting calls are rejected with their cause", func(t *testing.T) {
			b := boundRelay(t, false, false)
			other := b.secondHold(t)
			b.callFrom(t, 0, 0, 1)
			b.callFrom(t, 0, 2)
			b.callFrom(t, 1, 3)
			settleRelay(b.rawRelayHost)
			b.Worker.Send(&link.FailedMessage{Origin: b.Held.origin, Hold: b.Held.hold, Cause: link.WireCause{Kind: "waiting", Error: wireChord("unavailable", "parked")}})
			b.Worker.Send(goneMessage(other.origin, other.hold, "ended"))
			settleRelay(b.rawRelayHost)
			requireAnswers(t, relayAnswersOf(t, b.Raw), relayAnswer{2, "parked"}, relayAnswer{3, "ended"})
			requireInts(t, "calls", callIDs(b.Worker), []int{0, 1})
		})
	})
}

// workerEvents is an `event` message from a raw worker for held (one entry per value), with its origin there.
func workerEvents(t testing.TB, held heldHold, values []float64, hold ...int) *link.EventMessage {
	t.Helper()
	target := held.hold
	if len(hold) > 0 {
		target = hold[0]
	}
	message := &link.EventMessage{Origin: held.origin, Events: []link.WireEvent{}}
	for _, y := range values {
		message.Events = append(message.Events, link.WireEvent{Hold: target, Member: 0, At: 1, Value: mustJSONPart(t, jsonObject("y", y))})
	}
	return message
}

// tabEntry is an entry of an `event` message a raw tab received.
type tabEntry struct {
	Hold int
	Y    any
}

// tabEvents are the entries of the `event` messages a raw tab received, one list per message.
func tabEvents(t testing.TB, peer *RawPeer) [][]tabEntry {
	t.Helper()
	out := [][]tabEntry{}
	for _, message := range peer.Received() {
		if event, ok := message.(*link.EventMessage); ok {
			entries := []tabEntry{}
			for _, entry := range event.Events {
				entries = append(entries, tabEntry{entry.Hold, partValue(t, entry.Value).(*delta.JsonObject).Value("y")})
			}
			out = append(out, entries)
		}
	}
	return out
}

// workerAck is an event ack a raw worker received, with its origin.
type workerAck struct {
	Origin link.Optional[int]
	Events int
}

func eventAcksOf(peer *RawPeer) []workerAck {
	acks := []workerAck{}
	for _, message := range peer.Received() {
		if ack, ok := message.(*link.AckMessage); ok && ack.Events.Ok {
			acks = append(acks, workerAck{ack.Origin, ack.Events.Value})
		}
	}
	return acks
}

// eventRelayHost is a server serving Match itself and routing sessions to raw workers, and a raw tab.
type eventRelayHost struct {
	*rawRelayHost
	Probe *matchProbe
}

func eventRelay(t testing.TB) *eventRelayHost {
	t.Helper()
	probe := &matchProbe{}
	r := rawRelay(t, rawRelayOptions{Server: func(host FacetHost) error { return serveMatch(host, probe) }})
	return &eventRelayHost{rawRelayHost: r, Probe: probe}
}

// relayedHold holds PlacedNotes of session with id hold from the raw tab, bound by its index-th raw worker; the worker's view.
func (r *eventRelayHost) relayedHold(t testing.TB, hold int, session string, index int) (*RawPeer, heldHold) {
	t.Helper()
	holdPlaced(t, r.Raw, hold, session)
	worker := r.Workers.At(t, index)
	held := nextHeld(worker)
	bindEditor(t, worker, held)
	r.Raw.Next("bound")
	return worker, held
}

func ysOf(entries [][]tabEntry) [][]any {
	out := [][]any{}
	for _, each := range entries {
		ys := []any{}
		for _, entry := range each {
			ys = append(ys, entry.Y)
		}
		out = append(out, ys)
	}
	return out
}

func closedOf(peer *RawPeer) []*link.ClosedMessage {
	var closed []*link.ClosedMessage
	for _, message := range peer.Received() {
		if each, ok := message.(*link.ClosedMessage); ok {
			closed = append(closed, each)
		}
	}
	return closed
}

func TestRelayEvents(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		ack := func(n int) *link.AckMessage { return &link.AckMessage{Events: link.Some(n)} }

		// Ports "forward an upstream event message at once with the tab's origin, never acknowledging it themselves".
		t.Run("forward an upstream event message at once with the tab's origin, never acknowledging it themselves", func(t *testing.T) {
			r := eventRelay(t)
			worker, held := r.relayedHold(t, 0, "S1", 0)
			worker.Send(workerEvents(t, held, []float64{1, 2}))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "tab events", tabEvents(t, r.Raw), [][]tabEntry{{{0, 1.0}, {0, 2.0}}})
			for _, message := range r.Raw.Received() {
				if event, ok := message.(*link.EventMessage); ok && event.Origin.Ok {
					t.Fatalf("the forwarded event kept origin %v", event.Origin)
				}
			}
			if got := eventAcksOf(worker); len(got) != 0 {
				t.Fatalf("worker event acks = %v, want none", got)
			}
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker event acks", eventAcksOf(worker), []workerAck{{held.origin, 1}})
		})

		// Ports "return each ticket to its sender: the relay's own events and two workers' on one tab origin, acks spanning them".
		t.Run("return each ticket to its sender: the relay's own events and two workers' on one tab origin, acks spanning them", func(t *testing.T) {
			r := eventRelay(t)
			oneWorker, oneHeld := r.relayedHold(t, 0, "S1", 0)
			twoWorker, twoHeld := r.relayedHold(t, 1, "S2", 1)
			provHoldMatch(t, r.Raw, 2)
			r.Raw.Next("bound")
			// Tickets in order: worker one, the relay's own, worker two, worker one.
			oneWorker.Send(workerEvents(t, oneHeld, []float64{1}))
			settleRelay(r.rawRelayHost)
			hostTurn(r.Server, func() { must(t, r.Probe.paddle(0).Emit(sample(2))) })
			settleRelay(r.rawRelayHost)
			twoWorker.Send(workerEvents(t, twoHeld, []float64{3}))
			settleRelay(r.rawRelayHost)
			oneWorker.Send(workerEvents(t, oneHeld, []float64{4}))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "tab ys", ysOf(tabEvents(t, r.Raw)), [][]any{{1.0}, {2.0}, {3.0}, {4.0}})
			r.Raw.Send(ack(3))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker one acks", eventAcksOf(oneWorker), []workerAck{{oneHeld.origin, 1}})
			requireEqual(t, "worker two acks", eventAcksOf(twoWorker), []workerAck{{twoHeld.origin, 1}})
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker one acks", eventAcksOf(oneWorker), []workerAck{{oneHeld.origin, 1}, {oneHeld.origin, 1}})
		})

		// Ports "filter entries of holds not relayed over the link after a release: some, then all, forwarded and never acked by the relay".
		t.Run("filter entries of holds not relayed over the link after a release: some, then all, forwarded and never acked by the relay", func(t *testing.T) {
			r := eventRelay(t)
			worker, held := r.relayedHold(t, 0, "S1", 0)
			holdPlaced(t, r.Raw, 1, "S1")
			other := nextHeld(worker)
			bindEditor(t, worker, other)
			r.Raw.Next("bound")
			r.Raw.Send(&link.ReleaseMessage{Hold: 1})
			worker.Next("release")
			mixed := workerEvents(t, held, []float64{1})
			mixed.Events = append(mixed.Events, workerEvents(t, held, []float64{2}, 1).Events...)
			worker.Send(mixed)
			worker.Send(workerEvents(t, held, []float64{3}, 1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "tab events", tabEvents(t, r.Raw), [][]tabEntry{{{0, 1.0}}, {}})
			if got := eventAcksOf(worker); len(got) != 0 {
				t.Fatalf("worker event acks = %v, want none", got)
			}
			r.Raw.Send(ack(2))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker event acks", eventAcksOf(worker), []workerAck{{held.origin, 2}})
		})

		// Ports "credit two senders behind an upstream relay in order: a filtered second message behind an unconsumed first".
		t.Run("credit two senders behind an upstream relay in order: a filtered second message behind an unconsumed first", func(t *testing.T) {
			// The raw worker plays a relay with two senders behind it: its acks must arrive in its messages' order.
			r := eventRelay(t)
			worker, held := r.relayedHold(t, 0, "S1", 0)
			holdPlaced(t, r.Raw, 1, "S1")
			other := nextHeld(worker)
			bindEditor(t, worker, other)
			r.Raw.Next("bound")
			r.Raw.Send(&link.ReleaseMessage{Hold: 1})
			worker.Next("release")
			// The first sender's message, then the second's, all of whose entries the relay filters.
			worker.Send(workerEvents(t, held, []float64{1}))
			worker.Send(workerEvents(t, held, []float64{2}, 1))
			settleRelay(r.rawRelayHost)
			// An early ack of the filtered one would credit the first sender's message, still unconsumed at the tab.
			if got := eventAcksOf(worker); len(got) != 0 {
				t.Fatalf("worker event acks = %v, want none", got)
			}
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker event acks", eventAcksOf(worker), []workerAck{{held.origin, 1}})
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "worker event acks", eventAcksOf(worker), []workerAck{{held.origin, 1}, {held.origin, 1}})
		})

		// Ports "consume the tickets of an upstream link that dropped, never crediting its replacement".
		t.Run("consume the tickets of an upstream link that dropped, never crediting its replacement", func(t *testing.T) {
			r := eventRelay(t)
			firstWorker, firstHeld := r.relayedHold(t, 0, "S1", 0)
			firstWorker.Send(workerEvents(t, firstHeld, []float64{1}))
			settleRelay(r.rawRelayHost)
			firstWorker.Close()
			settleRelay(r.rawRelayHost)
			next := r.Workers.At(t, 1)
			held := nextHeld(next)
			bindEditor(t, next, held)
			r.Raw.Next("bound")
			next.Send(workerEvents(t, held, []float64{2}))
			settleRelay(r.rawRelayHost)
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			if got := eventAcksOf(next); len(got) != 0 {
				t.Fatalf("replacement's event acks = %v, want none", got)
			}
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			requireEqual(t, "replacement's event acks", eventAcksOf(next), []workerAck{{held.origin, 1}})
		})

		// Ports "retire the tab's origin only once its tickets drained: closed upstream after the last ack".
		t.Run("retire the tab's origin only once its tickets drained: closed upstream after the last ack", func(t *testing.T) {
			r := eventRelay(t)
			worker, held := r.relayedHold(t, 0, "S1", 0)
			worker.Send(workerEvents(t, held, []float64{1}))
			settleRelay(r.rawRelayHost)
			r.Raw.Send(&link.ReleaseMessage{Hold: 0})
			worker.Next("release")
			settleRelay(r.rawRelayHost)
			if got := closedOf(worker); len(got) != 0 {
				t.Fatalf("closed before the last ack: %v", got)
			}
			r.Raw.Send(ack(1))
			settleRelay(r.rawRelayHost)
			if got := closedOf(worker); len(got) != 1 || got[0].Origin != held.origin.Value {
				t.Fatalf("closed = %v, want one of the hold's origin", got)
			}
		})

		// Ports "drop the tab's tickets when it closes: one closed upstream, later events of the origin ignored".
		t.Run("drop the tab's tickets when it closes: one closed upstream, later events of the origin ignored", func(t *testing.T) {
			r := eventRelay(t)
			worker, held := r.relayedHold(t, 0, "S1", 0)
			worker.Send(workerEvents(t, held, []float64{1}))
			settleRelay(r.rawRelayHost)
			r.Raw.Close()
			settleRelay(r.rawRelayHost)
			if got := closedOf(worker); len(got) != 1 || got[0].Origin != held.origin.Value {
				t.Fatalf("closed = %v, want one of the hold's origin", got)
			}
			worker.Send(workerEvents(t, held, []float64{2}))
			settleRelay(r.rawRelayHost)
			if worker.Closed() {
				t.Fatal("the worker's link closed")
			}
		})
	})
}

// PlacedMatch is a worker type with an event member (placed by its session), and Lobby a server view re-exposing it.
var PlacedMatch = mustContract(contracts.Remote("app.placed-match", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, HostAddress: exSessionAddress,
	Variants: []contracts.VariantEntry{contracts.Var("player", contracts.VariantDeclaration{
		Events: []contracts.Property{contracts.Prop("paddle", Sample)},
	})},
}))

var Lobby = mustContract(contracts.Remote("app.lobby", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("player", contracts.VariantDeclaration{
		Events: []contracts.Property{contracts.Prop("paddle", Sample)},
	})},
}))

// Ports "events across real hosts › reach a tab from a worker through the relay, and through the server's
// re-exposure, value and stamp unchanged".
func TestRelayEventsAcrossRealHostsReachATabThroughTheRelayAndThroughTheServersReExposure(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var mu sync.Mutex
		var paddles []contracts.EventSource[delta.JsonValue]
		rig := relay(t, relayedOptions{
			Worker: func(host FacetHost) error {
				return host.Serve(PlacedMatch, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					paddle := self.Events()
					mu.Lock()
					paddles = append(paddles, paddle)
					mu.Unlock()
					return contracts.Implementation{Variant: "player", Members: contracts.VariantImplementation{
						Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle},
					}}, nil
				}, ServeOptions{})
			},
			Server: func(host FacetHost) error {
				return host.Serve(Lobby, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					session, _ := params.Value("session").(string)
					return contracts.Implementation{Variant: "player", Members: contracts.VariantImplementation{
						Events: map[string]contracts.EventMember[delta.JsonValue]{
							"paddle": self.Get(PlacedMatch, jsonObject("session", session)).Events("paddle"),
						},
					}}, nil
				}, ServeOptions{})
			},
		})
		direct, err := rig.Tab.Get(background, PlacedMatch, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		lobby, err := rig.Tab.Get(background, Lobby, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		var seenMu sync.Mutex
		var seen []string
		stamps := map[int64]bool{}
		listener := func(tag string) func(contracts.EventDelivery[delta.JsonValue]) {
			return func(e contracts.EventDelivery[delta.JsonValue]) {
				seenMu.Lock()
				defer seenMu.Unlock()
				seen = append(seen, tag+formatNumber(e.Value.(*delta.JsonObject).Value("y").(float64)))
				stamps[e.At] = true
			}
		}
		on(t, direct, "paddle", listener("direct"), background)
		on(t, lobby, "paddle", listener("lobby"), background)
		// The tab's direct hold and the server's hold for the lobby are two holds on the worker's one object.
		rig.Transported()
		mu.Lock()
		paddle := paddles[len(paddles)-1]
		mu.Unlock()
		hostTurn(rig.Worker("session|S1"), func() { must(t, paddle.Emit(sample(1))) })
		rhWaitFor(t, func() bool {
			rig.Transported()
			seenMu.Lock()
			defer seenMu.Unlock()
			return len(seen) == 2
		})
		seenMu.Lock()
		defer seenMu.Unlock()
		if !(seen[0] == "direct1" && seen[1] == "lobby1" || seen[0] == "lobby1" && seen[1] == "direct1") {
			t.Fatalf("seen = %v, want direct1 and lobby1", seen)
		}
		if len(stamps) != 1 {
			t.Fatalf("stamps = %v, want one", stamps)
		}
	})
}

// Ports the describe "relayed calls in a real worker's finite window › never exceed it when a hold with a forwarded
// call is released: the release goes before the next call".
func TestRelayedCallsInARealWorkersFiniteWindowNeverExceedItWhenAHoldWithAForwardedCallIsReleased(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		waiter := mustContract(contracts.Remote("app.waiter", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("id", contracts.Int())},
			HostAddress: exSessionAddress,
			Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
				Methods: []contracts.MethodEntry{contracts.Meth("wait", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
			})},
		}))
		var mu sync.Mutex
		var runs []int
		rig := relay(t, relayedOptions{
			WorkerCallWindow: intp(1),
			Worker: func(host FacetHost) error {
				return host.Serve(waiter, func(params *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
					id := params.Value("id").(float64)
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						Methods: map[string]contracts.MethodFunction{
							"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								mu.Lock()
								runs = append(runs, int(id))
								mu.Unlock()
								UntilAborted(ctx)
								return id, nil
							},
						},
					}}, nil
				}, ServeOptions{})
			},
		})
		a, err := rig.Tab.Get(background, waiter, jsonObject("session", "S1", "id", 1.0), GetOptions{})
		must(t, err)
		b, err := rig.Tab.Get(background, waiter, jsonObject("session", "S1", "id", 2.0), GetOptions{})
		must(t, err)
		startCall(t, a, "wait", nil, background)
		second, cancel := chordctx.WithCancel(background)
		startCall(t, b, "wait", nil, second)
		rig.Transported()
		runsNow := func() []int {
			mu.Lock()
			defer mu.Unlock()
			return append([]int(nil), runs...)
		}
		rhWaitFor(t, func() bool { rig.Transported(); return len(runsNow()) >= 1 })
		rig.Transported()
		// The worker's window of 1: b's call waits at the relay.
		requireInts(t, "runs", runsNow(), []int{1})
		a.Release()
		rhWaitFor(t, func() bool { rig.Transported(); return len(runsNow()) >= 2 })
		requireInts(t, "runs", runsNow(), []int{1, 2})
		if rig.Errors.Worker.Len() != 0 || rig.Errors.Server.Len() != 0 {
			t.Fatalf("errors = worker %v, server %v", rig.Errors.Worker.All(), rig.Errors.Server.All())
		}
		if kind := refStateKind(b); kind != RefStateLive {
			t.Fatalf("b = %v, want live", kind)
		}
		cancel(nil)
	})
}
