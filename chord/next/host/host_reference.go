package host

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/host/reference.ts
//
// References (§7): what `get` and `hold` return, built from the contract's members, and its binding to the object:
// counted as a reference, it follows the object's state, parks calls while loading, keeps the per-reference watches and
// is released once.
//
// Forced Go differences. Pi's reference is a plain object whose properties are the contract's members, which Go cannot
// build at run time: Reference has Call, Member and Keyed by name (a name the contract has no member of is an error, or
// nil for the accessors), and Variant, State, Retry and Release. Held is the same type (a held reference's variant may
// be unbound; Variant reports it). The classes behind a state or keyed member are ReferenceMember, ReferenceKeyed and
// KeyedMemberAt, exported because the app holds them. A Promise that Pi's methods return is a keyedPromise or a
// parkedCallResult when it settles inside a section (hydrated, live, pinKeys, call); the blocking helpers wait on
// them outside one. Every method of binding runs inside a section of the host's loop; the methods that block are
// documented as such and run their sections themselves.

// Reference is what `host.get` and `host.hold` return: a local contract's members, or for a remote contract the members
// of the variant its object chose, and its state, Retry and Release. Every use after Release returns an error.
type Reference struct {
	binding *binding
}

// Held is what `host.hold` returns: a Reference whose Variant is unbound until its first binding.
type Held = Reference

func newReference(b *binding) *Reference { return &Reference{binding: b} }

// Call calls the method `method` with args (nil for none): it runs while the reference is live, parks while it is
// loading (sent in order once live) and fails while it is failed or ended. It blocks.
func (r *Reference) Call(method string, args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
	b := r.binding
	if _, found := b.methods[method]; !found {
		return nil, fmt.Errorf("%s: %s is not a method", b.name, method)
	}
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call(method, args, ctx) })
	if err != nil {
		return nil, err
	}
	return result.wait(ctx)
}

// Member is the state member `name`, nil when the contract has none.
func (r *Reference) Member(name string) *ReferenceMember { return r.binding.members[name] }

// Keyed is the keyed member `name`, nil when the contract has none.
func (r *Reference) Keyed(name string) *ReferenceKeyed { return r.binding.keyed[name] }

// Events is the event member `name`, nil when the contract has none.
func (r *Reference) Events(name string) *ReferenceEvents { return r.binding.events[name] }

// Variant is the variant its remote object bound last; bound is false before the first binding and for a local contract.
// A released reference returns its end.
func (r *Reference) Variant() (variant string, bound bool, err error) {
	b := r.binding
	if b.contractKind != contracts.KindRemote {
		return "", false, nil
	}
	b.loop.Run(func() { variant, bound, err = b.variantOf() })
	return variant, bound, err
}

// State is the reference's state: loading, live, failed or ended; watches get every change.
func (r *Reference) State() chord.NativeWatchable[*RefState] {
	var inner *referenceState
	r.binding.loop.Run(func() { inner = r.binding.watchableState() })
	return stateWatchable{loop: r.binding.loop, inner: inner}
}

// stateWatchable is `ref.state` for callers outside the host's sections: each use enters one.
type stateWatchable struct {
	loop  sectionLoop
	inner *referenceState
}

func (s stateWatchable) Value() *RefState {
	var value *RefState
	s.loop.Run(func() { value = s.inner.Value() })
	return value
}

func (s stateWatchable) Watch(ctx context.Context) (chord.NativeWatchHandle[*RefState], error) {
	var watch chord.NativeWatchHandle[*RefState]
	var err error
	s.loop.Run(func() { watch, err = s.inner.Watch(ctx) })
	return watch, err
}

// Retry asks a failed object for an attempt now (the backoff skipped, a broken one renewed); an object parked on failed
// dependencies passes it on to them, once; otherwise nothing.
func (r *Reference) Retry() {
	b := r.binding
	b.loop.Run(func() { b.retry(newRetryWalk(link.RETRY_HOPS)) })
}

// Release releases the reference (idempotent); its object may end when it was its last.
func (r *Reference) Release() {
	b := r.binding
	b.loop.Run(b.release)
}

// target is what a binding is bound to: an object on this host (`chordObject`), or an object held over a path, the
// parent link or a route address (`remoteObject`). It tells its holders of each change of `state`.
type target interface {
	// label is the object in errors: its type, and its params key when it has params.
	label() string
	// kind is where it is: `local` for an object on this host, `remote` for one held over a link (another host decides what
	// it serves).
	kind() string
	// state is its state as its references show it.
	state() *RefState
	// stateFor is its state as a reference of version and fingerprint sees it: on this host `live` also while a restart's
	// open old attempt binds that version (decision "Reload restarts every affected object", rule 5); `state` over a link.
	stateFor(version int, fingerprint string) *RefState
	// hydrated is whether its state members have values: live once, for a remote object bound once.
	hydrated() bool
	// variant is the variant bound last; not bound for a local object and before the first binding.
	variant() (variant string, bound bool)
	// lastFailure is the error of its last failure (an attempt's, or the dependencies it is parked on) while none went
	// live since; nil otherwise.
	lastFailure() *data.ChordError
	// activationId is its instance id while it activates on this host (Q5); empty otherwise and over a link.
	activationId() string
	// blocksFirstPass is whether it holds up this host's start barrier: an object here without its first outcome
	// (decision (a)); a remote object never does.
	blocksFirstPass() bool
	// hold counts holder as a reference; the binding reads state itself.
	hold(holder holder)
	// unhold: holder was released.
	unhold(holder holder)
	// call calls a method of the live object for from, bound at version (an older one is mapped, Q10): the admission is
	// the synchronous part of Pi's async call (in a section), the result settles later, in one.
	call(method string, args any, ctx context.Context, parent telemetry.TelemetrySpan, from caller, version int) *parkedCallResult
	// followed is what a reference of version follows for state member now (decision "References are the only
	// indirection"): the live instance's member (a replica over a link), or nothing (`loading`, or `failed` with the
	// cause's error), with the member a reference takes its last value from.
	followed(member string, version int) followed
	// attachable is the instance its references follow now (they attach to it); nil once it stopped changing.
	attachable() attachable
	// instance is its live instance (a reference's keys pin there; a new one, they pin again); nil when not live.
	instance() any
	// keyedMember is the keyed member's ultimate slot (slice 6a §12; only local objects have any).
	keyedMember(member string) (*documents.KeyedSlot, error)
	// keyedMapping is how a consumer of version reads keyed member's documents: mapped back to its version when older
	// (slice 7b §7), nil when as they are; `contract_mismatch` when the provider refuses it.
	keyedMapping(member string, version int) (contracts.Mapping, error)
	// pinKey pins key of the keyed member on the live instance, loaded, as a consumer of version reads it (mapped when
	// older); only a live local object has keyed members. It blocks, outside a section.
	pinKey(member, key string, version int, ctx context.Context) (pinnedKey, error)
	// retry is `ref.retry()`, as one step of walk: an attempt now if failed; an object parked on failed dependencies
	// passes it on; ignored otherwise.
	retry(walk *retryWalk)
	// pathTo is the chain of activation waits from this object to goal, both included (Q5); nil when there is none.
	// visited keeps a search over shared dependencies linear in the waits.
	pathTo(goal *chordObject, visited map[target]struct{}) []*chordObject
}

