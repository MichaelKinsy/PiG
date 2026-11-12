package host

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/remote/consumer.ts

// Ports the events section of packages/chord/test/remote/consumer.test.ts (describe 'events at the consumer (decision
// "Events: fire-and-forget members")').

// eventClientOptions is the options of eventClient.
type eventClientOptions struct {
	Limits      link.PartLimits
	Compression link.Compression
	// Watcher binds the watcher variant (no paddle) first.
	Watcher bool
}

// eventClientHost is what eventClient returns: Pi's `{ client, peer, match, paddle, hold, errors, counts }`.
type eventClientHost struct {
	Client Host
	Peer   *RawPeer
	Match  *Reference
	Hold   int
	Errors *hostErrors
	meter  *telemetry.InMemoryTelemetryContext
	// inline makes the link deliver on the sender's goroutine while set (see turn).
	inline *atomic.Bool
	queue  *heldQueue
}

// turn is Pi's one JS turn on the client: the messages the peer sends in work reach the client in one section of its
// host (the link delivers inline), so what they queue (the event ack) runs after all of them.
func (k *eventClientHost) turn(work func()) {
	k.queue.flush()
	k.inline.Store(true)
	hostTurn(k.Client, work)
	k.inline.Store(false)
}

// counts are the counters of the client's telemetry, by name (with `.reason` when it has one).
func (k *eventClientHost) counts() map[string]float64 {
	result := map[string]float64{}
	for _, measurement := range k.meter.GetMetrics() {
		key := measurement.Name
		if reason, ok := measurement.Attributes.Get("reason"); ok {
			key += "." + reason.(string)
		}
		result[key] += measurement.Value
	}
	return result
}

// paddle subscribes listener to the match's paddle.
func (k *eventClientHost) paddle(t testing.TB, listener func(contracts.EventDelivery[delta.JsonValue])) contracts.EventSubscription {
	t.Helper()
	return on(t, k.Match, "paddle", listener, background)
}

// eventClient is a client connected to a raw provider with limits and compression, holding Match bound (the player
// variant unless Watcher); its background errors and its telemetry's counters.
func eventClient(t testing.TB, options eventClientOptions) *eventClientHost {
	t.Helper()
	k := &eventClientHost{Errors: &hostErrors{}, meter: telemetry.NewInMemoryTelemetryContext(), inline: &atomic.Bool{}, queue: newHeldQueue()}
	k.Client = newHost(t, HostOptions{Compression: options.Compression, Telemetry: k.meter, OnError: k.Errors.push})
	mustLoad(t)(k.Client.Load(nil, background))
	a, b := link.MemoryLinkPairOn(func(job func()) {
		if k.inline.Load() {
			job()
			return
		}
		k.queue.post(job)
	}, k.queue.flush)
	must(t, k.Client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec(), Limits: options.Limits}, background))
	k.Peer = newRawPeer(t, b, rawPeerOptions{})
	got := asyncGet(k.Client, Match, jsonObject("id", float64(1)), background, GetOptions{})
	k.Hold = k.Peer.Next("hold").(*link.HoldMessage).Hold
	variant := "player"
	if options.Watcher {
		variant = "watcher"
	}
	bindMatch(t, k.Peer, k.Hold, variant)
	k.Match = awaitReference(t, got)
	return k
}

// bindMatch sends `bound` for hold with the player (or watcher) variant and a score of 0.
func bindMatch(t testing.TB, peer *RawPeer, hold int, variant string) {
	t.Helper()
	names := wireNames(t, Match, variant)
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
		Hold: hold, Variant: names.Variant, Failed: []link.FailedMember{},
		Members: []link.BoundMember{{Member: names.State["score"], Value: mustJSONPart(t, 0.0)}},
	}}})
}

