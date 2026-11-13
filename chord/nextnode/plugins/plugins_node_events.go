package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
)

// Events across the bridge between a Go host and a Node host (node/events.mjs is the Node side). No Pi file corresponds to
// this one: in Pi the app's facets and the plugins' load into one host, so an event reaches a listener inside that host's
// flush; here the two hosts are joined by the control channel, which carries a link's own two messages (Pi
// remote/provider.ts `sendEvents`, `#eventAck`; remote/consumer.ts `#events`):
//
//	Go to Node   { op: "event", events: [{ hold, member, at, value }] }     one message per flush of a Go host
//	Node to Go   { event: "ack", events: n }                                n more of them were consumed there
//	Node to Go   { event: "event", events: [{ hold, member, at, value }] }  one message per flush of the Node host
//	Go to Node   { op: "ack", events: n }                                   n more of them were consumed here
//
// `hold` is the handle of the reference on the side that holds it, `member` an index into the type's event members in the
// order the Go side named them, `at` the emitting host's stamp and `value` the link's JSON part text (link.JsonPart), not
// the control channel's value form (plugins_node_wire.go): an event's value crosses as it crosses a link. The sending
// host bounds what is unacknowledged and drops beyond it, counted (a Go host's hostevents.Port, the Node host's
// events.mjs); the receiving side acknowledges a message once it delivered it.
//
// Async contract: nothing here blocks its caller. A Go host's flush hands its message over inside its section and the
// channel's reader hands over an acknowledgement, and both only queue: one goroutine at a time writes the queue to the
// channel in order, started by the first message that waits and ended with the last. It is joined when the process ended
// (NodeHost.wait, before Exited closes): nothing is written after, and what was queued is dropped with the process.
// `wait` waits, under the caller's context, until what was queued before a `mark` was written: never for what was queued
// after it, so a call's answer that follows the call's events is not held by the events of other objects.

// eventOutbox is the Go side's sender of the bridge's `event` and `ack` messages.
type eventOutbox struct {
	// control writes one control message to the process.
	control func(message any) error

	mu    sync.Mutex
	queue []outboxMessage
	// tickets are the ports of the `event` messages written and not acknowledged, oldest first (Pi remote/provider.ts
	// `#tickets`): several Go hosts can be bridged into one Node host, each with its own port and window.
	tickets []hostevents.Port
	sending bool
	// idle is closed when the sender started last has returned; nil before the first.
	idle    chan struct{}
	stopped bool
	// queued and written count the messages queued and written so far.
	queued, written int
	// progress is what the waits whose mark is not written yet wait on: closed at the next write, and nil from then
	// until a wait makes the next. The close and the nil are one critical section of mu, so a channel that was closed
	// is never found here again. A wait is parked only while a message is queued or being written, so the sender is
	// running and its next turn wakes it, stopped or not.
	progress chan struct{}
}

// outboxMessage is one queued message: port is the port an `event` message belongs to, nil for an `ack`.
type outboxMessage struct {
	message any
	port    hostevents.Port
}

// eventsMessage is an `event` message as the Go side sends it.
type eventsMessage struct {
	Op     string             `json:"op"`
	Events []hostevents.Entry `json:"events"`
}

// ackMessage is an `ack` message as the Go side sends it.
type ackMessage struct {
	Op     string `json:"op"`
	Events int    `json:"events"`
}

// event queues one flush's message of port. It is a port's `send`: it runs inside the Go host's section and only queues.
func (o *eventOutbox) event(port hostevents.Port, entries []hostevents.Entry) {
	o.enqueue(outboxMessage{message: eventsMessage{Op: "event", Events: entries}, port: port})
}

// ack queues the acknowledgement of that many `event` messages of the Node host.
func (o *eventOutbox) ack(messages int) {
	o.enqueue(outboxMessage{message: ackMessage{Op: "ack", Events: messages}})
}

func (o *eventOutbox) enqueue(message outboxMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return
	}
	o.queue = append(o.queue, message)
	o.queued++
	if o.sending {
		return
	}
	o.sending = true
	idle := make(chan struct{})
	o.idle = idle
	go func() {
		o.drain()
		close(idle)
	}()
}

