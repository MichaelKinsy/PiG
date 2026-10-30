package host

import (
	"context"
	"fmt"
	"math"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/remote/provider.ts. The provider side of one origin on a link (§3, §22, W1; decision
// "Relay flow control per origin"): the holds the peer sent for that origin, served here or relayed, by their ids; the
// calls on them, at most this side's N at once; and the window: at most K unacknowledged `bound` and `state` messages of
// this host's own, with what waits meanwhile, and the sender of every message in flight, so each `ack` reaches the host
// that sent its message.

// providerWindow is how many of this host's own `bound` and `state` messages may be unacknowledged per origin.
const providerWindow = 2

// providerEntry is a hold of a provider side: a *hold served here or a *relayedHold. The id is the key it is held under.
type providerEntry interface {
	// caller is whom the hold acts for.
	caller() caller
	retry(message *link.RetryMessage)
	drop()
}

// runningCall is a call being handled: on a hold served here (served), with whether its result carries byte segments and
// the abort of its handler's context; or relayed (relayed), its reply forwarded from upstream.
type runningCall struct {
	hold    providerEntry
	served  *hold
	relayed *relayedHold
	bytes   bool
	abort   func(cause error)
}

// relayState is a relaying origin: its number on upstream links, and the open upstream links it relayed holds to.
type relayState struct {
	origin int
	links  orderedTable[*hostLink, struct{}]
}

// providerSide is the provider side of one origin on a link. Ids are the origin's; a message naming an id this side does
// not know is ignored (Q1), one reusing a live id is a protocol error, and so are more concurrent calls than this side
// announced. This host's own `bound` and `state` messages go through the window: locally served holds wait while it is
// full (their ops in their members' pending queues, their bindings for the next slot). A relay forwards upstream
// messages at once (each upstream host keeps its own window for the origin it sees), so a slow consumer stalls only its
// origin.
type providerSide struct {
	link *hostLink
	// origin is whose work this side serves: 0 for the peer's own, a relay's number for one of its downstream links.
	origin  int
	calls   orderedTable[int, *runningCall]
	entries orderedTable[int, providerEntry]
	limit   int
	// sent is the sender of each `bound` and `state` message in flight, in order: the consumer acknowledges in order. A
	// nil sender is this host.
	sent []*hostLink
	// inFlight is this host's own messages in flight.
	inFlight int
	// touchedHolds are the holds the current batch touched, in order; its flush sends or queues them.
	touchedHolds []*hold
	// waiting are the holds whose ops wait in their pending queues for a slot.
	waiting orderedTable[*hold, struct{}]
	// binding are the holds waiting for a slot to send their first binding.
	binding orderedTable[*hold, struct{}]
	// bindingTurn: the waiting bindings take the next slot: the last one went to waiting state while they waited.
	bindingTurn bool
	// relay is set once a hold of this origin is relayed.
	relay *relayState
}

// newProviderSide makes the provider side of origin on l that accepts limit concurrent calls.
func newProviderSide(l *hostLink, origin int, limit int) *providerSide {
	return &providerSide{link: l, origin: origin, limit: limit}
}

// entryID is the id a provider entry is held under.
func entryID(entry providerEntry) int {
	switch typed := entry.(type) {
	case *hold:
		return typed.id
	case *relayedHold:
		return typed.id
	}
	panic("providerSide: an entry that is neither served nor relayed")
}

// message handles one message of this origin; a call whose arguments wait for an asynchronous inflate returns its
// handling. An error is a protocol error.
func (p *providerSide) message(message link.ProviderMessage) (*link.Handling, error) {
	switch typed := message.(type) {
	case *link.HoldMessage:
		return nil, p.hold(typed)
	case *link.ReleaseMessage:
		p.release(typed.Hold)
	case *link.CallMessage:
		return p.call(typed)
	case *link.AbortMessage:
		p.abort(typed.Call)
	case *link.RetryMessage:
		if entry, found := p.entries.get(typed.Hold); found {
			entry.retry(typed)
		}
	case *link.AckMessage:
		p.ack()
	}
	return nil, nil
}

// closed: the link closed, or the origin did (`closed`): every hold is released (relayed ones by one `closed` to each
// upstream link) and every call aborted.
func (p *providerSide) closed() {
	closing := p.link.host.closing()
	entries := p.entries.values()
	p.entries.clear()
	closed := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the link closed"}, nil)
	for _, call := range p.calls.values() {
		if call.served != nil {
			call.abort(closed)
		}
	}
	p.calls.clear()
	// While the host closes, it ends its objects itself.
	for _, entry := range entries {
		if served, isServed := entry.(*hold); isServed && !closing {
			served.release()
		} else {
			entry.drop()
		}
	}
	// A batch flushing later finds nothing of this side.
	p.touchedHolds = nil
	relay := p.relay
	if relay == nil {
		return
	}
	p.link.host.relays.delete(relay.origin)
	for _, upstream := range relay.links.keys() {
		upstream.send(&link.ClosedMessage{Origin: relay.origin})
	}
}

// send sends message on the link with this origin.
func (p *providerSide) send(message link.OriginMessage) {
	if p.origin == 0 {
		p.link.send(message)
		return
	}
	p.link.send(link.WithOrigin(message, link.Some(p.origin)))
}

// revokeUnbound ends with err every hold of this origin that acts for a principal revoked names and holds nothing:
// served here but unbound (before the publication, refused), or relayed and waiting for its route's link.
func (p *providerSide) revokeUnbound(revoked func(principal data.Principal) bool, err *data.ChordError) {
	for _, entry := range p.entries.values() {
		// A hold arriving on a link acts for a principal: the link's, or one its parent named.
		if !revoked(entry.caller().principal) {
			continue
		}
		switch typed := entry.(type) {
		case *hold:
			if typed.unbound() {
				typed.end(err)
			}
		case *relayedHold:
			if typed.waiting() {
				typed.revoked(err)
			}
		}
	}
}

// relayed: a pending hold turned out to be relayed: it takes the hold's place in the table.
func (p *providerSide) relayed(entry *relayedHold) { p.entries.set(entry.id, entry) }

// relayTo is this origin's number on upstream links (one per host, never reused), with l as one it relays to.
func (p *providerSide) relayTo(l *hostLink) int {
	if p.relay == nil {
		p.relay = &relayState{origin: p.link.host.relayOrigin(p)}
	}
	p.relay.links.set(l, struct{}{})
	return p.relay.origin
}

// forward forwards a message from upstream link from for this origin's relayed holds, with this origin: `bound` and
// `state` join the window's senders (their `ack` goes back to from), a reply ends its call, `gone` forgets its hold. A
// link answers only for what was relayed over it: an entry, a notice or a reply naming a hold (or a call) this origin
// did not relay over from is dropped (Q1); a `bound` or `state` left without entries is acknowledged to from at once.
// Parts travel unchecked and unparsed: the end that parses a part checks it against its own link's limits.
func (p *providerSide) forward(message link.ConsumerMessage, from *hostLink) {
	relay := p.relay
	if relay == nil {
		return
	}
	if _, linked := relay.links.get(from); !linked {
		return
	}
	switch forwarded := link.WithOrigin(message, link.Optional[int]{}).(type) {
	case *link.BoundMessage:
		kept, all := relayedOver(p, forwarded.Holds, func(entry link.BoundEntry) int { return entry.Hold }, from)
		if len(kept) == 0 {
			from.send(&link.AckMessage{Origin: link.Some(relay.origin)})
			return
		}
		p.sent = append(p.sent, from)
		if !all {
			narrowed := *forwarded
			narrowed.Holds = kept
			forwarded = &narrowed
		}
		p.send(forwarded)
	case *link.StateMessage:
		kept, all := relayedOver(p, forwarded.Holds, func(entry link.StateEntry) int { return entry.Hold }, from)
		if len(kept) == 0 {
			from.send(&link.AckMessage{Origin: link.Some(relay.origin)})
			return
		}
		p.sent = append(p.sent, from)
		if !all {
			narrowed := *forwarded
			narrowed.Holds = kept
			forwarded = &narrowed
		}
		p.send(forwarded)
	case *link.ResultMessage:
		p.forwardReply(forwarded.Call, forwarded, from)
	case *link.ErrorMessage:
		p.forwardReply(forwarded.Call, forwarded, from)
	case *link.GoneMessage:
		entry := p.relayedHold(forwarded.Hold, from)
		if entry == nil {
			return
		}
		entry.lost()
		p.send(forwarded)
	case *link.FailedMessage:
		if p.relayedHold(forwarded.Hold, from) != nil {
			p.send(forwarded)
		}
	case *link.LoadingMessage:
		if p.relayedHold(forwarded.Hold, from) != nil {
			p.send(forwarded)
		}
	}
}

// forwardReply forwards a reply of a call this origin relayed over from.
func (p *providerSide) forwardReply(id int, reply link.OriginMessage, from *hostLink) {
	call, found := p.calls.get(id)
	if found && call.relayed != nil && call.relayed.heldOn(from) {
		p.reply(id, reply)
	}
}

// unlinked: an upstream link closed: nothing more is relayed to it, nor accepted from it.
func (p *providerSide) unlinked(l *hostLink) {
	if p.relay != nil {
		p.relay.links.delete(l)
	}
}

// upstreamDropped: the upstream link of relayed hold entry dropped with err: its calls in flight fail, the hold goes
// loading downstream, and its path's next link holds it again.
func (p *providerSide) upstreamDropped(entry *relayedHold, err *data.ChordError) {
	for _, id := range p.calls.keys() {
		call, found := p.calls.get(id)
		if found && call.hold == providerEntry(entry) {
			p.reply(id, &link.ErrorMessage{Call: id, Error: link.WireErrorOf(err)})
		}
	}
	p.send(&link.LoadingMessage{Hold: entry.id})
}

// forget forgets a hold (released, refused or gone) and every call on it; local calls are aborted with err (its object's
// end for a gone hold, nil for `released`).
func (p *providerSide) forget(entry providerEntry, err *data.ChordError) {
	id := entryID(entry)
	if current, found := p.entries.get(id); found && current == entry {
		p.entries.delete(id)
	}
	var reason error = err
	if err == nil {
		reason = data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: fmt.Sprintf("hold %d: released", id)}, nil)
	}
	for _, callID := range p.calls.keys() {
		call, found := p.calls.get(callID)
		if !found || call.hold != entry {
			continue
		}
		p.calls.delete(callID)
		if call.served != nil {
			call.abort(reason)
		}
	}
	if served, isServed := entry.(*hold); isServed {
		p.unbind(served)
	}
}