// attachable is an instance a reference attaches to (`Target.attachable`): it keeps last values in that instance's
// variant ("" is Pi's `undefined`). It is a pointer type, compared by identity.
type attachable interface {
	attachedVariant() string
	// eventNode is event member member's node as a binding of version taps it; nil for a member its variant lacks.
	eventNode(member string, version int) *eventNode
}

// followedKind is what a reference follows of its target for one state member.
type followedKind uint8

const (
	followedLive followedKind = iota
	followedLacking
	followedLoading
	followedFailed
)

// followed is what a reference follows of its target for one state member (`Target.followed`): the member of the
// instance it follows (`live`, `instance` still changing), a member its live variant lacks (`lacking`,
// `invalid/member`), or nothing: its target loads (`loading`, no call) or failed (one `failed` call with the cause's
// error); `last` is the member of `instance`, which stopped changing, that a reference attached to it takes a last value
// from (never one it never attached to).
type followed struct {
	kind     followedKind
	source   state.DeriveSource
	instance any
	err      *data.ChordError
	last     state.DeriveSource
}

// retryWalk is one retry's walk through parked objects on this host (dependency model §3.7, audit A5): the objects it
// reached (one reached again, through a diamond, ignores it) and the hop budget a `retry` message to another host
// carries (none is sent at 0, so a waiting ring of hosts stops).
type retryWalk struct {
	visited map[*chordObject]struct{}
	hops    int
}

// newRetryWalk starts a walk here: `ref.retry()`'s, with the first host's budget (`link.RETRY_HOPS`), or a `retry`
// message's, with one hop less. Pi's `retryWalk(hops)`: the Go type takes that name.
func newRetryWalk(hops int) *retryWalk {
	return &retryWalk{visited: map[*chordObject]struct{}{}, hops: hops}
}

// contractRef is a consumer's contract version and fingerprint (its request's): the provider binds it or it waits unbound.
type contractRef struct {
	version     int
	fingerprint string
}

// checkedParams are a reference's params as L1 checked them, with their identity key.
type checkedParams struct {
	params *delta.JsonObject
	key    string
}

// bindingKind is where a binding stands: `unbound`, waiting for its object's activation to bind it (a `self.get`);
// `bound`; `waiting`, in the host's unbound index, `place` checking it again when woken: before the publication (no
// `failure`: `loading`), or after a check that found no fitting provider (`failed { waiting }`, `not_served` or
// `version`); `refused`, a broken refusal (a `hostAddress` or `authorize` that threw or refused) until `ref.retry()`
// places it again; a released binding keeps nothing but its name and its end (`ref.state`'s `ended`).
type bindingKind uint8

const (
	bindingUnbound bindingKind = iota
	bindingBound
	bindingWaiting
	bindingRefused
	bindingReleased
)

// bindingState is a binding's state; only the fields of its kind are set. `dependent` is the object whose attempt
// declared the reference with `self.get`, nil for `host.get`, `host.hold` and `ctx.get`.
type bindingState struct {
	kind      bindingKind
	batch     *state.Batch
	dependent *chordObject
	target    target
	failure   *failed
	// place checks a waiting binding again when woken; leave takes it out of the index (it ended).
	place func()
	leave func()
	// bind places a refused binding again.
	bind  func()
	ended *ended
}

// decision is what a wait for a state decides at each change.
type decision struct {
	kind decisionKind
	err  error
}

type decisionKind uint8

const (
	decisionWait decisionKind = iota
	decisionDone
	decisionFailed
)

var (
	waitDecision = decision{kind: decisionWait}
	doneDecision = decision{kind: decisionDone}
)

// stateWaiter is a wait for a state: checked at every change, rejected with the reference's end.
type stateWaiter struct {
	check func()
	end   func(err error)
}

// binding is a reference's binding to its object: counted by the object as one of its references while bound, and told
// of each change of its state. Calls run while live, park while loading (sent in order once live) and reject while
// failed. Released, or ended with its object, it ends the watches taken through it and rejects the calls made through it.
// It is a `holder` and an `unboundRequest`.
type binding struct {
	reference *Reference
	loop      sectionLoop
	// scheduler is the host's delivery scheduler, for `ref.state`'s watches (not the host: a released reference keeps none).
	scheduler *chord.DeliveryScheduler
	// from is who calls through this reference: the principal it was got as, and this host's incarnation.
	from caller
	// name is the contract's name, for errors.
	name         string
	contractKind contracts.Kind
	// parkTimeout is how long a call made while loading waits: the reference's `readyTimeout`.
	parkTimeout time.Duration
	state       bindingState
	watches     []*chord.Watch[delta.JsonValue]
	// calls aborts the reference's calls when it ends; made by the first call and dropped at the end.
	calls context.Context
	abort func(cause error)
	// parked are the calls made while loading; made by the first.
	parked *parkedCalls
	// waiters are the waits for a state (getters, member watches before the first binding); each checks the state at
	// every change.
	waiters []*stateWaiter
	// refStateOf is `ref.state`, made on first access.
	refStateOf *referenceState
	// ats are the keys its object's factory pinned with `ref.member.at(key)`: pinned once bound, released with it.
	ats []*KeyedMemberAt
	// consumer is the consumer's contract (its version is the binding's) until the reference is released.
	consumer *contracts.Contract
	// sources are its state members as consumers follow them, by name; each made by its first use.
	sources keyedMap[*referenceSource]
	// heldKeys are the keys of its object's keyed members it holds (key watches, `at(key)` pins); made by the first.
	heldKeys *referenceKeys
	// attached is the instance it attached to last: it takes last values only from that one.
	attached attachable
	// eventsOf are its events, for a contract with event members only; dropped at its end (it keeps no host).
	eventsOf *bindingEvents
	// events are its event members, by name.
	events map[string]*ReferenceEvents
	// unlisted is whether presence lists leave it out: a restart's readiness bindings (`unlist`).
	unlisted bool

	members map[string]*ReferenceMember
	keyed   map[string]*ReferenceKeyed
	methods map[string]struct{}
}

var (
	_ holder         = (*binding)(nil)
	_ unboundRequest = (*binding)(nil)
)

