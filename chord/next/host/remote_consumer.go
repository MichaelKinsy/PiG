package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/remote/consumer.ts. The consumer side of a link (§7, §22, W1): the holds this host's own
// remote references sent on it, by this side's ids, the calls through them within the peer's call window, and each
// `bound` and `state` message applied to their replicas as one batch, then acknowledged. Messages with an origin answer
// holds this host relays: they go to that origin's provider side. On a host without a compressor, the deflated parts of
// this host's own holds and calls are inflated asynchronously first, and the message is handled once they are.

// outgoingCall is a call through this side: waiting for a slot of the call window (no id yet), then in flight under its
// id until its reply, its abort, its hold's release or `gone`, or the link's close.
type outgoingCall struct {
	side *consumerSide
	hold int
	// method is the method's index in the bound variant, args its arguments' part (nil for none).
	method   int
	args     link.Part
	segments [][]byte
	result   *parkedCallResult
	// id is the call's id once it got its slot; hasID tells it has one.
	id    int
	hasID bool
	// done removes the caller's abort listener.
	done func() bool
}

func (c *outgoingCall) holdID() int { return c.hold }

// send sends a call that got its slot, with the next id.
func (c *outgoingCall) send() { c.side.send(c) }

// resolve settles the call with its value; the caller's abort listener goes.
func (c *outgoingCall) resolve(value any) {
	c.stopListening()
	c.result.settle(value, nil)
}

// reject settles the call with err; the caller's abort listener goes.
func (c *outgoingCall) reject(err error) {
	c.stopListening()
	c.result.settle(nil, err)
}

func (c *outgoingCall) stopListening() {
	if c.done != nil {
		c.done()
	}
}

// consumerSide is the consumer side of one link. Ids are its own counters, from 0; a reply naming an id this side gave up
// is ignored (Q1). When the link closes, every call rejects `unavailable` (never resent) and its path tells every holder.
type consumerSide struct {
	link     *hostLink
	entries  orderedTable[int, *remoteObject]
	calls    orderedTable[int, *outgoingCall]
	window   callWindow[*outgoingCall]
	nextHold int
	nextCall int
}

// newConsumerSide makes the consumer side of l.
func newConsumerSide(l *hostLink) *consumerSide { return &consumerSide{link: l} }

// attach holds holder on this link and returns its new id: its first hold, or a re-hold on a new link; chain is the
// activation chain it is held for, if any (Q5).
func (c *consumerSide) attach(holder *remoteObject, chain []string) int {
	id := c.nextHold
	c.nextHold++
	c.entries.set(id, holder)
	principal, incarnation := actingFor(c.link, holder.caller)
	request := holder.request
	if chain != nil {
		request.Chain = chain
	}
	c.link.send(&link.HoldMessage{
		Hold:        id,
		Type:        request.Type,
		Version:     request.Version,
		Fingerprint: request.Fingerprint,
		Params:      request.Params,
		Address:     request.Address,
		Chain:       request.Chain,
		Limit:       request.Limit,
		Principal:   principal,
		Incarnation: incarnation,
	})
	return id
}

// call calls method (an index into the bound variant) on hold with args: left out for a method without arguments, with
// byte segments when its argument schema has a `bytes()` leaf, deflated above the threshold except on the parent link.
// The result is parsed here. The caller's cancellation rejects it at once and sends `abort` when it is in flight. The
// returned result settles in a section.
func (c *consumerSide) call(hold int, method int, methodData *contracts.MethodData, args any, ctx context.Context) *parkedCallResult {
	result := newParkedCallResult()
	if err := ctx.Err(); err != nil {
		result.settle(nil, context.Cause(ctx))
		return result
	}
	var payload link.Payload
	var part link.Part
	if !contracts.TakesNoArgs(methodData) {
		var err error
		if payload, err = link.PayloadPart(args, methodData.Args.Bytes); err != nil {
			result.settle(nil, err)
			return result
		}
		// Up a parent link they travel uncompressed: the parent may have accepted this host as a child, and child links
		// refuse compressed parts (§22).
		if c.link.role == linkRoleParent {
			part = link.Text(payload.Json)
		} else {
			part = c.link.part(payload.Json, ctx)
		}
	}
	call := &outgoingCall{side: c, hold: hold, method: method, args: part, segments: payload.Segments, result: result}
	if ctx.Done() != nil {
		loop := c.link.host.loop()
		call.done = context.AfterFunc(ctx, func() {
			loop.Run(func() {
				c.cancel(call)
				result.settle(nil, context.Cause(ctx))
			})
		})
	}
	c.window.submit(call)
	return result
}