// reply: a call ended with reply (a result or an error): its slot is free; nothing is sent for one already gone.
func (p *providerSide) reply(id int, reply link.OriginMessage) {
	if !p.calls.delete(id) {
		return
	}
	p.send(reply)
}

// bind: a hold's object is live: its `bound` goes now when a slot is free, else with the next free slot's bindings.
func (p *providerSide) bind(h *hold, ctx context.Context) {
	if p.free() {
		p.sendOwn(&link.BoundMessage{Holds: []link.BoundEntry{h.boundEntry(ctx)}})
		return
	}
	p.binding.set(h, struct{}{})
}

// unbind: a hold stopped waiting for a slot: it was forgotten.
func (p *providerSide) unbind(h *hold) {
	p.waiting.delete(h)
	p.binding.delete(h)
}

// touched: a hold's members changed in the current batch; its flush sends or queues it.
func (p *providerSide) touched(h *hold) {
	if len(p.touchedHolds) == 0 {
		p.link.host.batch.Flushing(func(ctx context.Context) error {
			p.flush(ctx)
			return nil
		})
	}
	p.touchedHolds = append(p.touchedHolds, h)
}

// relayedOver is the entries whose holds this origin relayed over from, in one pass: the same slice (all true) when all
// are, else a copy made at the first one that is not.
func relayedOver[E any](p *providerSide, entries []E, holdOf func(entry E) int, from *hostLink) (kept []E, all bool) {
	var narrowed []E
	copied := false
	for index, entry := range entries {
		relayed := p.relayedHold(holdOf(entry), from) != nil
		switch {
		case copied:
			if relayed {
				narrowed = append(narrowed, entry)
			}
		case !relayed:
			narrowed = append(make([]E, 0, len(entries)), entries[:index]...)
			copied = true
		}
	}
	if !copied {
		return entries, true
	}
	return narrowed, false
}