// newBinding makes the binding of a reference of contract, unbound; parkTimeout is the reference's `readyTimeout`.
func newBinding(
	contract *contracts.Contract,
	host *chordHost,
	batch *state.Batch,
	dependent *chordObject,
	from caller,
	parkTimeout time.Duration,
	events *hostEvents,
) *binding {
	b := &binding{
		loop: host.loop(), scheduler: host.scheduler, from: from, name: contract.Name, contractKind: contract.Kind,
		consumer: contract, parkTimeout: parkTimeout, state: bindingState{kind: bindingUnbound, batch: batch, dependent: dependent},
		members: map[string]*ReferenceMember{}, keyed: map[string]*ReferenceKeyed{}, methods: map[string]struct{}{},
	}
	// Every member of every variant (one kind and signature per name, D11); the bound variant decides which work.
	variants, err := contracts.CurrentVariants(contract)
	if err != nil {
		panic(err)
	}
	b.events = map[string]*ReferenceEvents{}
	for _, variant := range variants.All() {
		// Event bookkeeping only for a contract with event members: others pay nothing for events per binding.
		if variant.Events.Len() > 0 {
			if b.eventsOf == nil {
				b.eventsOf = &bindingEvents{host: events}
			}
			for _, member := range variant.Events.Keys() {
				b.events[member] = &ReferenceEvents{binding: b, member: member}
			}
		}
		for _, member := range variant.State.Keys() {
			b.members[member] = &ReferenceMember{binding: b, member: member}
		}
		for _, member := range variant.Keyed.Keys() {
			b.keyed[member] = &ReferenceKeyed{binding: b, member: member}
		}
		for _, member := range variant.Methods.Keys() {
			b.methods[member] = struct{}{}
		}
	}
	b.reference = newReference(b)
	return b
}

// dependent is the object whose attempt declared this reference, until it is released.
func (b *binding) dependent() *chordObject {
	if b.state.kind == bindingReleased {
		return nil
	}
	return b.state.dependent
}

// caller is who calls through the reference (`host.references`).
func (b *binding) caller() caller { return b.from }

// principal is whom it acts for (the unbound index's revocation).
func (b *binding) principal() data.Principal { return b.from.principal }

// refState is the reference's state.
func (b *binding) refState() *RefState {
	switch b.state.kind {
	case bindingBound:
		// A bound reference keeps its contract until it is released.
		return b.state.target.stateFor(b.consumer.Version, b.consumer.Fingerprint)
	case bindingWaiting:
		if b.state.failure != nil {
			return b.state.failure
		}
		return refLoading
	case bindingRefused:
		return b.state.failure
	case bindingUnbound:
		return refLoading
	}
	return b.state.ended
}

// listed is whether presence lists show it (§19).
func (b *binding) listed() bool { return !b.unlisted }

// unlist keeps it out of presence lists, before it binds: a restart's readiness binding is no consumer of its own.
func (b *binding) unlist() { b.unlisted = true }

// hydratedNow is whether its members have values now: bound to an object that shows it live and has them.
func (b *binding) hydratedNow() bool {
	return b.state.kind == bindingBound && b.state.target.hydrated() && b.refState().Kind == RefStateLive
}

// untilState settles once done holds for the reference's state, checked now and at every change; rejects at its end or
// ctx's abort.
func (b *binding) untilState(done func(*RefState) bool, ctx context.Context) *keyedPromise[struct{}] {
	return b.until(func(current *RefState) decision {
		if done(current) {
			return doneDecision
		}
		return waitDecision
	}, ctx)
}

// target is the object it is bound to; nil while it is not bound.
func (b *binding) target() target {
	if b.state.kind == bindingBound {
		return b.state.target
	}
	return nil
}

// label is the object in errors: its label once bound, else the contract's name.
func (b *binding) label() string {
	if b.state.kind == bindingBound {
		return b.state.target.label()
	}
	return b.name
}

// lastFailure is the error of the object's last failure while none went live since, or of the check that left it
// unbound.
func (b *binding) lastFailure() *data.ChordError {
	switch b.state.kind {
	case bindingRefused:
		return b.state.failure.Cause.Error
	case bindingWaiting:
		if b.state.failure != nil {
			return b.state.failure.Cause.Error
		}
	case bindingBound:
		return b.state.target.lastFailure()
	}
	return nil
}

// contract is the consumer's contract version and fingerprint (`holder`).
func (b *binding) contract() contractRef {
	return contractRef{version: b.consumer.Version, fingerprint: b.consumer.Fingerprint}
}

// bind binds to goal, which counts this binding as a reference; its members and keys follow what goal shows now (one
// frame from their last values), and the reference takes its state.
func (b *binding) bind(goal target) {
	if b.state.kind != bindingUnbound {
		return
	}
	batch := b.state.batch
	b.state = bindingState{kind: bindingBound, batch: batch, dependent: b.state.dependent, target: goal}
	goal.hold(b)
	// `hold` may release it at once (a presence derive): `retarget` sees that. A batch only when members move.
	if b.following() {
		_ = batch.Within(func() error { b.retarget(); return nil }, chordctx.Background())
	} else {
		b.retarget()
	}
	b.moved()
}

// following is whether its members or keys were used: `retarget` moves them (in a batch).
func (b *binding) following() bool { return b.sources.len() > 0 || b.heldKeys != nil }

// bindingEvents are a binding's events, for a contract with event members only: the host's events; the instance its
// members tap and the counter value of its binding to it (`at`, taken whenever it attaches to another instance shown
// live, used or not, so a subscription made later still gets what was emitted since; nil while it points at nothing);
// its members' subscriptions and re-exposing nodes (`taps`, made by the first use).
type bindingEvents struct {
	host *hostEvents
	at   *eventsAt
	taps *referenceEventTaps
}

// retargetEvents: its event members follow current from the host's counter value now, when it is another instance than
// they follow: only while it shows its object live (a failed instance's admitted commits still reach its members, never
// its events).
func (b *binding) retargetEvents(events *bindingEvents, current attachable) {
	var instance attachable
	if b.refState().Kind == RefStateLive {
		instance = current
	}
	if events.at == nil {
		if instance == nil {
			return
		}
	} else if events.at.instance == instance {
		return
	}
	if instance == nil {
		events.at = nil
	} else {
		events.at = &eventsAt{instance: instance, boundAt: events.host.counter()}
	}
	if events.taps != nil {
		events.taps.retarget(events.at)
	}
}

// subscribeEvents subscribes listener to event member member at once, bound or not: it gets what this reference receives
// while it is bound to an instance whose variant has the member. Fails `released` once released, and an aborted ctx's
// cause.
func (b *binding) subscribeEvents(member string, listener eventListener, ctx context.Context) (contracts.EventSubscription, error) {
	if b.state.kind == bindingReleased {
		return nil, b.state.ended.Error
	}
	taps := b.eventTaps()
	return subscribe(
		// Its contract has event members (member is one) and it is not released: it has its events.
		b.eventsOf.host,
		listener,
		ctx,
		func(s *subscription) { taps.subscribe(member, s) },
		func(s *subscription) {
			if b.eventsOf != nil && b.eventsOf.taps != nil {
				b.eventsOf.taps.unsubscribe(member, s)
			}
		},
	)
}

// forwardEvents feeds node (an object's member re-exposing event member member) what this reference receives, until
// unforwardEvents; nothing for a released reference.
func (b *binding) forwardEvents(member string, node *eventNode) {
	if b.state.kind != bindingReleased {
		b.eventTaps().forward(member, node)
	}
}

// unforwardEvents stops feeding node.
func (b *binding) unforwardEvents(member string, node *eventNode) {
	if b.eventsOf != nil && b.eventsOf.taps != nil {
		b.eventsOf.taps.unforward(member, node)
	}
}

