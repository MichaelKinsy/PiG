package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
)

// Ports packages/chord/src/host/events.ts (events, decision "Events: fire-and-forget members": the third member kind's
// delivery on one host). An emit, or an event arriving over a link, takes the host's next counter value and waits in
// the host's list for the event flush, a microtask its first entry queues; commits flush synchronously, so every state
// message published before the flush runs is sent before it. At the flush each entry goes from its node (an object's own
// source, a re-exposed member, an older version's mapping, a remote object's member) to the taps of the bindings made
// before it (a hold over a link, a reference, the re-exposing nodes behind a reference): first every link's message,
// then the listeners, outside the publication's reentrancy fence, in entry order, then subscription order.
//
// Async contract (docs/extension-api-parity.md does not cover chord): `subscribe` is the one Promise of the file:
// `EventMember.on` returns an already settled Promise (reject with the abort reason of an aborted context, else
// resolve), so the Go call is blocking and returns (subscription, error) at once; nothing awaits anything else. The
// flush is a microtask (`Loop.Queue`), the listeners run synchronously inside it, a listener that throws (panics) is
// reported and ends its subscription. An abort listener on the context is `context.AfterFunc` entering the loop.

// eventListener is an event's listener, as EventMember.On takes it.
type eventListener = func(contracts.EventDelivery[delta.JsonValue])

// eventEntry is one event waiting for the flush: its node, its value and stamp (the emitting host's `Date.now()`), the
// host's counter value it took, and its value encoded for links once one needs it (one encode per representation,
// part; nil while undefined).
type eventEntry struct {
	node  *eventNode
	value delta.JsonValue
	at    int64
	n     int64
	part  link.Part
}

// tap is where a node delivers: one binding (a hold over a link, a reference, an older version's mapping), made when the
// host's counter was boundAt; it gets only entries with a higher counter value.
type tap struct {
	boundAt int64
	deliver func(entry *eventEntry, flush *eventFlush)
}

// eventSender is what sends a flush's events on a link once every entry was visited: one message per link and origin.
type eventSender interface {
	sendEvents()
}

// eventFlush is what taps hand to the flush while it visits them: a link's sender (once per flush), a listener's call.
type eventFlush struct {
	senders []eventSender
	calls   []eventCall
}

type eventCall struct {
	subscription *subscription
	entry        *eventEntry
}

func (f *eventFlush) sending(sender eventSender) { f.senders = append(f.senders, sender) }

func (f *eventFlush) call(sub *subscription, entry *eventEntry) {
	f.calls = append(f.calls, eventCall{sub, entry})
}

// eventReport is what a host's events report: a listener that threw (its subscription ended).
type eventReport func(err error)

// eventsLoop is what HostEvents asks of the host's loop: the clock, the microtask queue and the sections.
type eventsLoop interface {
	eventLoop
	Run(work func())
}

// eventSourceOwner is what an object's own source checks and counts with: the host's events, and its attempt's seal
// (`released` after).
type eventSourceOwner struct {
	events *hostEvents
	sealed func() error
}

// hostEvents is a host's events: its emit counter, its list waiting for the flush and the flush itself, and the order of
// its subscriptions. Nothing is kept once a flush ran.
type hostEvents struct {
	loop          eventsLoop
	metrics       *chordtelemetry.Instruments
	report        eventReport
	counter_      int64
	subscriptions int64
	list          []*eventEntry
}

func newHostEvents(loop eventsLoop, metrics *chordtelemetry.Instruments, report eventReport) *hostEvents {
	return &hostEvents{loop: loop, metrics: metrics, report: report}
}

// counter is the counter's value now: a binding made now gets only events pushed after it.
func (h *hostEvents) counter() int64 { return h.counter_ }

// push appends an event of node stamped at with the counter's next value; the first of a turn queues the flush.
func (h *hostEvents) push(node *eventNode, value delta.JsonValue, at int64) {
	h.counter_++
	if len(h.list) == 0 {
		h.loop.Queue(h.flush)
	}
	h.list = append(h.list, &eventEntry{node: node, value: value, at: at, n: h.counter_})
}

