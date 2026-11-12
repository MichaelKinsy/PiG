package host

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// No Pi file corresponds to this one (see chord/internal/hostevents): it is the provider side of a link
// (remote_provider.go `queueEvent`, `sendEvents`, `eventAck`: Pi remote/provider.ts:361-390, :675-693) for a consumer
// that is not a chord link: the plugins bridge, which holds this host's objects for a Node host through references.
// A tapped reference feeds one node per event member, as it feeds an object re-exposing the member
// (host_reference_events.go `forward`): the port gets what the reference receives, value and stamp unchanged, whatever
// instance the reference follows.
//
// Async contract: none of its own. Tap, Untap and Close enter the host's loop once (a section) and return; Ack queues
// its work on the loop (`Loop.Queue`, safe from any goroutine: a section of its own, on a task the host's loop owns and
// its close joins) and returns at once. The flush calls `send` inside its section, so `send` must not block.

func init() { hostevents.Open = openEventPort }

// eventPort is a hostevents.Port: its taps by hold, this flush's admitted entries, and its messages in flight (at most
// the host's event window).
type eventPort struct {
	events   *hostEvents
	send     func([]hostevents.Entry)
	holds    map[int]*portHold
	batch    []hostevents.Entry
	inFlight int
	// encoded is the entry encoded last and its text: an entry's taps are visited together, so one entry reaching
	// several holds is encoded once.
	encoded     *eventEntry
	encodedText string
	closed      bool
}

// portHold is one tapped reference: its binding, and the node each event member feeds.
type portHold struct {
	binding *binding
	members []string
	nodes   []*eventNode
}

func referenceOf(reference any) (*Reference, error) {
	ref, ok := reference.(*Reference)
	if !ok || ref == nil {
		return nil, fmt.Errorf("hostevents: %T is not a host reference", reference)
	}
	return ref, nil
}

// portEvents are the host's events a port takes this binding's from: it fails once released (as subscribeEvents) and
// for a contract without event members.
func (b *binding) portEvents() (*hostEvents, error) {
	if b.state.kind == bindingReleased {
		return nil, b.state.ended.Error
	}
	if b.eventsOf == nil {
		return nil, fmt.Errorf("%s: the type has no event member", b.name)
	}
	return b.eventsOf.host, nil
}

func openEventPort(reference any, send func([]hostevents.Entry)) (hostevents.Port, error) {
	ref, err := referenceOf(reference)
	if err != nil {
		return nil, err
	}
	b := ref.binding
	var port *eventPort
	b.loop.Run(func() {
		var events *hostEvents
		if events, err = b.portEvents(); err == nil {
			port = &eventPort{events: events, send: send, holds: map[int]*portHold{}}
		}
	})
	if err != nil {
		return nil, err
	}
	return port, nil
}

// Tap is hostevents.Port's: each event member of the reference feeds a node the port taps from the host's counter value
// now, as a hold taps its instance when it binds (remote_hold.go `tapEvents`).
func (p *eventPort) Tap(reference any, hold int) (members []string, err error) {
	ref, err := referenceOf(reference)
	if err != nil {
		return nil, err
	}
	b := ref.binding
	b.loop.Run(func() {
		events, failure := b.portEvents()
		switch {
		case failure != nil:
			err = failure
		case events != p.events:
			err = fmt.Errorf("%s: a reference of another host than the port's", b.name)
		case p.closed:
			err = errors.New("hostevents: the port is closed")
		case p.holds[hold] != nil:
			err = fmt.Errorf("hostevents: hold %d is tapped", hold)
		}
		if err != nil {
			return
		}
		members = slices.Sorted(maps.Keys(b.events))
		held := &portHold{binding: b, members: members, nodes: make([]*eventNode, len(members))}
		boundAt := p.events.counter()
		for index, member := range members {
			node := &eventNode{}
			node.tap(&tap{boundAt: boundAt, deliver: func(entry *eventEntry, flush *eventFlush) {
				p.queue(hold, index, entry, flush)
			}})
			b.forwardEvents(member, node)
			held.nodes[index] = node
		}
		p.holds[hold] = held
	})
	return members, err
}

// queue is an event flush's entry for hold's event member member: remote_provider.go's queueEvent. While the window is
// full it is dropped as visited (counted), before anything is encoded; else it joins this flush's message.
func (p *eventPort) queue(hold, member int, entry *eventEntry, flush *eventFlush) {
	if p.inFlight >= p.events.window {
		p.events.metrics.Counter("chord.events.dropped").Add(1, telemetry.SpanAttributes{telemetry.Attr("reason", "window")})
		return
	}
	if len(p.batch) == 0 {
		flush.sending(p)
	}
	if p.encoded != entry {
		text, err := link.JsonPart(entry.value)
		if err != nil {
			panic(err)
		}
		p.encoded, p.encodedText = entry, string(text)
		p.events.metrics.Counter("chord.events.encodes").Add(1, nil)
	}
	p.batch = append(p.batch, hostevents.Entry{Hold: hold, Member: member, At: entry.at, Value: p.encodedText})
}

// sendEvents: the event flush visited every entry (eventSender): the admitted entries go as one message, taking a slot.
func (p *eventPort) sendEvents() {
	entries := p.batch
	p.batch, p.encoded, p.encodedText = nil, nil, ""
	// The port closed during the flush.
	if len(entries) == 0 {
		return
	}
	p.inFlight++
	p.events.metrics.Counter("chord.events.sent").Add(float64(len(entries)), nil)
	p.send(entries)
}

// Ack is hostevents.Port's: remote_provider.go's eventAck for this host's own tickets. The slots are freed in a turn
// of their own, as a link's ack arrives in one (the window cannot change during a flush), queued and not entered: the
// caller is the bridge's channel reader, which must not wait for a section of this host.
func (p *eventPort) Ack(messages int) {
	p.events.loop.Queue(func() {
		for range messages {
			if p.inFlight == 0 {
				break
			}
			p.inFlight--
		}
	})
}

// Untap is hostevents.Port's.
func (p *eventPort) Untap(hold int) {
	p.events.loop.Run(func() { p.untap(hold) })
}

func (p *eventPort) untap(hold int) {
	held := p.holds[hold]
	if held == nil {
		return
	}
	delete(p.holds, hold)
	for index, node := range held.nodes {
		held.binding.unforwardEvents(held.members[index], node)
		node.clear()
	}
}

// Close is hostevents.Port's: what a flush under way admitted is not sent.
func (p *eventPort) Close() {
	p.events.loop.Run(func() {
		for _, hold := range slices.Collect(maps.Keys(p.holds)) {
			p.untap(hold)
		}
		p.closed = true
		p.batch = nil
	})
}

var (
	_ hostevents.Port = (*eventPort)(nil)
	_ eventSender     = (*eventPort)(nil)
)