// eventTaps are its event taps, made by the first use at its version, tapped on the instance it points at.
func (b *binding) eventTaps() *referenceEventTaps {
	events := b.eventsOf
	if events.taps == nil {
		events.taps = newReferenceEventTaps(b.consumer.Version, events.at)
	}
	return events.taps
}

// retarget runs inside a batch when following: its members and keys follow what its object shows now
// (`target.followed`): the live instance's members (moved there, one frame each), or their last values (one `failed`
// call for a failure). The first time it points at nothing, every member of its version gets its last value, read or not.
func (b *binding) retarget() {
	if b.state.kind != bindingBound {
		return
	}
	goal := b.state.target
	version := b.consumer.Version
	current := goal.attachable()
	if b.eventsOf != nil {
		b.retargetEvents(b.eventsOf, current)
	}
	if current != nil {
		b.attached = current
	} else if b.attached != nil {
		// Detaching from an instance it attached to: every member keeps its last value (none before it ever attached).
		b.keepAll(b.attached)
	}
	attached := b.attached
	for _, member := range b.sources.keys() {
		source, _ := b.sources.get(member)
		place(source, goal.followed(member, version), attached)
	}
	// Its last values taken: it keeps no node of that instance (GC).
	if current == nil {
		b.attached = nil
	}
	if b.heldKeys != nil {
		b.heldKeys.follow(chordctx.Background())
	}
}

// removed: its object left it (slice 7b §6): its type was removed, or its provider no longer binds its version. The
// request waits unbound with removal.cause in the host's index, its members and keys keeping their last values (taken
// from the instance it leaves; one `failed` call), and binds again, by name, when a publication serves it: its members
// move to the next object's instance and its keys pin there again (one frame each); parked calls flush or reject;
// presence lists it again.
func (b *binding) removed(r removal) {
	if b.state.kind != bindingBound {
		return
	}
	batch, dependent := b.state.batch, b.state.dependent
	cause := r.cause.Error
	_ = batch.Within(func() error {
		// Its last values from the instance it attached to (one refused at its binding attached to none).
		if b.attached != nil {
			b.keepAll(b.attached)
		}
		for _, source := range b.sources.values() {
			source.fail(cause, chordctx.Background())
		}
		if b.heldKeys != nil {
			b.heldKeys.fail(cause, chordctx.Background())
		}
		return nil
	}, chordctx.Background())
	// Its last values taken: it keeps no node of that instance (GC).
	b.attached = nil
	// Unbound: no events until it binds again (then from that binding's counter value, even to the same instance).
	if events := b.eventsOf; events != nil {
		events.at = nil
		if events.taps != nil {
			events.taps.retarget(nil)
		}
	}
	b.state = bindingState{kind: bindingUnbound, batch: batch, dependent: dependent}
	r.host.rewait(b, b.consumer, r.params, r.cause, r.ctx)
}

// keepAll gives every state member of its version, in the variant of the instance it attached to (its object may be
// live no more), a source, read or not, so each keeps a last value while it points at nothing (no-op for one made).
func (b *binding) keepAll(attached attachable) {
	name := attached.attachedVariant()
	if name == "" {
		name = contracts.LocalVariant
	}
	variants, err := contracts.CurrentVariants(b.consumer)
	if err != nil {
		panic(err)
	}
	variant, found := variants.Get(name)
	if !found {
		return
	}
	for _, member := range variant.State.Keys() {
		if _, err := b.memberSource(member); err != nil {
			panic(err)
		}
	}
}

// wait leaves the reference unbound in the host's unbound index (`leave` takes it out): `loading` before the
// publication (nil cause), else `failed` with cause (`waiting`); `place` checks it again when the index wakes it.
func (b *binding) wait(cause *data.FailureCause, place func(), leave func()) {
	if b.state.kind != bindingUnbound {
		return
	}
	var failure *failed
	if cause != nil {
		failure = &failed{Kind: RefStateFailed, Cause: *cause}
	}
	b.state = bindingState{
		kind: bindingWaiting, batch: b.state.batch, dependent: b.state.dependent, failure: failure, place: place, leave: leave,
	}
	// Unchanged while loading: nothing to tell.
	if failure != nil {
		b.moved()
	}
}

// wake is woken by the unbound index (out of it already): checked again where it goes.
func (b *binding) wake() {
	if b.state.kind != bindingWaiting {
		return
	}
	place := b.state.place
	b.state = bindingState{kind: bindingUnbound, batch: b.state.batch, dependent: b.state.dependent}
	place()
}

// refuse is a reference that cannot be bound (where it goes refused it): failed broken with cause until `ref.retry()`
// runs bind again (a route's `authorize` may admit it later).
func (b *binding) refuse(cause data.FailureCause, bind func()) {
	if b.state.kind != bindingUnbound {
		return
	}
	failure := &failed{Kind: RefStateFailed, Cause: cause}
	b.state = bindingState{
		kind: bindingRefused, batch: b.state.batch, dependent: b.state.dependent, failure: failure, bind: bind,
	}
	b.moved()
}

// moved: the state may have changed: `ref.state`'s watches are told; live sends the parked calls in order, failed
// rejects them with the cause; waits for a state decide; the object whose attempt declared it re-evaluates.
func (b *binding) moved() {
	if b.refStateOf != nil {
		b.refStateOf.moved()
	}
	current := b.refState()
	switch current.Kind {
	case RefStateLive:
		if b.parked != nil {
			b.parked.flush(b.send, func() bool { return b.refState().Kind == RefStateLive })
		}
	case RefStateFailed:
		if b.parked != nil {
			b.parked.reject(current.Cause.Error)
		}
	}
	for _, each := range slices.Clone(b.waiters) {
		each.check()
	}
	if dependent := b.dependent(); dependent != nil {
		dependent.dependencyMoved()
	}
}

// live resolves when the reference is live; rejects with a broken failure's error, or its end. Getters wait for it.
func (b *binding) live(ctx context.Context) *keyedPromise[struct{}] {
	return b.until(func(current *RefState) decision {
		if current.Kind == RefStateLive {
			return doneDecision
		}
		if current.Kind == RefStateFailed && current.Cause.Kind == data.FailureBroken {
			return decision{kind: decisionFailed, err: current.Cause.Error}
		}
		return waitDecision
	}, ctx)
}

// hydrated resolves once the object's members have values; rejects when the reference fails or is released first.
func (b *binding) hydrated(ctx context.Context) *keyedPromise[struct{}] {
	if b.hydratedNow() {
		done := newKeyedPromise[struct{}](b.loop)
		done.resolve(struct{}{})
		return done
	}
	return b.until(func(current *RefState) decision {
		switch current.Kind {
		case RefStateLive:
			return doneDecision
		case RefStateFailed:
			return decision{kind: decisionFailed, err: current.Cause.Error}
		}
		return waitDecision
	}, ctx)
}

// awaitHydrated blocks, outside a section, until the object's members have values.
func (b *binding) awaitHydrated(ctx context.Context) error {
	var wait *keyedPromise[struct{}]
	b.loop.Run(func() { wait = b.hydrated(ctx) })
	_, err := wait.await(ctx)
	return err
}

