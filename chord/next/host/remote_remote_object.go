package host

import (
	"context"
	"fmt"
	"slices"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/remote/remote-object.ts. An object held over a link (§7): what one remote reference is
// bound to on the consumer, across the links of its path (the parent, or a route address). Its state, the replicas of its
// last bound variant (kept while its link is down), its hold on the path's current link and calls through it.
//
// Every method runs inside a section of the host's loop.

// replicaSet is the replicas of a binding by member name, in the order they were made (Pi's `Map<string, Replica>`).
type replicaSet struct {
	names  []string
	byName map[string]*replica
}

func newReplicaSet() *replicaSet { return &replicaSet{byName: map[string]*replica{}} }

func (s *replicaSet) set(name string, r *replica) {
	if _, ok := s.byName[name]; !ok {
		s.names = append(s.names, name)
	}
	s.byName[name] = r
}

// boundVariant is the last binding: its variant, methods, the bound version's names (the wire's indexes), a replica per
// member, and a node per event member, at which the events arriving for this binding go out (a new binding has new nodes,
// so its reference's subscriptions move there from its counter value: nothing of an earlier binding reaches them).
type boundVariant struct {
	variant  string
	methods  *contracts.NamedMap[*contracts.MethodData]
	names    contracts.VariantNames
	replicas *replicaSet
	nodes    map[string]*eventNode
}

// remoteHold is the hold on the path's link while one is open: that link's consumer side and this hold's id on it.
type remoteHold struct {
	side *consumerSide
	id   int
}

// remoteObject is the object of the remote references with one sharing key, owned by its host across the links of its
// path (one held by a `self.get` reference or a private retry is its reference's alone). It is `loading` until a link's
// hold of it is bound, and again whenever that link drops or its relay's upstream starts over; each link holds it once,
// with its own id, each member with its own Delta decoder. It tells every binding of each change; the hold ends with the
// last (decision "References share one wire hold").
type remoteObject struct {
	// name is the object in errors: its type, and its params key when it has params.
	name     string
	contract *contracts.Contract
	host     *chordHost
	// request is what its holds ask for: the params as a part (checked by L1 when the reference was made), the address.
	request holdRequest
	// caller is whom its reference acts for: named on route links (actingFor).
	caller caller
	// checked are its params as L1 checked them, for placing it anew (a private retry after a part past the limits).
	checked checkedParams
	// path is its path: the parent's or a route address's; the host's `unplaced` one after a part past the limits.
	path *path
	// dependent is the object whose activation declared the reference (`self.get`): its holds carry its chain while it
	// activates.
	dependent *chordObject
	refState  *RefState
	// bound is the last binding, kept while loading or failed: members keep their last values and watches.
	bound *boundVariant
	held  *remoteHold
	// bindings are its references' bindings, in the order they joined (Pi's `Set<Binding>`); the hold ends with the last.
	bindings []*binding
	// unindex takes its entry out of its path's sharing table, once; nil for a private object or once taken out.
	unindex func()
	failure *data.ChordError
	// holding is its current `chord.hold` span until its hold's `bound` or failure; nil when nothing records.
	holding *chordtelemetry.OpenSpan
	// firstHold is the carrier of its first hold's span: a re-hold's span links to it.
	firstHold telemetry.TelemetryCarrier
	// holds is how many times it was held (on the links of its path).
	holds int
}

var (
	_ upstream = (*remoteObject)(nil)
	_ target   = (*remoteObject)(nil)
)

// newRemoteObject makes the object for its first reference (its getter's trigger), on p; unindex takes it out of its
// path's sharing table (nil: a private object, never shared).
func newRemoteObject(host *chordHost, contract *contracts.Contract, checked checkedParams, address link.WireAddress, from caller, p *path, dependent *chordObject, trigger telemetry.TelemetryContext, unindex func()) *remoteObject {
	name := contract.Name
	if checked.key != "" {
		name = fmt.Sprintf("%s(%s)", contract.Name, checked.key)
	}
	params, err := link.JsonPart(checked.params)
	if err != nil {
		// Checked by L1 when the reference was made: JSON already.
		panic(err)
	}
	r := &remoteObject{
		name: name, contract: contract, host: host, caller: from, checked: checked, path: p, dependent: dependent,
		refState: refLoading, unindex: unindex,
		request: holdRequest{
			Type: contract.Name, Version: contract.Version, Fingerprint: contract.Fingerprint, Params: params, Address: address,
		},
	}
	// Its hold is shared work (decision "Shared work"): a root on the host's adapter whatever its first getter's context,
	// linked to that getter's span when traced; its first hold carries the span's carrier (holdTrace).
	if !chordtelemetry.Untraced(host.telemetry) {
		r.holding = chordtelemetry.NewOpenSpan(host.telemetry, "chord.hold", r.holdAttributes())
		if !chordtelemetry.Untraced(trigger) {
			r.holding.Span().AddLink(telemetry.SpanLink{Context: trigger})
		}
		r.firstHold = chordtelemetry.CarrierOf(r.holding.Span())
	}
	return r
}

func (r *remoteObject) holdAttributes() telemetry.SpanAttributes {
	return telemetry.SpanAttributes{
		telemetry.Attr("chord.host", r.host.spanHost),
		telemetry.Attr("type", r.contract.Name),
		telemetry.Attr("object", chordtelemetry.Capped(r.name)),
		telemetry.Attr("address.kind", r.request.Address.Kind),
	}
}

// span is its `chord.hold` span until its first `bound` or failure (a route's resolve links to it); nil when not recorded.
func (r *remoteObject) span() telemetry.TelemetrySpan {
	if r.holding == nil {
		return nil
	}
	return r.holding.Span()
}

// holdTrace is the carrier of this hold: the first one's span's (a root of this host's adapter); a re-hold on a later
// link (a reconnect, days later for a page kept open) is a fresh `chord.hold` root linked to the first, so nothing keeps
// adding children under a span long ended (review L1). None when the host's adapter records nothing (then the first hold
// had no span either, whatever its getter's context).
func (r *remoteObject) holdTrace() telemetry.TelemetryCarrier {
	first := r.holds == 0
	r.holds++
	if first {
		return r.firstHold
	}
	if r.firstHold == nil {
		return nil
	}
	adapter := r.host.telemetry
	// A hold that dropped before it was bound ends here.
	r.endHold("ended", nil)
	r.holding = chordtelemetry.NewOpenSpan(adapter, "chord.hold", r.holdAttributes(), telemetry.SpanLink{Context: chordtelemetry.ParentOf(r.firstHold, adapter)})
	return chordtelemetry.CarrierOf(r.holding.Span())
}

// endHold is its first `bound` or failure (or its end before either): its `chord.hold` span ends with outcome.
func (r *remoteObject) endHold(outcome string, err error) {
	holding := r.holding
	if holding == nil {
		return
	}
	r.holding = nil
	if err != nil {
		chordtelemetry.SpanFailed(holding.Span(), "hold_failed", err)
	}
	holding.SetOutcome(outcome)
}

func (*remoteObject) kind() string { return "remote" }

// principal is the principal its reference acts for (Pi's `caller.principal`).
func (r *remoteObject) principal() data.Principal { return r.caller.principal }

func (r *remoteObject) label() string { return r.name }

func (r *remoteObject) state() *RefState { return r.refState }

// stateFor: over a link another host decides what is live: its state.
func (r *remoteObject) stateFor(int, string) *RefState { return r.refState }

// blocksFirstPass: an object over a link is no blocker of this host's first pass.
func (*remoteObject) blocksFirstPass() bool { return false }

// lastFailure is the error of the last `failed`, until `bound`.
func (r *remoteObject) lastFailure() *data.ChordError { return r.failure }

// variant is the variant last bound; false before the first binding.
func (r *remoteObject) variant() (string, bool) {
	if r.bound == nil {
		return "", false
	}
	return r.bound.variant, true
}

// hydrated is whether its state members have values: bound once.
func (r *remoteObject) hydrated() bool { return r.bound != nil }

// generationOf is how many values the replica of member took; false without one.
func (r *remoteObject) generationOf(member string) (int, bool) {
	if r.bound == nil {
		return 0, false
	}
	replica := r.bound.replicas.byName[member]
	if replica == nil {
		return 0, false
	}
	return replica.generation, true
}

// unseenMember is a state member a binding joining a bound object has not seen, with its replica's count of values then
// (0 without one): an entry of Pi's `ReadonlyMap<string, number>`.
type unseenMember struct {
	member string
	count  int
}

// unplaced is whether its path is the host's unplaced one: a part past the limits failed it, so it holds nothing anywhere.
func (r *remoteObject) unplaced() bool { return r.path == r.host.unplaced }

// hold counts h (a reference's binding) as one of its references. One joining it after it was bound (decision
// "References share one wire hold") has not seen what it finds: every member when it is not live, else the members
// failed or missing from the bound variant; each counts as seen once its replica takes a value after the join.
// `binding.bind` retargets and tells it.
func (r *remoteObject) hold(h holder) {
	b := h.(*binding)
	if r.bound != nil && len(r.bindings) > 0 {
		b.joined(r.unseen(r.bound))
	}
	if !slices.Contains(r.bindings, b) {
		r.bindings = append(r.bindings, b)
	}
}

// unseen are the members a binding joining now has not seen, each with its replica's count of values now (0 without
// one): its own history starts there.
func (r *remoteObject) unseen(bound *boundVariant) []unseenMember {
	live := r.refState.Kind == RefStateLive
	variants, err := contracts.CurrentVariants(r.contract)
	if err != nil {
		// A served or got contract was checked when it was made.
		return nil
	}
	var unseen []unseenMember
	listed := map[string]struct{}{}
	for _, variant := range variants.All() {
		for _, member := range contracts.VariantNamesOf(variant).State {
			if _, is := listed[member]; is {
				continue
			}
			listed[member] = struct{}{}
			replica := bound.replicas.byName[member]
			good := live && slices.Contains(bound.names.State, member) && replica != nil && replica.Start().Kind == state.StartValue
			if good {
				continue
			}
			count := 0
			if replica != nil {
				count = replica.generation
			}
			unseen = append(unseen, unseenMember{member: member, count: count})
		}
	}
	return unseen
}

// unhold: h was released. While others remain nothing is sent but the cancellation of its own calls (queued ones leave
// unsent, sent ones are aborted); with the last the hold is released and the object ends (no linger, D8).
func (r *remoteObject) unhold(h holder) {
	b, is := h.(*binding)
	if !is {
		return
	}
	at := slices.Index(r.bindings, b)
	if at < 0 {
		return
	}
	r.bindings = slices.Delete(r.bindings, at, at+1)
	if len(r.bindings) > 0 {
		if r.held != nil {
			r.held.side.cancelCalls(r.held.id, b, r.name)
		}
		return
	}
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: r.name + ": released"}, nil)
	// Out of the table before anything else runs: a reference made from here on holds anew.
	r.unpublish()
	if r.held != nil {
		r.held.side.release(r.held.id, err)
	}
	r.endWith(err)
}

