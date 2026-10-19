package host

// Ports packages/chord/src/object.ts: services, the factory's self, resources, contexts, references and objects.
//
// Every method that reads or writes object, reference or resource state runs on the host's loop (HostImpl.run inlines
// when the caller is already there). Pi's async methods are split at their first await: the synchronous prefix runs
// where Pi runs it, and the rest runs off the loop, re-entering it for each synchronous section. Factories, cell
// guards and status, shape and readable listeners run on the loop, as upstream runs them synchronously; methods,
// listener functions, tasks and resource open/close callbacks run off the loop, where they may block on other work.

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// ─── Services ────────────────────────────────────────────────────────────────

// Lifetime is how long objects of a type live: LifetimeReferenced (the default) or LifetimeHost.
type Lifetime string

// Lifetimes.
const (
	// LifetimeReferenced creates an object at the first get of its address and disposes it after the last reference is
	// released (plus Linger).
	LifetimeReferenced Lifetime = "referenced"
	// LifetimeHost creates the type's one object when its facet loads (loading waits until it is live) and keeps it
	// until the host closes or a reload removes its facet.
	LifetimeHost Lifetime = "host"
)

// ServeOptions are the options of serving a contract. Nil and zero members are absent. LifetimeHost is for local types
// whose input accepts `{}`; every get reaches the one object, created for the host principal. Only such objects may
// register into registries. Pi's types refuse the rest at compile time (ServeOptionsFor); Go has no type-level check,
// so Serve refuses a remote type, an input that does not accept `{}` and a Linger at runtime.
type ServeOptions struct {
	// Lifetime is how long objects of the type live; empty is LifetimeReferenced.
	Lifetime Lifetime
	// Linger is how long an object stays alive after its last reference is released (LifetimeReferenced only).
	Linger time.Duration
	// HasLinger is true when Linger is present: Pi's `linger !== undefined`, which LifetimeHost refuses even for 0.
	HasLinger bool
	// Accepts are adapters for consumers built against older contract versions.
	Accepts Accepts
}

// Factory builds an object's implementation from its input, the principal it is created for and its self.
type Factory func(input any, principal Principal, self Self) (Implementation, error)

// Service is a served contract: its factory, options and the facet that serves it.
type Service struct {
	contract *Contract
	factory  Factory
	options  ServeOptions
	facet    string
}

func makeService(contract *Contract, factory Factory, options ServeOptions, facet string) (*Service, error) {
	if err := checkLifetime(contract, options); err != nil {
		return nil, err
	}
	return &Service{contract: contract, factory: factory, options: options, facet: facet}, nil
}

// hostLifetime reports whether objects of service have LifetimeHost.
func hostLifetime(service *Service) bool { return service.options.Lifetime == LifetimeHost }

// checkLifetime refuses lifetimes the type cannot have, when it is served.
func checkLifetime(contract *Contract, options ServeOptions) error {
	lifetime := options.Lifetime
	if lifetime == "" {
		lifetime = LifetimeReferenced
	}
	if lifetime != LifetimeReferenced && lifetime != LifetimeHost {
		return typeError("%s: unknown lifetime %s", contract.Name, lifetime)
	}
	if lifetime != LifetimeHost {
		return nil
	}
	if contract.Scope != ScopeLocal {
		return typeError(`%s: lifetime "host" needs a local type (remote types are referenced)`, contract.Name)
	}
	if options.HasLinger || options.Linger != 0 {
		return typeError(`%s: an object of lifetime "host" does not linger (it lives as long as the host)`, contract.Name)
	}
	if _, err := Normalize(contract.Input, map[string]any{}, contract.Name+" input"); err != nil {
		return typeError(`%s: lifetime "host" needs an input that accepts {} (the object's one address)`, contract.Name)
	}
	return nil
}

// ─── Declarations ────────────────────────────────────────────────────────────

// Entry is one named entry of an object literal passed to Self.Register. A slice of entries keeps the literal's
// order, which Pi iterates with Object.entries and which a Go map would lose.
type Entry struct {
	Name  string
	Value any
}

// StateOptions are the options of Self.State.
type StateOptions struct {
	// Version is the meaning of the cell's value; 0 is the default 1. A reload that raises it runs Migrate.
	Version int
	// Migrate maps the value of an older version (from) to this version's, at the reload that raised the version. The
	// cell may be several versions behind. Without it, the value is kept and must fit the new schema.
	Migrate func(old any, from int) (any, error)
}

// StateInit is object.ts StateInit: a cell's first value from its source. Pass it as the initial value of Self.State.
// Init runs after the object's resources are open and its references are live, before the object goes live, so no
// call, listener or subscriber sees a value that is not from the source. It runs in a root of its own (acting as the
// host's own principal) and may block; the inits of one object run in parallel. Its value is validated against the
// schema; an error or an invalid value fails the activation. A Go value is never a StateInit unless it is one, as a
// JSON value never holds a function.
type StateInit struct {
	Init func(ctx Ctx) (any, error)
}

// Self is the factory's `self`: how a factory declares the object's state, references, resources, listeners, tasks,
// derivations, registrations and exposed resources.
type Self interface {
	// State declares a state cell named name: its schema (required: there is no TypeScript at runtime) and its initial
	// value (validated), or a StateInit to read the first value from its source before the object goes live. The name
	// identifies the cell across reloads: a reload keeps the cell and its value (initial is ignored, Init does not run),
	// migrates it when its version rises, and refuses a schema change without a version bump. A cell a reload adds runs
	// its Init while the reload prepares. options nil is Pi's undefined.
	State(name string, schema Schema, initial any, options *StateOptions) (*Cell[any], error)
	// Get makes an object-owned reference, identified by its address.
	Get(typ *Contract, input any, options *GetOptions) (Ref, error)
	// Resource declares a resource named name: opened before the object goes live, closed after it is disposed. A reload
	// keeps it unless its Version, Deps or Exclusive changed. declaration is a ResourceDeclaration.
	Resource(name string, declaration ResourceDeclarer) (Resource[any], error)
	// On reacts to source once the object is live: a readable (cell, derivation, reference state member) delivers its
	// current value, then every new value; an event source (or a resource whose value is one) delivers its events. Each
	// event runs handler in a fresh root. The name keeps the subscription across reloads while its source and select are
	// unchanged; a listener on state whose source the reload replaced (a derivation is new at every reload) subscribes
	// again and gets the current value again, so a handler of state must reconcile, not count.
	On(name string, source any, handler func(event any, ctx Ctx) error) error
	// OnSelect is On with select: it subscribes to select(value) instead (the opened value for a resource).
	OnSelect(name string, source any, selector func(value any) Subscribable[any], handler func(event any, ctx Ctx) error) error
	// Task declares long-running work of the live object (a code holder: its calls start fresh roots), restarted at
	// every reload.
	Task(fn func(ctx Ctx) error) error
	// Derive declares a synchronous projection of readables, recomputed when they change; a new one at every reload.
	Derive(sources Sources, fn func(values ...any) any) (Readable[any], error)
	// Register registers entries in registry. Objects of LifetimeHost only; keys are the registry's own keys.
	Register(registry Registry[any], entries []Entry) error
	// Expose is a member backed by one of this object's resources, for local contracts: references read the resource's
	// value once the object is live (nil before).
	Expose(resource Resource[any]) (Exposed, error)
}

// Exposed is a local member backed by a resource (`self.Expose(res)`): references read the resource's value once live.
type Exposed struct{ resource *ResourceImpl }

// ResourceImpl is one declared resource of an object.
type ResourceImpl struct {
	host *HostImpl
	key  string
	spec ResourceDeclaration[any]
	// deps are RefCore or *ResourceImpl values.
	deps []any
	// state is "closed", "opening", "open" or "unavailable" (a close or reopen failed during a reload rollback; the
	// value is gone for good).
	state string
	value any
}

func newResourceImpl(host *HostImpl, key string, spec ResourceDeclaration[any], deps []any) *ResourceImpl {
	return &ResourceImpl{host: host, key: key, spec: spec, deps: deps, state: "closed"}
}

// Key is the resource's declared name.
func (resource *ResourceImpl) Key() string { return resource.key }

// Value is the open resource's value; it fails not_ready until opened.
func (resource *ResourceImpl) Value() (value any, err error) {
	resource.host.run(func() { value, err = resource.valueOnLoop() })
	return value, err
}

func (resource *ResourceImpl) valueOnLoop() (any, error) {
	if resource.state == "unavailable" {
		return nil, NewChordError(CodeUnavailable, "resource "+resource.key+" could not be restored", ChordErrorOptions{})
	}
	if resource.state != "open" {
		return nil, NotReady("resource " + resource.key)
	}
	return resource.value, nil
}

func (resource *ResourceImpl) exclusive() bool { return resource.spec.Exclusive }

// open opens the resource, off the loop (object.ts:137-155). An open that completes after signal aborted (rollback,
// abandonment) closes the value it produced at once: nobody owns it any more.
func (resource *ResourceImpl) open(signal context.Context, order *[]*ResourceImpl) error {
	var opened *Promise[struct{}]
	resource.host.run(func() { opened, _ = resource.startOpen(signal, order) })
	//portlint:allow ctxmapping object.ts open awaits spec.open to the end and closes a value opened after abort; signal reaches the open, not the await
	_, err := opened.Await(context.Background())
	return err
}

// alreadyEntered is goEntered's channel for work that ran synchronously.
var alreadyEntered = func() <-chan struct{} {
	entered := make(chan struct{})
	close(entered)
	return entered
}()

// startOpen is open started on the loop. The synchronous start runs now; spec.Open runs as a task that has entered
// once it returned or waits; the rest is a continuation queued on the loop when spec.Open returned. So opens started
// in order whose spec.Open returns without a real wait join order in start order, as upstream's do (their
// continuations are microtasks queued in that order), and the others join it when they complete.
func (resource *ResourceImpl) startOpen(signal context.Context, order *[]*ResourceImpl) (opened *Promise[struct{}], entered <-chan struct{}) {
	host := resource.host
	if resource.state != "closed" {
		return resolvedPromise(&host.loop, struct{}{}), alreadyEntered
	}
	resource.state = "opening"
	opened, resolve, reject := newPromise[struct{}](&host.loop)
	entered = host.goEntered(func() {
		value, err := resource.spec.Open(ResourceOpenOptions{Signal: signal})
		host.post(func() {
			if err != nil {
				resource.state = "closed"
				reject(err)
				return
			}
			if signal.Err() != nil {
				resource.state = "closed"
				aborted := NewChordError(CodeAborted, "resource "+resource.key+" opened after abort", ChordErrorOptions{})
				if resource.spec.Close == nil {
					reject(aborted)
					return
				}
				host.goTask(func() {
					if err := resource.spec.Close(value); err != nil {
						reject(err)
						return
					}
					reject(aborted)
				})
				return
			}
			resource.value = value
			resource.state = "open"
			*order = append(*order, resource)
			resolve(struct{}{})
		})
	})
	return opened, entered
}

// close closes the resource, off the loop.
func (resource *ResourceImpl) close(order *[]*ResourceImpl) error {
	var rest func() error
	resource.host.run(func() { rest = resource.closeStart(order) })
	return rest()
}

// closeStart is close's synchronous part (object.ts:156-162, up to the await), on the loop: the resource is closed and
// leaves order. It returns the rest, the spec's close of the value, which may block and runs off the loop.
func (resource *ResourceImpl) closeStart(order *[]*ResourceImpl) func() error {
	if resource.state != "open" {
		return func() error { return nil }
	}
	resource.state = "closed"
	if index := slices.Index(*order, resource); index != -1 {
		*order = slices.Delete(*order, index, index+1)
	}
	value := resource.value
	resource.value = nil
	if resource.spec.Close == nil {
		return func() error { return nil }
	}
	return func() error { return resource.spec.Close(value) }
}

// closeAwait is `await resource.close(order)` on the loop (object.ts:156-163): the synchronous part runs now, and the
// spec's close starts once the previously started callback entered, as upstream calls it within the step; the promise
// settles when it returns.
func (resource *ResourceImpl) closeAwait(order *[]*ResourceImpl) *Promise[struct{}] {
	host := resource.host
	rest := resource.closeStart(order)
	closed, resolve, reject := newPromise[struct{}](&host.loop)
	<-host.goEntered(func() {
		if err := runStep(rest); err != nil {
			reject(err)
			return
		}
		resolve(struct{}{})
	})
	return closed
}

func (resource *ResourceImpl) markUnavailable() {
	resource.state = "unavailable"
	resource.value = nil
}

// adopt: a reload kept this resource (same version, deps and exclusivity): its later close is the new declaration's,
// and the old declaration (with the old code it closes over) is no longer referenced.
func (resource *ResourceImpl) adopt(spec ResourceDeclaration[any]) { resource.spec = spec }