// member is the state member behind `member`, as consumers follow it (`referenceSource`): the instance's member while its
// object is live, its last value while it points at nothing; reads fail with `released`, the failure's error, or
// `not_ready` when it never had a value. The error is `released` for a released reference.
func (b *binding) member(member string) (state.DeriveSource, error) {
	source, err := b.memberSource(member)
	if err != nil {
		return nil, err
	}
	return source, nil
}

// memberSource is the source of state member, made by its first use, following what its target shows now.
func (b *binding) memberSource(member string) (*referenceSource, error) {
	if source, found := b.sources.get(member); found {
		return source, nil
	}
	// A released reference fails with its end (`boundTarget`): it makes no source.
	if b.state.kind == bindingReleased {
		return nil, b.state.ended.Error
	}
	source := newReferenceSource(b.state.batch, b.scheduler, b)
	b.sources.set(member, source)
	// New, nothing follows it yet: placing it needs no batch.
	if b.state.kind == bindingBound {
		place(source, b.state.target.followed(member, b.consumer.Version), b.attached)
	}
	return source, nil
}

// none is what a read of a member that never had a value fails with: `released`, the failure's error, or `not_ready`.
func (b *binding) none() error {
	if _, err := b.boundTarget(); err != nil {
		return err
	}
	return notReady(b.label() + ": not live yet")
}

// keyedSlot is the keyed member's slot (slice 6a §12), as `member` resolves a state member.
func (b *binding) keyedSlot(member string) (*documents.KeyedSlot, error) {
	goal, err := b.boundTarget()
	if err != nil {
		return nil, err
	}
	return goal.keyedMember(member)
}

// keyedMapping is how this reference reads keyed member's documents (`target.keyedMapping`), as keyedSlot resolves.
func (b *binding) keyedMapping(member string) (contracts.Mapping, error) {
	goal, err := b.boundTarget()
	if err != nil {
		return nil, err
	}
	return goal.keyedMapping(member, b.consumer.Version)
}

// bindingKeys is the view of a binding its held keys use: the batch and the version captured when they were made, so a
// released reference's keys go on and release without its contract.
type bindingKeys struct {
	*binding
	batch   *state.Batch
	version int
}

func (k bindingKeys) keysBatch() *state.Batch                 { return k.batch }
func (k bindingKeys) keysScheduler() *chord.DeliveryScheduler { return k.scheduler }
func (k bindingKeys) keysLoop() sectionLoop                   { return k.loop }
func (k bindingKeys) keysVersion() int                        { return k.version }
func (k bindingKeys) keysTarget() target                      { return k.target() }

// keysInMemory resolves the keyed member to its ultimate slot (a local object's own, or the one it re-exposes).
func (k bindingKeys) keysInMemory(member, key string) bool {
	slot, err := k.keyedSlot(member)
	return err == nil && slot.InMemory(key)
}

// pinKey holds key of keyed member (a key watch, a factory's `at(key)`, an object re-exposing this member): its
// source, pinned on the live instance and following its object's instances (`referenceKeys`); release once. It blocks,
// outside a section.
func (b *binding) pinKey(member, key string, ctx context.Context) (pinnedKey, error) {
	var keys *referenceKeys
	var err error
	b.loop.Run(func() {
		if b.state.kind == bindingReleased {
			err = b.state.ended.Error
			return
		}
		if b.heldKeys == nil {
			b.heldKeys = newReferenceKeys(bindingKeys{binding: b, batch: b.state.batch, version: b.consumer.Version})
		}
		keys = b.heldKeys
	})
	if err != nil {
		return pinnedKey{}, err
	}
	return keys.hold(member, key, ctx)
}

// boundTarget is the bound object unless it failed: `released`, the failure's error, `not_ready` before the binding.
func (b *binding) boundTarget() (target, error) {
	switch b.state.kind {
	case bindingBound:
		if current := b.refState(); current.Kind == RefStateFailed {
			return nil, current.Cause.Error
		}
		return b.state.target, nil
	case bindingUnbound:
		return nil, notReady(b.name + ": not live yet")
	case bindingWaiting:
		if b.state.failure == nil {
			return nil, notReady(b.name + ": not live yet")
		}
		return nil, b.state.failure.Cause.Error
	case bindingRefused:
		return nil, b.state.failure.Cause.Error
	}
	return nil, b.state.ended.Error
}

func notReady(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: message}, nil)
}

// watch is a watch of `member` that ends when this reference is released, through its member (`referenceSource`): opened
// at once once it has a value (its last one while its object is not live, frames again when it is); before its first
// value it waits for its object's (`firstValues`: through failures, until the reference ends or ctx aborts). It blocks,
// outside a section, for a member that never had a value: Pi's `await ref.member.watch(ctx)`, whose watch is opened by the
// microtask that resumes after `firstValues`, in the turn that bound the reference, whenever the caller resumes. The checks
// and a member that has a value are synchronous, inside the caller's section if it is in one (a derive's compute is
// refused by the batch, as a synchronous throw).
func (b *binding) watch(member string, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	var source *referenceSource
	var opened *keyedPromise[*chord.Watch[delta.JsonValue]]
	var watch *chord.Watch[delta.JsonValue]
	var err error
	b.loop.Run(func() {
		if source, err = b.watchSource(member); err != nil {
			return
		}
		if !source.unbound() {
			watch, err = b.openMemberWatch(source, ctx)
			return
		}
		opened = newKeyedPromise[*chord.Watch[delta.JsonValue]](b.loop)
		b.openWhenBound(source, ctx, opened.settle)
	})
	if opened == nil {
		return watch, err
	}
	return opened.await(ctx)
}

// watchThen is `watch(member, ctx).then(then, then)` of Pi's `async watch`, called inside a section: then gets the watch, or
// nil and the error, as a microtask of the loop. A member that never had a value waits for its object's (`await
// firstValues`), and the watch is opened by the microtask that resumes after it: in the turn that bound the reference,
// before any section a link's frame starts, so a frame that arrives later reaches the watch (a watch's value is the
// member's when it was opened, and a listener gets only the frames after it). A caller that resumes from a goroutine of
// its own opens it after whatever section came between, and that section's change is folded into the value it never reads.
func (b *binding) watchThen(member string, ctx context.Context, then func(*chord.Watch[delta.JsonValue], error)) {
	settled := func(watch *chord.Watch[delta.JsonValue], err error) { b.loop.Queue(func() { then(watch, err) }) }
	source, err := b.watchSource(member)
	if err != nil {
		settled(nil, err)
		return
	}
	if !source.unbound() {
		settled(b.openMemberWatch(source, ctx))
		return
	}
	b.openWhenBound(source, ctx, settled)
}

// watchSource is the synchronous start of Pi's `async watch`: the checks, and the member's source.
func (b *binding) watchSource(member string) (*referenceSource, error) {
	// Checked here too: a constant's own watch has no batch to check. A released reference fails below.
	if b.state.kind != bindingReleased {
		if err := b.state.batch.AssertOutside("watch"); err != nil {
			return nil, err
		}
	}
	return b.memberSource(member)
}

// openMemberWatch is `this.follow(source.openWatch(context))`.
func (b *binding) openMemberWatch(source *referenceSource, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	watch, err := source.OpenWatch(ctx)
	if err != nil {
		return nil, err
	}
	return b.follow(watch)
}

