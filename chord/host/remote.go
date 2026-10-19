package host

// Ports packages/chord/src/remote.ts: references on objects of other hosts. One RemoteBinding per host and key is shared
// by every reference (RemoteRef) to the object: one hold over the link, one stream per state member, calls over the same
// hold.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// StateSink is a state frame as a binding receives it (its hold and member select the binding and the stream).
type StateSink = StateFrame

// RemoteChannel is what a binding needs from the link it is bound to (implemented by *Connection). Its methods run on
// the loop; call's promise settles with the result.
type RemoteChannel interface {
	// hold sends a hold for binding; again: the binding already has values (a re-hold), its unit is not urgent.
	hold(binding *RemoteBinding, again bool) int
	release(id int)
	call(hold int, member string, args any, hasArgs bool, root *RootRef, signal context.Context) *Promise[any]
	// wantState starts (sub) or stops (unsub) the state of a hold.
	wantState(hold int, wanted bool)
}

// bindingStatus is a binding's status: "ended" is gone with an error, or closed (no views, no linger).
type bindingStatus uint8

const (
	bindingLoading bindingStatus = iota
	bindingLive
	bindingEnded
)

// memberState is a member's value with the decoder of its current stream.
type memberState struct {
	value   any
	decoder *delta.Decoder
}

type hydrationWaiter struct {
	resolve func(struct{})
	reject  func(error)
}

// bindingHold is the current hold of a binding over a link.
type bindingHold struct {
	channel RemoteChannel
	id      int
}

// pendingShape is a shape waiting for its snapshots.
type pendingShape struct {
	shape   Shape
	missing *orderedMap[string, struct{}]
	values  *orderedMap[string, *memberState]
}

// RemoteBinding is one hold per host and key (type, normalized input, principal key, contract fingerprint), shared by
// every reference to it: one hold over the link, one stream per state member, calls over the same hold. References are
// views onto it.
//
// A hold implies the state of its variant (unless no view wants state): the provider answers with a unit, the shape
// followed by a snapshot of each state member the binding lacks. A shape is applied only once all of its snapshots are
// here, so a live binding always has every state member of its variant. After the first hydration the values stay
// readable while the binding re-holds (link loss): status loading, last values kept.
type RemoteBinding struct {
	host     *HostImpl
	key      string
	contract *Contract
	at       string
	input    any
	// principal is the forwarded principal (absent for the host's own principal).
	principal    Principal
	hasPrincipal bool
	status       bindingStatus
	err          error
	// shape is the applied shape (kept while re-holding).
	shape *Shape
	// hydrated: every state member of the applied shape has a value (stays true while re-holding).
	hydrated bool
	// hold is the current hold; nil while resolving the route or after a link loss.
	hold *bindingHold
	// stateOn is what the provider was told: state for this hold (state: false / sub / unsub).
	stateOn bool
	views   *orderedMap[*RemoteRef, struct{}]
	members *orderedMap[string, *memberState]
	// lingerStop cancels the linger timer of a binding without views (set by the host).
	lingerStop func()
	listeners  map[string]*orderedMap[*viewReadable, struct{}]
	pending    *pendingShape
	// live is resolved while a shape is applied for the current hold (calls wait for it).
	live livePromise
	// applied: a shape was applied once (a re-hold says again).
	applied bool
	// hydrationWaiters wait for the first hydration (whenHydrated).
	hydrationWaiters []hydrationWaiter
}

func newRemoteBinding(host *HostImpl, key string, contract *Contract, at string, input any, principal Principal, hasPrincipal bool) *RemoteBinding {
	binding := &RemoteBinding{
		host: host, key: key, contract: contract, at: at, input: input, principal: principal, hasPrincipal: hasPrincipal,
		views:     newOrderedMap[*RemoteRef, struct{}](),
		members:   newOrderedMap[string, *memberState](),
		listeners: map[string]*orderedMap[*viewReadable, struct{}]{},
	}
	binding.live = newLivePromise(&host.loop)
	return binding
}

func newLivePromise(loop *loop) livePromise {
	promise, resolve, reject := newPromise[struct{}](loop)
	return livePromise{promise: promise, resolve: resolve, reject: reject}
}

type livePromise struct {
	promise *Promise[struct{}]
	resolve func(struct{})
	reject  func(error)
}

func (binding *RemoteBinding) ended() bool { return binding.status == bindingEnded }

