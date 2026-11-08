package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/remote/hold.ts. A hold arriving on a link and served here (§7, §22, W1): where it goes
// once the host published (waiting in the unbound index until then), why it is unbound (refused: kept in the index while
// it waits for a name, dependency model §1), the object it keeps, the notices of its object's failures and new attempts,
// the snapshot and subscription taken in one synchronous step when a slot of the window is free, and the ops of its
// members per batch (sent at once, or pending while the window is full), each member with its own Delta encoder.
//
// Every method runs inside a section of the host's loop, as Pi's methods run in the event loop; `call` is the one
// blocking method (Pi's promise) and is called off the loop.

// holdRequest is what a `hold` message asks for. Whom it acts for is not part of it: a host takes that from the message
// once, as its link allows (providerSide), and names it again only on a route link (actingFor). Its Chain and Limit are the
// ones this host honours, sent on as they are when it relays the hold.
type holdRequest struct {
	Type        string
	Version     int
	Fingerprint string
	Params      link.Part
	Address     link.WireAddress
	Chain       []string
	Limit       link.Optional[int]
}

// holdFollowed is a state member a bound hold follows: its index in the bound version, its source and subscription (which
// feeds this record), its own encoder (R1, path interning) and the ops waiting for a slot of the window.
type holdFollowed struct {
	index      int
	source     state.SourceHandle
	subscriber *followedSubscriber
	encoder    *delta.Encoder
	pending    delta.PendingOps
}

// followedSubscriber feeds one followed member's calls into its hold's batch.
type followedSubscriber struct {
	hold     *hold
	followed *holdFollowed
}

type holdStateKind int

const (
	// holdUnplaced: in the unbound index until the host's publication places it.
	holdUnplaced holdStateKind = iota
	// holdPending: until its object is live (the activation, parking and retries; told: the failure its consumer was told, so
	// the next attempt is announced with `loading` and a call that crossed the failure on the wire gets it).
	holdPending
	// holdBinding: its object is live and its `bound` waits for a slot.
	holdBinding
	// holdBound: with the object's variant and the members it follows.
	holdBound
	// holdRefused: refused where it arrived, with cause: `waiting` (not served here, another version, placed elsewhere by
	// the provider's code) in the unbound index until a publication wakes it; `broken` (invalid params, a cycle, a route
	// that refused, a part past the limits) until a `retry` restarts it.
	holdRefused
	holdReleased
)

// holdState is where a hold stands; only the fields of its kind are set (Pi's discriminated union).
type holdState struct {
	kind    holdStateKind
	request holdRequest
	cause   data.FailureCause
	// told is the failure a pending hold's consumer was told.
	told    *data.ChordError
	variant *contracts.VariantData
	// names are the bound version's names of the variant, which the wire's indexes point into.
	names contracts.VariantNames
	// instance is what a bound hold was bound to: a swap brings another, which it binds to afresh.
	instance any
	// previous (a pending hold unbound with `loading`, and the binding after it, or a binding afresh after a swap in place):
	// the names the consumer still holds it bound with, which its calls sent before it learnt of the `loading` are decoded by.
	previous *boundNames
	// rebinding (a bound hold): a rebinding's names before it; until is its `bound`, this host's own message number. Calls its
	// consumer sent before it applied that `bound` carry their indexes, so they are decoded by them until its `ack`.
	rebinding *boundNames
	until     int
	// followed are the members it follows, in the order they were subscribed.
	followed []*holdFollowed
}

// boundNames is what calls need of a binding: its variant's data at the hold's version and the names the wire's indexes
// point into.
type boundNames struct {
	variant *contracts.VariantData
	names   contracts.VariantNames
}