// unpublish leaves its path's sharing table (once): it is never put back.
func (r *remoteObject) unpublish() {
	unindex := r.unindex
	r.unindex = nil
	if unindex != nil {
		unindex()
	}
}

// attach holds on link, the path's link now (a first hold, or a re-hold on a new link), with the activation chain of its
// dependent while that activates (Q5).
func (r *remoteObject) attach(l *hostLink) {
	var chain []string
	if r.dependent != nil {
		chain = r.dependent.activationChain()
	}
	r.held = &remoteHold{side: l.consumer, id: l.consumer.attach(r, chain, r.holdTrace())}
}

// unreachable: its path's address cannot be reached for now (a route's `resolve` failed, a suspension): `failed { waiting }`
// with err, still on the path, bound by its next link.
func (r *remoteObject) unreachable(err *data.ChordError) {
	r.failure = err
	r.endHold("failed", err)
	if r.refState.Kind != RefStateEnded {
		r.move(&RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: data.FailureWaiting, Error: err}})
	}
}

// withheld: its route's admission holds it back with cause (slice 14 §2.3): released at its provider when it is held there
// (its calls reject, `forbidden/revoked` for a denial), and `failed` with the cause; a denial drops its replicas. It stays on
// its path: an allow holds it again.
func (r *remoteObject) withheld(cause data.FailureCause) {
	held := r.held
	r.held = nil
	err := cause.Error
	if held != nil {
		rejected := err
		if cause.Kind == data.FailureDenied {
			rejected = revoked(r.name, err)
		}
		held.side.release(held.id, rejected)
	}
	r.failure = err
	r.endHold("failed", err)
	if cause.Kind == data.FailureDenied {
		r.bound = nil
	}
	if r.refState.Kind != RefStateEnded {
		r.move(&RefState{Kind: RefStateFailed, Cause: cause})
	}
}