// stateViews is the number of views that want state.
func (binding *RemoteBinding) stateViews() int {
	count := 0
	for _, view := range binding.views.Keys() {
		if view.wantsState {
			count++
		}
	}
	return count
}

// wantsState: whether the next hold should carry state: some view wants it, or nobody is left (lingering keeps it).
func (binding *RemoteBinding) wantsState() bool {
	if binding.views.Len() == 0 {
		return binding.stateOn
	}
	return binding.stateViews() > 0
}

// ─── views ───

func (binding *RemoteBinding) addView(view *RemoteRef) {
	binding.views.Set(view, struct{}{})
	if binding.lingerStop != nil {
		binding.lingerStop()
		binding.lingerStop = nil
	}
	binding.syncState()
	view.update()
}

func (binding *RemoteBinding) removeView(view *RemoteRef) {
	if !binding.views.Delete(view) {
		return
	}
	for _, set := range binding.listeners {
		for _, readable := range set.Keys() {
			if readable.view == view {
				set.Delete(readable)
			}
		}
	}
	if binding.views.Len() == 0 {
		binding.host.bindingIdle(binding)
		return
	}
	binding.syncState()
}

// syncState tells the provider to start or stop state when the views' wish changed.
func (binding *RemoteBinding) syncState() {
	if binding.ended() || binding.views.Len() == 0 {
		return
	}
	wanted := binding.stateViews() > 0
	if wanted == binding.stateOn {
		return
	}
	binding.stateOn = wanted
	if !wanted {
		binding.hydrated = false
		binding.members.Clear()
		if binding.pending != nil {
			binding.pending.missing.Clear()
		}
	}
	if binding.hold != nil {
		binding.hold.channel.wantState(binding.hold.id, wanted)
	}
	for _, view := range binding.views.Keys() {
		view.update()
	}
}

func (binding *RemoteBinding) listen(name string, readable *viewReadable) {
	set, ok := binding.listeners[name]
	if !ok {
		set = newOrderedMap[*viewReadable, struct{}]()
		binding.listeners[name] = set
	}
	set.Set(readable, struct{}{})
}

func (binding *RemoteBinding) unlisten(name string, readable *viewReadable) {
	if set, ok := binding.listeners[name]; ok {
		set.Delete(readable)
	}
}

// inVariant: whether name is a state member of the applied variant (as the consumer's contract describes it).
func (binding *RemoteBinding) inVariant(name string) bool {
	if binding.shape == nil || binding.shape.Variant == nil {
		return false
	}
	member, ok := binding.contract.Description.member(*binding.shape.Variant, name)
	return ok && member.Kind == MemberState
}

// read is the member's value as views read it: not_ready before the first hydration, not_in_variant outside the variant.
func (binding *RemoteBinding) read(name string) (any, error) {
	if binding.ended() {
		return nil, binding.terminalError()
	}
	if !binding.hydrated {
		return nil, NewChordError(CodeNotReady, binding.key+"."+name+" is not hydrated yet", ChordErrorOptions{})
	}
	if !binding.inVariant(name) {
		variant := "undefined"
		if binding.shape != nil && binding.shape.Variant != nil {
			variant = *binding.shape.Variant
		}
		return nil, NewChordError(CodeNotInVariant, fmt.Sprintf("%s: %s is not a state member of variant %s", binding.key, name, variant), ChordErrorOptions{})
	}
	if state, ok := binding.members.Get(name); ok {
		return state.value, nil
	}
	return nil, nil
}

// whenHydrated resolves once the binding has its state (it rejects if it ends first).
func (binding *RemoteBinding) whenHydrated() *Promise[struct{}] {
	loop := &binding.host.loop
	if binding.hydrated {
		return resolvedPromise(loop, struct{}{})
	}
	if binding.ended() {
		return rejectedPromise[struct{}](loop, binding.terminalError())
	}
	promise, resolve, reject := newPromise[struct{}](loop)
	binding.hydrationWaiters = append(binding.hydrationWaiters, hydrationWaiter{resolve: resolve, reject: reject})
	return promise
}

func (binding *RemoteBinding) settleHydration(err error) {
	waiters := binding.hydrationWaiters
	binding.hydrationWaiters = nil
	for _, waiter := range waiters {
		if err == nil {
			waiter.resolve(struct{}{})
		} else {
			waiter.reject(err)
		}
	}
}