// resourceFacade is what Self.Resources returns: the resource, except that Value fails not_ready while any factory
// runs, even if the resource is open (a reload's prepare rerun must not touch the native resource the old code uses).
type resourceFacade struct {
	resource *ResourceImpl
	host     *HostImpl
}

func (facade *resourceFacade) Key() string { return facade.resource.key }

func (facade *resourceFacade) Value() (value any, err error) {
	facade.host.run(func() {
		if facade.host.inFactory > 0 {
			err = NotReady("resource " + facade.resource.key + " (inside a factory)")
			return
		}
		value, err = facade.resource.valueOnLoop()
	})
	return value, err
}

func resourceOf(value any) *ResourceImpl {
	switch value := value.(type) {
	case *ResourceImpl:
		return value
	case *resourceFacade:
		return value.resource
	}
	return nil
}

// taskFn is a declared task.
type taskFn = func(ctx Ctx) error

// methodFunc is a method member: Pi's (args, ctx) => Promise, as a blocking call.
type methodFunc = func(args any, ctx Ctx) (any, error)

// Decls is everything one factory run declared, by kind and key.
type Decls struct {
	cells        *orderedMap[string, *Cell[any]]
	cellSchemas  *orderedMap[string, Schema]
	cellVersions *orderedMap[string, int]
	cellMigrates *orderedMap[string, func(old any, from int) (any, error)]
	refs         *orderedMap[string, RefCore]
	resources    *orderedMap[string, *ResourceImpl]
	// adopted are the resources this run kept from the previous one, with this run's declarations (adopted at commit).
	adopted   *orderedMap[*ResourceImpl, ResourceDeclaration[any]]
	listeners *orderedMap[string, ListenerDecl]
	// tasks are by position (`#0`, `#1`, …): tasks have no names.
	tasks *orderedMap[string, taskFn]
	// derives are by position: derivations have no names and are new at every run.
	derives       *orderedMap[string, *Derived]
	registrations *orderedMap[string, Registration]
}

func newDecls() *Decls {
	return &Decls{
		cells:         newOrderedMap[string, *Cell[any]](),
		cellSchemas:   newOrderedMap[string, Schema](),
		cellVersions:  newOrderedMap[string, int](),
		cellMigrates:  newOrderedMap[string, func(old any, from int) (any, error)](),
		refs:          newOrderedMap[string, RefCore](),
		resources:     newOrderedMap[string, *ResourceImpl](),
		adopted:       newOrderedMap[*ResourceImpl, ResourceDeclaration[any]](),
		listeners:     newOrderedMap[string, ListenerDecl](),
		tasks:         newOrderedMap[string, taskFn](),
		derives:       newOrderedMap[string, *Derived](),
		registrations: newOrderedMap[string, Registration](),
	}
}

func (decls *Decls) refsList() []RefCore               { return decls.refs.Values() }
func (decls *Decls) resourcesList() []*ResourceImpl    { return decls.resources.Values() }
func (decls *Decls) derivesList() []*Derived           { return decls.derives.Values() }
func (decls *Decls) registrationsList() []Registration { return decls.registrations.Values() }

// runCreated are the objects a factory run created (not reused from the previous run).
type runCreated struct {
	refs      []RefCore
	resources []*ResourceImpl
	derives   []*Derived
	// inits are the cells this run created with Init, which fills them before they are used.
	inits []cellInit
}

// cellInit is a cell created with Init and the function that fills it.
type cellInit struct {
	cell *Cell[any]
	init func(ctx Ctx) (any, error)
}

// Run is one factory run: its implementation, declarations and the objects it created.
type Run struct {
	implementation Implementation
	decls          *Decls
	created        *runCreated
}

// Installed is an implementation installed for an code version.
type Installed struct {
	codeVersion    int
	service        *Service
	implementation Implementation
	decls          *Decls
}

// ─── Contexts ────────────────────────────────────────────────────────────────

type ctxFields struct {
	// signals: the ctx is cancelled with the first of them; it stops following them when it closes.
	signals     []context.Context
	rootID      string
	codeVersion int
	principal   Principal
	// holder marks a code holder (a task): every call it makes starts a fresh root.
	holder bool
}

// CtxImpl is the Ctx of one root, call or task.
type CtxImpl struct {
	host        *HostImpl
	signal      context.Context
	release     func()
	sources     []context.Context
	rootID      string
	codeVersion int
	principal   Principal
	holder      bool
	refs        []RefCore
	closed      bool
	// closing is cancelled when the ctx closes: awaited Gets give up.
	closing       context.Context
	cancelClosing context.CancelCauseFunc
}

func newCtxImpl(host *HostImpl, fields ctxFields) *CtxImpl {
	signal, release := linkedSignal(fields.signals)
	//portlint:allow ctxbackground object.ts CtxImpl `#closing = new AbortController()`: the ctx's own controller, no parent
	closing, cancelClosing := context.WithCancelCause(context.Background())
	return &CtxImpl{
		host: host, signal: signal, release: release, sources: fields.signals, rootID: fields.rootID, codeVersion: fields.codeVersion,
		principal: fields.principal, holder: fields.holder, closing: closing, cancelClosing: cancelClosing,
	}
}

func (ctx *CtxImpl) Signal() context.Context { return ctx.signal }
func (ctx *CtxImpl) RootID() string          { return ctx.rootID }
func (ctx *CtxImpl) CodeVersion() int        { return ctx.codeVersion }
func (ctx *CtxImpl) Principal() Principal    { return ctx.principal }

// abortCause is why the ctx is aborted, nil while it is not. It asks the sources too: the signal follows them
// asynchronously (context.AfterFunc), and a check must see an abort that already happened, as upstream's linked signal
// follows its sources synchronously. It runs on the loop.
func (ctx *CtxImpl) abortCause() error {
	if ctx.signal.Err() != nil {
		return context.Cause(ctx.signal)
	}
	for _, source := range ctx.sources {
		if source.Err() != nil {
			return context.Cause(source)
		}
	}
	return nil
}

// check fails unless this ctx may start work on host now (not expired, not aborted, not from another host).
func (ctx *CtxImpl) check(host *HostImpl) error {
	if ctx.host != host {
		return Invalid("foreign_ctx", "ctx belongs to another host", nil)
	}
	var closed bool
	var cause error
	ctx.host.run(func() { closed, cause = ctx.closed, ctx.abortCause() })
	if closed {
		return NewChordError(CodeAborted, "ctx expired: its call already settled", ChordErrorOptions{Reason: "expired"})
	}
	if cause != nil {
		// A forced retirement aborted the root: its code version is gone, so the next call is superseded (retryable).
		if superseded, ok := cause.(*ChordError); ok && superseded.Code == CodeSuperseded { //nolint:errorlint // Pi's instanceof does not unwrap
			return NewChordError(CodeSuperseded, superseded.Message, ChordErrorOptions{Cause: superseded})
		}
		return NewChordError(CodeAborted, "ctx aborted", ChordErrorOptions{Cause: cause})
	}
	return nil
}

// Get holds an object for this call and waits for it to be ready (see awaitReady); the hold is released when the call
// that received this ctx settles.
func (ctx *CtxImpl) Get(typ *Contract, input any, options *GetOptions) (Ref, error) {
	if err := ctx.check(ctx.host); err != nil {
		return nil, err
	}
	merged := GetOptions{Principal: ctx.principal, HasPrincipal: true}
	if options != nil {
		if options.HasPrincipal {
			merged.Principal = options.Principal
		}
		merged.State = options.State
	}
	var ref RefCore
	var err error
	ctx.host.run(func() {
		ref, err = ctx.host.acquire(typ, input, &merged, "call", nil, false)
		if err == nil {
			ctx.refs = append(ctx.refs, ref)
		}
	})
	if err != nil {
		return nil, err
	}
	if err := awaitReady(ctx.host, ref, []context.Context{ctx.signal, ctx.closing}, ctx.host.readyTimeout); err != nil {
		return nil, err
	}
	return ref, nil
}

// child is a context for a call into object: same root and code version, its own call-owned holds, aborted on forced
// disposal. Code inside an object acts as the host principal.
func (ctx *CtxImpl) child(object *ObjectImpl) *CtxImpl {
	return newCtxImpl(ctx.host, ctxFields{
		signals:     []context.Context{ctx.signal, object.callAbort},
		rootID:      ctx.rootID,
		codeVersion: ctx.codeVersion,
		principal:   ctx.host.principal,
	})
}

// close expires the ctx, stops it following its signals, and releases its holds.
func (ctx *CtxImpl) close() {
	ctx.cancelClosing(NewChordError(CodeAborted, "ctx expired: its call already settled", ChordErrorOptions{Reason: "expired"}))
	ctx.host.run(func() {
		ctx.closed = true
		ctx.release()
		ctx.sources = nil
		refs := ctx.refs
		ctx.refs = nil
		for _, ref := range refs {
			ref.Release()
		}
	})
}

// linkedSignal is a signal cancelled, with the cause, by the first of sources to be cancelled, until release stops it
// following them (and so being kept by them). It carries the values of the first source. Not a child of its sources,
// not context.WithCancel(source) either: a context derived from a long-lived one (an object's or an code version's) stays in
// its cancellation tree for as long as that one lives, so every call or task context would keep what hangs on its signal,
// and the code that closes over, alive as long as the source (Pi reload.ts linkedSignal: Node's AbortSignal.any does
// the same).
func linkedSignal(sources []context.Context) (signal context.Context, release func()) {
	derived, cancel := context.WithCancelCause(context.WithoutCancel(sources[0]))
	var mu sync.Mutex
	var stops []func() bool
	released := false
	release = func() {
		mu.Lock()
		released = true
		pending := stops
		stops = nil
		mu.Unlock()
		for _, stop := range pending {
			stop()
		}
	}
	// forward ends the following and cancels the signal with the cause of the source that was cancelled first.
	forward := func(source context.Context) {
		release()
		cancel(context.Cause(source))
	}
	for _, source := range sources {
		if source.Err() != nil {
			forward(source)
			break
		}
		stop := context.AfterFunc(source, func() { forward(source) })
		mu.Lock()
		if released {
			mu.Unlock()
			stop()
			break
		}
		stops = append(stops, stop)
		mu.Unlock()
	}
	return derived, release
}

// raceSignal awaits promise off the loop unless signal aborts first (object.ts:328-340).
// awaitReady waits until ref is ready, at most ms and until a signal is cancelled. On failure the reference is
// released (only it: a binding other references share stays) and the error is returned: the activation error, aborted,
// or unavailable/ready_timeout. It blocks and is called off the loop (object.ts awaitReady).
func awaitReady(host *HostImpl, ref RefCore, signals []context.Context, ms time.Duration) error {
	//portlint:allow ctxbackground object.ts awaitReady `timeout = new AbortController()`: the call's own controller, no parent
	timeout, cancelTimeout := context.WithCancelCause(context.Background())
	defer cancelTimeout(nil)
	var address string
	var ready *Promise[struct{}]
	host.run(func() { address, ready = ref.address(), ref.readyPromise() })
	if ms != neverReady {
		timer := time.AfterFunc(nodeDelay(ms), func() {
			cancelTimeout(NewChordError(CodeUnavailable, fmt.Sprintf("%s was not ready after %s ms", address, formatMs(ms)), ChordErrorOptions{Reason: "ready_timeout"}))
		})
		defer timer.Stop()
	}
	linked, release := linkedSignal(append(slices.Clone(signals), timeout))
	defer release()
	if _, err := raceSignal(ready, linked); err != nil {
		ref.Release()
		if reason, ok := context.Cause(linked).(*ChordError); ok && reason.Reason == "ready_timeout" { //nolint:errorlint // Pi's instanceof does not unwrap
			return reason
		}
		// A signal that was cancelled released the reference (Ctx.close cancels its closing signal first): that
		// cancellation came first, as abort listeners run before the release in Pi.
		if IsChordError(err, CodeReleased) {
			for _, source := range signals {
				if source.Err() != nil {
					return NewChordError(CodeAborted, "call aborted", ChordErrorOptions{Cause: context.Cause(source)})
				}
			}
		}
		return err
	}
	return nil
}

func raceSignal[T any](promise *Promise[T], signal context.Context) (T, error) {
	aborted := func() error {
		return NewChordError(CodeAborted, "call aborted", ChordErrorOptions{Cause: context.Cause(signal)})
	}
	if signal.Err() != nil {
		var zero T
		return zero, aborted()
	}
	value, err := promise.Await(signal)
	if err != nil && signal.Err() != nil && !promise.Settled() {
		return value, aborted()
	}
	return value, err
}

// ─── Refs ─────────────────────────────────────────────────────────────────

// RefOwner is who a hold belongs to. "connection": a peer host over a link; "client": a browser (not a reload
// participant).
type RefOwner string