// openWhenBound is `if (source.unbound) await this.firstValues(context)` and the open after it, for a member that never had
// a value: it waits for its object's, as before the first binding. The microtask that resumes after the wait opens the
// watch and calls then with it (or with the wait's error), still in that turn.
func (b *binding) openWhenBound(source *referenceSource, ctx context.Context, then func(*chord.Watch[delta.JsonValue], error)) {
	b.firstValues(ctx).then(func(_ struct{}, err error) {
		if err != nil {
			then(nil, err)
			return
		}
		then(b.openMemberWatch(source, ctx))
	})
}

// firstValues resolves once its object's members have values (its first binding, for a watch on a reference that never
// had any); rejects only when the reference ends (released, revoked, its host closed) or ctx aborts: a failure is
// waited through (Opus G3).
func (b *binding) firstValues(ctx context.Context) *keyedPromise[struct{}] {
	if b.hydratedNow() {
		done := newKeyedPromise[struct{}](b.loop)
		done.resolve(struct{}{})
		return done
	}
	return b.until(func(current *RefState) decision {
		if current.Kind == RefStateLive {
			return doneDecision
		}
		return waitDecision
	}, ctx)
}

// awaitFirstValues blocks, outside a section, until `firstValues` resolved.
func (b *binding) awaitFirstValues(ctx context.Context) error {
	var wait *keyedPromise[struct{}]
	b.loop.Run(func() { wait = b.firstValues(ctx) })
	_, err := wait.await(ctx)
	return err
}

// watchOpener is a JSON watchable that opens a watch Chord can end (a source, a constant, a member source).
type watchOpener interface {
	OpenWatch(ctx context.Context) (*chord.Watch[delta.JsonValue], error)
}

// openWatch is `source.openWatch(context)`.
func openWatch(source state.DeriveSource, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	opener, ok := source.(watchOpener)
	if !ok {
		return nil, fmt.Errorf("%T: not a watchable that opens watches", source)
	}
	return opener.OpenWatch(ctx)
}

// follow ends watch when this reference is released; returns it. A watch opened while the reference was released (an
// async keyed watch racing `release`) is ended at once, its pin with it, and the caller gets `released`.
func (b *binding) follow(watch *chord.Watch[delta.JsonValue]) (*chord.Watch[delta.JsonValue], error) {
	if b.state.kind == bindingReleased {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
		return nil, b.state.ended.Error
	}
	b.watches = append(b.watches, watch)
	b.loop.Go(func() {
		<-watch.Closed()
		b.loop.Run(func() {
			if at := slices.Index(b.watches, watch); at >= 0 {
				b.watches = slices.Delete(b.watches, at, at+1)
			}
		})
	})
	return watch, nil
}

// at is `ref.member.at(key)`: only while its object's factory runs (the reference not yet bound); pinned once bound
// (`pinKeys`), released with the reference.
func (b *binding) at(member, key string) (*KeyedMemberAt, error) {
	if b.state.kind != bindingUnbound || b.state.dependent == nil {
		return nil, fmt.Errorf("%s: %s.at(key) is valid only in the factory", b.name, member)
	}
	at := &KeyedMemberAt{binding: b, member: member, key: key}
	b.ats = append(b.ats, at)
	return at, nil
}

// pinsKeys is whether its object's factory pinned keys on it (`at(key)`).
func (b *binding) pinsKeys() bool { return len(b.ats) > 0 }

// pinKeys pins the keys of `at(key)` on the live object's keyed members and loads those not in memory, before the
// dependent goes live (slice 6a §12); nil when there are none. A failed load rejects. The pins run on a task of the loop
// (they block) and settle the promise in a section.
func (b *binding) pinKeys(ctx context.Context) *keyedPromise[struct{}] {
	if len(b.ats) == 0 {
		return nil
	}
	done := newKeyedPromise[struct{}](b.loop)
	ats := slices.Clone(b.ats)
	b.loop.Go(func() {
		var err error
		for _, at := range ats {
			if err = at.pin(b, ctx); err != nil {
				break
			}
		}
		b.loop.Run(func() {
			if err != nil {
				done.reject(err)
			} else {
				done.resolve(struct{}{})
			}
		})
	})
	return done
}

// retry is `ref.retry()`, as a step of walk (a new one for `ref.retry()`, passed on through parked objects): the object
// is asked for an attempt now (ignored unless failed; a parked one passes it on); a refused reference is placed again;
// one waiting in the index is left as it is (an event wakes it).
func (b *binding) retry(walk *retryWalk) {
	switch b.state.kind {
	case bindingBound:
		b.state.target.retry(walk)
	case bindingRefused:
		bind := b.state.bind
		b.state = bindingState{kind: bindingUnbound, batch: b.state.batch, dependent: b.state.dependent}
		b.moved()
		bind()
	}
}

// release releases the reference (idempotent); the object may end when it was its last.
func (b *binding) release() {
	if b.state.kind == bindingReleased {
		return
	}
	if err := b.state.batch.AssertOutside("release"); err != nil {
		panic(err)
	}
	// First: an object over a link rejects the calls itself and sends no cancel for them.
	if b.state.kind == bindingBound {
		b.state.target.unhold(b)
	}
	b.end(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: b.name + ": released"}, nil))
}