func (binding *RemoteBinding) readable(name string) bool {
	return !binding.ended() && binding.hydrated && binding.inVariant(name)
}

func (binding *RemoteBinding) terminalError() error {
	if binding.err != nil {
		return binding.err
	}
	return NewChordError(CodeReleased, "reference "+binding.key+" released", ChordErrorOptions{})
}

// ─── driven by the host and the link (on the loop) ───

func (binding *RemoteBinding) bound(channel RemoteChannel, id int) {
	binding.hold = &bindingHold{channel: channel, id: id}
	binding.stateOn = binding.wantsState()
}

// stateMembers are the state members of shape the consumer's contract knows.
func (binding *RemoteBinding) stateMembers(shape Shape) []string {
	if shape.Variant == nil {
		return nil
	}
	members, ok := binding.contract.Description.Variants.Get(*shape.Variant)
	if !ok {
		return nil
	}
	var out []string
	for _, name := range shape.Members {
		if member, ok := members.Get(name); ok && member.Kind == MemberState {
			out = append(out, name)
		}
	}
	return out
}

// shaped: a unit begins: listed are the members whose snapshots follow.
func (binding *RemoteBinding) shaped(shape Shape, listed []string) {
	if binding.ended() {
		return
	}
	previous := binding.pending
	missing := newOrderedMap[string, struct{}]()
	for _, name := range listed {
		missing.Set(name, struct{}{})
	}
	// A newer shape before the older one's snapshots (relays may reorder them): wait for both.
	if previous != nil {
		for _, name := range previous.missing.Keys() {
			missing.Set(name, struct{}{})
		}
	}
	wanted := map[string]bool{}
	for _, name := range binding.stateMembers(shape) {
		wanted[name] = true
	}
	for _, name := range missing.Keys() {
		if !wanted[name] || !binding.stateOn {
			missing.Delete(name)
		}
	}
	values := newOrderedMap[string, *memberState]()
	if previous != nil {
		values = previous.values
	}
	binding.pending = &pendingShape{shape: shape, missing: missing, values: values}
	if missing.Len() == 0 {
		binding.apply()
	}
}

// state is a state frame for member name of the current hold.
func (binding *RemoteBinding) state(name string, frame StateFrame) {
	if binding.ended() {
		return
	}
	if frame.As == "error" {
		err := FromWireError(*frame.Error)
		// Before the first hydration nobody could read anything: the binding fails instead of waiting forever.
		if !binding.hydrated && binding.stateOn {
			binding.gone(err)
			binding.close()
		} else {
			binding.host.report(err)
		}
		return
	}
	pending := binding.pending
	if frame.As == "ops" {
		var target *memberState
		if pending != nil {
			target, _ = pending.values.Get(name)
		}
		if target == nil {
			target, _ = binding.members.Get(name)
		}
		if target == nil {
			return
		}
		ops, err := target.decoder.Decode(frame.Ops)
		if err == nil {
			target.value, err = delta.ApplyImmutable(target.value, ops)
		}
		if err != nil {
			binding.host.report(err)
			return
		}
		if current, _ := binding.members.Get(name); target == current {
			binding.deliver(name, StateEvent[any]{Type: StateEventOps, Ops: ops, Value: target.value})
		}
		return
	}
	fresh := &memberState{value: frame.Value, decoder: delta.NewDecoder()}
	if pending != nil {
		if _, isMissing := pending.missing.Get(name); isMissing || pending.values.Has(name) {
			pending.values.Set(name, fresh)
			pending.missing.Delete(name)
			if pending.missing.Len() == 0 {
				binding.apply()
			}
			return
		}
	}
	if !binding.inVariant(name) {
		return
	}
	binding.members.Set(name, fresh)
	binding.deliver(name, StateEvent[any]{Type: StateEventReset, Value: fresh.value})
}

// gone: the provider ended the object (or refused the hold): every view is gone.
func (binding *RemoteBinding) gone(err error) {
	if binding.ended() {
		return
	}
	binding.status = bindingEnded
	binding.err = err
	binding.hold = nil
	binding.pending = nil
	binding.live.reject(err)
	binding.settleHydration(err)
	for _, view := range binding.views.Keys() {
		view.end(err)
	}
	clear(binding.listeners)
	binding.host.forgetBinding(binding)
}