// hold is one hold served here: a reference to its object acting as its caller (the hold's principal). While pending, each
// failure of its object is sent as `failed` (with the rest of its backoff window) and the next attempt as `loading`; a
// hold that cannot be taken holds nothing: `failed { waiting }` (not served, another version) until a publication re-runs
// its start, `failed { broken }` (invalid params, a cycle) until a `retry` does. Once live it binds when its link's window
// has a slot, then sends a part per batch that changed its members (or keeps their ops pending while the window is full),
// and `gone` when its object ends. It counts as one of the host's references from its arrival to its end, bound or not.
type hold struct {
	id int
	// from is whom the hold acts for: its calls run as it, with its origin's incarnation.
	from caller
	// limits are the limits per part of its params and its calls' arguments: its link's, the decoded one smaller when
	// stamped (Q2).
	limits   link.Limits
	provider *providerSide
	state    holdState
	// request is what it asks for (its params owned), once it started: it is placed again after a reload left it unbound.
	request *holdRequest
	// object is the held object, from its binding until the hold is released.
	object *chordObject
	// changes are the members the current batch changed, with their ops, in order.
	changes []heldChange
	// failures are the members that failed since the last message that carried this hold.
	failures []link.FailedMember
	// retired are the members whose document was retired since the last message that carried this hold (ORed while it
	// waits).
	retired     []int
	touchedFlag bool
	// counted is whether the hold counts as one of the host's references (`host.activity`): from its arrival until it ends.
	counted bool
}

type heldChange struct {
	followed *holdFollowed
	ops      []delta.Op
}

var (
	_ unboundRequest = (*hold)(nil)
	_ holder         = (*hold)(nil)
)

func newHold(id int, provider *providerSide, from caller, limits link.Limits) *hold {
	h := &hold{id: id, provider: provider, from: from, limits: limits, state: holdState{kind: holdPending}, counted: true}
	provider.link.host.count("references", 1)
	return h
}

// dependent: a hold is declared by no object on this host.
func (*hold) dependent() *chordObject { return nil }

// caller is whom the hold acts for.
func (h *hold) caller() caller { return h.from }

// bound is whether the hold is bound: its members' changes are sent.
func (h *hold) bound() bool { return h.state.kind == holdBound }

// callable is whether calls on it reach its object: bound, or by the names it was bound with while it rebinds (a swap in
// place, a `loading` unbind: its fresh `bound` not sent yet).
func (h *hold) callable() bool { return h.names() != nil }

// refusal is what a call it does not take is refused with: the failure its consumer was told (refused, or failed while
// pending: the call crossed that `failed` on the wire, and its consumer shows that cause), else `not_ready` (not bound yet).
func (h *hold) refusal() *data.ChordError {
	switch {
	case h.state.kind == holdRefused:
		return h.state.cause.Error
	case h.state.kind == holdPending && h.state.told != nil:
		return h.state.told
	}
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: fmt.Sprintf("hold %d: not bound yet", h.id)}, nil)
}

// names are the names its calls are decoded by: its binding's (its previous ones until its consumer acknowledged the `bound`
// that brought them: another variant's indexes name other methods), or the ones it was bound with while it rebinds.
func (h *hold) names() *boundNames {
	switch h.state.kind {
	case holdBound:
		if h.state.rebinding != nil && !h.provider.acknowledged(h.state.until) {
			return h.state.rebinding
		}
		return &boundNames{variant: h.state.variant, names: h.state.names}
	case holdPending, holdBinding:
		return h.state.previous
	}
	return nil
}

// contract is its consumer's contract version and fingerprint (its request's): the provider binds them or it waits.
func (h *hold) contract() contractRef {
	return contractRef{version: h.request.Version, fingerprint: h.request.Fingerprint}
}

// principal is whom it acts for (the unbound index's revocation).
func (h *hold) principal() data.Principal { return h.from.principal }

// unbound is whether the hold holds no object: before the publication, or refused (revocation ends it, §4).
func (h *hold) unbound() bool { return h.object == nil && h.state.kind != holdReleased }

