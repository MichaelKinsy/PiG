package plugins

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// A NodeReference's event members: what the Go side does with the Node host's `event` messages (plugins_node_events.go).
// No Pi file corresponds to this one: it is a reference's event member (Pi host/reference-events.ts `ReferenceEvents`)
// for a reference that is held over the control channel. The Node host taps its own reference when it binds it for the
// Go side (node/bridge.mjs `get`, node/events.mjs `tap`), listener or not, as a hold over a link is tapped when it binds
// (Pi remote/hold.ts:590-594), so an event emitted once On returned is never lost to a subscription that is on its way.
//
// Async contract: On registers and returns at once (Pi's `on` is an already settled Promise). The listeners run on the
// goroutine that reads the channel, in the order the Node host sent the entries and, for one entry, in the order the
// subscriptions were made, before a reply that follows on the channel settles its Call; the message is acknowledged after
// they returned. A listener must return without waiting for the Node host: the goroutine that runs it is the one that
// reads the host's replies. Stop returns at once: no call of the listener starts after it returned, and one that is
// running on the reader finishes. A subscription ends with Stop, its context's abort, the reference's release and the
// process's exit; nothing is started for one but its context's own abort registration.

// NodeEvents is an event member of a NodeReference (contracts.EventMember): its listeners get what the Node host's
// reference receives, value and stamp as the plugin's object emitted them. Additive Go API, as NodeReference.
type NodeEvents struct {
	reference *NodeReference
	// subscriptions are its subscriptions in the order they were made, under reference.mu; replaced, never changed in
	// place: a delivery calls the ones it read.
	subscriptions []*nodeSubscription
}

// nodeSubscription is a subscription of a NodeEvents (contracts.EventSubscription).
type nodeSubscription struct {
	events   *NodeEvents
	listener func(contracts.EventDelivery[delta.JsonValue])
	ctx      context.Context
	// stopAbort lets go of the context's abort registration; set under reference.mu before the subscription is reachable.
	stopAbort func() bool
	stopped   atomic.Bool
}

// Events is the event member name, nil when the contract has none.
func (r *NodeReference) Events(name string) *NodeEvents { return r.events[name] }

// On subscribes listener at once (contracts.EventMember): it gets every event of the member that arrives from now on. It
// fails with `released` for a released reference, with the abort cause of an aborted ctx, and with the process's exit
// once the process ended.
func (e *NodeEvents) On(listener func(contracts.EventDelivery[delta.JsonValue]), ctx context.Context) (contracts.EventSubscription, error) {
	r := e.reference
	exited := false
	select {
	case <-r.node.exited:
		exited = true
	default:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		return nil, releasedError(r.contract.Name)
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	// eventsEnded: the process ended between the look above and the lock, and the reference's subscriptions with it.
	if exited || r.eventsEnded {
		return nil, r.node.exitError()
	}
	sub := &nodeSubscription{events: e, listener: listener, ctx: ctx}
	e.subscriptions = append(slices.Clone(e.subscriptions), sub)
	// Stop takes r.mu: an abort that is already there waits for this registration.
	sub.stopAbort = context.AfterFunc(ctx, sub.Stop)
	return sub, nil
}

// Stop ends the subscription (contracts.EventSubscription): its listener is not called again. Idempotent.
func (s *nodeSubscription) Stop() {
	e := s.events
	e.reference.mu.Lock()
	if s.stopped.Swap(true) {
		e.reference.mu.Unlock()
		return
	}
	if i := slices.Index(e.subscriptions, s); i >= 0 {
		e.subscriptions = slices.Delete(slices.Clone(e.subscriptions), i, i+1)
	}
	stopAbort := s.stopAbort
	e.reference.mu.Unlock()
	stopAbort()
}

// call calls the listener unless the subscription stopped or its context ended. A listener that panics is reported
// (`listener_error`, NodeOptions.OnError) and its subscription ends, as on a host (Pi host/events.ts `Subscription.call`).
func (s *nodeSubscription) call(delivery contracts.EventDelivery[delta.JsonValue]) {
	if s.stopped.Load() {
		return
	}
	if s.ctx.Err() != nil {
		s.Stop()
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.Stop()
			err, ok := recovered.(error)
			if !ok {
				err = errors.New(fmt.Sprint(recovered))
			}
			if report := s.events.reference.node.options.OnError; report != nil {
				report(NodeHostError{Kind: "listener_error", Err: err})
			}
		}
	}()
	s.listener(delivery)
}

// deliverEvent is one entry of an `event` message for this reference: dropped unparsed when nothing listens to its
// member (Pi remote/remote-object.ts:525-527), else parsed once and handed to the member's subscriptions in the order
// they were made.
func (r *NodeReference) deliverEvent(entry *hostevents.Entry) {
	if entry.Member < 0 || entry.Member >= len(r.eventOrder) {
		return
	}
	events := r.eventOrder[entry.Member]
	r.mu.Lock()
	subscriptions := events.subscriptions
	r.mu.Unlock()
	if len(subscriptions) == 0 {
		return
	}
	value, err := link.ParsePart(link.Text(entry.Value))
	if err != nil {
		return
	}
	delivery := contracts.EventDelivery[delta.JsonValue]{Value: value, At: entry.At}
	for _, sub := range subscriptions {
		sub.call(delivery)
	}
}

// endEvents ends every subscription of the reference, for good: its release, or the process's exit.
func (r *NodeReference) endEvents() {
	r.mu.Lock()
	r.eventsEnded = true
	var ended []*nodeSubscription
	for _, events := range r.eventOrder {
		ended = append(ended, events.subscriptions...)
		events.subscriptions = nil
	}
	r.mu.Unlock()
	for _, sub := range ended {
		if !sub.stopped.Swap(true) {
			sub.stopAbort()
		}
	}
}

var _ contracts.EventMember[delta.JsonValue] = (*NodeEvents)(nil)