// admitted: its route's admission allows it: loading (again, after what held it back) until its next binding.
func (r *remoteObject) admitted() { r.startOver() }

// dropped: the link dropped: loading until a new link's hold is bound (startOver).
func (r *remoteObject) dropped(*data.ChordError) {
	r.held = nil
	r.startOver()
}

// call calls a method of the bound variant over the link; one outside it fails `invalid/member`. Only a live object is
// called: bound on an open link. The result settles as the call over the link does.
func (r *remoteObject) call(method string, args any, b *binding, ctx context.Context, parent telemetry.TelemetrySpan) *parkedCallResult {
	result := newParkedCallResult()
	bound, held := r.bound, r.held
	methodData, ok := bound.methods.Get(method)
	if !ok {
		result.settle(nil, memberInvalid(fmt.Sprintf("%s (variant %s) has no method %q", r.name, bound.variant, method)))
		return result
	}
	// Arguments are checked by the provider.
	index := slices.Index(bound.names.Methods, method)
	if chordtelemetry.Untraced(parent) {
		held.side.call(held.id, index, methodData, args, b, ctx).then(r.host.loop(), result.settle)
		return result
	}
	attributes := telemetry.SpanAttributes{
		telemetry.Attr("chord.host", r.host.spanHost),
		telemetry.Attr("type", r.contract.Name),
		telemetry.Attr("member", method),
		telemetry.Attr("object", chordtelemetry.Capped(r.name)),
		telemetry.Attr("address.kind", r.request.Address.Kind),
	}
	attributes = append(attributes, r.host.PrincipalAttributes(r.caller.principal, ctx)...)
	span := chordtelemetry.NewOpenSpan(parent, "chord.call", attributes)
	sent := held.side.call(held.id, index, methodData, args, b, chordctx.WithTelemetry(ctx, span.Span()))
	sent.then(r.host.loop(), func(value any, err error) {
		span.EndWith(err, "call_failed", ctx)
		result.settle(value, err)
	})
	return result
}