// paddleEvent is an event entry of the paddle (or member, when given) of hold with y, stamped at.
func paddleEvent(t testing.TB, hold int, y float64, at int, member ...int) link.WireEvent {
	t.Helper()
	index := wireNames(t, Match, "player").Events["paddle"]
	if len(member) > 0 {
		index = member[0]
	}
	return link.WireEvent{Hold: hold, Member: index, At: at, Value: mustJSONPart(t, jsonObject("y", y))}
}

// eventAcks are the event acks a raw provider received, by their counts.
func eventAcks(peer *RawPeer) []int {
	acks := []int{}
	for _, message := range peer.Received() {
		if ack, ok := message.(*link.AckMessage); ok && ack.Events.Ok {
			acks = append(acks, ack.Events.Value)
		}
	}
	return acks
}

func sumInts(values []int) int {
	sum := 0
	for _, value := range values {
		sum += value
	}
	return sum
}

func TestConsumerEvents(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// Ports "deliver the entries in order with their stamps, and acknowledge once per inbound turn, after the flush".
		t.Run("deliver the entries in order with their stamps, and acknowledge once per inbound turn, after the flush", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{})
			type seenEvent struct {
				y  any
				at int64
			}
			var seen []seenEvent
			var acksAtDelivery []int
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, seenEvent{e.Value.(*delta.JsonObject).Value("y"), e.At})
				acksAtDelivery = append(acksAtDelivery, len(eventAcks(k.Peer)))
			})
			k.turn(func() {
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 1, 10), paddleEvent(t, k.Hold, 2, 11)}})
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 3, 12)}})
			})
			settle(k.Peer)
			requireEqual(t, "seen", seen, []seenEvent{{1.0, 10}, {2.0, 11}, {3.0, 12}})
			requireEqual(t, "acks at delivery", acksAtDelivery, []int{0, 0, 0})
			requireEqual(t, "event acks", eventAcks(k.Peer), []int{2})
		})

		// Ports "acknowledge dropped entries too: an unknown hold, a member nobody listens to, a value past the part limit".
		t.Run("acknowledge dropped entries too: an unknown hold, a member nobody listens to, a value past the part limit", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{Limits: link.PartLimits{Part: new(16.0)}})
			var seen []any
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, e.Value.(*delta.JsonObject).Value("y"))
			})
			ball := wireNames(t, Match, "player").Events["ball"]
			k.turn(func() {
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold+7, 1, 1)}})
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 2, 1, ball)}})
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{{
					Hold: k.Hold, Member: wireNames(t, Match, "player").Events["paddle"], At: 1,
					Value: mustJSONPart(t, jsonObject("y", 3.0, "pad", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")),
				}}})
				k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 4, 1)}})
			})
			settle(k.Peer)
			requireEqual(t, "seen", seen, []any{4.0})
			requireEqual(t, "event acks", eventAcks(k.Peer), []int{4})
			if got := k.counts()["chord.events.dropped.too_large"]; got != 1 {
				t.Fatalf("dropped too_large = %v, want 1", got)
			}
			// The oversized value failed nothing: the hold stays bound.
			if kind := refStateKind(k.Match); kind != RefStateLive {
				t.Fatalf("state = %v, want live", kind)
			}
		})

		// Ports "inflate nothing for a member nobody listens to, nor parse it; acknowledged all the same".
		t.Run("inflate nothing for a member nobody listens to, nor parse it; acknowledged all the same", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{})
			var seen []any
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, e.Value.(*delta.JsonObject).Value("y"))
			})
			deflated := func(y float64) link.Part {
				text, err := link.JsonPart(jsonObject("y", y))
				must(t, err)
				return link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte(string(text))))
			}
			names := wireNames(t, Match, "player")
			k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{{Hold: k.Hold, Member: names.Events["ball"], At: 1, Value: deflated(1)}}})
			settle(k.Peer)
			if got, found := k.counts()["chord.link.inflates"]; found && got != 0 {
				t.Fatalf("inflates = %v, want none", got)
			}
			k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{{Hold: k.Hold, Member: names.Events["paddle"], At: 1, Value: deflated(2)}}})
			rhWaitFor(t, func() bool { return len(eventAcks(k.Peer)) > 0 && sumInts(eventAcks(k.Peer)) == 2 })
			settle(k.Peer)
			requireEqual(t, "seen", seen, []any{2.0})
			if got := k.counts()["chord.link.inflates"]; got != 1 {
				t.Fatalf("inflates = %v, want 1", got)
			}
			if got := sumInts(eventAcks(k.Peer)); got != 2 {
				t.Fatalf("acked events = %d, want 2", got)
			}
		})

		// Ports "close the link for a member index outside the bound version (a protocol error)".
		t.Run("close the link for a member index outside the bound version (a protocol error)", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{})
			k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 1, 1, 9)}})
			settle(k.Peer)
			if !k.Peer.Closed() {
				t.Fatal("the link stayed open")
			}
			equalHostErrorKinds(t, k.Errors, ErrProtocolError)
		})

		// Ports "deliver nothing of an earlier binding: an event, a loading and a bound (the same variant, another) in one turn".
		t.Run("deliver nothing of an earlier binding: an event, a loading and a bound (the same variant, another) in one turn", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{})
			var seen []any
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, e.Value.(*delta.JsonObject).Value("y"))
			})
			for _, variant := range []string{"player", "watcher"} {
				k.turn(func() {
					k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 1, 1)}})
					k.Peer.Send(&link.LoadingMessage{Hold: k.Hold})
					bindMatch(t, k.Peer, k.Hold, variant)
				})
				settle(k.Peer)
			}
			if len(seen) != 0 {
				t.Fatalf("seen = %v, want none", seen)
			}
			if got, _, _ := k.Match.Variant(); got != "watcher" {
				t.Fatalf("variant = %q, want watcher", got)
			}
		})

		// Ports "deliver none while bound to a variant without the member, and again once a binding has it".
		t.Run("deliver none while bound to a variant without the member, and again once a binding has it", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{Watcher: true})
			var seen []any
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, e.Value.(*delta.JsonObject).Value("y"))
			})
			k.Peer.Send(&link.LoadingMessage{Hold: k.Hold})
			bindMatch(t, k.Peer, k.Hold, "player")
			settle(k.Peer)
			k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{paddleEvent(t, k.Hold, 5, 1)}})
			settle(k.Peer)
			requireEqual(t, "seen", seen, []any{5.0})
		})

		// "keep the event window exhausted while an asynchronous inflate holds the message: acknowledged once consumed":
		// Pi stalls a stubbed DecompressionStream; link.InflateAsync cannot be stalled (see the GC test of the
		// asynchronous inflate), so the Go form asserts what is observable: a deflated event is acknowledged once, after
		// it was delivered.
		t.Run("acknowledge a deflated event once, after it was consumed", func(t *testing.T) {
			k := eventClient(t, eventClientOptions{})
			var seen []any
			var acksAtDelivery []int
			k.paddle(t, func(e contracts.EventDelivery[delta.JsonValue]) {
				seen = append(seen, e.Value.(*delta.JsonObject).Value("y"))
				acksAtDelivery = append(acksAtDelivery, len(eventAcks(k.Peer)))
			})
			text, err := link.JsonPart(jsonObject("y", 1.0))
			must(t, err)
			value := link.NewDeflatedPart(nextnode.NodeCompression.Deflate([]byte(string(text))))
			k.Peer.Send(&link.EventMessage{Events: []link.WireEvent{{Hold: k.Hold, Member: wireNames(t, Match, "player").Events["paddle"], At: 1, Value: value}}})
			rhWaitFor(t, func() bool { return len(eventAcks(k.Peer)) > 0 })
			settle(k.Peer)
			requireEqual(t, "seen", seen, []any{1.0})
			requireEqual(t, "acks at delivery", acksAtDelivery, []int{0})
			requireEqual(t, "event acks", eventAcks(k.Peer), []int{1})
		})
	})
}