// RefCore is a claim on an object: a local *RefImpl, or a *RemoteRef on another host (no object here).
type RefCore interface {
	Ref
	address() string
	readyPromise() *Promise[struct{}]
	refObject() *ObjectImpl
}

// refBase are the names a reference answers itself; any other name is a member (object.ts:358-368).
var refBase = map[string]bool{
	"contract": true, "status": true, "ready": true, "variant": true, "shape": true, "error": true, "onShape": true,
	"onStatus": true, "release": true,
}

// statusListener is an OnStatus listener; its pointer is its identity.
type statusListener = func(status RefStatus, err error)

// notifyStatus queues a status notification (after the change that caused it is complete, like every publication).
func notifyStatus(host *HostImpl, listeners *orderedMap[*statusListener, struct{}], status RefStatus, err error) {
	if listeners.Len() == 0 {
		return
	}
	current := listeners.Keys()
	enqueue(&host.loop, func() {
		for _, listener := range current {
			if thrown := callReporting(func() { (*listener)(status, err) }); thrown != nil {
				host.report(thrown)
			}
		}
	})
}

// callReporting runs fn and returns what it threw (a panic), as Pi's try/catch around a listener reports it.
func callReporting(fn func()) (thrown error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				thrown = err
			} else {
				thrown = fmt.Errorf("%v", recovered)
			}
		}
	}()
	fn()
	return nil
}

// refOf is the hold a value is, for `deps: [reference]` and links.
func refOf(value any) RefCore {
	hold, _ := value.(RefCore)
	return hold
}

// readableLike reports whether value has a readable's events and subscribe (object.ts:1303-1307).
func readableLike(value any) bool {
	_, ok := value.(Readable[any])
	return ok
}

// variantOf is an implementation's variant: its string "kind" member.
func variantOf(implementation Implementation) (string, bool) {
	kind, ok := implementation.Value("variant").(string)
	return kind, ok
}

func variantPointer(implementation Implementation) *string {
	if kind, ok := variantOf(implementation); ok {
		return &kind
	}
	return nil
}

// memberReadable is a reference's state member: a stable readable that follows whatever source the current implementation
// exposes under that name. nil until the reference is live (or while the variant lacks the member). Rebinding (reload,
// variant) delivers a reset. Upstream is subscribed only while someone listens.
type memberReadable struct {
	ref      *RefImpl
	name     string
	values   *orderedMap[*func(any), struct{}]
	events   *orderedMap[*func(StateEvent[any]), struct{}]
	source   Readable[any]
	upstream func()
	// unseen are the event listeners that have not had their snapshot yet (added while the member was not readable).
	unseen *orderedMap[*func(StateEvent[any]), struct{}]
	// seen is the value listeners last saw (nil: none); a first upstream snapshot equal to it is not re-announced.
	seen *seenValue
}

type seenValue struct{ value any }

func newMemberReadable(ref *RefImpl, name string) *memberReadable {
	return &memberReadable{
		ref: ref, name: name, values: newOrderedMap[*func(any), struct{}](),
		events: newOrderedMap[*func(StateEvent[any]), struct{}](), unseen: newOrderedMap[*func(StateEvent[any]), struct{}](), upstream: func() {},
	}
}

func (readable *memberReadable) host() *HostImpl { return readable.ref.object.host }

// Value is the member's value. It panics with the ChordError Pi's getter throws: the reference's end error once it
// ended, not_ready until the reference is live, not_in_variant while the variant lacks the member.
func (readable *memberReadable) Value() any {
	var value any
	var err error
	readable.host().run(func() { value, err = readable.read() })
	if err != nil {
		panic(err)
	}
	return value
}

func (readable *memberReadable) read() (any, error) {
	ref := readable.ref
	if ref.status == RefReleased || ref.status == RefGone {
		return nil, ref.terminalError()
	}
	what := ref.addr + "." + readable.name
	if ref.status != RefLive {
		return nil, NotReady(what)
	}
	view := ref.currentView()
	if view == nil {
		return nil, NotReady(what)
	}
	source := readable.current()
	if source == nil {
		shown := "(local)"
		if kind, ok := variantOf(view); ok {
			shown = kind
		}
		return nil, NewChordError(CodeNotInVariant, fmt.Sprintf("%s: %s is not a state member of variant %s", ref.addr, readable.name, shown), ChordErrorOptions{})
	}
	return readValue(source)
}

// readValue reads source; a ChordError it panics with (not_ready of a state init, a remote member before its
// snapshot) is the error, any other panic is not Chord's to catch.
func readValue(source Readable[any]) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			chordError, ok := recovered.(*ChordError)
			if !ok {
				panic(recovered)
			}
			value, err = nil, chordError
		}
	}()
	return source.Value(), nil
}

// hydration: not hydrated while the reference is not live, or while its source (a forwarded remote member) is not.
func (readable *memberReadable) hydration() *Hydration {
	ref := readable.ref
	if ref.status == RefLive {
		return hydrationOf(readable.current())
	}
	return &Hydration{Hydrated: false, WhenHydrated: func() *Promise[struct{}] {
		hydrated, resolve, reject := newPromise[struct{}](&ref.host().loop)
		ref.ready.then(func(_ struct{}, err error) {
			if err != nil {
				reject(err)
				return
			}
			hydration := hydrationOf(readable.current())
			if hydration == nil {
				resolve(struct{}{})
				return
			}
			hydration.WhenHydrated().then(func(_ struct{}, err error) {
				if err != nil {
					reject(err)
					return
				}
				resolve(struct{}{})
			})
		})
		return hydrated
	}}
}

// readable is the current source if its value can be read now (live, in the variant, hydrated).
func (readable *memberReadable) readable() Readable[any] {
	source := readable.current()
	if source == nil {
		return nil
	}
	if hydration := hydrationOf(source); hydration != nil && !hydration.Hydrated {
		return nil
	}
	return source
}

func (readable *memberReadable) Subscribe(listener func(value any)) func() {
	key := &listener
	var failure error
	readable.host().run(func() {
		readable.values.Set(key, struct{}{})
		if source := readable.readable(); source != nil {
			value, err := readValue(source)
			if err != nil {
				failure = err
				return
			}
			readable.seen = &seenValue{value: value}
			readable.safe(func() { listener(value) })
		}
		readable.bind()
	})
	if failure != nil {
		panic(failure)
	}
	return func() { readable.host().run(func() { readable.remove(func() { readable.values.Delete(key) }) }) }
}

func (readable *memberReadable) Events(listener func(event StateEvent[any])) func() {
	key := &listener
	var failure error
	readable.host().run(func() {
		readable.events.Set(key, struct{}{})
		if source := readable.readable(); source != nil {
			value, err := readValue(source)
			if err != nil {
				failure = err
				return
			}
			readable.seen = &seenValue{value: value}
			readable.safe(func() { listener(StateEvent[any]{Type: StateEventSnap, Value: value}) })
		} else {
			readable.unseen.Set(key, struct{}{})
		}
		readable.bind()
	})
	if failure != nil {
		panic(failure)
	}
	return func() {
		readable.host().run(func() {
			readable.unseen.Delete(key)
			readable.remove(func() { readable.events.Delete(key) })
		})
	}
}

// bind (re)subscribes upstream to the current source; a changed source is announced as a reset.
func (readable *memberReadable) bind() {
	var source Readable[any]
	if readable.values.Len()+readable.events.Len() > 0 {
		source = readable.current()
	}
	if source == readable.source {
		return
	}
	readable.upstream()
	readable.upstream = func() {}
	readable.source = source
	if source == nil {
		return
	}
	first := true
	readable.upstream = source.Events(func(event StateEvent[any]) {
		isFirst := first
		first = false
		if isFirst && readable.unseen.Len() == 0 && readable.seen != nil && sameValue(event.Value, readable.seen.value) {
			return
		}
		readable.seen = &seenValue{value: event.Value}
		translated := event
		if isFirst {
			translated = StateEvent[any]{Type: StateEventReset, Value: event.Value}
		}
		events := readable.events.Keys()
		values := readable.values.Keys()
		unseen := readable.unseen.Keys()
		readable.unseen.Clear()
		// Queued: a rebind during a commit must not run observers before the commit completes.
		enqueue(&readable.host().loop, func() {
			for _, listener := range events {
				if !readable.events.Has(listener) {
					continue
				}
				delivered := translated
				if slices.Contains(unseen, listener) {
					delivered = StateEvent[any]{Type: StateEventSnap, Value: event.Value}
				}
				readable.safe(func() { (*listener)(delivered) })
			}
			for _, listener := range values {
				if readable.values.Has(listener) {
					readable.safe(func() { (*listener)(event.Value) })
				}
			}
		})
	})
}

func (readable *memberReadable) stop() {
	readable.values.Clear()
	readable.events.Clear()
	readable.unseen.Clear()
	readable.bind()
}

func (readable *memberReadable) current() Readable[any] {
	view := readable.ref.currentView()
	if view == nil || !readable.ref.inVariant(view, readable.name) {
		return nil
	}
	value := view.Value(readable.name)
	// Cells and derivations, or any readable an `accepts` adapter maps them to.
	if IsSource(value) || readableLike(value) {
		source, _ := value.(Readable[any])
		return source
	}
	return nil
}

func (readable *memberReadable) remove(del func()) {
	del()
	readable.bind()
}

func (readable *memberReadable) safe(fn func()) {
	if err := callReporting(fn); err != nil {
		readable.ref.object.host.report(err)
	}
}

// sameValue is JavaScript === for state values: identity for containers, equality for primitives.
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	switch ta.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Func, reflect.Chan:
		return reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer() &&
			(ta.Kind() != reflect.Slice || reflect.ValueOf(a).Len() == reflect.ValueOf(b).Len())
	}
	if !ta.Comparable() {
		return false
	}
	return a == b
}

// RefImpl is one hold on an object. Its members bind late to the current implementation.
type RefImpl struct {
	object   *ObjectImpl
	contract *Contract
	owner    RefOwner
	addr     string
	ready    *Promise[struct{}]
	status   RefStatus
	err      error

	shapeListeners *orderedMap[*func(Shape), struct{}]
	// goneListeners are notified when the reference becomes gone (links forward it).
	goneListeners   *orderedMap[*func(error), struct{}]
	readables       *orderedMap[string, *memberReadable]
	bindings        map[*Service]Adapter
	views           map[Implementation]Implementation
	callers         *orderedMap[string, methodFunc]
	statusListeners *orderedMap[*statusListener, struct{}]
}

func newRefImpl(object *ObjectImpl, contract *Contract, owner RefOwner) *RefImpl {
	ref := &RefImpl{
		object: object, contract: contract, owner: owner, addr: object.address,
		shapeListeners:  newOrderedMap[*func(Shape), struct{}](),
		goneListeners:   newOrderedMap[*func(error), struct{}](),
		readables:       newOrderedMap[string, *memberReadable](),
		bindings:        map[*Service]Adapter{},
		views:           map[Implementation]Implementation{},
		callers:         newOrderedMap[string, methodFunc](),
		statusListeners: newOrderedMap[*statusListener, struct{}](),
	}
	ref.status = RefLoading
	if object.status == ObjectLive {
		ref.status = RefLive
	}
	ready, resolve, reject := newPromise[struct{}](&object.host.loop)
	ref.ready = ready
	object.ready.then(func(_ struct{}, err error) {
		switch {
		case err != nil:
			reject(err)
		case ref.status == RefGone:
			reject(ref.err)
		case ref.status == RefReleased:
			reject(ref.releasedError())
		default:
			resolve(struct{}{})
		}
	})
	return ref
}

func (ref *RefImpl) host() *HostImpl { return ref.object.host }

func (ref *RefImpl) address() string                  { return ref.addr }
func (ref *RefImpl) readyPromise() *Promise[struct{}] { return ref.ready }
func (ref *RefImpl) refObject() *ObjectImpl           { return ref.object }

// Contract is the consumer contract token this reference was bound with.
func (ref *RefImpl) Contract() *Contract { return ref.contract }

// Status is the reference's status.
func (ref *RefImpl) Status() (status RefStatus) {
	ref.host().run(func() { status = ref.status })
	return status
}

// Ready waits until the reference is live; it fails when the reference is released or gone first, or when ctx ends.
func (ref *RefImpl) Ready(ctx context.Context) error {
	_, err := ref.ready.Await(ctx)
	return err
}

// Err is why the reference is gone, if it is.
func (ref *RefImpl) Err() (err error) {
	ref.host().run(func() { err = ref.err })
	return err
}

// Variant is the current variant, nil for none.
func (ref *RefImpl) Variant() (variant *string) {
	ref.host().run(func() {
		if view := ref.currentView(); view != nil {
			variant = variantPointer(view)
		}
	})
	return variant
}

// Shape is the provider's shape while the reference is live.
func (ref *RefImpl) Shape() (shape *Shape) {
	ref.host().run(func() { shape = ref.shape() })
	return shape
}