// relayedHold is the hold id of this origin when it is relayed over from; nil otherwise.
func (p *providerSide) relayedHold(id int, from *hostLink) *relayedHold {
	entry, found := p.entries.get(id)
	if !found {
		return nil
	}
	relayed, isRelayed := entry.(*relayedHold)
	if !isRelayed || !relayed.heldOn(from) {
		return nil
	}
	return relayed
}

// hold handles a hold message: a protocol error for a live id or compressed params (params are never compressed),
// otherwise admitted (hold.start, placed once the host published). Its activation chain is kept only from a trusted link
// (Q5); its decoded limit is the link's, or the smaller one a parent stamped (Q2), and travels with it when it is
// relayed.
func (p *providerSide) hold(message *link.HoldMessage) error {
	if _, live := p.entries.get(message.Hold); live {
		return fmt.Errorf("hold %d: the id is in use", message.Hold)
	}
	if _, deflated := message.Params.(*link.DeflatedPart); deflated {
		return fmt.Errorf("hold %d: compressed params", message.Hold)
	}
	l := p.link
	// Whom it acts for is decided here once (callerOf), never passed on: a relay names it only on a route link.
	request := holdRequest{
		Type: message.Type, Version: message.Version, Fingerprint: message.Fingerprint, Params: message.Params,
		Address: message.Address,
	}
	if message.Chain != nil && l.trusted {
		request.Chain = message.Chain
	}
	limit := l.limits.Decoded
	if l.role == linkRoleParent && message.Limit.Ok {
		limit = math.Min(limit, float64(message.Limit.Value))
	}
	if !math.IsInf(limit, 1) {
		request.Limit = link.Some(int(limit))
	}
	limits := l.limits
	if limit != l.limits.Decoded {
		limits = link.Limits{Part: l.limits.Part, Decoded: limit}
	}
	h := newHold(message.Hold, p, p.callerOf(message), limits)
	p.entries.set(message.Hold, h)
	h.start(request)
	return nil
}

