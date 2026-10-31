package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/remote/remote-object.ts. An object held over a link (§7): what one remote reference is
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

// boundVariant is the last binding: its variant, methods, the bound version's names (the wire's indexes) and a replica
// per member.
type boundVariant struct {
	variant  string
	methods  *contracts.NamedMap[*contracts.MethodData]
	names    contracts.VariantNames
	replicas *replicaSet
}

// remoteHold is the hold on the path's link while one is open: that link's consumer side and this hold's id on it.
type remoteHold struct {
	side *consumerSide
	id   int
}

// remoteObject is one remote reference's object, owned by its host across the links of its path. It is `loading` until a
// link's hold of it is bound, and again whenever that link drops or its relay's upstream starts over; each link holds it
// with its own id. Two references are two holds, each member of each with its own Delta decoder.
type remoteObject struct {
	// name is the object in errors: its type, and its params key when it has params.
	name     string
	contract *contracts.Contract
	host     *chordHost
	// request is what its holds ask for: the params as a part (checked by L1 when the reference was made), the address.
	request holdRequest
	// caller is whom its reference acts for: named on route links (actingFor).
	caller caller
	// params are its params, for placing it anew (`retry` after a part past the limits).
	params *delta.JsonObject
	// path is its path: the parent's or a route address's; the host's `unplaced` one after a part past the limits.
	path *path
	// dependent is the object whose activation declared the reference (`self.get`): its holds carry its chain while it
	// activates.
	dependent *chordObject
	refState  *RefState
	// bound is the last binding, kept while loading or failed: members keep their last values and watches.
	bound   *boundVariant
	held    *remoteHold
	binding holder
	failure *data.ChordError
}

var (
	_ upstream = (*remoteObject)(nil)
	_ target   = (*remoteObject)(nil)
)

func newRemoteObject(host *chordHost, contract *contracts.Contract, checked checkedParams, address link.WireAddress, from caller, p *path, dependent *chordObject) *remoteObject {
	name := contract.Name
	if checked.key != "" {
		name = fmt.Sprintf("%s(%s)", contract.Name, checked.key)
	}
	params, err := link.JsonPart(checked.params)
	if err != nil {
		// Checked by L1 when the reference was made: JSON already.
		panic(err)
	}
	return &remoteObject{
		name: name, contract: contract, host: host, caller: from, params: checked.params, path: p, dependent: dependent,
		refState: refLoading,
		request: holdRequest{
			Type: contract.Name, Version: contract.Version, Fingerprint: contract.Fingerprint, Params: params, Address: address,
		},
	}
}

func (*remoteObject) kind() string { return "remote" }

func (r *remoteObject) label() string { return r.name }

func (r *remoteObject) state() *RefState { return r.refState }

// heldOnLink is whether its hold is on a link now (a remote attempt answers for it), not while its path has no link.
func (r *remoteObject) heldOnLink() bool { return r.held != nil }

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

// hold: its one reference bound to it.
func (r *remoteObject) hold(h holder) { r.binding = h }

// unhold: the reference was released: the current link's provider is told and the reference's sent calls rejected.
func (r *remoteObject) unhold(holder) {
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: r.name + ": released"}, nil)
	if r.held != nil {
		r.held.side.release(r.held.id, err)
	}
	r.endWith(err)
}

// attach holds on link, the path's link now (a first hold, or a re-hold on a new link), with the activation chain of its
// dependent while that activates (Q5).
func (r *remoteObject) attach(l *hostLink) {
	var chain []string
	if r.dependent != nil {
		chain = r.dependent.activationChain()
	}
	r.held = &remoteHold{side: l.consumer, id: l.consumer.attach(r, chain)}
	// Held on a link now: a parked dependent checks again (`parked_long` counts a hold on a link only).
	if r.dependent != nil {
		r.dependent.dependencyMoved()
	}
}

// unreachable: its path's address cannot be reached for now (a route's `resolve` failed, a suspension): `failed { waiting }`
// with err, still on the path, bound by its next link.
func (r *remoteObject) unreachable(err *data.ChordError) {
	r.failure = err
	if r.refState.Kind != RefStateEnded {
		r.move(&RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: data.FailureWaiting, Error: err}})
	}
}

// dropped: the link dropped: loading until a new link's hold is bound (startOver).
func (r *remoteObject) dropped(*data.ChordError) {
	r.held = nil
	r.startOver()
	if r.dependent != nil {
		r.dependent.dependencyMoved()
	}
}

// call calls a method of the bound variant over the link; one outside it fails `invalid/member`. Only a live object is
// called: bound on an open link. The result settles as the call over the link does.
func (r *remoteObject) call(method string, args any, ctx context.Context, _ caller) *parkedCallResult {
	result := newParkedCallResult()
	bound, held := r.bound, r.held
	methodData, ok := bound.methods.Get(method)
	if !ok {
		result.settle(nil, memberInvalid(fmt.Sprintf("%s (variant %s) has no method %q", r.name, bound.variant, method)))
		return result
	}
	// Arguments are checked by the provider.
	sent := held.side.call(held.id, slices.Index(bound.names.Methods, method), methodData, args, ctx)
	sent.then(r.host.loop(), result.settle)
	return result
}