func (ref *RefImpl) shape() *Shape {
	if ref.status != RefLive {
		return nil
	}
	installed := ref.object.currentInstalled()
	if installed == nil {
		return nil
	}
	view := ref.safeView(installed)
	if view == nil {
		return nil
	}
	members := make([]string, 0, view.Len())
	for _, key := range view.Keys() {
		if key != "variant" {
			members = append(members, key)
		}
	}
	return &Shape{Variant: variantPointer(view), Members: members, Contract: installed.service.contract.Fingerprint}
}

// OnShape calls listener with every new shape; the returned function removes it.
func (ref *RefImpl) OnShape(listener func(shape Shape)) func() {
	key := &listener
	ref.host().run(func() { ref.shapeListeners.Set(key, struct{}{}) })
	return func() { ref.host().run(func() { ref.shapeListeners.Delete(key) }) }
}

// OnStatus is called after every status change; the returned function removes it.
func (ref *RefImpl) OnStatus(listener func(status RefStatus, err error)) func() {
	key := &listener
	ref.host().run(func() { ref.statusListeners.Set(key, struct{}{}) })
	return func() { ref.host().run(func() { ref.statusListeners.Delete(key) }) }
}

func (ref *RefImpl) statusChanged() {
	notifyStatus(ref.object.host, ref.statusListeners, ref.status, ref.err)
}

// Release releases the hold.
func (ref *RefImpl) Release() {
	ref.host().run(func() {
		if ref.status == RefReleased || ref.status == RefGone {
			return
		}
		ref.status = RefReleased
		ref.stopReadables()
		ref.clearBindings()
		ref.statusChanged()
		ref.object.release(ref)
	})
}

// forgetBindings drops what this reference bound for retired, unless an installed implementation of the object still shares it.
func (ref *RefImpl) forgetBindings(retired *Installed) {
	remaining := ref.object.implementations.Values()
	if !slices.ContainsFunc(remaining, func(other *Installed) bool { return other.service == retired.service }) {
		delete(ref.bindings, retired.service)
	}
	if !slices.ContainsFunc(remaining, func(other *Installed) bool { return sameValue(other.implementation, retired.implementation) }) {
		delete(ref.views, retired.implementation)
	}
}

// clearBindings drops every binding and view: a released or gone reference serves no calls.
func (ref *RefImpl) clearBindings() {
	clear(ref.bindings)
	clear(ref.views)
}

// ─── internal (on the loop) ───

func (ref *RefImpl) live() {
	if ref.status != RefLoading {
		return
	}
	ref.status = RefLive
	ref.statusChanged()
	ref.shapeChanged()
}

func (ref *RefImpl) gone(err error) {
	if ref.status == RefReleased || ref.status == RefGone {
		return
	}
	ref.status = RefGone
	ref.err = err
	ref.stopReadables()
	ref.clearBindings()
	ref.statusChanged()
	for _, listener := range ref.goneListeners.Keys() {
		(*listener)(err)
	}
}

func (ref *RefImpl) shapeChanged() {
	for _, readable := range ref.readables.Values() {
		readable.bind()
	}
	shape := ref.shape()
	if shape == nil {
		return
	}
	listeners := ref.shapeListeners.Keys()
	enqueue(&ref.object.host.loop, func() {
		for _, listener := range listeners {
			if err := callReporting(func() { (*listener)(*shape) }); err != nil {
				ref.object.host.report(err)
			}
		}
	})
}

func (ref *RefImpl) releasedError() error {
	return NewChordError(CodeReleased, "reference "+ref.addr+" released", ChordErrorOptions{})
}

func (ref *RefImpl) terminalError() error {
	if ref.status == RefGone {
		return ref.err
	}
	return ref.releasedError()
}

// viewOf is the implementation as this consumer sees it (through an accepts adapter when bound to another version).
func (ref *RefImpl) viewOf(installed *Installed) (Implementation, error) {
	adapter, ok := ref.bindings[installed.service]
	if !ok {
		var err error
		adapter, err = Bind(installed.service.contract, installed.service.options.Accepts, ref.contract)
		if err != nil {
			return nil, err
		}
		ref.bindings[installed.service] = adapter
	}
	if adapter == nil {
		return installed.implementation, nil
	}
	view, ok := ref.views[installed.implementation]
	if !ok {
		view = adapter(installed.implementation)
		ref.views[installed.implementation] = view
	}
	return view, nil
}

func (ref *RefImpl) currentView() Implementation {
	if ref.status != RefLive {
		return nil
	}
	installed := ref.object.currentInstalled()
	if installed == nil {
		return nil
	}
	return ref.safeView(installed)
}

// safeView is the view, or nil when this consumer no longer binds (reported; calls reject with the bind error).
func (ref *RefImpl) safeView(installed *Installed) Implementation {
	view, err := ref.viewOf(installed)
	if err != nil {
		ref.object.host.report(err)
		return nil
	}
	return view
}

// describe is the member's description in variant kind, or in the first variant that has it when kind is nil
// (variants in declaration order, object.ts describe).
func (ref *RefImpl) describe(kind *string, member string) (MemberDescription, bool) {
	description := ref.contract.Description
	if kind != nil {
		return description.member(*kind, member)
	}
	for _, variant := range description.Variants {
		if found, ok := variant.Value.Get(member); ok {
			return found, true
		}
	}
	return MemberDescription{}, false
}

// inVariant: remote consumers see only the members their contract lists for the current variant.
func (ref *RefImpl) inVariant(view Implementation, name string) bool {
	if ref.contract.Scope != ScopeRemote {
		return true
	}
	_, ok := ref.describe(variantPointer(view), name)
	return ok
}

// Member is a member by name: a method (methodFunc), a state member (Readable[any]), an opaque value, or nil.
func (ref *RefImpl) Member(name string) (member any) {
	if refBase[name] || name == "then" {
		return nil
	}
	ref.host().run(func() { member = ref.member(name) })
	return member
}

func (ref *RefImpl) member(name string) any {
	declared, isDeclared := ref.describe(nil, name)
	view := ref.currentView()
	value := view.Value(name)
	_, isFunction := value.(methodFunc)
	if (isDeclared && declared.Kind == MemberMethod) || (!isDeclared && isFunction) {
		return ref.caller(name)
	}
	if (isDeclared && declared.Kind == MemberState) || (!isDeclared && (IsSource(value) || readableLike(value))) {
		readable, ok := ref.readables.Get(name)
		if !ok {
			readable = newMemberReadable(ref, name)
			ref.readables.Set(name, readable)
		}
		return Readable[any](readable)
	}
	if view == nil || !ref.inVariant(view, name) {
		return nil
	}
	if exposed, ok := value.(Exposed); ok {
		resolved, _ := exposed.resource.valueOnLoop()
		return resolved
	}
	return value
}

func (ref *RefImpl) caller(name string) methodFunc {
	if caller, ok := ref.callers.Get(name); ok {
		return caller
	}
	noArgs := takesNoArgs(ref.contract, name)
	caller := func(args any, ctx Ctx) (any, error) {
		// A method without arguments is called as `ref.name(ctx)`: its args are ignored.
		if noArgs {
			args = nil
		}
		host := ref.object.host
		inFactory := false
		host.run(func() { inFactory = host.inFactory > 0 })
		if inFactory {
			return nil, NewChordError(CodeNotReady, ref.addr+"."+name+" called inside a factory", ChordErrorOptions{})
		}
		var impl *CtxImpl
		if ctx != nil {
			var ok bool
			if impl, ok = ctx.(*CtxImpl); !ok {
				return nil, Invalid("foreign_ctx", "ctx must come from a Chord host", nil)
			}
		}
		if impl == nil || impl.holder {
			// A code holder (task) starts a fresh root per call, cancelled with the holder.
			var options RootOptions
			if impl != nil {
				if err := impl.check(host); err != nil {
					return nil, err
				}
				options.Signal = impl.signal
			}
			return host.Root(func(root Ctx) (any, error) {
				return ref.object.call(ref, name, args, root.(*CtxImpl))
			}, options)
		}
		return ref.object.call(ref, name, args, impl)
	}
	ref.callers.Set(name, caller)
	return caller
}

func (ref *RefImpl) stopReadables() {
	for _, readable := range ref.readables.Values() {
		readable.stop()
	}
}

// ─── Objects ─────────────────────────────────────────────────────────────────

// ObjectStatus is an object's lifetime state.
type ObjectStatus string

const (
	ObjectActivating ObjectStatus = "activating"
	ObjectLive       ObjectStatus = "live"
	ObjectFailed     ObjectStatus = "failed"
	ObjectDisposing  ObjectStatus = "disposing"
	ObjectDisposed   ObjectStatus = "disposed"
)

type gateEvent struct {
	key   string
	event any
}

// objectGate is a reload's gate on an object: roots that touched it are grandfathered, listener events queue.
type objectGate struct {
	grandfathered *orderedMap[string, struct{}]
	queue         []gateEvent
	drained       func()
}

type subscription struct {
	// origin is what the listener was declared on: the resource (not its facade), or the source itself.
	origin      any
	selectFn    func(value any) Subscribable[any]
	unsubscribe func()
	buffer      []any
	buffering   bool
	// subscribe subscribes a state listener when it activates (nil: subscribed while staged).
	subscribe func()
}

// cellVersion is the version of the cell name in decls (default 1).
func cellVersion(decls *Decls, name string) int {
	if version, ok := decls.cellVersions.Get(name); ok {
		return version
	}
	return 1
}

// sameSelect: two select functions are the same if they are identical. Pi also compares their source text, so an
// inline arrow recreated by a factory rerun keeps its subscription (object.ts:723-729); Go functions have neither
// identity nor source text, so two non-nil selects compare by code pointer, which a rerun of the same closure keeps.
func sameSelect(a, b func(value any) Subscribable[any]) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// StagedListeners are listener subscriptions made ahead of a commit: activate switches over, discard drops them.
type StagedListeners struct {
	activate func()
	discard  func()
}

type runningTask struct {
	cancel context.CancelCauseFunc
	done   *Promise[struct{}]
}

// ObjectIdentity is what identifies an object.
type ObjectIdentity struct {
	Address   string
	TypeName  string
	Input     any
	Principal Principal
	// HostLifetime is the one object of a LifetimeHost type.
	HostLifetime bool
}

// ObjectImpl is one live object served by a host.
type ObjectImpl struct {
	host      *HostImpl
	address   string
	typeName  string
	input     any
	principal Principal
	// hostLifetime is the one object of a LifetimeHost type: only such objects may register.
	hostLifetime bool
	status       ObjectStatus
	ready        *Promise[struct{}]
	service      *Service
	// minCodeVersion: calls carrying an older code version are rejected upgrade_conflict (a state version raised or
	// exclusive resource replaced).
	minCodeVersion int
	decls          *Decls
	// pending are declarations of a factory run that has not been installed yet (activation or reload candidate).
	pending         *orderedMap[*Decls, struct{}]
	implementations *orderedMap[int, *Installed]
	refs            *orderedMap[*RefImpl, struct{}]
	leases          *orderedMap[string, int]
	openOrder       []*ResourceImpl
	callAbort       context.Context
	abortCall       context.CancelCauseFunc
	// activationAbort ends with the activation being abandoned.
	activationAbort context.Context
	abortActivation context.CancelCauseFunc
	gate            *objectGate
	// deferredRuntime: served only by a running reload's pending code version: listeners, tasks and registrations start at its
	// commit.
	deferredRuntime bool

	subscriptions *orderedMap[string, *subscription]
	invocations   *orderedMap[*Promise[struct{}], struct{}]
	tasks         *orderedMap[string, *runningTask]
	// stopped are the aborted task runs that have not finished yet.
	stopped *orderedMap[*Promise[struct{}], struct{}]
	// taskRuns is bumped by every start and stop of the tasks: a delayed start that is no longer the latest is dropped.
	taskRuns int
	linger   func()
	disposal *Promise[struct{}]
}

func newObjectImpl(host *HostImpl, identity ObjectIdentity) *ObjectImpl {
	object := &ObjectImpl{
		host: host, address: identity.Address, typeName: identity.TypeName, input: identity.Input,
		principal: identity.Principal, hostLifetime: identity.HostLifetime, status: ObjectActivating,
		ready:           resolvedPromise(&host.loop, struct{}{}),
		decls:           newDecls(),
		pending:         newOrderedMap[*Decls, struct{}](),
		implementations: newOrderedMap[int, *Installed](),
		refs:            newOrderedMap[*RefImpl, struct{}](),
		leases:          newOrderedMap[string, int](),
		subscriptions:   newOrderedMap[string, *subscription](),
		invocations:     newOrderedMap[*Promise[struct{}], struct{}](),
		tasks:           newOrderedMap[string, *runningTask](),
		stopped:         newOrderedMap[*Promise[struct{}], struct{}](),
	}
	//portlint:allow ctxbackground object.ts:775 `readonly callAbort = new AbortController()`: the object's own controller
	object.callAbort, object.abortCall = context.WithCancelCause(context.Background())
	//portlint:allow ctxbackground object.ts:776 `readonly activationAbort = new AbortController()`: the object's own controller
	object.activationAbort, object.abortActivation = context.WithCancelCause(context.Background())
	return object
}