// start admits the hold: before the host's publication it waits in the unbound index, placed by the publication. Then its
// params within the link's part limit (`too_large/part`), then relayed (it is replaced by a relayed hold), or served here,
// its params opened and parsed (a malformed part is a protocol error), its object taken and followed (`moved`) in the turn
// it arrived. A hold that cannot be served here waits refused (refuse).
func (h *hold) start(request holdRequest) {
	hl := h.provider.link
	host := hl.host
	if !host.published() {
		h.state = holdState{kind: holdUnplaced, request: owned(request)}
		host.index(request.Type, h)
		return
	}
	var served Serving
	var failure error
	handled := false
	func() {
		if oversize := link.Oversized(request.Params, hl.limits.Part); oversize != nil {
			failure = oversize
			return
		}
		placed, err := host.place(request, hl, h.from, hl.context)
		if err != nil {
			failure = err
			return
		}
		if placed.kind == placedPath {
			// Counted where it is served: a relayed hold is not this host's reference.
			h.drop()
			newRelayedHold(h.id, h.provider, request, h.from, placed.path).start()
			handled = true
			return
		}
		var params any
		opened, err := hl.openPart(request.Params, h.limits, hl.context)
		if err == nil {
			params, err = link.ParsePart(opened)
		}
		if err != nil {
			// Past its limits: the hold's `too_large/part`. No JSON: a protocol error, reported, and the link closes.
			if _, tooLarge := data.IsChordErrorCode(err, data.CodeTooLarge); tooLarge {
				failure = err
				return
			}
			host.report(HostError{Kind: ErrProtocolError, Error: err})
			hl.close()
			handled = true
			return
		}
		served, err = host.serving(request, params, h.from.principal, hl.context)
		if err != nil {
			failure = err
		}
	}()
	if handled {
		return
	}
	if failure != nil {
		h.refuse(request, data.FailureCause{Kind: data.FailureBroken, Error: data.AsChordError(failure, request.Type)})
		return
	}
	if served.Kind == ServingWaiting {
		h.refuse(request, served.Cause)
		return
	}
	own := owned(request)
	h.request = &own
	h.object = served.Object
	h.object.hold(h)
	h.moved()
}

// wake: woken by the unbound index (out of it already): before the publication, placed now; refused `waiting`, its start
// runs again, announced with `loading` (then `bound` or `failed`).
func (h *hold) wake() {
	switch h.state.kind {
	case holdUnplaced:
		request := h.state.request
		h.state = holdState{kind: holdPending}
		h.start(request)
	case holdRefused:
		h.restart(h.state.request)
	}
}

// moved: the object's state changed, or the hold took it: a pending hold binds once it is live (when a slot is free), sends
// each failure, and announces the attempt after a failure with `loading`. A bound hold (or one whose `bound` waits for a
// slot) whose object leaves `live` goes back to pending (slice 7b §6), except while a reload's swap runs in place; a
// bound one whose object went live with another attempt (a swap) gets a fresh `bound` (audit A9).
func (h *hold) moved() {
	current := h.state
	object := h.object
	if object == nil {
		return
	}
	if current.kind == holdBound || current.kind == holdBinding {
		shown := object.state()
		if shown.Kind == RefStateLive {
			if current.kind == holdBound && object.instance() != current.instance {
				h.rebind(current)
			}
			return
		}
		// Its instance still changes (a swap's old one, a restart's drain, a live failure's admitted commits): it follows
		// it on until it stopped (slice 7b, decision "References are the only indirection").
		if current.kind == holdBound && object.attachable() == current.instance {
			return
		}
		if current.kind == holdBinding && shown.Kind == RefStateLoading && object.swapsInPlace() {
			return
		}
		var cause *data.ChordError
		if shown.Kind == RefStateFailed {
			cause = shown.Cause.Error
		}
		h.unbind(cause)
	}
	h.pending(object)
}

// memberFailed: member followed failed with err: its failure waits to be sent, replacing one still waiting.
func (h *hold) memberFailed(member *holdFollowed, err error) {
	member.pending.Take()
	failure := link.FailedMember{Member: member.index, Error: wireError(err)}
	at := slices.IndexFunc(h.failures, func(f link.FailedMember) bool { return f.Member == member.index })
	// A new list when one is replaced (rare): a list handed to an entry is never written to.
	if at == -1 {
		h.failures = append(h.failures, failure)
	} else {
		replaced := slices.Clone(h.failures)
		replaced[at] = failure
		h.failures = replaced
	}
}

// memberHealed: member index has a value again: a failure of it still waiting to be sent (rare) is not sent.
func (h *hold) memberHealed(index int) {
	if len(h.failures) > 0 {
		h.failures = slices.DeleteFunc(slices.Clone(h.failures), func(f link.FailedMember) bool { return f.Member == index })
	}
}

