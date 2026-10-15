package host

// Ports packages/chord/src/remote.ts: references on objects of other hosts, bound to a link.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// StateSink is a state frame as a remote readable receives it.
type StateSink = StateFrame

// RemoteChannel is what a remote reference needs from the link it is bound to (implemented by *Connection). Its methods
// run on the loop; call's promise settles with the result.
type RemoteChannel interface {
	hold(ref *RemoteRef) int
	release(id int)
	call(hold int, member string, args any, hasArgs bool, root *RootRef, signal context.Context) *Promise[any]
	sub(hold int, member string, sink func(frame StateSink)) int
	unsub(id int)
}

type remoteBinding struct {
	channel RemoteChannel
	id      int
}

type remoteSub struct {
	channel RemoteChannel
	id      int
}

// remoteReadable is a state member of a remote reference: hydrated while subscribed (snapshot, then decoded ops); nil
// before. Re-subscribing after a reconnect delivers the new snapshot as a reset.
type remoteReadable struct {
	ref     *RemoteRef
	name    string
	values  *orderedMap[*func(any), struct{}]
	events  *orderedMap[*func(StateEvent[any]), struct{}]
	sub     *remoteSub
	decoder *delta.Decoder
	value   any
}

func newRemoteReadable(ref *RemoteRef, name string) *remoteReadable {
	return &remoteReadable{
		ref: ref, name: name, values: newOrderedMap[*func(any), struct{}](),
		events: newOrderedMap[*func(StateEvent[any]), struct{}](), decoder: delta.NewDecoder(),
	}
}

func (readable *remoteReadable) Value() (value any) {
	readable.ref.host.run(func() { value = readable.value })
	return value
}

func (readable *remoteReadable) Subscribe(listener func(value any)) func() {
	key := &listener
	readable.ref.host.run(func() {
		readable.values.Set(key, struct{}{})
		readable.safe(func() { listener(readable.value) })
		readable.bind()
	})
	return func() { readable.ref.host.run(func() { readable.values.Delete(key); readable.bind() }) }
}

func (readable *remoteReadable) Events(listener func(event StateEvent[any])) func() {
	key := &listener
	readable.ref.host.run(func() {
		readable.events.Set(key, struct{}{})
		readable.safe(func() { listener(StateEvent[any]{Type: StateEventSnap, Value: readable.value}) })
		readable.bind()
	})
	return func() { readable.ref.host.run(func() { readable.events.Delete(key); readable.bind() }) }
}

// bind subscribes upstream while someone listens and the reference is bound; drops a subscription on an old link.
func (readable *remoteReadable) bind() {
	listening := readable.values.Len()+readable.events.Len() > 0
	// Nobody listens: the value would go stale, so forget it. The next subscriber sees nil, then the snapshot.
	if !listening {
		readable.value = nil
	}
	var binding *remoteBinding
	if listening {
		binding = readable.ref.binding
	}
	if binding != nil && readable.sub != nil && readable.sub.channel == binding.channel {
		return
	}
	if readable.sub != nil {
		readable.sub.channel.unsub(readable.sub.id)
		readable.sub = nil
	}
	if binding == nil {
		return
	}
	channel := binding.channel
	readable.decoder = delta.NewDecoder()
	id := channel.sub(binding.id, readable.name, readable.receive)
	readable.sub = &remoteSub{channel: channel, id: id}
}

// detach: the link is gone: keep the last value, resubscribe on the next binding.
func (readable *remoteReadable) detach() { readable.sub = nil }

func (readable *remoteReadable) stop() {
	readable.values.Clear()
	readable.events.Clear()
	readable.bind()
}

func (readable *remoteReadable) receive(frame StateSink) {
	if frame.Error != nil {
		readable.ref.host.report(FromWireError(*frame.Error))
		return
	}
	if frame.Ops == nil && !frame.HasSnap && !frame.HasReset {
		return
	}
	var event StateEvent[any]
	if frame.Ops != nil {
		ops, err := readable.decoder.Decode(frame.Ops)
		if err == nil {
			readable.value, err = delta.ApplyImmutable(readable.value, ops)
		}
		if err != nil {
			readable.ref.host.report(err)
			return
		}
		event = StateEvent[any]{Type: StateEventOps, Ops: ops, Value: readable.value}
	} else {
		// Listeners already saw a snapshot (nil or an older value), so every upstream snapshot is a reset.
		readable.decoder = delta.NewDecoder()
		if frame.HasSnap {
			readable.value = frame.Snap
		} else {
			readable.value = frame.Reset
		}
		event = StateEvent[any]{Type: StateEventReset, Value: readable.value}
	}
	for _, listener := range readable.events.Keys() {
		readable.safe(func() { (*listener)(event) })
	}
	for _, listener := range readable.values.Keys() {
		readable.safe(func() { (*listener)(readable.value) })
	}
}