// start activates the object (on the loop); ready settles with the activation. As object.ts:796-812 and #activate's
// `await after?.catch(noop)` (object.ts:820), the factory runs one loop turn later (after after settles), and each
// step after it is a continuation on the loop, so activations started in one section finalize in start order.
func (object *ObjectImpl) start(after *Promise[struct{}]) {
	ready, resolve, reject := newPromise[struct{}](&object.host.loop)
	object.ready = ready
	settle := func(err error) {
		if err == nil {
			for _, ref := range object.refs.Keys() {
				ref.live()
			}
			object.maybeIdle()
			resolve(struct{}{})
			return
		}
		if object.status == ObjectActivating {
			object.status = ObjectFailed
		}
		object.host.forget(object)
		// A ChordError (forbidden, a dependency's error, ...) keeps its code; anything else is unavailable.
		chordError := asChordError(err, "activating "+object.address)
		for _, ref := range object.refs.Keys() {
			ref.gone(chordError)
		}
		reject(chordError)
	}
	// The activation's own promise settles first; ready's reactions follow one turn later (object.ts:797).
	done := func(err error) { object.host.post(func() { settle(err) }) }
	if after == nil {
		object.host.post(func() { object.activate(done) })
		return
	}
	after.then(func(struct{}, error) { object.activate(done) })
}

func (object *ObjectImpl) abandoned() error {
	return NewChordError(CodeAborted, object.address+" abandoned", ChordErrorOptions{})
}

// activate is activation as one transaction, on the loop: factory, opens, then a synchronous finalization (registry
// check and publication, implementations, runtime), each step a continuation of the last. Every failure, including one
// during finalization or a release while listeners start, unwinds everything this activation did (off the loop) and
// then reports to done.
func (object *ObjectImpl) activate(done func(err error)) {
	signal := object.activationAbort
	if signal.Err() != nil {
		done(object.abandoned())
		return
	}
	service := object.host.service(object.typeName)
	if service == nil {
		done(NewChordError(CodeUnavailable, object.typeName+" is not served", ChordErrorOptions{Reason: "unknown_type"}))
		return
	}
	object.service = service

	run, err := object.run(service, nil)
	if err != nil {
		done(err)
		return
	}
	settle := func(err error) {
		if err == nil {
			err = object.finalize(run, signal)
		}
		if err == nil {
			object.pending.Delete(run.decls)
			done(nil)
			return
		}
		object.host.goTask(func() {
			err := object.failActivation(run, err)
			object.host.run(func() {
				object.pending.Delete(run.decls)
				done(err)
			})
		})
	}
	object.openPromise(run.created.resources, run.created.refs, signal).then(func(_ struct{}, err error) {
		if err != nil {
			settle(err)
			return
		}
		if signal.Err() != nil {
			settle(object.abandoned())
			return
		}
		// A local object goes live only with its state, also where it forwards or derives from a remote reference's member.
		object.waitHydrated(run).then(func(_ struct{}, err error) {
			if err != nil {
				settle(err)
				return
			}
			if signal.Err() != nil {
				settle(object.abandoned())
				return
			}
			// Cells declared with init read their first value after the resources are open and before anything reads them.
			object.initCellsPromise(run, signal).then(func(_ struct{}, err error) { settle(err) })
		})
	})
}

// waitHydrated is object.ts waitHydrated, started on the loop: it settles once the state sources a run exposes or
// derives from have their values.
func (object *ObjectImpl) waitHydrated(run *Run) *Promise[struct{}] {
	loop := &object.host.loop
	type exposure struct {
		name  string
		value any
	}
	var exposed []exposure
	var sources []any
	for _, derived := range run.created.derives {
		sources = append(sources, derived)
	}
	if run.implementation != nil {
		for name, value := range run.implementation.All() {
			exposed = append(exposed, exposure{name, value})
			sources = append(sources, value)
		}
	}
	pending := unhydrated(sources...)
	if len(pending) == 0 {
		return resolvedPromise(loop, struct{}{})
	}
	waits := make([]*Promise[struct{}], len(pending))
	for index, hydration := range pending {
		waits[index] = hydration.WhenHydrated()
	}
	return object.withinReadyTimeout(promiseAll(loop, waits), func() []string {
		var waiting []string
		for _, entry := range exposed {
			if hydration := hydrationOf(entry.value); hydration != nil && !hydration.Hydrated {
				waiting = append(waiting, object.address+"."+entry.name)
			}
		}
		for _, derived := range run.created.derives {
			if hydrationOf(derived) != nil {
				waiting = append(waiting, "derive "+derived.Key)
			}
		}
		return waiting
	})
}

// withinReadyTimeout is work within the host's readyTimeout; a timeout names what is still awaited.
func (object *ObjectImpl) withinReadyTimeout(work *Promise[struct{}], waiting func() []string) *Promise[struct{}] {
	ms := object.host.readyTimeout
	if ms == neverReady {
		return work
	}
	result, resolve, reject := newPromise[struct{}](&object.host.loop)
	stop := object.host.after(ms, func() {
		suffix := ""
		if list := waiting(); len(list) > 0 {
			suffix = ", waiting on " + strings.Join(list, ", ")
		}
		reject(NewChordError(CodeUnavailable, fmt.Sprintf("%s was not ready after %s ms%s (a reference cycle across hosts?)", object.address, formatMs(ms), suffix), ChordErrorOptions{Reason: "ready_timeout"}))
	})
	work.then(func(_ struct{}, err error) {
		stop()
		if err != nil {
			reject(err)
			return
		}
		resolve(struct{}{})
	})
	return result
}

// initCellsPromise is object.ts initCells, started on the loop: it fills the cells run created with Init, each in a
// root of its own, in parallel (each starts once the previously started one entered, as an async arrow runs up to its
// first await), and settles with the first failure in start order once all settled. Runs after the run's resources
// are open and its references live, before anything reads the cells.
func (object *ObjectImpl) initCellsPromise(run *Run, signal context.Context) *Promise[struct{}] {
	host := object.host
	loop := &host.loop
	if len(run.created.inits) == 0 {
		return resolvedPromise(loop, struct{}{})
	}
	var all []*Promise[struct{}]
	for _, item := range run.created.inits {
		filled, resolve, reject := newPromise[struct{}](loop)
		all = append(all, filled)
		<-host.goEntered(func() {
			var value any
			_, err := host.Root(func(ctx Ctx) (any, error) {
				err := runStep(func() (err error) {
					value, err = item.init(ctx)
					return err
				})
				return value, err
			}, RootOptions{Signal: signal})
			host.post(func() {
				if err == nil && signal.Err() == nil {
					err = item.cell.Initialize(value)
				}
				if err != nil {
					reject(err)
					return
				}
				resolve(struct{}{})
			})
		})
	}
	initialized, resolve, reject := newPromise[struct{}](loop)
	promiseAllSettled(loop, all).then(func(results []settledResult[struct{}], _ error) {
		for _, result := range results {
			if result.err != nil {
				reject(result.err)
				return
			}
		}
		resolve(struct{}{})
	})
	return initialized
}

// initCells is initCellsPromise awaited off the loop.
func (object *ObjectImpl) initCells(run *Run, signal context.Context) error {
	var initialized *Promise[struct{}]
	object.host.run(func() { initialized = object.initCellsPromise(run, signal) })
	//portlint:allow ctxmapping object.ts initCells awaits Promise.allSettled of every init so all settle before failing; signal reaches each init's root
	_, err := initialized.Await(context.Background())
	return err
}

// finalize is activation's synchronous finalization, on the loop.
func (object *ObjectImpl) finalize(run *Run, signal context.Context) error {
	if signal.Err() != nil {
		return object.abandoned()
	}
	if err := object.host.checkRegistrations(object, run.decls); err != nil {
		return err
	}
	object.decls = run.decls
	for retained, services := range object.host.retainedCodeVersions() {
		if service, ok := services.Get(object.typeName); ok && service == object.service {
			object.implementations.Set(retained, &Installed{codeVersion: retained, service: object.service, implementation: run.implementation, decls: run.decls})
		}
	}
	pending, hasPending := object.host.pendingCodeVersion()
	if !object.implementations.Has(object.host.currentCodeVersion()) && (!hasPending || !object.implementations.Has(pending)) {
		return NewChordError(CodeSuperseded, object.address+": code changed during activation", ChordErrorOptions{})
	}
	for _, derived := range run.created.derives {
		if err := derived.Attach(nil); err != nil {
			return err
		}
	}
	object.refreshSchemas()
	// Served only by a running reload's pending code version: new code must not run (listeners, tasks, registrations)
	// before that reload commits.
	object.deferredRuntime = !object.implementations.Has(object.host.currentCodeVersion())
	if !object.deferredRuntime {
		if run.decls.registrations.Len() > 0 {
			if err := object.host.registriesChanged(object); err != nil {
				return err
			}
		}
		if err := object.startRuntime(); err != nil {
			return err
		}
	}
	// A listener starting up may have released the last hold synchronously: no resurrection.
	if signal.Err() != nil {
		return object.abandoned()
	}
	object.status = ObjectLive
	return nil
}

// failActivation is activation's catch block, off the loop.
func (object *ObjectImpl) failActivation(run *Run, cause error) error {
	var waits []*Promise[struct{}]
	var thrown error
	object.host.run(func() {
		// Invocations started during finalization (listener roots) end before their resources close.
		object.abortCall(cause)
		object.stopListeners()
		object.stopTasks()
		registered := object.decls.registrations.Len() > 0 && !object.deferredRuntime
		object.implementations.Clear()
		object.decls = newDecls()
		if registered {
			thrown = object.host.registriesChanged(nil)
		}
		waits = append(object.invocations.Keys(), object.stopped.Keys()...)
	})
	if thrown != nil {
		return thrown
	}
	//portlint:allow ctxbackground object.ts:865 `await Promise.allSettled([...invocations, ...stopped])` has no abort
	_, _ = promiseAllSettled(&object.host.loop, waits).Await(context.Background())
	if err := object.unwind(run); err != nil {
		object.host.report(err)
	}
	return cause
}

// run runs the factory synchronously, on the loop. With previous, it runs in recording mode for a reload prepare.
// The run's declarations stay in pending (visible to the cycle check) until the caller installs or discards them.
func (object *ObjectImpl) run(service *Service, previous *Decls) (*Run, error) {
	self := newSelfImpl(object, previous)
	discard := func(run *Run) {
		object.pending.Delete(run.decls)
		object.host.goTask(func() {
			if err := object.unwind(run); err != nil {
				object.host.report(err)
			}
		})
	}
	object.pending.Set(self.decls, struct{}{})
	object.host.inFactory++
	var implementation Implementation
	err := callReporting(func() {
		var factoryErr error
		implementation, factoryErr = service.factory(object.input, object.principal, self)
		if factoryErr != nil {
			panic(factoryErr)
		}
	})
	object.host.inFactory--
	if err != nil {
		discard(self.run(nil))
		return nil, err
	}
	run := self.run(implementation)
	if err := validateImplementation(service, run.implementation); err != nil {
		discard(run)
		return nil, err
	}
	return run, nil
}

// open opens resources (dependencies first) and waits for holds, off the loop; settles all before failing.
func (object *ObjectImpl) open(resources []*ResourceImpl, refs []RefCore, signal context.Context) error {
	var opened *Promise[struct{}]
	object.host.run(func() { opened = object.openPromise(resources, refs, signal) })
	//portlint:allow ctxmapping object.ts:923 awaits Promise.allSettled of every open and hold so all settle before failing; signal reaches each open
	_, err := opened.Await(context.Background())
	return err
}

