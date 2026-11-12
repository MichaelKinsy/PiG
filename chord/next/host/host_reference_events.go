package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/host/reference-events.ts (a reference's event members, decision "Events: fire-and-forget
// members": what a reference exposes per event member (`on` only, never `emit`), what an object re-exposing one
// implements its member with, and a binding's taps on the instance it is bound to). Per member a binding keeps its
// subscriptions and the re-exposing nodes it feeds, tapped on the instance's node from the counter value of its binding
// to that instance; it moves them to every new instance it binds (an older version's mapping; a variant without the
// member: none), and keeps them untapped while it points at nothing. Nothing missed is replayed.
//
// Async contract: `EventMember.on` is Pi's already settled Promise (see host_events.go): On blocks for nothing, enters the
// loop once and returns (subscription, error). No other Promise; every method here runs inside a section.

// ReferenceEvents is a reference's event member: its listeners follow the reference's binding (contracts.EventMember).
type ReferenceEvents struct {
	binding *binding
	member  string
}

// On subscribes listener to the member at once, bound or not (contracts.EventMember).
func (r *ReferenceEvents) On(listener func(contracts.EventDelivery[delta.JsonValue]), ctx context.Context) (contracts.EventSubscription, error) {
	var (
		sub contracts.EventSubscription
		err error
	)
	r.binding.loop.Run(func() { sub, err = r.binding.subscribeEvents(r.member, listener, ctx) })
	return sub, err
}

var _ contracts.EventMember[delta.JsonValue] = (*ReferenceEvents)(nil)

// reexposedEvents is an object's event member re-exposing a reference's (ReferenceEvents): the reference feeds it what it
// receives, value and stamp unchanged, from the attempt's members check until its end; its own taps are the object's
// bindings.
type reexposedEvents struct {
	eventNode
	source *ReferenceEvents
}

func newReexposedEvents(source *ReferenceEvents) *reexposedEvents {
	return &reexposedEvents{source: source}
}

// attach: fed by its reference from now on.
func (r *reexposedEvents) attach() { r.source.binding.forwardEvents(r.source.member, &r.eventNode) }

// detach: its attempt ended: no longer fed, no binding taps it.
func (r *reexposedEvents) detach() {
	r.source.binding.unforwardEvents(r.source.member, &r.eventNode)
	r.clear()
}

// memberTaps is one event member of a binding: its subscriptions, the nodes it feeds, and its tap on the instance's node.
type memberTaps struct {
	subscriptions []*subscription
	forwards      []*eventNode
	attached      *attachedTap
}

type attachedTap struct {
	node *eventNode
	tap  *tap
}

// eventsAt is the instance a binding's event members tap, and the host's counter value when it bound to it.
type eventsAt struct {
	instance attachable
	boundAt  int64
}

// referenceEventTaps are a binding's taps: per event member used (a subscription, a re-exposing node), tapped on the
// node of the instance it points at, at its version; a member nobody uses any more is untapped and forgotten.
type referenceEventTaps struct {
	members map[string]*memberTaps
	// order keeps the members in the order they were first used (a Map's insertion order).
	order   []string
	version int
	// at is the instance it points at and the counter value of its binding to it; nil while it points at nothing.
	at *eventsAt
}

func newReferenceEventTaps(version int, at *eventsAt) *referenceEventTaps {
	return &referenceEventTaps{members: map[string]*memberTaps{}, version: version, at: at}
}

// retarget: its binding points at at's instance now, bound at at.boundAt (or at nothing): every member moves there.
func (t *referenceEventTaps) retarget(at *eventsAt) {
	t.at = at
	for _, member := range slices.Clone(t.order) {
		if taps, ok := t.members[member]; ok {
			t.place(member, taps)
		}
	}
}

// subscribe adds sub to member's listeners.
func (t *referenceEventTaps) subscribe(member string, sub *subscription) {
	taps := t.taps(member)
	taps.subscriptions = append(taps.subscriptions, sub)
}

// unsubscribe: sub stopped.
func (t *referenceEventTaps) unsubscribe(member string, sub *subscription) {
	taps, ok := t.members[member]
	if !ok {
		return
	}
	if i := slices.Index(taps.subscriptions, sub); i >= 0 {
		taps.subscriptions = slices.Delete(slices.Clone(taps.subscriptions), i, i+1)
	}
	t.forgetUnused(member, taps)
}

// forward feeds node (an object re-exposing this member) what member receives.
func (t *referenceEventTaps) forward(member string, node *eventNode) {
	taps := t.taps(member)
	if !slices.Contains(taps.forwards, node) {
		taps.forwards = append(taps.forwards, node)
	}
}

// unforward stops feeding node.
func (t *referenceEventTaps) unforward(member string, node *eventNode) {
	taps, ok := t.members[member]
	if !ok {
		return
	}
	if i := slices.Index(taps.forwards, node); i >= 0 {
		taps.forwards = slices.Delete(slices.Clone(taps.forwards), i, i+1)
	}
	t.forgetUnused(member, taps)
}

// end: its binding ended: every subscription stops, nothing is tapped or fed any more.
func (t *referenceEventTaps) end() {
	members := make([]*memberTaps, 0, len(t.order))
	for _, member := range t.order {
		members = append(members, t.members[member])
	}
	t.members = map[string]*memberTaps{}
	t.order = nil
	for _, taps := range members {
		if taps.attached != nil {
			taps.attached.node.untap(taps.attached.tap)
		}
		for _, sub := range slices.Clone(taps.subscriptions) {
			sub.stop()
		}
	}
}

// taps is member's record, made and tapped by its first use.
func (t *referenceEventTaps) taps(member string) *memberTaps {
	taps, ok := t.members[member]
	if !ok {
		taps = &memberTaps{}
		t.members[member] = taps
		t.order = append(t.order, member)
		t.place(member, taps)
	}
	return taps
}

// place taps member on the node of the instance pointed at (none: a variant without it, or nothing pointed at).
func (t *referenceEventTaps) place(member string, taps *memberTaps) {
	if taps.attached != nil {
		taps.attached.node.untap(taps.attached.tap)
	}
	taps.attached = nil
	at := t.at
	if at == nil {
		return
	}
	node := at.instance.eventNode(member, t.version)
	if node == nil {
		return
	}
	tp := &tap{boundAt: at.boundAt, deliver: func(entry *eventEntry, flush *eventFlush) { deliverMember(taps, entry, flush) }}
	node.tap(tp)
	taps.attached = &attachedTap{node: node, tap: tp}
}

// forgetUnused untaps and forgets member once nobody uses it.
func (t *referenceEventTaps) forgetUnused(member string, taps *memberTaps) {
	if len(taps.subscriptions) > 0 || len(taps.forwards) > 0 {
		return
	}
	delete(t.members, member)
	if i := slices.Index(t.order, member); i >= 0 {
		t.order = slices.Delete(t.order, i, i+1)
	}
	if taps.attached != nil {
		taps.attached.node.untap(taps.attached.tap)
	}
}

// deliverMember is one entry for a member: its listeners' calls, and the nodes re-exposing it, which deliver to their own
// bindings.
func deliverMember(taps *memberTaps, entry *eventEntry, flush *eventFlush) {
	for _, sub := range taps.subscriptions {
		flush.call(sub, entry)
	}
	for _, node := range taps.forwards {
		node.deliver(entry, flush)
	}
}