// unbind: its object left `live` (a restart, a parked swap, a live failure): no longer subscribed or waiting for a slot,
// pending again. With err (a failure, sent by `pending`, which a consumer applies to a bound hold as a rebinding too, model
// §5) its calls in flight are rejected. Without (`loading`, which tells the consumer it rebinds) they stay, and calls by the
// names it was bound with keep arriving (sent before its consumer learnt of the `loading`): each gets exactly one answer from
// what it waits on, the instance it already runs on (its result, or `released` at the seal) or its object's gate (the new
// instance's result, its failure, `park_timeout`, its end), design §22.
func (h *hold) unbind(err *data.ChordError) {
	previous := h.names()
	if h.state.kind == holdBound {
		unfollow(&h.state)
	}
	h.changes = nil
	h.failures = nil
	h.retired = link.NoRetired
	h.touchedFlag = false
	if err != nil {
		h.provider.unbound(h, err)
		h.state = holdState{kind: holdPending}
		return
	}
	h.provider.unbind(h)
	h.state = holdState{kind: holdPending, previous: previous}
	h.provider.send(&link.LoadingMessage{Hold: h.id})
}

// rebind: a swap of its object went live in place (slice 7b §5, audit A9): no longer subscribed to the old instance, it
// gets a fresh `bound` of the new one's members (as a restart, a variant or a version change does), answering calls by
// the names its consumer has until it applied that.
func (h *hold) rebind(state holdState) {
	// The names its consumer has: the old binding's, or older ones while it has not applied that `bound` yet.
	previous := h.names()
	unfollow(&state)
	// Its ops still waiting for a slot are the old instance's: the `bound` replaces them.
	h.provider.unbind(h)
	h.state = holdState{kind: holdBinding, previous: previous}
	h.provider.bind(h, h.provider.link.context)
}

// retry handles a `retry` message with Hops left of its budget: the object is asked for an attempt now (it ignores this
// unless failed; parked, it passes it on, to other hosts with one hop less); a refused hold is started again (a route's
// `authorize` may admit it now).
func (h *hold) retry(message *link.RetryMessage) {
	if h.state.kind != holdRefused {
		if h.object != nil {
			h.object.retry(newRetryWalk(message.Hops - 1))
		}
		return
	}
	if h.state.cause.Kind == data.FailureWaiting {
		h.provider.link.host.unindex(h.state.request.Type, h)
	}
	h.restart(h.state.request)
}

// pending is the pending hold's part of `moved`: binds once live, sends each failure, announces the next attempt.
func (h *hold) pending(object *chordObject) {
	current := h.state
	if current.kind != holdPending {
		return
	}
	shown := object.state()
	switch shown.Kind {
	case RefStateLive:
		// Calls by the names it was bound with are still answered until the fresh `bound` is sent.
		h.state = holdState{kind: holdBinding, previous: current.previous}
		h.provider.bind(h, h.provider.link.context)
	case RefStateFailed:
		// Each failure and each new cause (a parked object's blocker changed) is a message of its own.
		message := &link.FailedMessage{
			Hold:  h.id,
			Cause: link.WireCause{Kind: string(shown.Cause.Kind), Error: wireError(shown.Cause.Error)},
		}
		if shown.RetryAt != nil {
			message.RetryIn = link.Some(int(max(0, *shown.RetryAt-h.provider.link.host.loop().Now())))
		}
		h.provider.send(message)
		// Told a failure, its consumer rejects what it still waits on: calls by the old names are not taken any more.
		h.state = holdState{kind: holdPending, told: shown.Cause.Error}
	case RefStateLoading:
		if current.told != nil {
			h.provider.send(&link.LoadingMessage{Hold: h.id})
			h.state = holdState{kind: holdPending}
		}
	}
}

// restart starts a refused hold again, announced with `loading`.
func (h *hold) restart(request holdRequest) {
	h.state = holdState{kind: holdPending}
	h.provider.send(&link.LoadingMessage{Hold: h.id})
	h.start(request)
}

// call calls a method of the held object as the hold's caller, started inside the section that dispatches the call (in
// message order, as Pi's synchronous start of the handler); the result settles in a later section.
func (h *hold) call(method string, args any, ctx context.Context) *parkedCallResult {
	if h.object == nil {
		result := newParkedCallResult()
		result.settle(nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "hold released"}, nil))
		return result
	}
	return h.object.call(method, args, ctx, h.from, h.contract().version)
}