// release: hold was released: the provider is told, its calls fail with err.
func (c *consumerSide) release(hold int, err *data.ChordError) {
	c.link.send(&link.ReleaseMessage{Hold: hold})
	c.forget(hold, err)
}

// retry is `ref.retry()` on a failed hold, as retry operation: the provider is asked for an attempt now.
func (c *consumerSide) retry(hold int, operation *retryOperation) {
	message := &link.RetryMessage{Hold: hold, Operation: operation.id}
	if len(operation.through) != 0 {
		message.Through = operation.through
	}
	c.link.send(message)
}

// forget drops hold from the tables; its calls, in flight and waiting, fail with err and free their slots.
func (c *consumerSide) forget(hold int, err *data.ChordError) {
	c.entries.delete(hold)
	c.failCalls(hold, err)
}

// opened: the peer's `hello` arrived: calls go out within its window.
func (c *consumerSide) opened(calls int) { c.window.opened(calls) }

// message handles one message from the provider: this host's own, or (with an origin) one for a relayed hold. A `bound`,
// `state` or `result` with deflated parts to inflate asynchronously returns that handling and is handled after it.
func (c *consumerSide) message(message link.Message) (*link.Handling, error) {
	if origin := link.OriginOf(message); origin.Ok {
		if side, found := c.link.host.relays.get(origin.Value); found {
			side.forward(message.(link.ConsumerMessage), c.link)
		}
		return nil, nil
	}
	switch typed := message.(type) {
	case *link.BoundMessage:
		if deflated := c.deflatedBound(typed.Holds); deflated != nil {
			return c.link.inflate(deflated, func() error { _, err := c.message(message); return err }), nil
		}
		return nil, c.bound(typed.Holds)
	case *link.StateMessage:
		if deflated := c.deflatedState(typed.Holds); deflated != nil {
			return c.link.inflate(deflated, func() error { _, err := c.message(message); return err }), nil
		}
		return nil, c.state(typed.Holds)
	case *link.FailedMessage:
		if holder, found := c.entries.get(typed.Hold); found {
			return nil, holder.failed(typed)
		}
	case *link.LoadingMessage:
		c.loading(typed.Hold)
	case *link.ResultMessage:
		return c.reply(typed.Call, typed, typed.Value, typed.Segments, nil)
	case *link.ErrorMessage:
		return c.reply(typed.Call, typed, nil, nil, typed.Error)
	case *link.GoneMessage:
		return nil, c.gone(typed)
	}
	return nil, nil
}

// closed: the link closed: calls in flight and waiting reject `unavailable` (never resent, §7); the tables are dropped.
func (c *consumerSide) closed() {
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the link closed"}, nil)
	calls := append(c.calls.values(), c.window.clear()...)
	c.calls.clear()
	c.entries.clear()
	for _, call := range calls {
		call.reject(err)
	}
}

// send sends a call that got its slot, with the next id.
func (c *consumerSide) send(call *outgoingCall) {
	id := c.nextCall
	c.nextCall++
	call.id, call.hasID = id, true
	c.calls.set(id, call)
	c.link.send(&link.CallMessage{Call: id, Hold: call.hold, Method: call.method, Args: call.args, Segments: call.segments})
}