// end ends the reference without releasing it from its object (it ended): watches end, calls (running and parked) reject
// with err, `ref.state` is `ended` with it; the object whose attempt declared it checks again.
func (b *binding) end(err *data.ChordError) {
	finished := &ended{Kind: RefStateEnded, Error: err}
	previous := b.state
	b.state = bindingState{kind: bindingReleased, ended: finished}
	b.consumer = nil
	if previous.kind == bindingWaiting {
		previous.leave()
	}
	for _, at := range b.ats {
		at.release()
	}
	// Its members' and keys' watches end, derives over them are marked; their instance subscriptions go.
	sources := b.sources.values()
	keys := b.heldKeys
	b.sources = keyedMap[*referenceSource]{}
	b.heldKeys = nil
	b.attached = nil
	// Its subscriptions end and its event members untap (taken out first: a stopping subscription finds none).
	var eventTaps *referenceEventTaps
	if b.eventsOf != nil {
		eventTaps = b.eventsOf.taps
	}
	b.eventsOf = nil
	if eventTaps != nil {
		eventTaps.end()
	}
	if previous.kind != bindingReleased && (len(sources) > 0 || keys != nil) {
		end := func() {
			for _, source := range sources {
				source.end(err)
			}
			if keys != nil {
				keys.end(err)
			}
		}
		// A batch only when something still follows them (an ending object's derives detached already).
		used := keys != nil
		for _, source := range sources {
			used = used || source.inUse()
		}
		if used {
			_ = previous.batch.Within(func() error { end(); return nil }, chordctx.Background())
		} else {
			end()
		}
	}
	if b.abort != nil {
		b.abort(err)
	}
	b.calls, b.abort = nil, nil
	if b.parked != nil {
		b.parked.reject(err)
	}
	b.parked = nil
	for _, watch := range slices.Clone(b.watches) {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
	// A retained released reference keeps none of what its watches followed.
	b.watches = nil
	if b.refStateOf != nil {
		b.refStateOf.released(finished)
	}
	// Each removes itself; promise reactions run later.
	for _, each := range slices.Clone(b.waiters) {
		each.end(err)
	}
	// An ended dependency breaks a parked dependent (it cannot come back).
	if previous.kind != bindingReleased && previous.dependent != nil {
		previous.dependent.dependencyMoved()
	}
}

// call is the call a reference's method makes: the result settles in a section.
func (b *binding) call(method string, args any, ctx context.Context) (*parkedCallResult, error) {
	current := b.refState()
	switch current.Kind {
	case RefStateLive:
		// Calls queued before it went live are still being sent: a new one never overtakes them.
		if b.parked != nil && b.parked.waiting() {
			return b.parked.park(method, args, ctx)
		}
		return b.send(method, args, ctx), nil
	case RefStateLoading:
		if b.parked == nil {
			b.parked = newParkedCalls(b.loop, b.parkTimeout, b.label())
		}
		return b.parked.park(method, args, ctx)
	case RefStateFailed:
		return nil, current.Cause.Error
	}
	return nil, current.Error
}

// send calls the live object; the call is aborted when the reference ends.
func (b *binding) send(method string, args any, ctx context.Context) *parkedCallResult {
	// Only a bound reference is live.
	goal := b.state.target
	if b.calls == nil {
		b.calls, b.abort = chordctx.WithCancel(chordctx.Background())
	}
	linked := withLinkedAbortSignal(b.calls, ctx)
	out := newParkedCallResult()
	goal.call(method, args, linked.context, chordctx.Telemetry(ctx), b.from, b.consumer.Version).then(b.loop, func(value any, err error) {
		linked.release()
		out.settle(value, err)
	})
	return out
}

// until settles once decide does for the reference's state, checked now and at every change; rejects when the reference
// ends (with its error, `released` when it is released already) or ctx aborts.
func (b *binding) until(decide func(*RefState) decision, ctx context.Context) *keyedPromise[struct{}] {
	promise := newKeyedPromise[struct{}](b.loop)
	if b.state.kind == bindingReleased {
		promise.reject(b.state.ended.Error)
		return promise
	}
	var each *stateWaiter
	var stop func() bool
	settle := func(err error) {
		if at := slices.Index(b.waiters, each); at >= 0 {
			b.waiters = slices.Delete(b.waiters, at, at+1)
		}
		if stop != nil {
			stop()
		}
		if err != nil {
			promise.reject(err)
		} else {
			promise.resolve(struct{}{})
		}
	}
	each = &stateWaiter{
		check: func() {
			switch decided := decide(b.refState()); decided.kind {
			case decisionDone:
				settle(nil)
			case decisionFailed:
				settle(decided.err)
			}
		},
		end: settle,
	}
	b.waiters = append(b.waiters, each)
	if ctx.Done() != nil {
		if ctx.Err() != nil {
			settle(context.Cause(ctx))
			return promise
		}
		stop = context.AfterFunc(ctx, func() { b.loop.Run(func() { settle(context.Cause(ctx)) }) })
	}
	each.check()
	return promise
}

// watchableState is `ref.state`, made on first access.
func (b *binding) watchableState() *referenceState {
	if b.refStateOf != nil {
		return b.refStateOf
	}
	// A released reference's state never changes: it is made ended, keeping no batch.
	if b.state.kind == bindingReleased {
		made := newReferenceState(b.refState, nil, nil)
		made.released(b.state.ended)
		b.refStateOf = made
		return made
	}
	b.refStateOf = newReferenceState(b.refState, b.state.batch, b.scheduler)
	return b.refStateOf
}

// variantOf is the variant bound last (`bound` false before the first binding); a released reference returns its end.
func (b *binding) variantOf() (variant string, bound bool, err error) {
	if b.state.kind == bindingReleased {
		return "", false, b.state.ended.Error
	}
	if b.state.kind != bindingBound {
		return "", false, nil
	}
	variant, bound = b.state.target.variant()
	return variant, bound, nil
}

// ReferenceMember is a state member of a reference: the provider's member, followed through this reference.
type ReferenceMember struct {
	binding *binding
	member  string
}

// Value is the member's current value; the error is what the getter throws (`not_ready` before the first binding, the
// failure's error, `released`).
func (m *ReferenceMember) Value() (delta.JsonValue, error) {
	var value delta.JsonValue
	var err error
	m.binding.loop.Run(func() {
		var source state.DeriveSource
		if source, err = m.source(); err == nil {
			value, err = source.Value()
		}
	})
	return value, err
}

// Watch is a watch of the member that ends when the reference is released. It blocks.
func (m *ReferenceMember) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	watch, err := m.binding.watch(m.member, ctx)
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// WatchThen is `member.watch(ctx).then(then, then)`, not awaited (Pi's followers of a held reference: `choice.pick.watch(ctx).then(start,
// () => {})`): it returns at once, and then gets the watch, or nil and the error (the reference was released, or ctx
// aborted, before its first value), as a microtask of the host's loop. For a reference not bound yet that is the turn that
// binds it, before any section a link's frame starts, so every change of the member after that bind reaches the watch's
// listener: a goroutine that calls Watch and starts the result opens it after whatever section came first, whose change the
// watch then holds as its value, which a follower that only listens never reads. then runs inside a section of the loop: it
// must not block, and nothing it calls may wait for the loop or for a watch to end. In particular it does not call the
// watch's Stop: Stop waits for the watch to close, and a watch whose ctx was cancelled closes only after its abort entered the
// loop, which the callback holds. A callback that must end the watch cancels its ctx or releases the reference instead.
func (m *ReferenceMember) WatchThen(ctx context.Context, then func(watch chord.WatchHandle[delta.JsonValue], err error)) {
	m.binding.loop.Run(func() {
		m.binding.watchThen(m.member, ctx, func(watch *chord.Watch[delta.JsonValue], err error) {
			if err != nil {
				then(nil, err)
				return
			}
			then(watch, nil)
		})
	})
}

// source is the provider's member: a source or a constant.
func (m *ReferenceMember) source() (state.DeriveSource, error) { return m.binding.member(m.member) }

// ReferenceKeyed is a keyed member of a reference (slice 6a §12): reads by key behave as calls (they wait while loading).
type ReferenceKeyed struct {
	binding *binding
	member  string
}

// slot is the provider's slot: the ultimate provider's, through re-exposures (rule 22).
func (k *ReferenceKeyed) slot() (*documents.KeyedSlot, error) {
	var slot *documents.KeyedSlot
	var err error
	k.binding.loop.Run(func() { slot, err = k.binding.keyedSlot(k.member) })
	return slot, err
}

// keyedBinding is this member as another object re-exposing it has it: its keys held through this reference.
func (k *ReferenceKeyed) keyedBinding() *keyedBinding {
	return &keyedBinding{kind: keyedReexposed, binding: k.binding, member: k.member}
}

// Get is the document at key, found false when absent. It blocks.
func (k *ReferenceKeyed) Get(ctx context.Context, key string) (value delta.JsonValue, found bool, err error) {
	if err = k.binding.awaitHydrated(ctx); err != nil {
		return nil, false, err
	}
	slot, err := k.slot()
	if err != nil {
		return nil, false, err
	}
	mapping, err := k.binding.keyedMapping(k.member)
	if err != nil {
		return nil, false, err
	}
	document, err := slot.Get(key, ownAccess(slot), ctx)
	if err != nil || document == nil {
		return nil, false, err
	}
	if mapping != nil {
		mapped, err := mapping(document)
		if err != nil {
			return nil, false, err
		}
		return mapped, true, nil
	}
	return document, true, nil
}

// Watch is a watch of the document at key across the object's attempts (it holds the key through the object), nil when
// absent; it ends with this reference. It blocks.
func (k *ReferenceKeyed) Watch(ctx context.Context, key string) (chord.WatchHandle[delta.JsonValue], error) {
	b := k.binding
	if err := b.awaitFirstValues(ctx); err != nil {
		return nil, err
	}
	slot, err := k.slot()
	if err != nil {
		return nil, err
	}
	document, err := slot.Get(key, ownAccess(slot), ctx)
	if err != nil || document == nil {
		return nil, err
	}
	held, err := b.pinKey(k.member, key, ctx)
	if err != nil {
		return nil, err
	}
	var watch *chord.Watch[delta.JsonValue]
	b.loop.Run(func() {
		stored, keyErr := slot.Key(key)
		if keyErr == nil {
			var tracker *delta.Tracker
			tracker, keyErr = slot.Current(stored)
			// Retired between the read and now: absent.
			if keyErr == nil && tracker == nil {
				held.release()
				return
			}
		}
		if keyErr != nil {
			held.release()
			err = keyErr
			return
		}
		opened, openErr := openWatch(held.source, ctx)
		if openErr != nil {
			held.release()
			err = openErr
			return
		}
		b.loop.Go(func() {
			<-opened.Closed()
			b.loop.Run(held.release)
		})
		watch, err = b.follow(opened)
	})
	if err != nil || watch == nil {
		return nil, err
	}
	return watch, nil
}

// At is `ref.member.at(key)`, in a factory only: the pinned key's values are the member's documents.
func (k *ReferenceKeyed) At(key string) (*KeyedMemberAt, error) {
	var at *KeyedMemberAt
	var err error
	k.binding.loop.Run(func() { at, err = k.binding.at(k.member, key) })
	return at, err
}

// ownAccess is how a consumer reads its provider's keyed documents as the provider's own code may: current version, own
// token.
func ownAccess(slot *documents.KeyedSlot) documents.Access {
	return documents.Access{Token: slot.Owner().Own(), Version: slot.Definition().Current().Version}
}

// atState is where a `ref.member.at(key)` stands.
type atState uint8

const (
	atUnpinned atState = iota
	atPinned
	atReleased
)

// KeyedMemberAt is `ref.member.at(key)`: the key's source across the object's attempts once the reference is bound and
// the key held (`pin`), until the reference ends (`release`, exactly once): then it drops the source (a retained
// reference keeps no provider).
type KeyedMemberAt struct {
	binding *binding
	member  string
	key     string
	state   atState
	held    pinnedKey
}

// sourceOf is the key's source; `not_ready` before it is held, `released` after.
func (a *KeyedMemberAt) source() (state.DeriveSource, error) {
	if a.state == atPinned {
		return a.held.source, nil
	}
	what := fmt.Sprintf("%s.at(%s)", a.member, a.key)
	if a.state == atReleased {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: what + ": released"}, nil)
	}
	return nil, notReady(what + ": not loaded yet")
}