// followed is what its reference follows of member (a consumer over a link binds at its own version, mapped at the
// provider): the replica while live, a member the bound variant lacks (`invalid/member`); while loading or failed
// nothing, the replica (kept until the next binding) giving its last value (rule 5).
func (r *remoteObject) followed(member string, _ int) followed {
	bound := r.bound
	var replica *replica
	if bound != nil {
		replica = bound.replicas.byName[member]
	}
	var last state.DeriveSource
	if replica != nil {
		last = replica
	}
	switch r.refState.Kind {
	case RefStateLive:
		if replica != nil {
			return followed{kind: followedLive, source: replica, instance: bound}
		}
		variant := ""
		if bound != nil {
			variant = bound.variant
		}
		return followed{kind: followedLacking, err: memberInvalid(fmt.Sprintf("%s (variant %s) has no state member %q", r.name, variant, member))}
	case RefStateFailed:
		// A denial keeps no value (slice 14 §2.2).
		if r.refState.Cause.Kind == data.FailureDenied {
			return followed{kind: followedDenied, err: r.refState.Cause.Error}
		}
		return followed{kind: followedFailed, err: r.refState.Cause.Error, last: last, instance: bound}
	}
	return followed{kind: followedLoading, last: last, instance: bound}
}

// attachedVariant makes a binding an `attachable`: the variant its replicas are of.
func (b *boundVariant) attachedVariant() string { return b.variant }

// eventNode is event member member's node as a binding taps it: a remote binding's reference binds at its own version (the
// provider mapped what arrives).
func (b *boundVariant) eventNode(member string, version int) *eventNode { return b.nodes[member] }

// eventNodes are the nodes of a new binding with event members members (new nodes: nothing of an earlier binding reaches
// its reference's subscriptions); nil for a variant without event members.
func eventNodes(members []string) map[string]*eventNode {
	if len(members) == 0 {
		return nil
	}
	nodes := make(map[string]*eventNode, len(members))
	for _, member := range members {
		nodes[member] = &eventNode{}
	}
	return nodes
}