// cancel cancels call: a waiting one leaves the window and nothing is sent; one in flight sends `abort` and frees its
// slot. Settling the caller is the caller's.
func (c *consumerSide) cancel(call *outgoingCall) {
	if !call.hasID {
		c.window.remove(call)
		return
	}
	if !c.calls.delete(call.id) {
		return
	}
	c.link.send(&link.AbortMessage{Call: call.id})
	c.window.freed()
}

// deflatedBound is the deflated parts of this side's own holds in entries that wait for an asynchronous inflate, each
// with the link's decoded limit; nil when none does (nothing allocated). Entries of holds this side gave up are not
// inflated (Q1).
func (c *consumerSide) deflatedBound(entries []link.BoundEntry) []inflatePart {
	var deflated []inflatePart
	for _, entry := range entries {
		if _, held := c.entries.get(entry.Hold); !held {
			continue
		}
		for _, member := range entry.Members {
			deflated = c.inflating(deflated, member.Value)
		}
	}
	return deflated
}

// deflatedState is deflatedBound for the entries of a `state` message.
func (c *consumerSide) deflatedState(entries []link.StateEntry) []inflatePart {
	var deflated []inflatePart
	for _, entry := range entries {
		if _, held := c.entries.get(entry.Hold); !held {
			continue
		}
		for _, member := range entry.Members {
			deflated = c.inflating(deflated, member.Ops)
		}
	}
	return deflated
}

func (c *consumerSide) inflating(deflated []inflatePart, part link.Part) []inflatePart {
	if part, waits := c.link.inflates(part, c.link.limits); waits {
		return append(deflated, inflatePart{part: part, limit: c.link.limits.Decoded})
	}
	return deflated
}

// bound handles one `bound` message: every entry prepared first (one that does not fit closes the link before anything
// changed) and applied in one batch; then the references go live, and the message is acknowledged. An entry with a part
// past the link's limits fails only its hold (`too_large/part`); the others apply.
func (c *consumerSide) bound(holds []link.BoundEntry) error {
	var adoptions []func()
	var bound []*remoteObject
	var oversized []oversizedHold
	for _, entry := range holds {
		holder, found := c.entries.get(entry.Hold)
		if !found {
			continue
		}
		adopt, err := holder.prepareBound(entry, c.link)
		if err != nil {
			tooLarge, ok := data.IsChordErrorCode(err, data.CodeTooLarge)
			if !ok {
				return err
			}
			oversized = append(oversized, oversizedHold{holder, tooLarge})
			continue
		}
		adoptions = append(adoptions, adopt)
		bound = append(bound, holder)
	}
	if err := c.adopt(adoptions); err != nil {
		return err
	}
	// After the batch: the references go live with every replica of the message in place.
	for _, holder := range bound {
		holder.wentLive()
	}
	for _, each := range oversized {
		each.holder.tooLarge(each.err)
	}
	c.link.send(&link.AckMessage{})
	return nil
}

// oversizedHold is a hold whose entry had a part past the link's limits.
type oversizedHold struct {
	holder *remoteObject
	err    *data.ChordError
}

// state handles one `state` message, as bound does it.
func (c *consumerSide) state(holds []link.StateEntry) error {
	var adoptions []func()
	var oversized []oversizedHold
	for _, entry := range holds {
		holder, found := c.entries.get(entry.Hold)
		if !found {
			continue
		}
		adopt, err := holder.prepare(entry, c.link)
		if err != nil {
			tooLarge, ok := data.IsChordErrorCode(err, data.CodeTooLarge)
			if !ok {
				return err
			}
			oversized = append(oversized, oversizedHold{holder, tooLarge})
			continue
		}
		adoptions = append(adoptions, adopt)
	}
	if err := c.adopt(adoptions); err != nil {
		return err
	}
	for _, each := range oversized {
		each.holder.tooLarge(each.err)
	}
	c.link.send(&link.AckMessage{})
	return nil
}