// nextSubscription is the next subscription's place in the listeners' order.
func (h *hostEvents) nextSubscription() int64 {
	h.subscriptions++
	return h.subscriptions
}

// flush visits every entry (its node's taps), sends what links collected, then calls the listeners in entry order, then
// subscription order; what they emit goes into the next flush.
func (h *hostEvents) flush() {
	entries := h.list
	h.list = nil
	flush := &eventFlush{}
	for _, entry := range entries {
		entry.node.deliver(entry, flush)
	}
	for _, sender := range flush.senders {
		sender.sendEvents()
	}
	// Grouped by entry already; an entry reaching several references is put in subscription order (stable).
	if len(flush.calls) > 1 {
		slices.SortStableFunc(flush.calls, func(a, b eventCall) int {
			if a.entry.n != b.entry.n {
				return int(a.entry.n - b.entry.n)
			}
			return int(a.subscription.order - b.subscription.order)
		})
	}
	for _, call := range flush.calls {
		call.subscription.call(call.entry, h.report)
	}
}

// eventNode is a source of events on a host: its taps are the bindings it delivers to. An object's own source, a
// re-exposed member, an older version's mapping and a remote object's member are nodes (each embeds one).
type eventNode struct {
	taps []*tap
}

// tapped is whether any binding taps it: an event arriving for a node nobody taps is dropped on arrival.
func (n *eventNode) tapped() bool { return len(n.taps) > 0 }

// tap makes t's binding get this node's entries from now on (those with a higher counter value than its boundAt).
func (n *eventNode) tap(t *tap) {
	if !slices.Contains(n.taps, t) {
		n.taps = append(n.taps, t)
	}
}

// untap: t's binding ended.
func (n *eventNode) untap(t *tap) {
	if i := slices.Index(n.taps, t); i >= 0 {
		n.taps = slices.Delete(slices.Clone(n.taps), i, i+1)
	}
}

// deliver delivers entry to every tap whose binding was made before it (boundAt < n).
func (n *eventNode) deliver(entry *eventEntry, flush *eventFlush) {
	for _, t := range n.taps {
		if t.boundAt < entry.n {
			t.deliver(entry, flush)
		}
	}
}

// clear: its instance ended: no binding taps it any more.
func (n *eventNode) clear() { n.taps = nil }

// subscription is a subscription (EventMember.On): its listener until Stop, its context's abort or its owner's end; a
// call due when it stopped is skipped. A listener that panics is reported (`listener_error`) and its subscription ends.
type subscription struct {
	// order is its place in the listeners' order (the host's subscriptions, in the order they were made).
	order    int64
	loop     eventsLoop
	listener eventListener
	// stopAbort and stopped are its context's abort registration and what tells its owner it stopped, both let go at
	// Stop: a kept handle keeps neither.
	stopAbort func() bool
	stopped   func(*subscription)
	// ctx is the context it was made under, until it stops: an abort ends it at once, as Pi's abort listener runs inside
	// the abort (Go's AfterFunc only enters the loop later), so a call due after the abort is skipped.
	ctx context.Context
}

func newSubscription(order int64, loop eventsLoop, listener eventListener, ctx context.Context, stopped func(*subscription)) *subscription {
	s := &subscription{order: order, loop: loop, listener: listener, stopped: stopped, ctx: ctx}
	s.stopAbort = context.AfterFunc(ctx, func() { loop.Run(s.stop) })
	return s
}

// Stop ends the subscription; its listener is not called again, a call already due included. Idempotent.
func (s *subscription) Stop() { s.loop.Run(s.stop) }

func (s *subscription) stop() {
	stopped := s.stopped
	if stopped == nil {
		return
	}
	s.listener = nil
	s.stopped = nil
	s.ctx = nil
	s.stopAbort()
	s.stopAbort = nil
	stopped(s)
}