// openPromise is object.ts open (object.ts:905-923), started on the loop: each resource starts opening once its
// dependencies opened, in upstream's start order, and opens concurrently with the others; each joins openOrder when
// it completes. The promise settles with the first failure once every open and hold settled.
func (object *ObjectImpl) openPromise(resources []*ResourceImpl, refs []RefCore, signal context.Context) *Promise[struct{}] {
	loop := &object.host.loop
	opening := map[*ResourceImpl]*Promise[struct{}]{}
	sequence := alreadyEntered
	var openOne func(resource *ResourceImpl) *Promise[struct{}]
	openOne = func(resource *ResourceImpl) *Promise[struct{}] {
		if promise, ok := opening[resource]; ok {
			return promise
		}
		promise, resolve, reject := newPromise[struct{}](loop)
		opening[resource] = promise
		var deps []*Promise[struct{}]
		for _, dep := range resource.deps {
			switch dep := dep.(type) {
			case *ResourceImpl:
				if slices.Contains(resources, dep) {
					deps = append(deps, openOne(dep))
				}
			case RefCore:
				deps = append(deps, dep.readyPromise())
			}
		}
		start := func() <-chan struct{} {
			opened, entered := resource.startOpen(signal, &object.openOrder)
			opened.then(func(_ struct{}, err error) {
				if err != nil {
					reject(err)
					return
				}
				resolve(struct{}{})
			})
			return entered
		}
		promiseAll(loop, deps).then(func(_ struct{}, err error) {
			if err != nil {
				reject(err)
				return
			}
			// Upstream calls each spec.open synchronously in a microtask, so the next starts only once this one
			// returned or awaited: an open starts after the previously started one entered.
			previous := sequence
			select {
			case <-previous:
				sequence = start()
				return
			default:
			}
			gate := make(chan struct{})
			sequence = gate
			object.host.goTask(func() {
				<-previous
				var entered <-chan struct{}
				object.host.run(func() { entered = start() })
				<-entered
				close(gate)
			})
		})
		return promise
	}
	var all []*Promise[struct{}]
	for _, resource := range resources {
		all = append(all, openOne(resource))
	}
	readies := make([]*Promise[struct{}], len(refs))
	for index, hold := range refs {
		readies[index] = hold.readyPromise()
	}
	all = append(all, object.withinReadyTimeout(promiseAll(loop, readies), func() []string {
		var waiting []string
		for _, hold := range refs {
			if hold.Status() == RefLoading {
				waiting = append(waiting, hold.address())
			}
		}
		return waiting
	}))
	opened, resolve, reject := newPromise[struct{}](loop)
	promiseAllSettled(loop, all).then(func(results []settledResult[struct{}], _ error) {
		for _, result := range results {
			if result.err != nil {
				reject(result.err)
				return
			}
		}
		resolve(struct{}{})
	})
	return opened
}

// unwind undoes the objects a run created, off the loop: closes its resources (reverse open order) and releases its
// holds.
func (object *ObjectImpl) unwind(run *Run) error {
	var steps []func() error
	object.host.run(func() {
		for _, derived := range run.created.derives {
			derived.Detach()
		}
		resources := slices.Clone(run.created.resources)
		slices.SortStableFunc(resources, func(a, b *ResourceImpl) int {
			return slices.Index(object.openOrder, b) - slices.Index(object.openOrder, a)
		})
		for _, resource := range resources {
			steps = append(steps, func() error { return resource.close(&object.openOrder) })
		}
		for _, hold := range run.created.refs {
			steps = append(steps, func() error { hold.Release(); return nil })
		}
	})
	return runAll(steps, object.address+": cleanup failed")
}