// drain writes the queue in order until it is empty or the sender stopped.
func (o *eventOutbox) drain() {
	for wrote := false; ; wrote = true {
		o.mu.Lock()
		if wrote {
			o.written++
			if o.progress != nil {
				close(o.progress)
				o.progress = nil
			}
		}
		if len(o.queue) == 0 || o.stopped {
			o.queue, o.sending = nil, false
			o.mu.Unlock()
			return
		}
		next := o.queue[0]
		o.queue[0] = outboxMessage{}
		o.queue = o.queue[1:]
		if next.port != nil {
			// The ticket is there before the message is: its acknowledgement cannot arrive first.
			o.tickets = append(o.tickets, next.port)
		}
		o.mu.Unlock()
		_ = o.control(next.message)
	}
}

// mark is the number of messages queued so far: what a wait waits for.
func (o *eventOutbox) mark() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.queued
}

// wait waits until the messages queued before mark was taken were written, or the sender stopped (nil), or ctx ends
// (its cause). What was queued after the mark is not waited for: each write wakes the waits, and one whose mark is
// written returns.
func (o *eventOutbox) wait(mark int, ctx context.Context) error {
	for {
		o.mu.Lock()
		if o.written >= mark || o.stopped {
			o.mu.Unlock()
			return nil
		}
		if o.progress == nil {
			o.progress = make(chan struct{})
		}
		progress := o.progress
		o.mu.Unlock()
		select {
		case <-progress:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// flush waits until every message queued before the call was written, or ctx ends.
func (o *eventOutbox) flush(ctx context.Context) error { return o.wait(o.mark(), ctx) }

// acked is an `ack` of the Node host: it consumed that many more `event` messages, the oldest tickets, and each frees a
// slot of its port's window. One that counts more than were written finds no ticket for the rest and is ignored there
// (Pi remote/provider.ts:681-684): no error, and no window grows.
func (o *eventOutbox) acked(messages int) {
	o.mu.Lock()
	count := max(0, min(messages, len(o.tickets)))
	taken := slices.Clone(o.tickets[:count])
	clear(o.tickets[:count])
	o.tickets = o.tickets[count:]
	o.mu.Unlock()
	for len(taken) > 0 {
		port, run := taken[0], 1
		for run < len(taken) && taken[run] == port {
			run++
		}
		port.Ack(run)
		taken = taken[run:]
	}
}

// stop ends the sender: what is queued is dropped, nothing is written from now on, and the goroutine that was writing
// has left drain when stop returns.
func (o *eventOutbox) stop() {
	o.mu.Lock()
	o.stopped = true
	o.tickets = nil
	idle := o.idle
	o.mu.Unlock()
	if idle != nil {
		<-idle
	}
}

// controlEvents is the sender's write: one control message to the process.
func (h *NodeHost) controlEvents(message any) error {
	port := h.port.Load()
	if port == nil {
		return errors.New("the node subprocess's channel is gone")
	}
	return port.control(message)
}

// eventsAcked is an `ack` message of the Node host. It runs on the goroutine that reads the channel: a port's Ack never
// waits for its host.
func (h *NodeHost) eventsAcked(payload []byte) {
	var message struct {
		Events int `json:"events"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return
	}
	h.events.acked(message.Events)
}

// nodeEvents is one `event` message of the Node host: each entry goes to the reference it names (dropped for a hold this
// side released or never had, as a consumer drops one whose hold it does not know: Pi remote/consumer.ts:312-320), in the
// message's order, and the message is acknowledged once the listeners returned, whatever it held: a message that does not
// decode is acknowledged too, or its slot of the Node host's window would never be free again. It runs on the goroutine
// that reads the channel, which writes nothing: the acknowledgement goes through the sender.
func (h *NodeHost) nodeEvents(payload []byte) {
	var message struct {
		Events []hostevents.Entry `json:"events"`
	}
	if err := json.Unmarshal(payload, &message); err == nil {
		for i := range message.Events {
			if reference := h.bridge.reference(message.Events[i].Hold); reference != nil {
				reference.deliverEvent(&message.Events[i])
			}
		}
	}
	h.events.ack(1)
}