// call calls its listener with entry's value and stamp, unless it stopped.
func (s *subscription) call(entry *eventEntry, report eventReport) {
	listener := s.listener
	if listener == nil {
		return
	}
	if s.ctx.Err() != nil {
		s.stop()
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			report(recoveredError(recovered))
			s.stop()
		}
	}()
	listener(contracts.EventDelivery[delta.JsonValue]{Value: entry.value, At: entry.at})
}

// subscribe subscribes listener under ctx with install (its owner keeps it and taps for it), at once; it returns the
// abort cause of an aborted context (installing nothing).
func subscribe(events *hostEvents, listener eventListener, ctx context.Context, install func(*subscription), stopped func(*subscription)) (contracts.EventSubscription, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	s := newSubscription(events.nextSubscription(), events.loop, listener, ctx, stopped)
	install(s)
	return s, nil
}

// ownEventSource is an object's own source (`self.events<T>()`), owned by its attempt: an emit takes the host's next
// counter value and waits for the flush; its own On subscriptions tap it from their subscription on. After the seal an
// emit returns `released`.
type ownEventSource struct {
	eventNode
	owner *eventSourceOwner
	// subscriptions are its own subscriptions (the owner's `on`), each with its tap; made by the first.
	subscriptions map[*subscription]*tap
}

func newOwnEventSource(owner *eventSourceOwner) *ownEventSource { return &ownEventSource{owner: owner} }

// ownedBy is whether owner owns it: an object implements its event members with its own attempt's sources only.
func (s *ownEventSource) ownedBy(owner *eventSourceOwner) bool { return s.owner == owner }

// Emit emits value now, outside any transaction (contracts.EventSource). It is one section of the host's loop (inline
// inside one): the rest of a handler that suspended runs outside the section that admitted its call, and Pi's emit is
// atomic on the one event loop.
func (s *ownEventSource) Emit(value delta.JsonValue) (err error) {
	owner := s.owner
	owner.events.loop.Run(func() {
		if err = owner.sealed(); err != nil {
			return
		}
		owner.events.metrics.Counter("chord.events.emitted").Add(1, nil)
		owner.events.push(&s.eventNode, value, owner.events.loop.Now())
	})
	return err
}

// On subscribes listener (contracts.EventMember).
func (s *ownEventSource) On(listener func(contracts.EventDelivery[delta.JsonValue]), ctx context.Context) (contracts.EventSubscription, error) {
	events := s.owner.events
	var (
		subscription contracts.EventSubscription
		err          error
	)
	events.loop.Run(func() { subscription, err = s.on(events, listener, ctx) })
	return subscription, err
}

func (s *ownEventSource) on(events *hostEvents, listener func(contracts.EventDelivery[delta.JsonValue]), ctx context.Context) (contracts.EventSubscription, error) {
	return subscribe(events, listener, ctx,
		func(sub *subscription) {
			t := &tap{boundAt: events.counter(), deliver: func(entry *eventEntry, flush *eventFlush) { flush.call(sub, entry) }}
			if s.subscriptions == nil {
				s.subscriptions = map[*subscription]*tap{}
			}
			s.subscriptions[sub] = t
			s.tap(t)
		},
		func(sub *subscription) {
			t, ok := s.subscriptions[sub]
			if !ok {
				return
			}
			delete(s.subscriptions, sub)
			s.untap(t)
		})
}

// detach: its attempt ended: every binding's tap goes, and its own subscriptions end.
func (s *ownEventSource) detach() {
	s.clear()
	subscriptions := make([]*subscription, 0, len(s.subscriptions))
	for sub := range s.subscriptions {
		subscriptions = append(subscriptions, sub)
	}
	slices.SortFunc(subscriptions, func(a, b *subscription) int { return int(a.order - b.order) })
	for _, sub := range subscriptions {
		sub.stop()
	}
}

var _ contracts.EventSource[delta.JsonValue] = (*ownEventSource)(nil)
