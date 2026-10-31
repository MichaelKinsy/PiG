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
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/reference.ts
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
	b.loop.Run(func() { b.retry(newRetryOperation()) })
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
	// state is its state as its references show it.
	state() *RefState
	// hydrated is whether its state members have values: live once, for a remote object bound once.
	hydrated() bool
	// variant is the variant bound last; not bound for a local object and before the first binding.
	variant() (variant string, bound bool)
	// lastFailure is the error of its last failure (an attempt's, or the dependencies it is parked on) while none went
	// live since; nil otherwise.
	lastFailure() *data.ChordError
	// activationId is its instance id while it activates on this host (Q5); empty otherwise and over a link.
	activationId() string
	// heldOnLink is whether it is held on a link now: a remote attempt answers for it there (`parked_long` counts it
	// while it is loading); not while its path has no link, and never for an object here.
	heldOnLink() bool
	// blocksFirstPass is whether it holds up this host's start barrier: an object here without its first outcome
	// (decision (a)); a remote object never does.
	blocksFirstPass() bool
	// hold counts holder as a reference; the binding reads state itself.
	hold(holder holder)
	// unhold: holder was released.
	unhold(holder holder)
	// call calls a method of the live object for from: the admission is the synchronous part of Pi's async call (in a
	// section), the result settles later, in one.
	call(method string, args any, ctx context.Context, from caller) *parkedCallResult
	// member is the state member as Chord follows it: a local object's member source (across its attempts), or the
	// replica of the last binding over a link.
	member(member string) (state.DeriveSource, error)
	// lastValues is whether its members keep their last values while it is failed, for same-host reads and watches (a
	// local object once an attempt went live, slice 6a §1.4); a remote object's failure is its reads' error.
	lastValues() bool
	// keyedMember is the keyed member's ultimate slot (slice 6a §12; only local objects have any).
	keyedMember(member string) (*documents.KeyedSlot, error)
	// pinKey pins key of the keyed member (a reference's `at(key)`, a key watch): its source across the object's attempts
	// once loaded (`keyed-members.ts`); only a live local object takes it. It blocks, outside a section.
	pinKey(member, key string, ctx context.Context) (pinnedKey, error)
	// retry is `ref.retry()` (operation once per retry, deduping diamonds): an attempt now if failed; an object parked
	// on failed dependencies passes it on; ignored otherwise.
	retry(operation *retryOperation)
	// pathTo is the chain of activation waits from this object to goal, both included (Q5); nil when there is none.
	// visited keeps a search over shared dependencies linear in the waits.
	pathTo(goal *chordObject, visited map[target]struct{}) []*chordObject
}

// retryOperation is one `ref.retry()` passing on through parked objects, across links too (the `retry` message's
// `operation` and `through`): its id (a uuidv7) and the instance ids of the objects it passed through, added hop by hop
// (at most `CHAIN`). An object in `through` drops it, so a ring of hosts stops after one lap however many retries run at
// once; an object remembers the last few ids it handled too, so a diamond retries it once.
type retryOperation struct {
	id      string
	through []string
}