func (readable *remoteReadable) safe(fn func()) {
	if err := callReporting(fn); err != nil {
		readable.ref.host.report(err)
	}
}

type remoteRefFields struct {
	contract *Contract
	at       string
	input    any
	// principal is a forwarded principal (absent for the host's own principal).
	principal    Principal
	hasPrincipal bool
	owner        RefOwner
	address      string
}

type livePromise struct {
	promise *Promise[struct{}]
	resolve func(struct{})
	reject  func(error)
}

// RemoteRef is a hold on an object on another host, bound to a link. Late-bound like a local reference.
type RemoteRef struct {
	host         *HostImpl
	contract     *Contract
	at           string
	input        any
	principal    Principal
	hasPrincipal bool
	owner        RefOwner
	addr         string
	ready        *Promise[struct{}]
	status       RefStatus
	err          error
	shape        *Shape
	binding      *remoteBinding

	shapeListeners  *orderedMap[*func(Shape), struct{}]
	statusListeners *orderedMap[*statusListener, struct{}]
	readables       *orderedMap[string, *remoteReadable]
	callers         *orderedMap[string, methodFunc]
	resolveReady    func(struct{})
	rejectReady     func(error)
	readySettled    bool
	live            livePromise
}

func newRemoteRef(host *HostImpl, fields remoteRefFields) *RemoteRef {
	ref := &RemoteRef{
		host: host, contract: fields.contract, at: fields.at, input: fields.input, principal: fields.principal,
		hasPrincipal: fields.hasPrincipal, owner: fields.owner, addr: fields.address, status: RefLoading,
		shapeListeners:  newOrderedMap[*func(Shape), struct{}](),
		statusListeners: newOrderedMap[*statusListener, struct{}](),
		readables:       newOrderedMap[string, *remoteReadable](),
		callers:         newOrderedMap[string, methodFunc](),
	}
	ref.ready, ref.resolveReady, ref.rejectReady = newPromise[struct{}](&host.loop)
	ref.live = ref.newLive()
	return ref
}

func (ref *RemoteRef) newLive() livePromise {
	promise, resolve, reject := newPromise[struct{}](&ref.host.loop)
	return livePromise{promise: promise, resolve: resolve, reject: reject}
}

func (ref *RemoteRef) address() string                  { return ref.addr }
func (ref *RemoteRef) readyPromise() *Promise[struct{}] { return ref.ready }
func (ref *RemoteRef) refObject() *ObjectImpl           { return nil }

// Contract is the consumer contract token this reference was bound with.
func (ref *RemoteRef) Contract() *Contract { return ref.contract }

func (ref *RemoteRef) Status() (status RefStatus) {
	ref.host.run(func() { status = ref.status })
	return status
}

func (ref *RemoteRef) Ready(ctx context.Context) error {
	_, err := ref.ready.Await(ctx)
	return err
}

func (ref *RemoteRef) Err() (err error) {
	ref.host.run(func() { err = ref.err })
	return err
}

func (ref *RemoteRef) Variant() (variant *string) {
	ref.host.run(func() {
		if ref.shape != nil {
			variant = ref.shape.Variant
		}
	})
	return variant
}

func (ref *RemoteRef) Shape() (shape *Shape) {
	ref.host.run(func() { shape = ref.shape })
	return shape
}

func (ref *RemoteRef) OnShape(listener func(shape Shape)) func() {
	key := &listener
	ref.host.run(func() { ref.shapeListeners.Set(key, struct{}{}) })
	return func() { ref.host.run(func() { ref.shapeListeners.Delete(key) }) }
}

func (ref *RemoteRef) OnStatus(listener func(status RefStatus, err error)) func() {
	key := &listener
	ref.host.run(func() { ref.statusListeners.Set(key, struct{}{}) })
	return func() { ref.host.run(func() { ref.statusListeners.Delete(key) }) }
}