// Value is the key's current value.
func (a *KeyedMemberAt) Value() (delta.JsonValue, error) {
	var value delta.JsonValue
	var err error
	a.binding.loop.Run(func() {
		var source state.DeriveSource
		if source, err = a.source(); err == nil {
			value, err = source.Value()
		}
	})
	return value, err
}

// Watch is a watch of the key's document; it ends with the reference.
func (a *KeyedMemberAt) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	var watch *chord.Watch[delta.JsonValue]
	var err error
	a.binding.loop.Run(func() {
		var source state.DeriveSource
		if source, err = a.source(); err != nil {
			return
		}
		if watch, err = openWatch(source, ctx); err != nil {
			return
		}
		watch, err = a.binding.follow(watch)
	})
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// pin holds the key through b, loaded; it holds nothing after release (a hold that arrives after release is released). It blocks.
func (a *KeyedMemberAt) pin(b *binding, ctx context.Context) error {
	var unpinned bool
	b.loop.Run(func() { unpinned = a.state == atUnpinned })
	if !unpinned {
		return nil
	}
	held, err := b.pinKey(a.member, a.key, ctx)
	if err != nil {
		return err
	}
	b.loop.Run(func() {
		if a.state != atUnpinned {
			held.release()
			return
		}
		a.state, a.held = atPinned, held
	})
	return nil
}

// release releases the pin and drops the source (once).
func (a *KeyedMemberAt) release() {
	previous, held := a.state, a.held
	a.state, a.held = atReleased, pinnedKey{}
	if previous == atPinned {
		held.release()
	}
}

// resolveKeyed is what a keyed member's value is (slice 6a §12): a keyed document handle's slot, or a reference's keyed
// member (re-exposed); nil for anything else.
func resolveKeyed(value any) (*keyedBinding, error) {
	switch handle := value.(type) {
	case *documents.Keyed:
		return &keyedBinding{kind: keyedOwn, slot: handle.Slot}, nil
	case *ReferenceKeyed:
		return handle.keyedBinding(), nil
	}
	return nil, nil
}

// resolveWatchable is the watchable Chord follows behind a state member or a derive source (Q14): a source or a constant
// itself, a singleton handle's slot, a reference member's provider member, a keyed member's pinned key (a key source
// itself for `handle.at(key)`); nil for anything else, a native watchable included. The error is what the getter of a
// reference member throws.
func resolveWatchable(value any) (state.DeriveSource, error) {
	switch handle := value.(type) {
	case *documents.Singleton:
		return handle.Slot, nil
	case *ReferenceMember:
		return handle.source()
	case *KeyedMemberAt:
		return handle.source()
	case state.SourceHandle:
		return handle, nil
	case *state.Constant:
		return handle, nil
	}
	return nil, nil
}

// place points source at what its reference follows now (`followed`), inside a batch; a new one takes its last value
// from the instance that stopped, only when its reference was attached to that one (attached).
func place(source *referenceSource, found followed, attached attachable) {
	takesLast := func() bool {
		return source.unbound() && found.last != nil && attached != nil && found.instance == any(attached)
	}
	switch found.kind {
	case followedLive:
		source.bind(found.source)
	case followedLacking:
		source.vanish(found.err, chordctx.Background())
	case followedLoading:
		if takesLast() {
			source.bind(found.last)
		}
		source.hold()
	case followedFailed:
		if takesLast() {
			source.bind(found.last)
		}
		source.fail(found.err, chordctx.Background())
	}
}