// method is the method at index of the bound variant and whether its result carries byte segments (its schema has a
// `bytes()` leaf). It fails on an index outside the variant (a protocol error); a hold that is not callable has none.
func (h *hold) method(index int) (name string, resultBytes bool, err error) {
	// While it rebinds: by the names the consumer still has (its object answers by name).
	names := h.names()
	if names != nil && index >= 0 && index < len(names.names.Methods) {
		name = names.names.Methods[index]
		return name, names.variant.Methods.Value(name).Result.Bytes, nil
	}
	return "", false, fmt.Errorf("hold %d: no method %d", h.id, index)
}

// boundEntry is the hold's binding, sent when a slot is free. The snapshot of every member, as a value or failed, and the
// subscription are taken in this synchronous step (race 2), so each member's next ops continue exactly from it. Names
// travel as indexes into the bound version (2b). Counts one encode (`chord.link.encodes`).
func (h *hold) boundEntry(ctx context.Context) link.BoundEntry {
	object := h.object
	contract := object.served.contract
	// A live remote object has its variant.
	variant, _ := object.variant()
	bound := h.bindTo(object)
	var snapshot []link.BoundMember
	var failures []link.FailedMember
	version := h.contract().version
	current := version == contract.Version
	var objectMembers map[string]state.DeriveSource
	if current {
		list, err := object.stateMembers()
		if err != nil {
			// The object is live (a binding is taken only then).
			panic(err)
		}
		objectMembers = make(map[string]state.DeriveSource, len(list))
		for _, m := range list {
			objectMembers[m.name] = m.source
		}
	}
	for index, member := range bound.names.State {
		// An older version's members are its instance's mapping (its refusals unbound it before: `ChordObject`).
		var source state.DeriveSource
		if current {
			source = objectMembers[member]
		} else {
			var err error
			if source, err = object.liveMember(member, version); err != nil {
				panic(err)
			}
		}
		value, err := source.Value()
		if err != nil {
			// A failed member (a derive whose compute throws) is followed: it recovers with a root replacement.
			failures = append(failures, link.FailedMember{Member: index, Error: wireError(err)})
			if handle, ok := source.(state.SourceHandle); ok {
				h.follow(bound, index, handle)
			}
			continue
		}
		part, err := h.encodePart(value, ctx)
		if err != nil {
			// A value past the link's limits: failed for this hold, which takes no more of it.
			failures = append(failures, link.FailedMember{Member: index, Error: wireError(err)})
			continue
		}
		snapshot = append(snapshot, link.BoundMember{Member: index, Value: part})
		// A constant never changes: nothing to follow.
		if handle, ok := source.(state.SourceHandle); ok {
			h.follow(bound, index, handle)
		}
	}
	h.changes = nil
	h.failures = nil
	h.retired = link.NoRetired
	countEncode(ctx)
	order, err := contracts.VariantOrder(contract)
	if err != nil {
		panic(err)
	}
	return link.BoundEntry{Hold: h.id, Variant: slices.Index(order, variant), Members: snapshot, Failed: failures}
}

// encodePart is a member value as a part of its link, within its limits.
func (h *hold) encodePart(value delta.JsonValue, ctx context.Context) (link.Part, error) {
	text, err := link.JsonPart(value)
	if err != nil {
		return nil, err
	}
	return h.provider.link.part(string(text), ctx), nil
}

// entry is this batch's entry of the `state` message: the members it changed with their ops, each encoded once with its
// member's encoder, and the members that failed; resets both. Counts one encode per hold (`chord.link.encodes`).
func (h *hold) entry(ctx context.Context) link.StateEntry {
	var members []link.StateMember
	for _, change := range h.take() {
		members = append(members, link.StateMember{Member: change.followed.index, Ops: h.encodeOps(change.followed, change.ops, ctx)})
	}
	return h.entryOf(members, ctx)
}

func (h *hold) encodeOps(member *holdFollowed, ops []delta.Op, ctx context.Context) link.Part {
	wire, err := member.encoder.Encode(ops)
	if err != nil {
		panic(err)
	}
	text, err := link.JsonPart(wire)
	if err != nil {
		panic(err)
	}
	return h.provider.link.part(string(text), ctx)
}