// eventNodeOf is where an event of member member (an index into the bound version's event members) of its hold goes: its
// live binding's node for it, when something taps it; nil when the value is to be dropped unopened (not live, no
// listener). An index outside the bound version is an error (a protocol error).
func (r *remoteObject) eventNodeOf(member int) (*eventNode, error) {
	bound := r.bound
	if r.refState.Kind != RefStateLive || bound == nil {
		return nil, nil
	}
	if member < 0 || member >= len(bound.names.Events) {
		return nil, fmt.Errorf("%s: no event member %d", r.label(), member)
	}
	// A name of the bound variant's event members has its node.
	node := bound.nodes[bound.names.Events[member]]
	if !node.tapped() {
		return nil, nil
	}
	return node, nil
}

// event is one entry of an `event` message for its hold: the value of event member member stamped at, taking this host's
// next counter value, at the node its reference's subscriptions tap (dropped unopened when it is not live or nothing
// listens); dropped past the link's limits (`too_large`, counted; the hold stays bound).
func (r *remoteObject) event(member int, at int, value link.Part, hl *hostLink) error {
	node, err := r.eventNodeOf(member)
	if err != nil || node == nil {
		return err
	}
	events := r.host.eventFlow
	opened, err := hl.openPart(value, hl.limits, hl.context)
	if err != nil {
		if _, tooLarge := data.IsChordErrorCode(err, data.CodeTooLarge); !tooLarge {
			return err
		}
		events.metrics.Counter("chord.events.dropped").Add(1, telemetry.SpanAttributes{telemetry.Attr("reason", "too_large")})
		return nil
	}
	// Fresh JSON from the link: event values are never validated (as state, §30).
	parsed, err := link.ParsePart(opened)
	if err != nil {
		return err
	}
	events.push(node, parsed, int64(at))
	return nil
}

// attachable is its binding while live (its replicas change); nil while loading or failed.
func (r *remoteObject) attachable() attachable {
	if r.refState.Kind == RefStateLive && r.bound != nil {
		return r.bound
	}
	return nil
}

// instance: keyed members are only in local contracts: no instance to pin a key on.
func (*remoteObject) instance() any { return nil }

// keyedMapping: keyed members are only in local contracts (keyedMember).
func (r *remoteObject) keyedMapping(member string, _ int) (contracts.Mapping, error) {
	_, err := r.keyedMember(member)
	return nil, err
}

// pinKey: keyed members are only in local contracts (keyedMember).
func (r *remoteObject) pinKey(member, _ string, _ int, _ context.Context) (pinnedKey, error) {
	_, err := r.keyedMember(member)
	return pinnedKey{}, err
}

// keyedMember: keyed members are only in local contracts, whose objects are never held over a link (slice 6a §12).
func (r *remoteObject) keyedMember(member string) (*documents.KeyedSlot, error) {
	return nil, memberInvalid(fmt.Sprintf("%s has no keyed member %q: keyed members are local", r.name, member))
}

// queryMember: `queries` members are only in local contracts, as keyed ones (slice 14 §3.1).
func (r *remoteObject) queryMember(member, _ string) (*OwnQuery, error) {
	return nil, memberInvalid(fmt.Sprintf("%s has no queries member %q: named queries are local", r.name, member))
}

// activationId: an object over a link activates on another host: it is in no chain here (Q5).
func (*remoteObject) activationId() string { return "" }

// pathTo: an object over a link waits for nothing on this host: no chain of waits (its holds carry the chain, Q5).
func (*remoteObject) pathTo(*chordObject, map[target]struct{}) []*chordObject { return nil }

// retry is `ref.retry()` from b, as a step of walk: a failed object's provider is asked for an attempt with the walk's
// hop budget, none left: nothing is sent (a hold it refused is started there again, for every reference sharing it; a
// parked object there passes it on with one hop less). After a part past the link's limits b alone moves to a private
// object of its own, placed anew and held with a new id (retryAlone).
func (r *remoteObject) retry(walk *retryWalk, b *binding) {
	if r.refState.Kind != RefStateFailed {
		return
	}
	if r.unplaced() {
		r.retryAlone(b)
		return
	}
	if r.held != nil && walk.hops > 0 {
		r.held.side.retry(r.held.id, walk.hops)
	}
}