// detached: the link went down but may come back (upstream reconnect): keep the values, wait for the re-hold.
func (binding *RemoteBinding) detached() {
	if binding.ended() {
		return
	}
	binding.hold = nil
	binding.pending = nil
	if binding.status == bindingLive {
		binding.status = bindingLoading
		binding.live = newLivePromise(&binding.host.loop)
	}
	for _, view := range binding.views.Keys() {
		view.update()
	}
}

// close: no views and no linger: it releases the hold and forgets the binding.
func (binding *RemoteBinding) close() {
	if binding.lingerStop != nil {
		binding.lingerStop()
		binding.lingerStop = nil
	}
	if binding.hold != nil {
		binding.hold.channel.release(binding.hold.id)
	}
	binding.hold = nil
	if !binding.ended() {
		binding.status = bindingEnded
		err := NewChordError(CodeReleased, "reference "+binding.key+" released", ChordErrorOptions{})
		binding.live.reject(err)
		binding.settleHydration(err)
		for _, view := range binding.views.Keys() {
			view.end(err)
		}
	}
	binding.pending = nil
	clear(binding.listeners)
	binding.host.forgetBinding(binding)
}

// call calls name over the current hold, waiting for it while the binding (re)holds. It blocks and is called off the
// loop.
func (binding *RemoteBinding) call(name string, args any, ctx Ctx) (any, error) {
	host := binding.host
	for {
		var ended bool
		var terminal error
		var live *Promise[struct{}]
		var waitForLive bool
		host.run(func() {
			if ended = binding.ended(); ended {
				terminal = binding.terminalError()
				return
			}
			live = binding.live.promise
			waitForLive = binding.status != bindingLive || binding.hold == nil
		})
		if ended {
			return nil, terminal
		}
		if waitForLive {
			if _, err := raceSignal(live, ctx.Signal()); err != nil {
				return nil, err
			}
		}
		var result *Promise[any]
		var err error
		retry := false
		host.run(func() {
			hold := binding.hold
			if hold == nil || binding.status != bindingLive {
				retry = true
				return
			}
			var variant *string
			if binding.shape != nil {
				variant = binding.shape.Variant
			}
			var description MemberDescription
			ok := false
			if variant != nil {
				description, ok = binding.contract.Description.member(*variant, name)
			}
			if !ok || description.Kind != MemberMethod {
				shown := "undefined"
				if variant != nil {
					shown = *variant
				}
				err = NewChordError(CodeNotInVariant, fmt.Sprintf("%s: %s is not a method of variant %s", binding.key, name, shown), ChordErrorOptions{})
				return
			}
			// Send only what the consumer's own schema knows (the additive rule's premise; see ObjectImpl.call).
			var sent any
			if description.Args != nil {
				sent = cleanArgs(binding.contract, description.Args, args)
			}
			result = hold.channel.call(hold.id, name, sent, description.Args != nil, &RootRef{RootID: ctx.RootID(), CodeVersion: ctx.CodeVersion()}, ctx.Signal())
		})
		if retry {
			continue
		}
		if err != nil {
			return nil, err
		}
		//portlint:allow ctxbackground ctx.Signal() reaches the remote call (hold.channel.call above), which rejects result on abort; the await has no abort of its own, as in remote.ts
		return result.Await(context.Background())
	}
}

// apply applies the pending shape with its snapshots: members outside the variant go, new values are announced.
func (binding *RemoteBinding) apply() {
	pending := binding.pending
	if pending == nil {
		return
	}
	binding.pending = nil
	binding.shape = &pending.shape
	wanted := map[string]bool{}
	for _, name := range binding.stateMembers(pending.shape) {
		wanted[name] = true
	}
	for _, name := range binding.members.Keys() {
		if !wanted[name] {
			binding.members.Delete(name)
		}
	}
	for _, name := range pending.values.Keys() {
		if wanted[name] {
			state, _ := pending.values.Get(name)
			binding.members.Set(name, state)
		}
	}
	hydrated := binding.stateOn
	for name := range wanted {
		if !binding.members.Has(name) {
			hydrated = false
		}
	}
	binding.hydrated = hydrated
	if binding.hydrated {
		binding.settleHydration(nil)
	}
	binding.status = bindingLive
	binding.applied = true
	binding.live.resolve(struct{}{})
	for _, view := range binding.views.Keys() {
		view.update()
	}
	for _, name := range pending.values.Keys() {
		if wanted[name] {
			state, _ := pending.values.Get(name)
			binding.deliver(name, StateEvent[any]{Type: StateEventReset, Value: state.value})
		}
	}
	for _, view := range binding.views.Keys() {
		view.shapeChanged()
	}
}