// queue: the window is full: this batch's ops join the members' pending queues (raw, never mutated).
func (h *hold) queue() {
	for _, change := range h.take() {
		change.followed.pending.Push(change.ops)
	}
}

// pendingEntry is the entry of a freed slot's `state` message: each member's pending ops encoded once, or `[["r", value]]`
// after an overflow, all reflecting the latest commit; and the members that failed meanwhile. Counts one encode per hold.
func (h *hold) pendingEntry(ctx context.Context) link.StateEntry {
	var members []link.StateMember
	// Only a bound hold has pending ops.
	for _, member := range h.state.followed {
		if member.pending.Size() == 0 {
			continue
		}
		taken := member.pending.Take()
		ops := taken.Ops
		if taken.Replace {
			value, _ := member.source.Value()
			ops = []delta.Op{{"r", value}}
		}
		if len(ops) > 0 {
			members = append(members, link.StateMember{Member: member.index, Ops: h.encodeOps(member, ops, ctx)})
		}
	}
	return h.entryOf(members, ctx)
}

// release: the consumer released the hold, or its link closed: its waits stop and its object loses this reference.
func (h *hold) release() {
	h.drop()
	if h.object != nil {
		h.object.unhold(h)
	}
	h.object = nil
}

// drop ends the hold without releasing its object (the host closes and ends its objects itself).
func (h *hold) drop() {
	host := h.provider.link.host
	switch {
	case h.state.kind == holdBound:
		unfollow(&h.state)
	case h.state.kind == holdUnplaced || (h.state.kind == holdRefused && h.state.cause.Kind == data.FailureWaiting):
		host.unindex(h.state.request.Type, h)
	}
	h.state = holdState{kind: holdReleased}
	if h.counted {
		host.count("references", -1)
	}
	h.counted = false
}

// end: the object ended while held (revocation; a reload, slice 7b), or the hold was revoked (or its host closed) while it
// held no object: the hold sends `gone` with err.
func (h *hold) end(err *data.ChordError) {
	if h.state.kind == holdReleased {
		return
	}
	h.provider.forget(h, err)
	h.drop()
	h.object = nil
	h.provider.send(&link.GoneMessage{Hold: h.id, Error: wireError(err)})
}

// bindTo is the binding: its variant and names at the hold's version, with no member followed yet, and the names its consumer
// still has (a rebinding's) until it acknowledged this `bound`, the next of this host's own messages.
func (h *hold) bindTo(object *chordObject) *holdState {
	variants, err := contracts.VariantsAt(object.served.contract, h.contract().version)
	if err != nil {
		panic(err)
	}
	variant, _ := object.variant()
	data := variants.Value(variant)
	previous := h.names()
	h.state = holdState{kind: holdBound, variant: data, names: contracts.VariantNamesOf(data), instance: object.instance()}
	if previous != nil {
		h.state.rebinding, h.state.until = previous, h.provider.nextOwn()
	}
	return &h.state
}

// retarget: a hold over a link follows its object's instances in `moved` (a fresh `bound` for a new one).
func (*hold) retarget() {}

// following: it moves nothing in `retarget`.
func (*hold) following() bool { return false }

// removed: its object left it (slice 7b §6): its type was removed (`not_served`) or its provider no longer binds the
// hold's version (`version`): no longer subscribed, its calls in flight rejected, it waits refused with removal.cause in
// the host's index (a publication starts it again) and the consumer is sent `failed`, which unbinds a bound hold.
func (h *hold) removed(r removal) {
	if h.state.kind == holdReleased || h.object == nil {
		return
	}
	if h.state.kind == holdBound {
		unfollow(&h.state)
	}
	h.changes = nil
	h.failures = nil
	h.retired = link.NoRetired
	h.touchedFlag = false
	h.provider.unbound(h, r.cause.Error)
	h.object = nil
	h.refuse(*h.request, r.cause)
}

// unfollow stops following every member of a bound hold.
func unfollow(bound *holdState) {
	for _, member := range bound.followed {
		member.source.Unsubscribe(member.subscriber)
	}
}

// follow follows member index from its snapshot on (a failed one too: it may recover in this hold): its own encoder and
// pending queue, and a subscription feeding its calls into this hold's batch.
func (h *hold) follow(bound *holdState, index int, source state.SourceHandle) {
	member := &holdFollowed{index: index, source: source, encoder: delta.NewEncoder()}
	member.subscriber = &followedSubscriber{hold: h, followed: member}
	source.Subscribe(member.subscriber)
	bound.followed = append(bound.followed, member)
}