// retryAlone moves b to a private object (never shared) that keeps what this one had for it (its variant, copies of its
// replicas with their counts: its last values and seen members stay its own) and places it anew; the others stay here,
// failed, until they retry. The binding leaves first, so this object ending with its last binding never ends it. A
// placement that fails fails only the private object broken (`hostAddress`, a route that refuses).
func (r *remoteObject) retryAlone(b *binding) {
	host := r.host
	own := newRemoteObject(host, r.contract, r.checked, r.request.Address, r.caller, host.unplaced, r.dependent, chordctx.Telemetry(chordctx.Background()), nil)
	if bound := r.bound; bound != nil {
		replicas := newReplicaSet()
		for _, member := range bound.replicas.names {
			replicas.set(member, bound.replicas.byName[member].fork(host.batch))
		}
		forked := *bound
		forked.replicas = replicas
		forked.nodes = eventNodes(bound.names.Events)
		own.bound = &forked
	}
	host.unplaced.attach(own)
	if at := slices.Index(r.bindings, b); at >= 0 {
		r.bindings = slices.Delete(r.bindings, at, at+1)
	}
	b.transfer(own)
	if len(r.bindings) == 0 {
		r.endWith(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: r.name + ": released"}, nil))
	}
	own.place()
}

// place places a private object anew, on its path now (a failing `hostAddress` or route fails it broken).
func (r *remoteObject) place() {
	placed, err := r.host.pathOf(r.contract, r.checked.params, r.caller.principal)
	if err != nil {
		r.fail(data.AsChordError(err, r.contract.Name))
		return
	}
	r.path.detach(r)
	r.path = placed
	placed.attach(r)
}

// tooLarge: a part of its snapshot or of a state change was past the link's limits (`too_large/part`): the hold is
// released at the provider, it leaves the sharing table, and every binding fails broken with err (they share the part),
// final: no link holds it again (a reconnect included); each binding's `retry` moves it alone (retryAlone).
func (r *remoteObject) tooLarge(err *data.ChordError) {
	held := r.held
	if held == nil {
		return
	}
	r.held = nil
	r.unpublish()
	held.side.release(held.id, err)
	r.path.detach(r)
	r.path = r.host.unplaced
	r.path.attach(r)
	r.fail(err)
}

// fail fails broken with err: no automatic retry.
func (r *remoteObject) fail(err *data.ChordError) {
	r.failure = err
	r.endHold("failed", err)
	r.move(&RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: data.FailureBroken, Error: err}})
}

// prepareBound prepares one entry of a `bound` message (a first binding, a re-hold; a live hold is bound again): the
// variant and every state member once, as a value or as failed. It returns what applies it inside the batch of the whole
// message: a member it had gets the snapshot as one frame `[["r", value]]` and a fresh decoder, a member outside the new
// variant fails (its replica stays, for a later binding that has it again), a new one gets its first value. It fails on a
// snapshot that does not fit (a protocol error), and `too_large/part` for a value past the link's limits, before anything
// changed. wentLive follows the batch.
func (r *remoteObject) prepareBound(entry link.BoundEntry, hl *hostLink) (func(), error) {
	order, err := contracts.VariantOrder(r.contract)
	if err != nil {
		return nil, err
	}
	if entry.Variant < 0 || entry.Variant >= len(order) {
		return nil, fmt.Errorf("hold %d: no variant %d", entry.Hold, entry.Variant)
	}
	variant := order[entry.Variant]
	variants, err := contracts.CurrentVariants(r.contract)
	if err != nil {
		return nil, err
	}
	variantData := variants.Value(variant)
	names := contracts.VariantNamesOf(variantData)
	members := map[string]memberSnapshot{}
	var memberOrder []string
	add := func(index int, snapshot memberSnapshot) error {
		if index < 0 || index >= len(names.State) {
			return fmt.Errorf("hold %d: member %d in bound", entry.Hold, index)
		}
		member := names.State[index]
		if _, dup := members[member]; dup {
			return fmt.Errorf("hold %d: member %d in bound", entry.Hold, index)
		}
		members[member] = snapshot
		memberOrder = append(memberOrder, member)
		return nil
	}
	// Fresh JSON from the link: state is JSON and never validated (§30).
	for _, m := range entry.Members {
		opened, err := hl.openPart(m.Value, hl.limits, hl.context)
		if err != nil {
			return nil, err
		}
		value, err := link.ParsePart(opened)
		if err != nil {
			return nil, err
		}
		if err := add(m.Member, memberSnapshot{kind: "value", value: value}); err != nil {
			return nil, err
		}
	}
	for _, m := range entry.Failed {
		received, err := link.ErrorFromWire(m.Error)
		if err != nil {
			return nil, err
		}
		if err := add(m.Member, memberSnapshot{kind: "failed", err: received}); err != nil {
			return nil, err
		}
	}
	if len(members) != len(names.State) {
		return nil, fmt.Errorf("hold %d: the snapshot misses a member", entry.Hold)
	}
	return func() {
		var previous *replicaSet
		if r.bound != nil {
			previous = r.bound.replicas
		}
		replicas := newReplicaSet()
		// First: a derive recomputing in this batch reads the new variant through its reference.
		r.bound = &boundVariant{variant: variant, methods: variantData.Methods, names: names, replicas: replicas, nodes: eventNodes(names.Events)}
		for _, member := range memberOrder {
			snapshot := members[member]
			var existing *replica
			if previous != nil {
				existing = previous.byName[member]
			}
			if existing == nil {
				replicas.set(member, newReplica(r.host.batch, snapshot))
			} else {
				existing.reset(snapshot)
				replicas.set(member, existing)
			}
		}
		if previous != nil {
			for _, member := range previous.names {
				if _, kept := members[member]; kept {
					continue
				}
				gone := previous.byName[member]
				gone.vanish(memberInvalid(fmt.Sprintf("%s (variant %s) has no state member %q", r.name, variant, member)))
				replicas.set(member, gone)
			}
		}
	}, nil
}