func (binding *RemoteBinding) deliver(name string, event StateEvent[any]) {
	set, ok := binding.listeners[name]
	if !ok {
		return
	}
	for _, readable := range set.Keys() {
		readable.deliver(event)
	}
}

// viewReadable is a state member of a remote reference: the binding's value. Reading fails not_ready before the first
// hydration and not_in_variant outside the variant (Value panics with the error, as Pi's getter throws). A listener
// added before the hydration gets its snapshot then.
type viewReadable struct {
	view   *RemoteRef
	name   string
	values *orderedMap[*func(any), struct{}]
	events *orderedMap[*func(StateEvent[any]), struct{}]
	// unseen are the event listeners that have not had their snapshot yet.
	unseen *orderedMap[*func(StateEvent[any]), struct{}]
}

func newViewReadable(view *RemoteRef, name string) *viewReadable {
	return &viewReadable{
		view: view, name: name, values: newOrderedMap[*func(any), struct{}](),
		events: newOrderedMap[*func(StateEvent[any]), struct{}](), unseen: newOrderedMap[*func(StateEvent[any]), struct{}](),
	}
}

func (readable *viewReadable) host() *HostImpl { return readable.view.binding.host }

// Value is the member's value. It panics with the ChordError Pi's getter throws: not_ready before the first hydration,
// not_in_variant outside the variant, and the end's error after the reference ended.
func (readable *viewReadable) Value() any {
	var value any
	var err error
	readable.host().run(func() { value, err = readable.read() })
	if err != nil {
		panic(err)
	}
	return value
}

func (readable *viewReadable) read() (any, error) {
	if readable.view.ended() {
		return nil, readable.view.terminalError()
	}
	return readable.view.binding.read(readable.name)
}

// hydration: not hydrated before the binding's first snapshots (a local object forwarding this member waits for them).
func (readable *viewReadable) hydration() *Hydration {
	binding := readable.view.binding
	if binding.hydrated {
		return nil
	}
	return &Hydration{Hydrated: false, WhenHydrated: binding.whenHydrated}
}

func (readable *viewReadable) Subscribe(listener func(value any)) func() {
	key := &listener
	readable.host().run(func() {
		readable.values.Set(key, struct{}{})
		readable.attach()
		if readable.readable() {
			value := readable.current()
			readable.safe(func() { listener(value) })
		}
	})
	return func() { readable.host().run(func() { readable.removeValue(key) }) }
}

func (readable *viewReadable) Events(listener func(event StateEvent[any])) func() {
	key := &listener
	readable.host().run(func() {
		readable.events.Set(key, struct{}{})
		readable.attach()
		if readable.readable() {
			value := readable.current()
			readable.safe(func() { listener(StateEvent[any]{Type: StateEventSnap, Value: value}) })
		} else {
			readable.unseen.Set(key, struct{}{})
		}
	})
	return func() {
		readable.host().run(func() {
			readable.unseen.Delete(key)
			readable.events.Delete(key)
			readable.detachIfIdle()
		})
	}
}

func (readable *viewReadable) current() any {
	state, _ := readable.view.binding.members.Get(readable.name)
	if state == nil {
		return nil
	}
	return state.value
}

func (readable *viewReadable) deliver(event StateEvent[any]) {
	for _, listener := range readable.events.Keys() {
		if !readable.unseen.Delete(listener) {
			readable.safe(func() { (*listener)(event) })
		} else if event.Type != StateEventOps {
			readable.safe(func() { (*listener)(StateEvent[any]{Type: StateEventSnap, Value: event.Value}) })
		}
	}
	for _, listener := range readable.values.Keys() {
		readable.safe(func() { (*listener)(event.Value) })
	}
}

func (readable *viewReadable) stop() {
	readable.values.Clear()
	readable.events.Clear()
	readable.unseen.Clear()
	readable.view.binding.unlisten(readable.name, readable)
}

func (readable *viewReadable) readable() bool {
	return !readable.view.ended() && readable.view.binding.readable(readable.name)
}

func (readable *viewReadable) attach() {
	if !readable.view.ended() {
		readable.view.binding.listen(readable.name, readable)
	}
}