// Advance feeds the member's call of this batch into its hold. Failures fold per member: a failure drops what was pending
// for it and replaces a failure not sent yet (one at most per member, however long the consumer stalls); the value after
// it (a root replacement) replaces the failure. A retirement waits for a value.
func (s *followedSubscriber) Advance(call chord.WatchCall[delta.JsonValue], _ context.Context) {
	h, member := s.hold, s.followed
	h.touch()
	if call.Kind == chord.WatchCallFailed {
		h.memberFailed(member, call.Error)
		return
	}
	h.memberHealed(member.index)
	// A retired derive's frame may carry no ops (rule 21): only the flag travels.
	if len(call.Ops) > 0 {
		h.changes = append(h.changes, heldChange{followed: member, ops: call.Ops})
	}
	// A new list per retirement (rare): the shared empty one is never written to.
	if call.Retired && !slices.Contains(h.retired, member.index) {
		h.retired = append(slices.Clone(h.retired), member.index)
	}
}

// End: the member's source ended with its object, which ends this hold.
func (*followedSubscriber) End(chord.WatchEnd) {}

// EndAfterDelivery: likewise.
func (*followedSubscriber) EndAfterDelivery() {}

// take takes the current batch's changes; the hold is untouched again.
func (h *hold) take() []heldChange {
	changes := h.changes
	h.changes = nil
	h.touchedFlag = false
	return changes
}

// entryOf is a state entry with members and the failures and retirements since the last one; counts one encode.
func (h *hold) entryOf(members []link.StateMember, ctx context.Context) link.StateEntry {
	failures := h.failures
	retired := h.retired
	h.failures = nil
	h.retired = link.NoRetired
	if len(retired) > 0 && len(failures) > 0 {
		// A failed member's retirement travels with its next value, never with its failure.
		failedAt := func(index int) bool {
			return slices.ContainsFunc(failures, func(f link.FailedMember) bool { return f.Member == index })
		}
		var kept, sent []int
		for _, index := range retired {
			if failedAt(index) {
				kept = append(kept, index)
			} else {
				sent = append(sent, index)
			}
		}
		if len(kept) > 0 {
			h.retired = kept
			retired = sent
			if retired == nil {
				retired = link.NoRetired
			}
		}
	}
	countEncode(ctx)
	return link.StateEntry{Hold: h.id, Members: members, Failed: failures, Retired: retired}
}

func (h *hold) touch() {
	if h.touchedFlag {
		return
	}
	h.touchedFlag = true
	h.provider.touched(h)
}

// refuse refuses the hold with cause, holding nothing: it keeps request, indexed under its type while `waiting` (a
// publication starts it again), for a `retry` otherwise.
func (h *hold) refuse(request holdRequest, cause data.FailureCause) {
	h.state = holdState{kind: holdRefused, request: owned(request), cause: cause}
	if cause.Kind == data.FailureWaiting {
		h.provider.link.host.index(request.Type, h)
	}
	h.provider.send(&link.FailedMessage{
		Hold:  h.id,
		Cause: link.WireCause{Kind: string(cause.Kind), Error: wireError(cause.Error)},
	})
}

func countEncode(ctx context.Context) {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.link.encodes", telemetry.InstrumentOptions{}).Add(1, nil)
}

// wireError is the error as it crosses a link.
func wireError(err error) link.WireError { return link.WireErrorOf(err) }

// owned is request with its params owned, not a view of the message they came in (which the link's buffers may hold).
func owned(request holdRequest) holdRequest {
	if bytes, ok := request.Params.(link.Bytes); ok {
		request.Params = link.Bytes(slices.Clone(bytes))
	}
	return request
}

// actingFor is the fields of a `hold` message naming whom it acts for (from): sent only on a route link, whose child
// honours them (§4); none elsewhere.
func actingFor(hl *hostLink, from caller) (principal data.Principal, incarnation string) {
	if hl.role == linkRoleRoute && from.principal != nil {
		return from.principal, from.incarnation
	}
	return nil, ""
}