func (ref *RemoteRef) statusChanged() {
	notifyStatus(ref.statusListeners, ref.status, ref.err, ref.host.report)
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

func (ref *RemoteRef) Release() {
	ref.host.run(func() {
		if ref.status == RefReleased || ref.status == RefGone {
			return
		}
		ref.status = RefReleased
		ref.statusChanged()
		err := NewChordError(CodeReleased, "reference "+ref.addr+" released", ChordErrorOptions{})
		ref.settleReady(err)
		ref.live.reject(err)
		for _, readable := range ref.readables.Values() {
			readable.stop()
		}
		if ref.binding != nil {
			ref.binding.channel.release(ref.binding.id)
		}
		ref.binding = nil
		ref.host.forgetRemote(ref)
	})
}

// ─── driven by the link (on the loop) ───

func (ref *RemoteRef) bound(channel RemoteChannel, id int) {
	ref.binding = &remoteBinding{channel: channel, id: id}
}

func (ref *RemoteRef) shaped(shape Shape) {
	if ref.status == RefReleased || ref.status == RefGone {
		return
	}
	ref.shape = &shape
	wasLoading := ref.status == RefLoading
	ref.status = RefLive
	if wasLoading {
		ref.statusChanged()
		ref.settleReady(nil)
		ref.live.resolve(struct{}{})
	}
	for _, readable := range ref.readables.Values() {
		readable.bind()
	}
	for _, listener := range ref.shapeListeners.Keys() {
		if err := callReporting(func() { (*listener)(shape) }); err != nil {
			ref.host.report(err)
		}
	}
}

func (ref *RemoteRef) gone(err error) {
	if ref.status == RefReleased || ref.status == RefGone {
		return
	}
	ref.status = RefGone
	ref.err = err
	ref.statusChanged()
	ref.binding = nil
	ref.settleReady(err)
	ref.live.reject(err)
	for _, readable := range ref.readables.Values() {
		readable.stop()
	}
	ref.host.forgetRemote(ref)
}

// detached: the link went down but may come back (upstream reconnect): wait, then re-hold.
func (ref *RemoteRef) detached() {
	if ref.status == RefReleased || ref.status == RefGone {
		return
	}
	ref.binding = nil
	for _, readable := range ref.readables.Values() {
		readable.detach()
	}
	if ref.status == RefLive {
		ref.status = RefLoading
		ref.live = ref.newLive()
		ref.statusChanged()
	}
}

// ─── members ───

// Member is a member by name: a method (methodFunc), a state member (Readable[any]) or nil.
func (ref *RemoteRef) Member(name string) (member any) {
	if refBase[name] || name == "then" {
		return nil
	}
	ref.host.run(func() { member = ref.member(name) })
	return member
}

func (ref *RemoteRef) member(name string) any {
	var description MemberDescription
	found := false
	for _, variant := range ref.contract.Description.Variants {
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
			readable = newRemoteReadable(ref, name)
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
	noArgs := takesNoArgs(ref.contract, name)
	caller := func(args any, ctx Ctx) (any, error) {
		// A method without arguments is called as `ref.name(ctx)`: its args are ignored.
		if noArgs {
			args = nil
		}
		host := ref.host
		inFactory := false
		host.run(func() { inFactory = host.inFactory > 0 })
		if inFactory {
			return nil, NewChordError(CodeNotReady, ref.addr+"."+name+" called inside a factory", ChordErrorOptions{})
		}
		impl, isImpl := ctx.(*CtxImpl)
		if isImpl {
			if err := impl.check(impl.host); err != nil {
				return nil, err
			}
		}
		if ctx == nil || (isImpl && impl.holder) {
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

// call calls a method over the link, off the loop.
func (ref *RemoteRef) call(name string, args any, ctx Ctx) (any, error) {
	for {
		var status RefStatus
		var err error
		var live *Promise[struct{}]
		ref.host.run(func() {
			status, err, live = ref.status, ref.err, ref.live.promise
		})
		if status == RefReleased || status == RefGone {
			if status == RefGone {
				return nil, err
			}
			return nil, NewChordError(CodeReleased, "reference "+ref.addr+" released", ChordErrorOptions{})
		}
		if status == RefLoading {
			if _, err := raceSignal(live, ctx.Signal()); err != nil {
				return nil, err
			}
		}
		var result *Promise[any]
		retry := false
		ref.host.run(func() {
			binding := ref.binding
			if binding == nil {
				retry = true
				return
			}
			var kind *string
			if ref.shape != nil {
				kind = ref.shape.Variant
			}
			var description MemberDescription
			ok := false
			if kind != nil {
				description, ok = ref.contract.Description.member(*kind, name)
			}
			if !ok || description.Kind != MemberMethod {
				variant := "undefined"
				if kind != nil {
					variant = *kind
				}
				err = NewChordError(CodeNotInVariant, fmt.Sprintf("%s: %s is not a method of variant %s", ref.addr, name, variant), ChordErrorOptions{})
				return
			}
			// Send only what the consumer's own schema knows (the additive rule's premise; see ObjectImpl.call).
			var sent any
			if description.Args != nil {
				sent = cleanArgs(ref.contract, description.Args, args)
			}
			result = binding.channel.call(binding.id, name, sent, description.Args != nil, &RootRef{RootID: ctx.RootID(), CodeVersion: ctx.CodeVersion()}, ctx.Signal())
		})
		if retry {
			continue
		}
		if err != nil {
			return nil, err
		}
		//portlint:allow ctxbackground ctx.Signal() reaches the remote call (binding.channel.call above), which rejects result on abort; the await has no abort of its own, as in remote.ts
		return result.Await(context.Background())
	}
}