func (object *ObjectImpl) currentInstalled() *Installed {
	if installed, ok := object.implementations.Get(object.host.currentCodeVersion()); ok {
		return installed
	}
	values := object.implementations.Values()
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

// refreshSchemas gives every state source exposed as a remote state member that member's schema (checked per write
// in dev mode).
func (object *ObjectImpl) refreshSchemas() {
	schemas := map[any][]Schema{}
	for _, installed := range object.implementations.Values() {
		for source, list := range memberSchemas(installed.service, installed.implementation) {
			schemas[source] = append(schemas[source], list...)
		}
	}
	for _, decls := range object.allDecls() {
		for _, cell := range decls.cells.Values() {
			cell.Schemas = schemas[cell]
		}
		for _, derived := range decls.derives.Values() {
			derived.Schemas = schemas[derived]
		}
	}
}

func (object *ObjectImpl) allDecls() []*Decls {
	result := []*Decls{object.decls}
	add := func(decls *Decls) {
		if !slices.Contains(result, decls) {
			result = append(result, decls)
		}
	}
	for _, decls := range object.pending.Keys() {
		add(decls)
	}
	for _, installed := range object.implementations.Values() {
		add(installed.decls)
	}
	return result
}

// ─── runtime: listeners and tasks of the current declarations ───

func listenerOrigin(decl ListenerDecl) any {
	if resource := resourceOf(decl.Source); resource != nil {
		return resource
	}
	return decl.Source
}

// listenSource resolves a listener's subscribable: select(resource value), select(source) or the source itself.
func (object *ObjectImpl) listenSource(key string, decl ListenerDecl) (Subscribable[any], error) {
	value := decl.Source
	if resource := resourceOf(decl.Source); resource != nil {
		var err error
		if value, err = resource.valueOnLoop(); err != nil {
			return nil, err
		}
	}
	source := value
	if decl.Select != nil {
		var subscribable Subscribable[any]
		if err := callReporting(func() { subscribable = decl.Select(value) }); err != nil {
			return nil, err
		}
		source = subscribable
	}
	subscribable := subscribableOf(source)
	if subscribable == nil {
		return nil, Invalid("implementation", "listener "+key+": source has no subscribe()", nil)
	}
	return subscribable, nil
}

// subscribableOf is a source's subscribe: an event source, or a readable (its Subscribe calls the listener with each
// value, so a Readable is a Subscribable).
func subscribableOf(source any) Subscribable[any] {
	if source, ok := source.(Subscribable[any]); ok {
		return source
	}
	return nil
}

// stageListeners subscribes the listeners of decls without delivering, on the loop: events are buffered until
// activate. A listener declared on the same resource (or source) with the same select keeps its subscription without
// calling select again (only its function is swapped, by name), so sources that accept a single subscriber survive
// reloads. It fails, with nothing left subscribed, if any source fails.
func (object *ObjectImpl) stageListeners(decls *Decls) (*StagedListeners, error) {
	fresh := newOrderedMap[string, *subscription]()
	kept := map[string]bool{}
	for _, key := range decls.listeners.Keys() {
		decl, _ := decls.listeners.Get(key)
		origin := listenerOrigin(decl)
		if previous, ok := object.subscriptions.Get(key); ok && sameValue(previous.origin, origin) && sameSelect(previous.selectFn, decl.Select) {
			kept[key] = true
			continue
		}
		source, err := object.listenSource(key, decl)
		if err == nil {
			entry := &subscription{origin: origin, selectFn: decl.Select, unsubscribe: func() {}, buffering: true}
			fresh.Set(key, entry)
			// State (a readable) is subscribed when the listener activates: it delivers the value current then, not one of
			// the candidate before the commit (a new derivation computes its value at the commit).
			if _, readable := source.(Readable[any]); readable {
				entry.buffering = false
				entry.subscribe = func() {
					entry.unsubscribe = source.Subscribe(func(event any) { object.deliver(key, event) })
				}
				continue
			}
			err = callReporting(func() {
				entry.unsubscribe = source.Subscribe(func(event any) {
					if entry.buffering {
						entry.buffer = append(entry.buffer, event)
					} else {
						object.deliver(key, event)
					}
				})
			})
		}
		if err != nil {
			for _, entry := range fresh.Values() {
				entry.unsubscribe()
			}
			return nil, err
		}
	}
	return &StagedListeners{
		activate: func() {
			for _, key := range object.subscriptions.Keys() {
				if kept[key] {
					continue
				}
				subscription, _ := object.subscriptions.Get(key)
				subscription.unsubscribe()
				object.subscriptions.Delete(key)
			}
			for _, key := range fresh.Keys() {
				entry, _ := fresh.Get(key)
				object.subscriptions.Set(key, entry)
				if entry.subscribe != nil {
					if err := callReporting(entry.subscribe); err != nil {
						object.host.report(err)
					}
				}
				buffered := entry.buffer
				entry.buffer = nil
				entry.buffering = false
				for _, event := range buffered {
					object.deliver(key, event)
				}
			}
		},
		discard: func() {
			for _, entry := range fresh.Values() {
				entry.unsubscribe()
			}
		},
	}, nil
}

func (object *ObjectImpl) startRuntime() error {
	staged, err := object.stageListeners(object.decls)
	if err != nil {
		return err
	}
	staged.activate()
	object.startTasks()
	return nil
}

// startTasks starts the tasks of the current declarations. Runs aborted by stopTasks (a reload, a gate) may still be
// finishing: their replacements start once they all finished, or after the task stop deadline (reported), so the old
// and the new run of a task never work side by side. A later start or stop, or the object leaving live, cancels a
// delayed start.
func (object *ObjectImpl) startTasks() {
	object.taskRuns++
	run := object.taskRuns
	host := object.host
	start := func() {
		for _, key := range object.decls.tasks.Keys() {
			object.startTask(key, host.currentCodeVersion())
		}
	}
	if object.stopped.Len() == 0 {
		start()
		return
	}
	deadline := host.taskStopDeadline
	proceed, resolve, _ := newPromise[bool](&host.loop)
	stopTimer := host.after(deadline, func() { resolve(true) })
	allSettledVoid(&host.loop, object.stopped.Keys()).then(func(struct{}, error) { resolve(false) })
	proceed.then(func(missed bool, _ error) {
		stopTimer()
		if run != object.taskRuns || object.status != ObjectLive {
			return
		}
		if missed {
			host.report(NewChordError(CodeAborted, fmt.Sprintf("%s: aborted tasks still running after %s ms; starting their replacements anyway", object.address, formatMs(deadline)), ChordErrorOptions{}))
		}
		start()
	})
}

func (object *ObjectImpl) stopListeners() {
	for _, subscription := range object.subscriptions.Values() {
		subscription.unsubscribe()
	}
	object.subscriptions.Clear()
}

// startTask runs a declared task as an owned background task, on the loop. Like Pi's startTask, which calls the task
// function synchronously up to its first await (object.ts), it returns once the task has entered: returned or parked
// in its first wait.
func (object *ObjectImpl) startTask(key string, codeVersion int) {
	fn, ok := object.decls.tasks.Get(key)
	if !ok {
		return
	}
	//portlint:allow ctxbackground object.ts:1055 `const controller = new AbortController()`: the task's own controller, chained to callAbort below
	controller, cancel := context.WithCancelCause(context.Background())
	ctx := newCtxImpl(object.host, ctxFields{
		signals:     []context.Context{controller, object.callAbort},
		rootID:      "task:" + object.address + ":" + key,
		codeVersion: codeVersion,
		principal:   object.host.principal,
		holder:      true,
	})
	done, resolve, _ := newPromise[struct{}](&object.host.loop)
	task := &runningTask{cancel: cancel, done: done}
	object.tasks.Set(key, task)
	object.host.enter(func() {
		err := callReporting(func() {
			if err := fn(ctx); err != nil {
				panic(err)
			}
		})
		if err != nil && ctx.signal.Err() == nil {
			object.host.report(err)
		}
		ctx.close()
		object.host.run(func() {
			if current, ok := object.tasks.Get(key); ok && current == task {
				object.tasks.Delete(key)
			}
		})
		resolve(struct{}{})
	})
}

// stopTasks aborts running tasks; their completion is awaited at disposal.
func (object *ObjectImpl) stopTasks() {
	for _, abort := range object.detachTasks() {
		abort()
	}
}

// detachTasks is stopTasks with the aborts returned instead of called: a reload commit calls them once the new
// code version's tasks have entered, as Pi's stopped task resumes only after the commit's synchronous step (reload.ts:519-533).
// The stopped runs stay in object.stopped until they finish; a delayed start of replacements is cancelled.
func (object *ObjectImpl) detachTasks() []func() {
	object.taskRuns++
	aborts := make([]func(), 0, object.tasks.Len())
	for _, task := range object.tasks.Values() {
		aborts = append(aborts, func() { task.cancel(NewChordError(CodeAborted, "task stopped", ChordErrorOptions{})) })
		done := task.done
		object.stopped.Set(done, struct{}{})
		done.then(func(struct{}, error) { object.stopped.Delete(done) })
	}
	object.tasks.Clear()
	return aborts
}

// deliver delivers one listener event, in publication order. The invocation is a root that holds a lease on this
// object (a reload gate waits for it like for any other root that touched the object), counts as an invocation
// (disposal waits for it) and is cancelled by forced disposal.
func (object *ObjectImpl) deliver(key string, event any) {
	enqueue(&object.host.loop, func() {
		// The invocation starts synchronously inside the publication, as upstream's host.root does.
		object.startListener(key, event)
	})
}

// startListener is deliver's publication task, on the loop: the root, its lease, the child ctx and the invocation are
// made here, in publication order, and the listener function runs as an owned task that the publication awaits until
// it entered (returned or really waits).
func (object *ObjectImpl) startListener(key string, event any) {
	host := object.host
	if object.gate != nil {
		object.gate.queue = append(object.gate.queue, gateEvent{key: key, event: event})
		return
	}
	decl, ok := object.decls.listeners.Get(key)
	if !ok || (object.status != ObjectLive && object.status != ObjectActivating) {
		return
	}
	codeVersion, root, err := host.enterRoot(host.newRootID(), host.current.id, joinOptions{})
	if err != nil {
		host.report(err)
		return
	}
	object.lease(root)
	child := root.child(object)
	invocation := object.beginInvocation()
	// Upstream runs the listener inside its publication, synchronously up to its first await (object.ts:1098-1108):
	// the publication waits until the function entered, lending it the loop meanwhile.
	host.enter(func() {
		var fnErr error
		err := callReporting(func() { fnErr = decl.Fn(event, child) })
		if err == nil {
			err = fnErr
		}
		child.close()
		invocation.resolve(struct{}{})
		host.run(func() {
			object.endInvocation(invocation)
			host.leaveRoot(codeVersion, root)
		})
		if err != nil {
			host.report(err)
		}
	})
}

// openGate opens the object's gate and replays queued listener events through the current listeners.
func (object *ObjectImpl) openGate() {
	gate := object.gate
	object.gate = nil
	if gate == nil {
		return
	}
	for _, queued := range gate.queue {
		object.deliver(queued.key, queued.event)
	}
}

// ─── calls ───

// admit is admission for a call or write carrying ctx, on the loop.
func (object *ObjectImpl) admit(ctx Ctx) error {
	if object.host.failed != nil {
		return NewChordError(CodeUnavailable, object.host.Name()+" failed and admits no work", ChordErrorOptions{Reason: "host_failed"})
	}
	if !object.host.hasCodeVersion(ctx.CodeVersion()) {
		return NewChordError(CodeSuperseded, fmt.Sprintf("code version %d is retired", ctx.CodeVersion()), ChordErrorOptions{})
	}
	if ctx.CodeVersion() < object.minCodeVersion {
		return NewChordError(CodeUpgradeConflict, fmt.Sprintf("%s was upgraded in code version %d", object.address, object.minCodeVersion), ChordErrorOptions{})
	}
	if object.gate != nil && !object.gate.grandfathered.Has(ctx.RootID()) {
		return NewChordError(CodeUpgradeConflict, object.address+" is being upgraded", ChordErrorOptions{})
	}
	return nil
}

// lease records that the root of ctx touched this object (a reload gate grandfathers it), on the loop.
func (object *ObjectImpl) lease(ctx Ctx) {
	if object.leases.Has(ctx.RootID()) {
		return
	}
	object.leases.Set(ctx.RootID(), ctx.CodeVersion())
	object.host.touch(ctx.RootID(), object)
}

// guard admits a cell write. A method or task writes from off the loop, so the object's state is read on the loop
// (inline when the caller is already there).
func (object *ObjectImpl) guard(ctx Ctx) (err error) {
	object.host.run(func() { err = object.guardOnLoop(ctx) })
	return err
}

func (object *ObjectImpl) guardOnLoop(ctx Ctx) error {
	if object.host.inFactory > 0 {
		return Invalid("read_only", "cells cannot be written while a factory runs", nil)
	}
	if impl, ok := ctx.(*CtxImpl); ok {
		if err := impl.check(object.host); err != nil {
			return err
		}
	}
	if ctx.Signal().Err() != nil {
		return NewChordError(CodeAborted, "write with an aborted ctx", ChordErrorOptions{})
	}
	if object.status == ObjectDisposed {
		return NewChordError(CodeGone, object.address+" is disposed", ChordErrorOptions{})
	}
	if strings.HasPrefix(ctx.RootID(), "task:") {
		return nil
	}
	return object.admit(ctx)
}

// cleanArgs is Value.Clean(schema, Value.Clone(value)); a decoded token's schemas strip nothing.
func cleanArgs(contract *Contract, schema Schema, value any) any {
	cloned := typebox.Clone(value)
	if contract.decoded {
		return cloned
	}
	return typebox.Clean(schema, cloned)
}

// call calls a method of this object through reference, off the loop.
func (object *ObjectImpl) call(ref *RefImpl, name string, args any, ctx *CtxImpl) (any, error) {
	host := object.host
	if err := ctx.check(host); err != nil {
		return nil, err
	}
	var err error
	loading := false
	host.run(func() {
		if ref.status == RefReleased || ref.status == RefGone {
			err = ref.terminalError()
			return
		}
		loading = ref.status == RefLoading
	})
	if err != nil {
		return nil, err
	}
	if loading {
		if _, err := raceSignal(ref.ready, ctx.signal); err != nil {
			return nil, err
		}
	}
	var fn methodFunc
	var input any
	var validated *MemberDescription
	var child *CtxImpl
	takesArgs, returns := true, true
	host.run(func() {
		if ref.status != RefLive || object.status != ObjectLive {
			if ref.status == RefLive {
				err = NewChordError(CodeGone, fmt.Sprintf("%s is %s", object.address, object.status), ChordErrorOptions{})
			} else {
				err = ref.terminalError()
			}
			return
		}
		if err = object.admit(ctx); err != nil {
			return
		}
		installed, ok := object.implementations.Get(ctx.codeVersion)
		if !ok {
			err = NewChordError(CodeSuperseded, fmt.Sprintf("%s has no implementation for code version %d", object.address, ctx.codeVersion), ChordErrorOptions{})
			return
		}
		var view Implementation
		if view, err = ref.viewOf(installed); err != nil {
			return
		}
		kind := variantPointer(view)
		remote := ref.contract.Scope == ScopeRemote
		local := LocalVariant
		wantedKind := &local
		if remote {
			wantedKind = kind
		}
		wanted, hasWanted := ref.describe(wantedKind, name)
		method, isMethod := view.Value(name).(methodFunc)
		if !isMethod || (remote && (!hasWanted || wanted.Kind != MemberMethod)) {
			variant := "(local)"
			if kind != nil {
				variant = *kind
			}
			err = NewChordError(CodeNotInVariant, fmt.Sprintf("%s: %s is not a method of variant %s", object.address, name, variant), ChordErrorOptions{})
			return
		}
		fn = method
		direct := view == installed.implementation
		schema, hasSchema, schemaContract := wanted, hasWanted, ref.contract
		if direct {
			variant := LocalVariant
			if kind != nil {
				variant = *kind
			}
			schemaContract = installed.service.contract
			schema, hasSchema = schemaContract.Description.member(variant, name)
		}
		var described *MemberDescription
		if hasSchema && schema.Kind == MemberMethod {
			described = &schema
		}
		if remote {
			validated = described
		}
		// No arguments: the handler takes only ctx. No result (void): the caller gets undefined, whatever it returned.
		takesArgs = described == nil || described.Args != nil
		returns = described == nil || described.Result != nil
		if takesArgs {
			// The additive rule assumes a consumer sends only what its own schema knows, and a provider sees only what
			// its schema knows: strip with both before validating.
			sent := args
			if remote && hasWanted && wanted.Kind == MemberMethod && wanted.Args != nil {
				sent = cleanArgs(ref.contract, wanted.Args, args)
			}
			input = sent
			if validated != nil && validated.Args != nil {
				input = cleanArgs(schemaContract, validated.Args, sent)
				if err = assertValid("args", object.address+"."+name+" args", validated.Args, input); err != nil {
					return
				}
			}
		}
		object.lease(ctx)
		child = ctx.child(object)
	})
	if err != nil {
		return nil, err
	}
	// object.ts:1186-1205: the invocation's settle handler (removal and #maybeIdle) runs before call's finally releases
	// the holds its ctx made, so an idle callee starts its disposal before those holds' objects do.
	result, err := object.invoke(func() (any, error) {
		var value any
		var fnErr error
		if thrown := callReporting(func() { value, fnErr = fn(input, child) }); thrown != nil {
			return nil, thrown
		}
		return value, fnErr
	})
	child.close()
	if err != nil {
		return nil, err
	}
	if !returns {
		return nil, nil
	}
	if validated != nil && validated.Result != nil {
		if err := assertValid("result", object.address+"."+name+" result", validated.Result, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// invoke runs an invocation (method or listener) that keeps the object alive until it settles, off the loop.
func (object *ObjectImpl) invoke(fn func() (any, error)) (any, error) {
	var invocation *invocation
	object.host.run(func() { invocation = object.beginInvocation() })
	value, err := fn()
	invocation.resolve(struct{}{})
	object.host.run(func() { object.endInvocation(invocation) })
	return value, err
}

// invocation is a running invocation and the resolver of its promise.
type invocation struct {
	promise *Promise[struct{}]
	resolve func(struct{})
}

// beginInvocation counts an invocation that keeps the object alive until endInvocation, on the loop.
func (object *ObjectImpl) beginInvocation() *invocation {
	promise, resolve, _ := newPromise[struct{}](&object.host.loop)
	object.invocations.Set(promise, struct{}{})
	return &invocation{promise: promise, resolve: resolve}
}

// endInvocation drops a settled invocation, on the loop.
func (object *ObjectImpl) endInvocation(invocation *invocation) {
	object.invocations.Delete(invocation.promise)
	if object.invocations.Len() == 0 {
		object.maybeIdle()
	}
}

// rootEnded ends the root's lease (on the loop); the last grandfathered root drains a reload gate.
func (object *ObjectImpl) rootEnded(rootID string) {
	object.leases.Delete(rootID)
	if gate := object.gate; gate != nil && gate.grandfathered.Delete(rootID) && gate.grandfathered.Len() == 0 {
		gate.drained()
	}
}

// ─── lifetime ───

func (object *ObjectImpl) addRef(ref *RefImpl) {
	object.refs.Set(ref, struct{}{})
	if object.linger != nil {
		object.linger()
	}
	object.linger = nil
}

func (object *ObjectImpl) release(ref *RefImpl) {
	if object.refs.Delete(ref) {
		object.maybeIdle()
	}
}

// maybeIdle: no holds and no invocations: dispose (after linger for a live object).
func (object *ObjectImpl) maybeIdle() {
	if object.refs.Len() > 0 || object.invocations.Len() > 0 || object.linger != nil {
		return
	}
	if object.status == ObjectActivating {
		object.background(object.dispose(false, nil))
		return
	}
	if object.status != ObjectLive {
		return
	}
	var linger time.Duration
	if object.service != nil {
		linger = object.service.options.Linger
	}
	if linger <= 0 {
		object.background(object.dispose(false, nil))
		return
	}
	object.linger = object.host.after(linger, func() {
		object.linger = nil
		if object.refs.Len() == 0 && object.invocations.Len() == 0 && object.status == ObjectLive {
			object.background(object.dispose(false, nil))
		}
	})
}

func (object *ObjectImpl) background(disposal *Promise[struct{}]) {
	disposal.then(func(_ struct{}, err error) {
		if err != nil {
			object.host.report(err)
		}
	})
}

// dispose disposes the object (on the loop). Forced: references become gone at once (new calls are rejected) and
// running invocations are cancelled. Either way, resources close only after every invocation settled.
func (object *ObjectImpl) dispose(forced bool, reason error) *Promise[struct{}] {
	if object.disposal == nil {
		object.disposal = object.disposeOnce(forced, reason)
	}
	return object.disposal
}

func (object *ObjectImpl) disposeOnce(forced bool, reason error) *Promise[struct{}] {
	done, resolve, reject := newPromise[struct{}](&object.host.loop)
	wasActivating := object.status == ObjectActivating
	object.status = ObjectDisposing
	object.host.forget(object)
	if object.linger != nil {
		object.linger()
	}
	object.linger = nil
	err := reason
	if err == nil {
		err = NewChordError(CodeGone, object.address+" was disposed", ChordErrorOptions{})
	}
	if forced {
		for _, ref := range object.refs.Keys() {
			ref.gone(err)
		}
		object.abortCall(err)
	}
	object.abortActivation(err)
	var decls []*Decls
	var invocations []*Promise[struct{}]
	settle := func() {
		for _, ref := range object.refs.Keys() {
			ref.gone(err)
		}
		decls = object.allDecls()
		object.stopTasks()
		object.stopListeners()
		invocations = object.invocations.Keys()
	}
	if !wasActivating {
		settle()
	}
	// The rest is a chain of loop steps (object.ts:1260-1300): await the invocations, then the synchronous cleanup,
	// then runAll of the closes, each resource close started like upstream's await (closeAwait). Disposals therefore
	// close in the order they started, as upstream's microtasks do.
	afterInvocations := func(struct{}, error) {
		registered := !object.deferredRuntime && slices.ContainsFunc(decls, func(set *Decls) bool { return set.registrations.Len() > 0 })
		object.implementations.Clear()
		object.decls = newDecls()
		if registered {
			if thrown := object.host.registriesChanged(nil); thrown != nil {
				// registriesChanged threw before the try block: the object stays disposing (object.ts:1281).
				reject(thrown)
				return
			}
		}
		refs := newOrderedMap[RefCore, struct{}]()
		for _, set := range decls {
			for _, derived := range set.derives.Values() {
				derived.Detach()
			}
			for _, hold := range set.refs.Values() {
				refs.Set(hold, struct{}{})
			}
		}
		loop := &object.host.loop
		steps := []func() *Promise[struct{}]{func() *Promise[struct{}] { return allSettledVoid(loop, object.stopped.Keys()) }}
		order := slices.Clone(object.openOrder)
		slices.Reverse(order)
		for _, resource := range order {
			steps = append(steps, func() *Promise[struct{}] { return resource.closeAwait(&object.openOrder) })
		}
		for _, hold := range refs.Keys() {
			steps = append(steps, func() *Promise[struct{}] {
				hold.Release()
				return resolvedPromise(loop, struct{}{})
			})
		}
		runAllOnLoop(loop, steps, object.address+": disposal failed").then(func(_ struct{}, err error) {
			object.status = ObjectDisposed
			object.host.disposed(object)
			if err != nil {
				reject(err)
				return
			}
			resolve(struct{}{})
		})
	}
	awaitInvocations := func() { allSettledVoid(&object.host.loop, invocations).then(afterInvocations) }
	if wasActivating {
		object.ready.then(func(struct{}, error) {
			settle()
			awaitInvocations()
		})
	} else {
		awaitInvocations()
	}
	return done
}

// allSettledVoid is Promise.allSettled over promises, settling without a value once all have.
func allSettledVoid(loop *loop, promises []*Promise[struct{}]) *Promise[struct{}] {
	done, resolve, _ := newPromise[struct{}](loop)
	promiseAllSettled(loop, promises).then(func([]settledResult[struct{}], error) { resolve(struct{}{}) })
	return done
}

// runAllOnLoop is errors.ts runAll (:63-74) on the loop: each step runs after the previous step's promise settled,
// and the result is the one failure or an AggregateError of several.
func runAllOnLoop(loop *loop, steps []func() *Promise[struct{}], message string) *Promise[struct{}] {
	done, resolve, reject := newPromise[struct{}](loop)
	var failures []error
	var next func(index int)
	next = func(index int) {
		if index == len(steps) {
			switch len(failures) {
			case 0:
				resolve(struct{}{})
			case 1:
				reject(failures[0])
			default:
				reject(&AggregateError{Message: message, Errors: failures})
			}
			return
		}
		var step *Promise[struct{}]
		if thrown := callReporting(func() { step = steps[index]() }); thrown != nil {
			failures = append(failures, thrown)
			next(index + 1)
			return
		}
		step.then(func(_ struct{}, err error) {
			if err != nil {
				failures = append(failures, err)
			}
			next(index + 1)
		})
	}
	next(0)
	return done
}

// memberSchemas are the remote state-member schemas an implementation exposes, by source.
func memberSchemas(service *Service, implementation Implementation) map[any][]Schema {
	schemas := map[any][]Schema{}
	contract := service.contract
	if contract.Scope != ScopeRemote {
		return schemas
	}
	kind, _ := variantOf(implementation)
	members, _ := contract.Description.Variants.Get(kind)
	for _, member := range members {
		description := member.Value
		source := implementation.Value(member.Name)
		if description.Kind != MemberState || !IsSource(source) {
			continue
		}
		schemas[source] = append(schemas[source], description.Schema)
	}
	return schemas
}

func validateImplementation(service *Service, implementation Implementation) error {
	contract := service.contract
	fail := func(message string) error { return Invalid("implementation", contract.Name+": "+message, nil) }
	if implementation == nil {
		return fail("factory must return an object")
	}
	var members Fields[MemberDescription]
	if contract.Scope == ScopeRemote {
		kind, isString := implementation.Value("variant").(string)
		variant, ok := contract.Description.Variants.Get(kind)
		if !isString || !ok {
			return fail("unknown variant " + jsString(implementation.Value("variant")))
		}
		members = variant
		for _, key := range implementation.Keys() {
			if key != "variant" && !members.Has(key) {
				return fail(key + " is not in variant " + kind)
			}
		}
	} else {
		members, _ = contract.Description.Variants.Get(LocalVariant)
	}
	for _, member := range members {
		name, description := member.Name, member.Value
		value, present := implementation.Get(name)
		if _, isMethod := value.(methodFunc); description.Kind == MemberMethod && !isMethod {
			return fail(name + " must be a method")
		}
		if description.Kind == MemberState && !IsSource(value) && !readableLike(value) {
			return fail(name + " must be a cell, a derivation or a held object's state member")
		}
		if description.Kind == MemberOpaque && (!present || value == nil) {
			return fail(name + " is missing")
		}
	}
	return nil
}

// jsString is JavaScript's String(value) for the values an implementation's kind can hold.
func jsString(value any) string {
	if value == nil {
		return "undefined"
	}
	return fmt.Sprint(value)
}

// ─── The factory's self ──────────────────────────────────────────────────────

// SelfImpl is the self one factory run gets.
type SelfImpl struct {
	object   *ObjectImpl
	previous *Decls
	decls    *Decls
	created  *runCreated
}

func newSelfImpl(object *ObjectImpl, previous *Decls) *SelfImpl {
	return &SelfImpl{object: object, previous: previous, decls: newDecls(), created: &runCreated{}}
}

func (s *SelfImpl) run(implementation Implementation) *Run {
	return &Run{implementation: implementation, decls: s.decls, created: s.created}
}

// unique refuses a second declaration of name of one kind in this run (TypeScript cannot see string names).
func unique[V any](s *SelfImpl, existing *orderedMap[string, V], kind, name string) error {
	if name == "" {
		return &TypeError{Message: kind + " needs a name"}
	}
	if existing.Has(name) {
		return Invalid("duplicate", fmt.Sprintf("%s: duplicate %s name %s", s.object.typeName, kind, name), nil)
	}
	return nil
}

// stateInit is the initializer of an initial value that is a StateInit with an Init (object.ts isStateInit).
func stateInit(initial any) (func(ctx Ctx) (any, error), bool) {
	switch typed := initial.(type) {
	case StateInit:
		return typed.Init, typed.Init != nil
	case *StateInit:
		if typed != nil {
			return typed.Init, typed.Init != nil
		}
	}
	return nil, false
}

// State declares a state cell (on the loop: the factory runs there).
func (s *SelfImpl) State(name string, schema Schema, initial any, options *StateOptions) (*Cell[any], error) {
	if err := unique(s, s.decls.cells, "state", name); err != nil {
		return nil, err
	}
	version := 1
	if options != nil && options.Version != 0 {
		version = options.Version
	}
	if version < 1 {
		return nil, &TypeError{Message: "state " + name + ": version must be a positive integer"}
	}
	host := s.object.host
	var cell *Cell[any]
	if s.previous != nil {
		cell, _ = s.previous.cells.Get(name)
	}
	if cell == nil {
		var err error
		options := SourceOptions{Dev: host.dev, Report: host.report, Exec: host.run, loop: &host.loop}
		if init, isInit := stateInit(initial); isInit {
			cell, err = NewCellAwaitingInit[any](name, schema, s.object.guard, options)
			if err == nil {
				s.created.inits = append(s.created.inits, cellInit{cell: cell, init: init})
			}
		} else {
			cell, err = NewCell[any](name, schema, initial, s.object.guard, options)
		}
		if err != nil {
			return nil, err
		}
	}
	s.decls.cells.Set(name, cell)
	s.decls.cellSchemas.Set(name, schema)
	s.decls.cellVersions.Set(name, version)
	if options != nil && options.Migrate != nil {
		s.decls.cellMigrates.Set(name, options.Migrate)
	}
	return cell, nil
}

// Get makes an object-owned hold.
func (s *SelfImpl) Get(typ *Contract, input any, options *GetOptions) (Ref, error) {
	host := s.object.host
	// A prepare rerun (recording mode) binds new holds against the reload's pending contracts: a breaking provider
	// change and its updated consumer reload together.
	candidate := s.previous != nil
	principal := host.principal
	if options != nil && options.HasPrincipal {
		principal = options.Principal
	}
	address, err := host.addressOf(typ, input, principal, candidate)
	if err != nil {
		return nil, err
	}
	if declared, ok := s.decls.refs.Get(address); ok {
		if declared.Contract().Fingerprint != typ.Fingerprint {
			return nil, Invalid("duplicate", fmt.Sprintf("%s: %s held twice with different contracts", s.object.typeName, typ.Name), nil)
		}
		return declared, nil
	}
	if s.previous != nil {
		if existing, ok := s.previous.refs.Get(address); ok && existing.Contract().Fingerprint == typ.Fingerprint {
			if status := existing.Status(); status != RefGone && status != RefReleased {
				s.decls.refs.Set(address, existing)
				return existing, nil
			}
		}
	}
	ref, err := host.acquire(typ, input, options, "object", s.object, candidate)
	if err != nil {
		return nil, err
	}
	s.decls.refs.Set(address, ref)
	s.created.refs = append(s.created.refs, ref)
	return ref, nil
}

// Resource declares a resource named name.
func (s *SelfImpl) Resource(name string, declaration ResourceDeclarer) (Resource[any], error) {
	if err := unique(s, s.decls.resources, "resource", name); err != nil {
		return nil, err
	}
	spec := declaration.resourceDeclaration()
	deps := make([]any, 0, len(spec.Deps))
	for _, dep := range spec.Deps {
		var resolved any
		if ref := refOf(dep); ref != nil {
			resolved = ref
		} else if resource := resourceOf(dep); resource != nil {
			resolved = resource
		} else {
			return nil, &TypeError{Message: "resource " + name + ": deps must be references or resources of this object"}
		}
		deps = append(deps, resolved)
	}
	var existing *ResourceImpl
	if s.previous != nil {
		existing, _ = s.previous.resources.Get(name)
	}
	kept := existing != nil && existing.spec.Version == spec.Version && existing.exclusive() == spec.Exclusive &&
		len(existing.deps) == len(deps) && slices.EqualFunc(existing.deps, deps, sameValue)
	resource := existing
	if kept {
		s.decls.adopted.Set(existing, spec)
	} else {
		resource = newResourceImpl(s.object.host, name, spec, deps)
		s.created.resources = append(s.created.resources, resource)
	}
	s.decls.resources.Set(name, resource)
	return &resourceFacade{resource: resource, host: s.object.host}, nil
}

// On declares a listener named name.
func (s *SelfImpl) On(name string, source any, handler func(event any, ctx Ctx) error) error {
	return s.OnSelect(name, source, nil, handler)
}

// OnSelect declares a listener named name that subscribes to selector(value).
func (s *SelfImpl) OnSelect(name string, source any, selector func(value any) Subscribable[any], handler func(event any, ctx Ctx) error) error {
	if err := unique(s, s.decls.listeners, "listener", name); err != nil {
		return err
	}
	if handler == nil {
		return &TypeError{Message: "listener " + name + ": the handler must be a function"}
	}
	s.decls.listeners.Set(name, ListenerDecl{Source: source, Select: selector, Fn: handler})
	return nil
}

// Task declares a task, by position.
func (s *SelfImpl) Task(fn func(ctx Ctx) error) error {
	if fn == nil {
		return &TypeError{Message: "a task must be a function"}
	}
	s.decls.tasks.Set(fmt.Sprintf("#%d", s.decls.tasks.Len()), fn)
	return nil
}

// Derive declares a derivation, by position: new at every run.
func (s *SelfImpl) Derive(sources Sources, fn func(values ...any) any) (Readable[any], error) {
	if fn == nil {
		return nil, &TypeError{Message: "a derivation needs a function"}
	}
	host := s.object.host
	key := fmt.Sprintf("#%d", s.decls.derives.Len())
	derived, err := NewDerived(key, sources, fn, SourceOptions{Dev: host.dev, Report: host.report, loop: &host.loop})
	if err != nil {
		return nil, err
	}
	s.created.derives = append(s.created.derives, derived)
	s.decls.derives.Set(key, derived)
	return derived, nil
}

// Expose is a member backed by one of this object's resources.
func (s *SelfImpl) Expose(resource Resource[any]) (Exposed, error) {
	resolved := resourceOf(resource)
	if resolved == nil || !slices.Contains(s.decls.resources.Values(), resolved) {
		return Exposed{}, &TypeError{Message: "expose takes a resource declared by this object"}
	}
	return Exposed{resource: resolved}, nil
}

// Register registers entries in registry (objects of LifetimeHost only).
func (s *SelfImpl) Register(registry Registry[any], list []Entry) error {
	if !s.object.hostLifetime {
		return Invalid("not_host_lifetime", s.object.typeName+`: only objects of lifetime "host" may register`, nil)
	}
	for _, entry := range list {
		id := registry.Name + "\x00" + entry.Name
		if err := unique(s, s.decls.registrations, "register", id); err != nil {
			return err
		}
		s.decls.registrations.Set(id, Registration{Registry: registry, Key: entry.Name, Value: entry.Value})
	}
	return nil
}