// callerOf is whom a hold acts for: on a parent link the hold's own principal and incarnation when it names them,
// everywhere else the link's principal and the peer's incarnation (a child cannot claim to be someone else).
func (p *providerSide) callerOf(message *link.HoldMessage) caller {
	l := p.link
	if l.role != linkRoleParent {
		return caller{principal: l.principal, incarnation: l.peer()}
	}
	// The message schema checked a principal's shape on an untrusted link; a trusted parent is believed.
	from := caller{principal: message.Principal, incarnation: message.Incarnation}
	if from.principal == nil {
		from.principal = l.principal
	}
	if from.incarnation == "" {
		from.incarnation = l.peer()
	}
	return from
}

func (p *providerSide) release(id int) {
	entry, found := p.entries.get(id)
	if !found {
		return
	}
	p.forget(entry, nil)
	switch typed := entry.(type) {
	case *hold:
		typed.release()
	case *relayedHold:
		typed.release()
	}
}

// call handles a call: one on a hold this side does not know is ignored (Q1), more than this side's N at once and
// compressed arguments from a child (clients never compress) are protocol errors. On a relayed hold it is forwarded, its
// arguments checked against this link's part limit first (the bytes are here). On a served hold that is not bound yet
// it is refused `not_ready`; its method is an index into the bound variant (one outside it is a protocol error) and its
// arguments are opened and parsed here within the hold's limits (past one the call fails `too_large/part`; a malformed
// part is a protocol error), absent ones `null`. Arguments this host must inflate asynchronously are inflated first,
// then the call is handled again.
func (p *providerSide) call(message *link.CallMessage) (*link.Handling, error) {
	if _, used := p.calls.get(message.Call); used {
		return nil, fmt.Errorf("call %d: the id is in use", message.Call)
	}
	entry, found := p.entries.get(message.Hold)
	if !found {
		return nil, nil
	}
	if p.calls.len() >= p.limit {
		return nil, fmt.Errorf("call %d: more than %d calls", message.Call, p.limit)
	}
	part := message.Args
	if _, deflated := part.(*link.DeflatedPart); deflated && p.link.role == linkRoleChild {
		return nil, fmt.Errorf("call %d: compressed arguments from a child", message.Call)
	}
	if relayed, isRelayed := entry.(*relayedHold); isRelayed {
		if part != nil {
			if err := link.Oversized(part, p.link.limits.Part); err != nil {
				p.refuse(message.Call, err)
				return nil, nil
			}
		}
		p.calls.set(message.Call, &runningCall{hold: relayed, relayed: relayed})
		relayed.call(message)
		return nil, nil
	}
	served := entry.(*hold)
	if !served.bound() {
		p.refuse(message.Call, data.NewChordError(data.ChordErrorInit{
			Code: data.CodeNotReady, Message: fmt.Sprintf("hold %d: not bound yet", served.id),
		}, nil))
		return nil, nil
	}
	name, resultBytes, err := served.method(message.Method)
	if err != nil {
		return nil, err
	}
	if deflated, inflates := p.link.inflates(part, served.limits); inflates {
		handling := p.link.inflate([]inflatePart{{part: deflated, limit: served.limits.Decoded}}, func() error {
			_, err := p.call(message)
			return err
		})
		return handling, nil
	}
	var args any
	if part != nil {
		json, err := p.link.openPart(part, served.limits, p.link.context)
		if err != nil {
			if tooLarge, ok := data.IsChordErrorCode(err, data.CodeTooLarge); ok {
				p.refuse(message.Call, tooLarge)
				return nil, nil
			}
			return nil, err
		}
		if args, err = link.ParsePayload(json, message.Segments); err != nil {
			return nil, err
		}
	}
	signal, abort := chordctx.WithCancel(chordctx.Background())
	call := &runningCall{hold: served, served: served, bytes: resultBytes, abort: abort}
	p.calls.set(message.Call, call)
	// A link's context has no abort signal, so this is the call's own signal: no combined signal per call.
	p.run(message.Call, call, name, args, chordctx.WithAbortSignal(p.link.context, signal))
	return nil, nil
}