// adopt applies prepared entries in one batch of this host, so no watch or derive sees half a provider commit (W1).
func (c *consumerSide) adopt(adoptions []func()) error {
	if len(adoptions) == 0 {
		return nil
	}
	return c.link.host.batch.Commit(func() error {
		for _, adopt := range adoptions {
			adopt()
		}
		return nil
	}, c.link.context)
}

// loading: the hold's object, or its relay's upstream, starts over; its calls still waiting for a slot reject
// `unavailable` (they may name a method of the old variant).
func (c *consumerSide) loading(hold int) {
	holder, found := c.entries.get(hold)
	if !found {
		return
	}
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: fmt.Sprintf("hold %d: rebinding", hold)}, nil)
	for _, call := range c.window.removeHold(hold) {
		call.reject(err)
	}
	holder.loading()
}

// gone: one for a hold this side gave up is ignored (Q1); the holder ends, which forgets its hold here and fails its
// calls.
func (c *consumerSide) gone(message *link.GoneMessage) error {
	holder, found := c.entries.get(message.Hold)
	if !found {
		return nil
	}
	received, err := link.ErrorFromWire(message.Error)
	if err != nil {
		return err
	}
	holder.lost(data.AsChordError(received, fmt.Sprintf("hold %d", message.Hold)))
	return nil
}

// reply handles a reply: its call settles with the parsed result or the error. A result past the link's limits rejects
// the call `too_large/part`. A malformed part rejects the call `unavailable` and returns an error (a protocol error: the
// link closes). A result to inflate asynchronously is inflated first (for a call still waiting), then handled again.
func (c *consumerSide) reply(id int, reply link.Message, value link.Part, segments [][]byte, wire link.WireError) (*link.Handling, error) {
	call, found := c.calls.get(id)
	if !found {
		return nil, nil
	}
	l := c.link
	if wire == nil {
		if deflated, waits := l.inflates(value, l.limits); waits {
			return l.inflate([]inflatePart{{part: deflated, limit: l.limits.Decoded}}, func() error {
				_, err := c.message(reply)
				return err
			}), nil
		}
	}
	c.calls.delete(id)
	c.window.freed()
	settled, err := c.parseReply(wire, value, segments)
	if err == nil {
		if wire != nil {
			call.reject(settled.(error))
		} else {
			call.resolve(settled)
		}
		return nil, nil
	}
	if _, ok := data.IsChordErrorCode(err, data.CodeTooLarge); ok {
		call.reject(err)
		return nil, nil
	}
	call.reject(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: fmt.Sprintf("call %d: a malformed reply", id)}, nil))
	return nil, err
}

// parseReply is the error a reply carries (as an error value), or its parsed value; nil for an absent one.
func (c *consumerSide) parseReply(wire link.WireError, value link.Part, segments [][]byte) (any, error) {
	if wire != nil {
		received, err := link.ErrorFromWire(wire)
		if err != nil {
			return nil, err
		}
		if received == nil {
			return nil, errors.New("reply: an error that is nothing")
		}
		return received, nil
	}
	if value == nil {
		return nil, nil
	}
	json, err := c.link.openPart(value, c.link.limits, c.link.context)
	if err != nil {
		return nil, err
	}
	return link.ParsePayload(json, segments)
}

// failCalls fails hold's calls in flight and waiting with err; each in-flight one frees its slot.
func (c *consumerSide) failCalls(hold int, err *data.ChordError) {
	// Waiting ones first: a freed slot must not send one of them.
	failed := c.window.removeHold(hold)
	for _, id := range c.calls.keys() {
		call, found := c.calls.get(id)
		if !found || call.hold != hold {
			continue
		}
		c.calls.delete(id)
		c.window.freed()
		failed = append(failed, call)
	}
	for _, call := range failed {
		call.reject(err)
	}
}