func (readable *viewReadable) removeValue(key *func(any)) {
	readable.values.Delete(key)
	readable.detachIfIdle()
}

func (readable *viewReadable) detachIfIdle() {
	if readable.values.Len()+readable.events.Len() == 0 {
		readable.view.binding.unlisten(readable.name, readable)
	}
}

func (readable *viewReadable) safe(fn func()) {
	if err := callReporting(fn); err != nil {
		readable.view.binding.host.report(err)
	}
}

// RemoteRef is a reference to an object on another host: a view onto the host's shared binding for its key. Each view
// has its own owner, status, observers and Release; ending by the provider and re-holding belong to the binding.
type RemoteRef struct {
	binding *RemoteBinding
	owner   RefOwner
	// wantsState is false for a method-only view (state: false), ready once live.
	wantsState bool
	ready      *Promise[struct{}]
	status     RefStatus
	err        error

	shapeListeners  *orderedMap[*func(Shape), struct{}]
	statusListeners *orderedMap[*statusListener, struct{}]
	readables       *orderedMap[string, *viewReadable]
	callers         *orderedMap[string, methodFunc]
	resolveReady    func(struct{})
	rejectReady     func(error)
	readySettled    bool
}

func newRemoteRef(binding *RemoteBinding, owner RefOwner, wantsState bool) *RemoteRef {
	ref := &RemoteRef{
		binding: binding, owner: owner, wantsState: wantsState, status: RefLoading,
		shapeListeners:  newOrderedMap[*func(Shape), struct{}](),
		statusListeners: newOrderedMap[*statusListener, struct{}](),
		readables:       newOrderedMap[string, *viewReadable](),
		callers:         newOrderedMap[string, methodFunc](),
	}
	ref.ready, ref.resolveReady, ref.rejectReady = newPromise[struct{}](&binding.host.loop)
	return ref
}

func (ref *RemoteRef) host() *HostImpl { return ref.binding.host }

func (ref *RemoteRef) address() string                  { return ref.binding.key }
func (ref *RemoteRef) readyPromise() *Promise[struct{}] { return ref.ready }
func (ref *RemoteRef) refObject() *ObjectImpl           { return nil }

func (ref *RemoteRef) ended() bool { return ref.status == RefReleased || ref.status == RefGone }

// Contract is the consumer contract token this reference was bound with.
func (ref *RemoteRef) Contract() *Contract { return ref.binding.contract }

func (ref *RemoteRef) Status() (status RefStatus) {
	ref.host().run(func() { status = ref.status })
	return status
}

func (ref *RemoteRef) Ready(ctx context.Context) error {
	_, err := ref.ready.Await(ctx)
	return err
}

func (ref *RemoteRef) Err() (err error) {
	ref.host().run(func() { err = ref.err })
	return err
}

func (ref *RemoteRef) Variant() (variant *string) {
	ref.host().run(func() {
		if ref.binding.shape != nil {
			variant = ref.binding.shape.Variant
		}
	})
	return variant
}

func (ref *RemoteRef) Shape() (shape *Shape) {
	ref.host().run(func() { shape = ref.binding.shape })
	return shape
}

func (ref *RemoteRef) OnShape(listener func(shape Shape)) func() {
	key := &listener
	ref.host().run(func() { ref.shapeListeners.Set(key, struct{}{}) })
	return func() { ref.host().run(func() { ref.shapeListeners.Delete(key) }) }
}

func (ref *RemoteRef) OnStatus(listener func(status RefStatus, err error)) func() {
	key := &listener
	ref.host().run(func() { ref.statusListeners.Set(key, struct{}{}) })
	return func() { ref.host().run(func() { ref.statusListeners.Delete(key) }) }
}

func (ref *RemoteRef) notify() {
	notifyStatus(ref.host(), ref.statusListeners, ref.status, ref.err)
}

func (ref *RemoteRef) settleReady(err error) {
	if ref.readySettled {
		return
	}
	ref.readySettled = true
	if err == nil {
		ref.resolveReady(struct{}{})
	} else {
		ref.rejectReady(err)
	}
}

func (ref *RemoteRef) terminalError() error {
	if ref.status == RefGone {
		return ref.err
	}
	return NewChordError(CodeReleased, "reference "+ref.address()+" released", ChordErrorOptions{})
}