// wentLive: after a `bound` message's batch: the reference goes live (its parked calls are sent, a parked dependent goes
// on).
func (r *remoteObject) wentLive() {
	r.failure = nil
	r.endHold("bound", nil)
	r.move(refLive)
}

// failed is a `failed` message: the provider's object failed and no attempt runs (RetryIn: the rest of its backoff
// window), or the hold is unbound there or its object parked on a failed dependency (`waiting`), each new cause a new
// message. On a bound hold it unbinds it (slice 7b, model §5): the members keep their last values until the next
// `bound`. A denial (`denied`: an admission refused this hold's caller, slice 14 §2.2) drops its replicas: no value of it
// stays readable here. It fails on an error that is no wire error (a protocol error).
func (r *remoteObject) failed(message *link.FailedMessage) error {
	// A plugin's error a provider failed with is this object's failure, as a ChordError.
	received, err := link.ErrorFromWire(message.Cause.Error)
	if err != nil {
		return err
	}
	failure := data.AsChordError(received, r.name)
	r.failure = failure
	r.endHold("failed", failure)
	if message.Cause.Kind == string(data.FailureDenied) {
		r.bound = nil
	}
	next := &RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: data.FailureKind(message.Cause.Kind), Error: failure}}
	if message.RetryIn.Ok {
		at := r.host.loop().Now() + int64(message.RetryIn.Value)
		next.RetryAt = &at
	}
	r.move(next)
	return nil
}

// loading: the provider's object started a new attempt after a failure, or its relay's upstream link dropped (startOver).
func (r *remoteObject) loading() { r.startOver() }