// member is the replica of member; `not_ready` before the first binding, `invalid/member` for a member no binding had (one
// a later variant lacks reads its `invalid/member` failure).
func (r *remoteObject) member(member string) (state.DeriveSource, error) {
	if r.bound == nil {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: r.name + ": not bound yet"}, nil)
	}
	found, ok := r.bound.replicas.byName[member]
	if !ok {
		return nil, memberInvalid(fmt.Sprintf("%s (variant %s) has no state member %q", r.name, r.bound.variant, member))
	}
	return found, nil
}

// lastValues: a failed remote object's members are not read: its failure is the reads' error (see `Target.lastValues`).
func (*remoteObject) lastValues() bool { return false }

// pinKey: keyed members are only in local contracts (keyedMember).
func (r *remoteObject) pinKey(member, _ string, _ context.Context) (pinnedKey, error) {
	_, err := r.keyedMember(member)
	return pinnedKey{}, err
}

// keyedMember: keyed members are only in local contracts, whose objects are never held over a link (slice 6a §12).
func (r *remoteObject) keyedMember(member string) (*documents.KeyedSlot, error) {
	return nil, memberInvalid(fmt.Sprintf("%s has no keyed member %q: keyed members are local", r.name, member))
}

// activationId: an object over a link activates on another host: it is in no chain here (Q5).
func (*remoteObject) activationId() string { return "" }

// pathTo: an object over a link waits for nothing on this host: no chain of waits (its holds carry the chain, Q5).
func (*remoteObject) pathTo(*chordObject, map[target]struct{}) []*chordObject { return nil }

// retry is `ref.retry()`: a failed object's provider is asked for an attempt (a hold it refused is started there again; a
// parked object there passes it on as an operation of its own host). One that failed for a part past the link's limits is
// placed anew and held with a new id, now or on its path's next link (a `hostAddress` that throws fails it broken again).
func (r *remoteObject) retry(operation *retryOperation) {
	if r.refState.Kind != RefStateFailed {
		return
	}
	if r.path == r.host.unplaced {
		r.place()
		return
	}
	if r.held != nil {
		r.held.side.retry(r.held.id, operation)
	}
}

// place places a reference whose part was past the limits anew, on its path now (a throwing `hostAddress` fails it).
func (r *remoteObject) place() {
	placed, err := r.host.pathOf(r.contract, r.params, r.caller.principal)
	if err != nil {
		r.fail(data.AsChordError(err, r.contract.Name))
		return
	}
	r.path.detach(r)
	r.path = placed
	r.move(refLoading)
	placed.attach(r)
}

// tooLarge: a part of its snapshot or of a state change was past the link's limits (`too_large/part`): the hold is
// released at the provider, and the reference fails broken with err, final: no link holds it again (a reconnect included)
// until `retry`.
func (r *remoteObject) tooLarge(err *data.ChordError) {
	held := r.held
	if held == nil {
		return
	}
	r.held = nil
	held.side.release(held.id, err)
	r.path.detach(r)
	r.path = r.host.unplaced
	r.path.attach(r)
	r.fail(err)
}

// fail fails broken with err: no automatic retry.
func (r *remoteObject) fail(err *data.ChordError) {
	r.failure = err
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
		r.bound = &boundVariant{variant: variant, methods: variantData.Methods, names: names, replicas: replicas}
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
	r.move(refLive)
}

// failed is a `failed` message: the provider's object failed and no attempt runs (RetryIn: the rest of its backoff
// window), or the hold is unbound there or its object parked on a failed dependency (`waiting`), each new cause a new
// message. Ignored once live (only a pending hold is told). It fails on an error that is no wire error (a protocol error).
func (r *remoteObject) failed(message *link.FailedMessage) error {
	if r.refState.Kind == RefStateLive {
		return nil
	}
	// A plugin's error a provider failed with is this object's failure, as a ChordError.
	received, err := link.ErrorFromWire(message.Cause.Error)
	if err != nil {
		return err
	}
	failure := data.AsChordError(received, r.name)
	r.failure = failure
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
	if held != nil {
		held.side.release(held.id, err)
	}
	r.lost(err)
}

// lost: `gone`, or the host closes: the reference ends, its calls reject with err.
func (r *remoteObject) lost(err *data.ChordError) {
	binding := r.binding
	if r.held != nil {
		r.held.side.forget(r.held.id, err)
	}
	r.endWith(err)
	if binding != nil {
		binding.end(err)
	}
}

// endWith ends the object: detached from its path, waits rejected, replicas ended; keeps nothing of its reference.
func (r *remoteObject) endWith(err *data.ChordError) {
	bound := r.bound
	r.refState = &RefState{Kind: RefStateEnded, Error: err}
	r.binding = nil
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

// move moves to next and tells the reference.
func (r *remoteObject) move(next *RefState) {
	r.refState = next
	if r.binding != nil {
		r.binding.moved()
	}
}

// memberInvalid is the `invalid/member` error.
func memberInvalid(message string) *data.ChordError { return data.Invalid(data.ReasonMember, message) }