// run starts a call's handler, in this section and so in message order (Pi's `#run` starts it synchronously), and sends its
// result (absent when nil) or error when it settled, unless it was aborted, released or gone meanwhile. A result that
// cannot be encoded is sent as its error. Nobody waits for it: the reaction is a microtask of the host's loop.
func (p *providerSide) run(id int, call *runningCall, method string, args any, ctx context.Context) {
	result := call.served.call(method, args, ctx)
	result.then(p.link.host.loop(), func(value any, err error) {
		var reply link.OriginMessage
		if err != nil {
			reply = &link.ErrorMessage{Call: id, Error: link.WireErrorOf(err)}
		} else if value == nil {
			reply = &link.ResultMessage{Call: id}
		} else if encoded, encodeErr := p.result(id, value, call.bytes); encodeErr != nil {
			reply = &link.ErrorMessage{Call: id, Error: link.WireErrorOf(encodeErr)}
		} else {
			reply = encoded
		}
		p.reply(id, reply)
	})
}

// refuse answers call id with err before it was admitted: it took no slot.
func (p *providerSide) refuse(id int, err error) {
	p.send(&link.ErrorMessage{Call: id, Error: link.WireErrorOf(err)})
}

func (p *providerSide) abort(id int) {
	call, found := p.calls.get(id)
	if !found {
		return
	}
	p.calls.delete(id)
	if call.relayed != nil {
		call.relayed.abort(id)
		return
	}
	call.abort(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: fmt.Sprintf("call %d: aborted by its caller", id)}, nil))
}

// ack answers the oldest message in flight: this host's own frees a slot, a relayed one is acknowledged to its upstream
// link (closed meanwhile: nothing to do). An ack crossing nothing in flight is ignored.
func (p *providerSide) ack() {
	if len(p.sent) == 0 {
		return
	}
	sender := p.sent[0]
	p.sent[0] = nil
	p.sent = p.sent[1:]
	if sender != nil {
		// A relayed message implies the relay's number.
		sender.send(&link.AckMessage{Origin: link.Some(p.relay.origin)})
		return
	}
	p.inFlight--
	p.slotFreed()
}

// flush is the end of a batch: while a slot is free and nothing waits, one `state` message with an entry per touched
// hold; otherwise each touched hold's ops join its pending queues until a slot frees.
func (p *providerSide) flush(ctx context.Context) {
	holds := p.touchedHolds
	// The side closed during the batch: nothing is sent.
	if len(holds) == 0 {
		return
	}
	p.touchedHolds = nil
	if p.free() {
		entries := make([]link.StateEntry, len(holds))
		for index, h := range holds {
			entries[index] = h.entry(ctx)
		}
		p.sendOwn(&link.StateMessage{Holds: entries})
		return
	}
	// Only a bound hold is touched, and a bound hold never waits for its binding.
	for _, h := range holds {
		h.queue()
		p.waiting.set(h, struct{}{})
	}
}

// free reports whether this host's next message goes out now: a slot is free and nothing waits for one.
func (p *providerSide) free() bool {
	return p.binding.len() == 0 && p.waiting.len() == 0 && p.inFlight < providerWindow
}