// newRetryOperation is a new retry operation, through nothing yet (`ref.retry()`, a `retry` message that names none).
// Pi's `retryOperation()`: the Go type takes that name.
func newRetryOperation() *retryOperation { return &retryOperation{id: uuidv7()} }

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
	// keys are the keys its object's factory pinned with `ref.member.at(key)`: pinned once bound, released with it.
	keys []*KeyedMemberAt

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
) *binding {
	b := &binding{
		loop: host.loop(), scheduler: host.scheduler, from: from, name: contract.Name, contractKind: contract.Kind,
		parkTimeout: parkTimeout, state: bindingState{kind: bindingUnbound, batch: batch, dependent: dependent},
		members: map[string]*ReferenceMember{}, keyed: map[string]*ReferenceKeyed{}, methods: map[string]struct{}{},
	}
	// Every member of every variant (one kind and signature per name, D11); the bound variant decides which work.
	variants, err := contracts.CurrentVariants(contract)
	if err != nil {
		panic(err)
	}
	for _, variant := range variants.All() {
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
		return b.state.target.state()
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

// bind binds to goal, which counts this binding as a reference; the reference takes its state.
func (b *binding) bind(goal target) {
	if b.state.kind != bindingUnbound {
		return
	}
	b.state = bindingState{kind: bindingBound, batch: b.state.batch, dependent: b.state.dependent, target: goal}
	goal.hold(b)
	b.moved()
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
	if b.state.kind == bindingBound && b.state.target.hydrated() {
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

// member is the state member behind `member`: its last value while loading, and while failed when its object keeps one
// (a local object that went live once, slice 6a §1.4); the error is `released`, `not_ready` before the first binding, and
// the failure's error otherwise.
func (b *binding) member(member string) (state.DeriveSource, error) {
	if b.state.kind == bindingBound && b.state.target.lastValues() {
		return b.state.target.member(member)
	}
	goal, err := b.boundTarget()
	if err != nil {
		return nil, err
	}
	return goal.member(member)
}

// keyedSlot is the keyed member's slot (slice 6a §12), as `member` resolves a state member.
func (b *binding) keyedSlot(member string) (*documents.KeyedSlot, error) {
	goal, err := b.boundTarget()
	if err != nil {
		return nil, err
	}
	return goal.keyedMember(member)
}

// pinKey pins key of the keyed member on the bound object (`target.pinKey`), as keyedSlot resolves. It blocks, outside a
// section: the object is looked up in one.
func (b *binding) pinKey(member, key string, ctx context.Context) (pinnedKey, error) {
	var goal target
	var err error
	b.loop.Run(func() { goal, err = b.boundTarget() })
	if err != nil {
		return pinnedKey{}, err
	}
	return goal.pinKey(member, key, ctx)
}

// boundTarget is the bound object unless it failed: `released`, the failure's error, `not_ready` before the binding.
func (b *binding) boundTarget() (target, error) {
	switch b.state.kind {
	case bindingBound:
		if current := b.state.target.state(); current.Kind == RefStateFailed {
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

// watch is a watch of `member` that ends when this reference is released. Before the first binding it waits for it (and
// rejects when the reference fails or is released first); an object that keeps its last values (`lastValues`) gives a
// watch at once, which gets frames again when the object is live again. It blocks, outside a section.
func (b *binding) watch(member string, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	var err error
	var wait bool
	b.loop.Run(func() {
		// Checked here too: a constant's own watch has no batch to check. A released reference fails below.
		if b.state.kind != bindingReleased {
			err = b.state.batch.AssertOutside("watch")
		}
		wait = b.state.kind != bindingBound || !b.state.target.lastValues()
	})
	if err != nil {
		return nil, err
	}
	if wait {
		if err = b.awaitHydrated(ctx); err != nil {
			return nil, err
		}
	}
	var watch *chord.Watch[delta.JsonValue]
	b.loop.Run(func() {
		var source state.DeriveSource
		if source, err = b.member(member); err != nil {
			return
		}
		if watch, err = openWatch(source, ctx); err != nil {
			return
		}
		watch, err = b.follow(watch)
	})
	if err != nil {
		return nil, err
	}
	return watch, nil
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
	b.keys = append(b.keys, at)
	return at, nil
}

// pinsKeys is whether its object's factory pinned keys on it (`at(key)`).
func (b *binding) pinsKeys() bool { return len(b.keys) > 0 }

// pinKeys pins the keys of `at(key)` on the live object's keyed members and loads those not in memory, before the
// dependent goes live (slice 6a §12); nil when there are none. A failed load rejects. The pins run on a task of the loop
// (they block) and settle the promise in a section.
func (b *binding) pinKeys(ctx context.Context) *keyedPromise[struct{}] {
	if len(b.keys) == 0 {
		return nil
	}
	done := newKeyedPromise[struct{}](b.loop)
	keys := slices.Clone(b.keys)
	b.loop.Go(func() {
		var err error
		for _, at := range keys {
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

// retry is `ref.retry()`, as operation (one per `ref.retry()`, passed on through parked objects): the object is asked for
// an attempt now (ignored unless failed; a parked one passes it on); a refused reference is placed again; one waiting in
// the index is left as it is (an event wakes it).
func (b *binding) retry(operation *retryOperation) {
	switch b.state.kind {
	case bindingBound:
		b.state.target.retry(operation)
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
	if previous.kind == bindingWaiting {
		previous.leave()
	}
	for _, at := range b.keys {
		at.release()
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
		b.calls, b.abort = chordctx.WithCancel(context.Background())
	}
	linked := withLinkedAbortSignal(b.calls, ctx)
	out := newParkedCallResult()
	goal.call(method, args, linked.context, b.from).then(b.loop, func(value any, err error) {
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
	document, err := slot.Get(key, ownAccess(slot), ctx)
	if err != nil || document == nil {
		return nil, false, err
	}
	return document, true, nil
}

// Watch is a watch of the document at key across the object's attempts (it holds the key through the object), nil when
// absent; it ends with this reference. It blocks.
func (k *ReferenceKeyed) Watch(ctx context.Context, key string) (chord.WatchHandle[delta.JsonValue], error) {
	b := k.binding
	if err := b.awaitHydrated(ctx); err != nil {
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
			held.release()
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

// pin holds the key through b (once), loaded; nothing once released (a hold that lands then is released). It blocks.
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