func (ref *RemoteRef) Release() {
	ref.host().run(func() {
		if ref.ended() {
			return
		}
		ref.status = RefReleased
		ref.notify()
		ref.settleReady(NewChordError(CodeReleased, "reference "+ref.address()+" released", ChordErrorOptions{}))
		for _, readable := range ref.readables.Values() {
			readable.stop()
		}
		ref.binding.removeView(ref)
	})
}

// ─── driven by the binding (on the loop) ───

// update follows the binding: live once it has a shape (and, for a state view, its state) over a current hold.
func (ref *RemoteRef) update() {
	if ref.ended() {
		return
	}
	binding := ref.binding
	live := binding.status == bindingLive && binding.hold != nil && (!ref.wantsState || binding.hydrated)
	next := RefLoading
	if live {
		next = RefLive
	}
	if next == ref.status {
		return
	}
	ref.status = next
	ref.notify()
	if live {
		ref.settleReady(nil)
	}
}

func (ref *RemoteRef) end(err error) {
	if ref.ended() {
		return
	}
	if chordError, isChord := err.(*ChordError); isChord && chordError.Code == CodeReleased { //nolint:errorlint // Pi's instanceof does not unwrap
		ref.status = RefReleased
	} else {
		ref.status = RefGone
		ref.err = err
	}
	ref.notify()
	ref.settleReady(err)
	for _, readable := range ref.readables.Values() {
		readable.stop()
	}
}

func (ref *RemoteRef) shapeChanged() {
	shape := ref.binding.shape
	if shape == nil || ref.ended() {
		return
	}
	for _, listener := range ref.shapeListeners.Keys() {
		if err := callReporting(func() { (*listener)(*shape) }); err != nil {
			ref.host().report(err)
		}
	}
}

// ─── members ───

// Member is a member by name: a method (methodFunc), a state member (Readable[any]) or nil.
func (ref *RemoteRef) Member(name string) (member any) {
	if refBase[name] || name == "then" {
		return nil
	}
	ref.host().run(func() { member = ref.member(name) })
	return member
}

func (ref *RemoteRef) member(name string) any {
	var description MemberDescription
	found := false
	for _, variant := range ref.binding.contract.Description.Variants {
		if member, ok := variant.Value.Get(name); ok {
			description, found = member, true
			break
		}
	}
	if !found {
		return nil
	}
	switch description.Kind {
	case MemberMethod:
		return ref.caller(name)
	case MemberState:
		readable, ok := ref.readables.Get(name)
		if !ok {
			readable = newViewReadable(ref, name)
			ref.readables.Set(name, readable)
		}
		return Readable[any](readable)
	}
	return nil
}

func (ref *RemoteRef) caller(name string) methodFunc {
	if caller, ok := ref.callers.Get(name); ok {
		return caller
	}
	noArgs := takesNoArgs(ref.binding.contract, name)
	caller := func(args any, ctx Ctx) (any, error) {
		// A method without arguments is called as `ref.name(ctx)`: its args are ignored.
		if noArgs {
			args = nil
		}
		host := ref.host()
		inFactory := false
		host.run(func() { inFactory = host.inFactory > 0 })
		if inFactory {
			return nil, NewChordError(CodeNotReady, ref.address()+"."+name+" called inside a factory", ChordErrorOptions{})
		}
		// The same checks as a local reference's caller (RefImpl.caller and ObjectImpl.call): a ctx from another host or
		// one not made by Chord would carry a root this host does not own over its link.
		impl, isImpl := ctx.(*CtxImpl)
		if ctx != nil && !isImpl {
			return nil, Invalid("foreign_ctx", "ctx must come from a Chord host", nil)
		}
		if isImpl {
			if err := impl.check(host); err != nil {
				return nil, err
			}
		}
		if ctx == nil || impl.holder {
			var options RootOptions
			if ctx != nil {
				options.Signal = ctx.Signal()
			}
			return host.Root(func(root Ctx) (any, error) { return ref.call(name, args, root) }, options)
		}
		return ref.call(name, args, ctx)
	}
	ref.callers.Set(name, caller)
	return caller
}

// call calls a method over the binding's hold, off the loop.
func (ref *RemoteRef) call(name string, args any, ctx Ctx) (any, error) {
	var err error
	ref.host().run(func() {
		if ref.ended() {
			err = ref.terminalError()
		}
	})
	if err != nil {
		return nil, err
	}
	return ref.binding.call(name, args, ctx)
}