// slotFreed: a slot may be free: while there is room, the waiting state goes out as one message and the waiting bindings
// as one `bound`, taking turns when both wait (state first), so neither a hold that keeps changing nor a stream of new
// holds starves the other.
func (p *providerSide) slotFreed() {
	ctx := p.link.context
	for p.inFlight < providerWindow {
		switch {
		case p.binding.len() > 0 && (p.waiting.len() == 0 || p.bindingTurn):
			holds := p.binding.keys()
			p.binding.clear()
			p.bindingTurn = false
			// Every snapshot in one synchronous step: the binding shows all of them at one moment.
			entries := make([]link.BoundEntry, len(holds))
			for index, h := range holds {
				entries[index] = h.boundEntry(ctx)
			}
			p.sendOwn(&link.BoundMessage{Holds: entries})
		case p.waiting.len() > 0:
			holds := p.waiting.keys()
			p.waiting.clear()
			p.bindingTurn = p.binding.len() > 0
			entries := make([]link.StateEntry, len(holds))
			for index, h := range holds {
				entries[index] = h.pendingEntry(ctx)
			}
			p.sendOwn(&link.StateMessage{Holds: entries})
		default:
			return
		}
	}
}

// result is a call's result message, its part deflated above the threshold when this host compresses; with byte segments
// when its method's result schema has a `bytes()` leaf. Results are not validated: they are the provider's.
func (p *providerSide) result(id int, result any, bytes bool) (*link.ResultMessage, error) {
	payload, err := link.PayloadPart(result, bytes)
	if err != nil {
		return nil, err
	}
	value := p.link.part(payload.Json, p.link.context)
	return &link.ResultMessage{Call: id, Value: value, Segments: payload.Segments}, nil
}

// sendOwn sends one of this host's own `bound` or `state` messages, taking a slot.
func (p *providerSide) sendOwn(message link.OriginMessage) {
	p.inFlight++
	p.sent = append(p.sent, nil)
	p.send(message)
}

// orderedTableNode is one entry of an orderedTable; removed when deleted.
type orderedTableNode[K comparable, V any] struct {
	key     K
	value   V
	removed bool
}

// orderedTable is the Go form of a JavaScript Map (or Set, with an empty value) whose entries keep their insertion order;
// a re-set keeps the place. Pi's iterations over it either take a snapshot or only delete what they visit, so only
// snapshots (keys, values) are offered. The zero value is empty.
type orderedTable[K comparable, V any] struct {
	nodes []*orderedTableNode[K, V]
	index map[K]*orderedTableNode[K, V]
}

func (t *orderedTable[K, V]) len() int { return len(t.index) }

func (t *orderedTable[K, V]) get(key K) (V, bool) {
	node, found := t.index[key]
	if !found {
		var zero V
		return zero, false
	}
	return node.value, true
}

func (t *orderedTable[K, V]) set(key K, value V) {
	if node, found := t.index[key]; found {
		node.value = value
		return
	}
	if t.index == nil {
		t.index = map[K]*orderedTableNode[K, V]{}
	}
	node := &orderedTableNode[K, V]{key: key, value: value}
	t.nodes = append(t.nodes, node)
	t.index[key] = node
}

// delete removes key and reports whether it was there.
func (t *orderedTable[K, V]) delete(key K) bool {
	node, found := t.index[key]
	if !found {
		return false
	}
	node.removed = true
	delete(t.index, key)
	// A removed node stays in the list until the next compaction: it must keep neither the key nor the value alive.
	var zeroKey K
	var zeroValue V
	node.key, node.value = zeroKey, zeroValue
	if len(t.nodes) >= 2*len(t.index)+16 {
		live := make([]*orderedTableNode[K, V], 0, len(t.index))
		for _, each := range t.nodes {
			if !each.removed {
				live = append(live, each)
			}
		}
		t.nodes = live
	}
	return true
}

func (t *orderedTable[K, V]) clear() {
	t.nodes = nil
	clear(t.index)
}

// keys is the keys as they are now, in order.
func (t *orderedTable[K, V]) keys() []K {
	keys := make([]K, 0, len(t.index))
	for _, node := range t.nodes {
		if !node.removed {
			keys = append(keys, node.key)
		}
	}
	return keys
}

// values is the values as they are now, in order.
func (t *orderedTable[K, V]) values() []V {
	values := make([]V, 0, len(t.index))
	for _, node := range t.nodes {
		if !node.removed {
			values = append(values, node.value)
		}
	}
	return values
}