// prepare decodes and applies one entry of a state message (its members by index, each with its ops), without changing
// anything yet: it returns what adopts it into the replicas, run inside the batch that applies the whole message. It fails
// on an entry that does not fit (a protocol error), and `too_large/part` for ops past the link's limits.
func (r *remoteObject) prepare(entry link.StateEntry, hl *hostLink) (func(), error) {
	bound := r.bound
	if r.refState.Kind != RefStateLive || bound == nil {
		return nil, fmt.Errorf("hold %d: state before bound", entry.Hold)
	}
	find := func(index int) (*replica, error) {
		if index >= 0 && index < len(bound.names.State) {
			if found, ok := bound.replicas.byName[bound.names.State[index]]; ok {
				return found, nil
			}
		}
		return nil, fmt.Errorf("hold %d: no state member %d", entry.Hold, index)
	}
	// Made by the first retirement (rare): no allocation per message otherwise.
	var retirements map[*replica]struct{}
	for _, member := range entry.Retired {
		target, err := find(member)
		if err != nil {
			return nil, err
		}
		if retirements == nil {
			retirements = map[*replica]struct{}{}
		}
		if _, twice := retirements[target]; twice {
			return nil, fmt.Errorf("hold %d: member %d retired twice in one state", entry.Hold, member)
		}
		retirements[target] = struct{}{}
	}
	type update struct {
		target *replica
		value  delta.JsonValue
		ops    []delta.Op
	}
	changed := map[*replica]struct{}{}
	updates := make([]update, 0, len(entry.Members))
	for _, m := range entry.Members {
		target, err := find(m.Member)
		if err != nil {
			return nil, err
		}
		if _, twice := changed[target]; twice {
			return nil, fmt.Errorf("hold %d: member %d twice in one state", entry.Hold, m.Member)
		}
		changed[target] = struct{}{}
		opened, err := hl.openPart(m.Ops, hl.limits, hl.context)
		if err != nil {
			return nil, err
		}
		wire, err := link.ParsePart(opened)
		if err != nil {
			return nil, err
		}
		value, ops, err := target.decode(wire)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update{target, value, ops})
	}
	type failure struct {
		target *replica
		err    error
	}
	failures := make([]failure, 0, len(entry.Failed))
	for _, m := range entry.Failed {
		target, err := find(m.Member)
		if err != nil {
			return nil, err
		}
		received, err := link.ErrorFromWire(m.Error)
		if err != nil {
			return nil, err
		}
		failures = append(failures, failure{target, received})
	}
	return func() {
		for _, u := range updates {
			_, retired := retirements[u.target]
			u.target.adopt(u.value, u.ops, retired)
		}
		for _, f := range failures {
			f.target.fail(f.err)
		}
		// A retirement without ops (a derive whose value stayed, rule 21): the flag alone, after the failures (a failed
		// member keeps it for its next value).
		for _, member := range entry.Retired {
			target, _ := find(member)
			if _, with := changed[target]; !with {
				target.retireAlone()
			}
		}
	}, nil
}

// revoke: its principal was revoked on this host (`host.revoke`): released at its provider if it is held there, then ended
// with err as `lost` does; no later link holds it.
func (r *remoteObject) revoke(err *data.ChordError) {
	held := r.held
	r.held = nil
	r.unpublish()
	if held != nil {
		held.side.release(held.id, err)
	}
	r.lost(err)
}

// lost: `gone`, or the host closes: every reference ends, their calls reject with err. The bindings are taken and the
// object ends first, so each ends exactly once whatever ending one does.
func (r *remoteObject) lost(err *data.ChordError) {
	bindings := slices.Clone(r.bindings)
	r.unpublish()
	if r.held != nil {
		r.held.side.forget(r.held.id, err)
	}
	r.endWith(err)
	for _, b := range bindings {
		b.end(err)
	}
}

// endWith ends the object: out of the table, detached from its path, replicas ended; keeps none of its references.
func (r *remoteObject) endWith(err *data.ChordError) {
	r.unpublish()
	bound := r.bound
	r.endHold("ended", nil)
	r.refState = &RefState{Kind: RefStateEnded, Error: err}
	r.bindings = nil
	r.dependent = nil
	r.held = nil
	r.bound = nil
	r.path.detach(r)
	if bound != nil {
		for _, name := range bound.replicas.names {
			bound.replicas.byName[name].end(err)
		}
	}
}

// startOver: loading again until the next binding; the members keep their last values. A drop while it is loading already
// changes nothing: a dependent parked on it goes on waiting for the next link's binding (a drop is not a dependency
// failure).
func (r *remoteObject) startOver() {
	if kind := r.refState.Kind; kind != RefStateEnded && kind != RefStateLoading {
		r.move(refLoading)
	}
}

// move moves to next and tells every binding, as a local object tells its holders: their members follow what it shows
// now (all in one batch), then each its state, in the order they joined; one released meanwhile is not told. A broken
// failure takes it out of the sharing table first, for good: a new reference holds anew.
func (r *remoteObject) move(next *RefState) {
	if next.Kind == RefStateFailed && next.Cause.Kind == data.FailureBroken {
		r.unpublish()
	}
	r.refState = next
	if len(r.bindings) == 0 {
		return
	}
	bindings := slices.Clone(r.bindings)
	if slices.ContainsFunc(bindings, (*binding).following) {
		_ = r.host.batch.Within(func() error {
			for _, b := range bindings {
				b.retarget()
			}
			return nil
		}, chordctx.Background())
	} else {
		for _, b := range bindings {
			b.retarget()
		}
	}
	for _, b := range bindings {
		if slices.Contains(r.bindings, b) {
			b.moved()
		}
	}
}

// memberInvalid is the `invalid/member` error.
func memberInvalid(message string) *data.ChordError { return data.Invalid(data.ReasonMember, message) }
