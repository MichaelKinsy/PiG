package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/host/attempt.ts. Attempts (§6, §13, §14, §15): one activation of an object, a fresh
// instance each time. It is the `self` its factory declares with (handles to the object's documents and jobs, under its
// seal token); it runs the activation steps, each failing with its own kind; it takes the live object's calls and starts
// its routines and jobs; and it ends with its seal (slice 6a §1.3), the drain and the closes of its natives.
//
// Async contract. `activate` returns a promise (a keyedPromise settled in a section): the steps Pi awaits are
// continuations on the host's Loop in Pi's microtask order (`await undefined` is a `Queue`, an awaited Promise a `then`),
// and each step of Pi that blocks on a store, a native or a followed runtime (`documents.load`, `prepareTables`,
// `job.load`, `native.open`, `follow.attach`) is an owned task of the loop (`awaitTask`) that settles its promise in a
// section. `call` admits the call in the caller's section and runs the handler (a blocking Go function) on an owned task;
// its result settles later. `close` blocks (the drain, the natives' closes, the job writes) and is called outside a section.
//
// Forced Go differences.
//   - `Self`'s declarations throw in Pi outside the factory, on a repeated name, a refused claim and so on: they panic with
//     the error Pi throws, and the factory runs under tryPlugin, which returns it as the activation's `broken` cause.
//   - The factory of a local type and of a remote one are LocalFactory and RemoteFactory (`Factory<C, P>`'s two arms); they
//     return a contracts.Implementation (members by name). `Self` is one interface for the plain and the store arm:
//     `StoreSelf`'s extras are on the same attempt, and the type of the factory's `self` (Pi: by the contract's params)
//     is a type-level check: a factory of a type with params that calls them is refused at run time by the same
//     `persist needs a stable identity` check that guards its data.
//   - `HandleOf<D>` is `any` (a *documents.Singleton or a *documents.Keyed by the definition's `Keyed`); `self.derive` and
//     the other sources are `chord.Watchable[delta.JsonValue]` through Expose, which is also how any state source is an
//     implementation's state member. `self.registry` returns the registry's value source itself. Pi's generics are
//     type-level (owner class a): the value types are `any`.
//   - Pi's `Context` aborts through an AbortController's signal: the attempt's `abort` is the cancel function of its
//     `opening` context, called at once by `abandon`; contexts derived from it abort on a goroutine of their own.
//   - The attempt's running work is a slice of nativeWork (insertion order), the drain waits on them.

// LocalFactory is a local type's factory: synchronous and declarative, run once per attempt. It returns the members of its
// contract as an Implementation (Variant empty). The type of `self` for a type with empty params is StoreSelf: assert it.
//
// It is an alias of the function type, so a function literal served as `any` is a LocalFactory for the host's type
// switch.
type LocalFactory = func(params *delta.JsonObject, self Self) (contracts.Implementation, error)

// RemoteFactory is a remote type's factory: it also gets the principal in its object's identity and returns one variant,
// `{ variant, members }` as an Implementation.
//
// An alias of the function type, as LocalFactory.
type RemoteFactory = func(params *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error)

// Self is what a factory declares with. Declarations (`State`, `Derive`, `Get`, `Registry`, `Writable`, `RetryOn`, `Open`,
// `On`, `OnLive`, `Job`) are valid only while it runs: elsewhere they panic with the error Pi throws.
type Self interface {
	RoutineDeclarations
	// State is this object's documents of definition, in memory for its life (across its attempts): a *documents.Singleton,
	// or a *documents.Keyed when the definition is keyed.
	State(definition documents.AnyDefinition) any
	// Derive is a value computed from sources (JSON watchables and native ones: a writable, a registry value, `ref.state`)
	// by the pure, synchronous compute; always JSON.
	Derive(sources []any, compute func(values []any) (delta.JsonValue, error)) chord.Watchable[delta.JsonValue]
	// Get is a reference to another object, live once this object is, released when it ends.
	Get(typ *contracts.Contract, params *delta.JsonObject) *Reference
	// Registry is registry R's composed value as this plugin's version of R sees it: a declared dependency, as Get is (the
	// attempt parks until it has a usable value: `failed { waiting }` while it has none, woken when it has). Read Value at
	// use; once live, a later failure of the registry keeps its last good value while every contribution it was built from
	// is still in the table.
	Registry(registry registries.Registry[any, any]) *registries.RegistryValueSource
	// Writable is an in-process value this attempt sets (identity compared): a contribution, a derive source and an `On`
	// source, never a state member; its watches end with the attempt, and Set after its end returns `released`.
	Writable(initial any) state.Writable[any]
	// RetryOn declares wake signals: a new value of any of sources (registry values; constants never change) retries this
	// object while it is failed, as `ref.retry()` does, and once more at once if its current activation fails. Followed from
	// the factory's return until the next attempt's declaration or the object's end; failures wake nothing.
	RetryOn(sources ...any)
	// Open is a native, opened during activation after the references are live and the earlier natives are open, and closed
	// in reverse when the attempt ends. open gets a context aborted when the attempt is abandoned; close one aborted at the
	// close timeout. Return `data.Broken(err)` from open when retrying is pointless.
	Open(
		open func(ctx context.Context) (any, error),
		closer func(value any, reason NativeCloseReason, ctx context.Context) error,
		options ...NativeOptions,
	) Native[any]
	// Tx runs fn as one transaction of this object (§9).
	Tx(fn func(tx *documents.Transaction) (any, error), ctx context.Context) (any, error)
	// Follow is a source following another runtime's synchronous observer (Durable's views, slice 6b §3): attach runs
	// during activation once the natives opened (an error or no attachment fails the activation `transient`), and the
	// observer is detached when the attempt ends. Its runtime failing fails this object `transient`; its `ended` is final,
	// the source keeping its last value. A state member, a derive source, an `On` source.
	Follow(attach FollowAttach[delta.JsonValue]) chord.Watchable[delta.JsonValue]
	// Job is this object's job definition (§15), once per job name: Run runs each started run outside transactions, Finish
	// records its outcome in one. Starts are taken once the object is live.
	Job(definition *JobDefinition, implementation JobImplementation) JobHandle
}

// StoreSelf is what the factory of a `local` type with empty params declares with: one object per host and type, so its
// data may persist (P3).
type StoreSelf interface {
	Self
	// Table is this object's table definition in the host's store, once per table name.
	Table(definition *store.TableDefinition) *store.TableHandle
	// Contribute contributes value to registry R from the moment this object is live until it ends, fails or restarts (R21:
	// a host-lifetime object): a plain value, or a Chord watchable of one (followed: its change recomposes). At most one
	// per registry.
	Contribute(registry registries.Registry[any, any], value any)
}

// notLive is the error of an object, or a getter, that waited ms without the object going live.
func notLive(label string, ms time.Duration) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeUnavailable,
		Message: label + ": did not become live within " + strconv.FormatInt(ms.Milliseconds(), 10) + " ms",
	}, nil)
}

// activationKind is how an attempt's activation ended.
type activationKind uint8

const (
	// activationLive: the attempt is live.
	activationLive activationKind = iota
	// activationFailed: it failed with cause.
	activationFailed
	// activationEnded: it was abandoned because its object ended, with err.
	activationEnded
)

// activation is how an attempt's activation ended; only the fields of its kind are set. Pi's `NotLive` is an activation
// of kind failed or ended.
type activation struct {
	kind  activationKind
	cause data.FailureCause
	err   *data.ChordError
}

// registryDeclarations are an attempt's registry reads, contributions and wake signals, and its writables (slice 8).
type registryDeclarations struct {
	// registries are the registry values it declared (`self.registry`): dependencies, as its references are.
	registries []*registries.RegistryValueSource
	// reads release its registry reads (with its references): a read keeps an older version's mapping.
	reads []func()
	// retrySources are its wake signals (`self.retryOn`), handed to its object right after the factory.
	retrySources []retrySourceType
	// writables are its writables, whose watches end with it.
	writables []interface{ Detach(err error) }
	// declared are its contributions as declared, resolved during activation into contributions.
	declared      []declaredContribution
	contributions []*registries.RegistryContribution
}

type declaredContribution struct {
	registry registries.Registry[any, any]
	value    any
}

// declaredReference is a reference this attempt declared with `self.get` (params checked), bound during activation.
type declaredReference struct {
	binding  *binding
	contract *contracts.Contract
	params   checkedParams
}

// declaredTable is a table this attempt declared, by SQL name.
type declaredTable struct {
	handle *store.TableHandle
	name   string
	sql    string
}

// runningWork is a handler, routine call or job run in the drain, with its bookkeeping.
type runningWork struct {
	work    *nativeWork
	cleanup func()
}

// attempt is one attempt of an object. Activation: factory, documents hydrated (the object's slots), references bound, then
// parked until every dependency is live (no timer: the deadline paused), then its own steps: natives opened one after
// another, followed sources attached, members checked, derives computed; bounded by the host's `readyTimeout`. Ending:
// sealed at once (calls aborted, its transactions refused and the one holding the line discarded, its job invocations cut
// off), then the drain, the closes in reverse, derives detached; its object releases its references. Documents and job runs
// stay on the object.
type attempt struct {
	object *chordObject
	// ctx is the object's context: the natives' closes run under it.
	ctx context.Context
	// abort aborts opening when the attempt is abandoned or ends: openers, activation's waits and calls run under it.
	abort func(cause error)
	// opening is the object's context under this attempt's signal.
	opening context.Context
	// token is the seal token its handles and transactions carry (the seal fence).
	token *documents.SealToken
	// declared are the documents it declared, in declaration order ("declared twice" is per attempt).
	declared      []documents.AnyDefinition
	declaredNames map[string]struct{}
	// pins are its factory's `at(key)` pins: the object holds them from its factory's return to the next one's (rule 6).
	pins      []func()
	declaring bool
	// isLive: it went live: its derives' failures are reported from then on (`derive_failed`).
	isLive bool
	// members are the checked implementation and, for a remote object, its variant, once activation checked them.
	members *members
	variant *string
	derives []*state.Derive
	// follows are its followed sources, attached in declaration order after the natives.
	follows  []*followSource
	routines routines
	jobs     []*job
	jobNames map[string]struct{}
	// tables are the tables it declared, by name.
	tables     []declaredTable
	tableNames map[string]struct{}
	// references are those this attempt declared with `self.get`.
	references []declaredReference
	natives    []nativeSlot
	// running are running calls' handlers, routine calls and job runs, each with its bookkeeping (call counts, `ctx.get`
	// references): an ending attempt waits for them (the drain); the bookkeeping runs once, when its work settles or at
	// teardown for work stuck past the drain (decision "The seal fence" (4)).
	running []*runningWork
	// abandoned is what activation returns once the attempt was abandoned: a timeout, a cycle through its object, or its
	// end; nil is Pi's undefined.
	abandoned *activation
	// parked are its dependencies while it is parked on them (the dependency check).
	parked    []dependency
	isParking bool
	// moved: a dependency changed since the last check: the parked attempt checks again (one check per batch of changes).
	moved bool
	// changed is what a parked attempt waiting for a change waits on; resolved after the change's batch.
	changed *keyedPromise[struct{}]
	// endedEarly is a followed source's end while this attempt activated: it fails as it goes live (if its activation
	// returned).
	endedEarly *data.ChordError
	// registry are its registry reads, contributions, wake signals and writables, made by the first: most attempts have none.
	registry *registryDeclarations
}

var (
	_ StoreSelf                        = (*attempt)(nil)
	_ documents.KeyDeclarer            = (*attempt)(nil)
	_ registries.RegistryWaiter        = (*attempt)(nil)
	_ store.TableOwner                 = (*attemptTableOwner)(nil)
	_ chord.Watchable[delta.JsonValue] = (*exposedWatchable)(nil)
)

func newAttempt(object *chordObject, ctx context.Context) *attempt {
	opening, abort := chordctx.WithCancel(ctx)
	return &attempt{
		object: object, ctx: ctx, abort: abort, opening: opening, token: documents.NewSealToken(),
		declaredNames: map[string]struct{}{}, jobNames: map[string]struct{}{}, tableNames: map[string]struct{}{},
	}
}

func (a *attempt) loop() sectionLoop { return a.object.host.loop() }

// declarationError is the Error Pi's declaration throws.
func declarationError(format string, args ...any) error { return fmt.Errorf(format, args...) }

// State implements Self.
func (a *attempt) State(definition documents.AnyDefinition) any {
	a.assertDeclaring("state")
	what := `state "` + definition.Name + `"`
	if _, twice := a.declaredNames[definition.Name]; twice {
		panic(declarationError("%s: %s is declared twice", a.object.text, what))
	}
	if definition.Persist {
		a.assertPersistable(what)
		a.mustClaim(a.object.claimDocuments("chord"))
	}
	handle, err := a.object.documents.Declare(definition, a.token, a)
	if err != nil {
		panic(err)
	}
	a.declaredNames[definition.Name] = struct{}{}
	a.declared = append(a.declared, definition)
	return handle
}

// At is `handle.at(key)`: pins key of slot while this attempt's factory's pins are held, and returns its source.
func (a *attempt) At(slot *documents.KeyedSlot, key string) (*documents.KeySource, error) {
	a.assertDeclaring("state(...).at")
	release, err := slot.Pin(key, a.opening)
	if err != nil {
		return nil, err
	}
	a.pins = append(a.pins, release)
	return slot.KeySourceOf(key), nil
}

// Table implements StoreSelf.
func (a *attempt) Table(definition *store.TableDefinition) *store.TableHandle {
	a.assertDeclaring("table")
	object := a.object
	chordStore := a.assertPersistable(`table "` + definition.Name + `"`)
	if _, twice := a.tableNames[definition.Name]; twice {
		panic(declarationError(`%s: table "%s" is declared twice`, object.text, definition.Name))
	}
	sqlName := store.TableSqlName(object.served.contract.Name, definition.Name)
	a.mustClaim(object.claimTable(sqlName))
	handle := store.NewTableHandle(definition, sqlName, chordStore, &attemptTableOwner{attempt: a})
	a.tableNames[definition.Name] = struct{}{}
	a.tables = append(a.tables, declaredTable{handle: handle, name: definition.Name, sql: sqlName})
	return handle
}

// attemptTableOwner is the attempt as the owner of its table handles.
type attemptTableOwner struct{ attempt *attempt }

// Label names the object in messages.
func (o *attemptTableOwner) Label() string { return o.attempt.object.text }

// Scope is the object's documents: a transaction of another object refuses the handle.
func (o *attemptTableOwner) Scope() any { return o.attempt.object.documents }

// AssertLive returns `released` once the object ended.
func (o *attemptTableOwner) AssertLive() error { return o.attempt.object.documents.AssertLive() }

// Fail fails the object `broken` with a row the code cannot read.
func (o *attemptTableOwner) Fail(err *data.ChordError) {
	o.attempt.object.failLive(o.attempt, liveFailure{kind: data.FailureBroken, err: err})
}

// Derive implements Self.
func (a *attempt) Derive(sources []any, compute func(values []any) (delta.JsonValue, error)) chord.Watchable[delta.JsonValue] {
	a.assertDeclaring("derive")
	unwrapped := make([]any, len(sources))
	for index, source := range sources {
		unwrapped[index] = unwrapMember(source)
	}
	derive := state.NewDerive(unwrapped, compute, a.object.host.batch)
	a.derives = append(a.derives, derive)
	return Expose(derive)
}

// Get implements Self.
func (a *attempt) Get(typ *contracts.Contract, params *delta.JsonObject) *Reference {
	a.assertDeclaring("get")
	host := a.object.host
	checked, err := checkParams(typ, params, a.opening)
	if err != nil {
		panic(err)
	}
	bound, err := host.reference(typ, a.object, a.caller(), GetOptions{})
	if err != nil {
		panic(err)
	}
	a.references = append(a.references, declaredReference{binding: bound, contract: typ, params: checked})
	return bound.reference
}

// Registry implements Self.
func (a *attempt) Registry(registry registries.Registry[any, any]) *registries.RegistryValueSource {
	a.assertDeclaring("registry")
	value := a.object.host.registryRecord(registry.Name).Value(registry.Version)
	declarations := a.declarations()
	declarations.registries = append(declarations.registries, value)
	return value
}

// Writable implements Self.
func (a *attempt) Writable(initial any) state.Writable[any] {
	a.assertDeclaring("writable")
	writable := state.NewWritableSource[any](initial, &attemptWritableOwner{attempt: a}, a.object.host.batch)
	declarations := a.declarations()
	declarations.writables = append(declarations.writables, writable)
	return writable
}

// attemptWritableOwner is the attempt as the owner of its writables: the object's context, and usable until it is sealed.
type attemptWritableOwner struct{ attempt *attempt }

func (o *attemptWritableOwner) Context() context.Context { return o.attempt.ctx }

func (o *attemptWritableOwner) AssertUsable() error {
	if o.attempt.token.Sealed() {
		return o.attempt.object.ended()
	}
	return nil
}

// RetryOn implements Self.
func (a *attempt) RetryOn(sources ...any) {
	a.assertDeclaring("retryOn")
	declarations := a.declarations()
	for _, source := range sources {
		wake, err := retrySource(unwrapMember(source), a.object.text)
		if err != nil {
			panic(err)
		}
		declarations.retrySources = append(declarations.retrySources, wake)
	}
}

// Contribute implements StoreSelf.
func (a *attempt) Contribute(registry registries.Registry[any, any], value any) {
	a.assertDeclaring("contribute")
	object := a.object
	// R21: the types allow it only to host-lifetime objects; plugin code is bundled without type checks.
	if object.served.kind != servedLocal || !object.served.hostLifetime {
		panic(declarationError("%s: self.contribute is valid only in a local type with empty params", object.text))
	}
	declarations := a.declarations()
	for _, each := range declarations.declared {
		if each.registry.Name == registry.Name {
			panic(declarationError("%s: registry %s is contributed to twice", object.text, registry.Name))
		}
	}
	value = unwrapMember(value)
	declarations.declared = append(declarations.declared, declaredContribution{registry: registry, value: value})
}

// Open implements Self.
func (a *attempt) Open(
	open func(ctx context.Context) (any, error),
	closer func(value any, reason NativeCloseReason, ctx context.Context) error,
	options ...NativeOptions,
) Native[any] {
	a.assertDeclaring("open")
	host := a.object.host
	closeTimeout := host.timeouts.close
	if len(options) > 0 && options[0].CloseTimeout != nil {
		closeTimeout = *options[0].CloseTimeout
	}
	owner := nativeOwner{
		object: a.object.text, position: len(a.natives) + 1, context: a.ctx, report: host.report, loop: host.loop(),
	}
	native := newNativeHandle[any](open, closer, closeTimeout, owner)
	a.natives = append(a.natives, native)
	return native
}

// Tx implements Self.
func (a *attempt) Tx(fn func(tx *documents.Transaction) (any, error), ctx context.Context) (any, error) {
	return a.object.documents.Tx(fn, a.token, ctx)
}

// Follow implements Self.
func (a *attempt) Follow(attach FollowAttach[delta.JsonValue]) chord.Watchable[delta.JsonValue] {
	a.assertDeclaring("follow")
	object := a.object
	owner := followOwner{label: object.text, ctx: a.ctx, ended: a.followEnded}
	source := newFollowSource(attach, owner, object.host.batch, object.host.loop())
	a.follows = append(a.follows, source)
	return Expose(source)
}

// On implements Self.
func (a *attempt) On(source JSONSource, fn OnHandler[delta.JsonValue]) {
	a.assertDeclaring("on")
	a.routines.on(source, fn)
}

// OnNative implements Self.
func (a *attempt) OnNative(source NativeSource, fn OnNativeHandler[any]) {
	a.assertDeclaring("on")
	a.routines.onNative(source, fn)
}

// OnLive implements Self.
func (a *attempt) OnLive(fn OnLiveHandler) {
	a.assertDeclaring("onLive")
	a.routines.onLive(fn)
}

// Job implements Self.
func (a *attempt) Job(definition *JobDefinition, implementation JobImplementation) JobHandle {
	a.assertDeclaring("job")
	object := a.object
	if _, twice := a.jobNames[definition.Name]; twice {
		panic(declarationError(`%s: job "%s" is declared twice`, object.text, definition.Name))
	}
	if definition.Persist != nil {
		a.assertPersistable(`job "` + definition.Name + `"`)
		a.mustClaim(object.claimDocuments("chord.jobs"))
		a.mustClaim(object.claimDocuments("chord.job-states"))
	}
	owner := jobOwner{
		host: object.host, caller: a.caller(), context: a.ctx, signal: a.opening, token: a.token,
		documents: object.documents, admit: a.admit,
	}
	records, err := object.jobRecords(definition)
	if err != nil {
		panic(err)
	}
	made := newJob(records, implementation, owner)
	a.jobNames[definition.Name] = struct{}{}
	a.jobs = append(a.jobs, made)
	return made
}

// activate activates once ending (the previous instance's teardown, nil when none runs) has finished; readyTimeout bounds
// both (an attempt abandoned while it waits has opened nothing). The factory runs in this section when nothing is
// waited for.
func (a *attempt) activate(ending *keyedPromise[struct{}]) *keyedPromise[activation] {
	host := a.object.host
	ready := host.timeouts.ready
	result := newKeyedPromise[activation](a.loop())
	dl := newDeadline(host.loop(), ready, func() {
		a.abandon(activation{kind: activationFailed, cause: data.FailureCause{
			Kind: data.FailureTransient, Error: notLive(a.object.text, ready),
		}})
	})
	finish := func(outcome activation) {
		dl.clear()
		result.resolve(outcome)
	}
	if ending == nil {
		a.activateSteps(dl, finish)
		return result
	}
	ending.thenWithContext(a.opening, func(_ struct{}, err error) {
		if err != nil {
			// An ending never rejects: only an abandonment (the deadline) ends this wait early.
			finish(*a.abandoned)
			return
		}
		a.activateSteps(dl, finish)
	})
	return result
}

// abandon abandons the activation with outcome (once): its waits stop and the openers' contexts are aborted.
func (a *attempt) abandon(outcome activation) {
	if a.abandoned != nil {
		return
	}
	a.abandoned = &outcome
	if outcome.kind == activationEnded {
		a.abort(outcome.err)
		return
	}
	a.abort(outcome.cause.Error)
}

// followEnded: a followed runtime ended its observer (R4): `failed` fails this attempt `transient`, live or activating;
// `ended` does only while it activates (it would go live over a source that ended). An activation that already returned
// fails as it goes live.
func (a *attempt) followEnded(end chord.WatchEnd) {
	if end.Kind == chord.WatchEnded && a.isLive {
		return
	}
	label := a.object.text
	var err *data.ChordError
	if end.Kind == chord.WatchFailed {
		err = data.AsChordError(end.Error, label)
	} else {
		err = data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: label + ": a followed source ended while it activated"}, nil)
	}
	if a.isLive {
		a.object.failLive(a, liveFailure{kind: data.FailureTransient, err: err})
		return
	}
	if a.endedEarly == nil {
		a.endedEarly = err
	}
	a.abandon(activation{kind: activationFailed, cause: data.FailureCause{Kind: data.FailureTransient, Error: err}})
}

// live: the object went live: its jobs take starts and its routines start, in this turn; both stop when it is sealed.
func (a *attempt) live() {
	a.isLive = true
	if early := a.endedEarly; early != nil {
		a.object.failLive(a, liveFailure{kind: data.FailureTransient, err: early})
		return
	}
	for _, each := range a.jobs {
		each.live()
	}
	object := a.object
	a.routines.start(routineOwner{
		label: object.text, host: object.host, caller: a.caller(), context: a.opening, admit: a.admit,
	})
}

// isParked is whether it is parked on its dependencies (the dependency check runs).
func (a *attempt) isParked() bool { return a.isParking }

// contributionList are its contributions, resolved by its activation: in the registries' tables while its object is live.
func (a *attempt) contributionList() []*registries.RegistryContribution {
	if a.registry == nil {
		return nil
	}
	return a.registry.contributions
}

// variantName is the variant a remote object's factory chose; not found for a local object.
func (a *attempt) variantName() (string, bool) {
	if a.variant == nil {
		return "", false
	}
	return *a.variant, true
}

// stateMembers are the state members, as Chord follows them.
func (a *attempt) stateMembers() []stateMember {
	listed := make([]stateMember, 0, a.members.state.Len())
	for name, source := range a.members.state.All() {
		listed = append(listed, stateMember{name: name, source: source})
	}
	return listed
}

// call calls a method for from: a method outside the variant fails `invalid/member` first, then the arguments are checked
// against the current version (L1); the handler runs under a ctx aborted by the caller and by this attempt's end. Either
// rejects the caller at once; the handler runs on, its transactions refused, and the drain waits for it. Its `ctx.get`
// references are released when it settles. The admission is this section's; the result settles later.
func (a *attempt) call(method string, args any, ctx context.Context, from caller) *parkedCallResult {
	result := newParkedCallResult()
	if ctx.Err() != nil {
		result.settle(nil, context.Cause(ctx))
		return result
	}
	object := a.object
	host := object.host
	handler, found := a.members.methods.Get(method)
	if !found {
		result.settle(nil, data.Invalid(data.ReasonMember, object.text+a.inVariant()+` has no method "`+method+`"`))
		return result
	}
	checked, err := data.CheckArgs(object.served.provider, method, args, object.served.contract.Version, ctx)
	if err != nil {
		result.settle(nil, err)
		return result
	}
	linked := withLinkedAbortSignal(a.opening, ctx)
	callContext := linked.context
	// A local object's handlers act as the host, its principal and its incarnation (§4); a remote one's as their caller.
	// The caller stays the reference's (presence, §19).
	acting := caller{principal: host.principal, incarnation: host.incarnation}
	if object.served.kind == servedRemote {
		acting = from
	}
	handlerCtx := newCallContext(callContext, acting, host)
	// Admitted to the drain before the handler runs: the handler may end its own object before it returns.
	host.count(activityCalls, 1)
	settle := a.admit(func() {
		host.count(activityCalls, -1)
		linked.release()
		handlerCtx.settle()
	})
	// The caller's wait ends at once when the call's context is aborted: awaitWithContext(running, callContext).
	stopWait := context.AfterFunc(callContext, func() { result.settle(nil, context.Cause(callContext)) })
	loop := host.loop()
	// The handler's synchronous part runs inside this section, as Pi's does: the section is lent to it (Loop.Lend) until it
	// returns or parks (Park of its ctx, at the first wait for something that is not a section of the host).
	signal := parking.New()
	handlerCtx.signal = signal
	loop.Lend(signal, func() {
		// Plugin code, not type-checked: a panic becomes the rejection.
		value, err := tryPlugin(func() (any, error) { return handler(checked.Args, handlerCtx) })
		signal.Return()
		loop.Run(func() {
			stopWait()
			settle(err)
			// An abort that came before the handler returned wins, as Pi's `awaitWithContext` rejects at the abort: the
			// listener of `AfterFunc` runs on a goroutine of its own and may not have settled the result yet.
			if callContext.Err() != nil {
				result.settle(nil, context.Cause(callContext))
				return
			}
			result.settle(value, err)
		})
	})
	return result
}

// keyedMember is the keyed member `member`'s ultimate slot (slice 6a §12, rule 22: through re-exposures); an unknown one
// fails `invalid/member`.
func (a *attempt) keyedMember(member string) (*documents.KeyedSlot, error) {
	keyed, found := a.members.keyed.Get(member)
	if !found {
		return nil, data.Invalid(data.ReasonMember, a.object.text+` has no keyed member "`+member+`"`)
	}
	if keyed.kind == keyedOwn {
		return keyed.slot, nil
	}
	return keyed.binding.keyedSlot(keyed.member)
}

// keyedMembers are the keyed members as checked: what each is (keyedBinding), for the keys its object holds across attempts.
func (a *attempt) keyedMembers() *contracts.NamedMap[*keyedBinding] { return a.members.keyed }

// owns is whether source (a state member) changes only through this object's own commits: one of this attempt's derives, or
// one of the object's documents. Another object's member source or document is foreign.
func (a *attempt) owns(source state.DeriveSource) bool {
	if derive, ok := source.(*state.Derive); ok {
		return slices.Contains(a.derives, derive)
	}
	if owned, ok := source.(interface{ Owner() *documents.Documents }); ok {
		return owned.Owner() == a.object.documents
	}
	return false
}

// member is the state member `member`, as Chord follows it; one outside the variant fails `invalid/member`.
func (a *attempt) member(member string) (state.DeriveSource, error) {
	source, found := a.members.state.Get(member)
	if !found {
		return nil, data.Invalid(data.ReasonMember, a.object.text+a.inVariant()+` has no state member "`+member+`"`)
	}
	return source, nil
}

// seal ends the attempt at once (the seal fence): calls, openers and routines aborted with err (their callers rejected with
// it), its transactions refused and the one holding the line discarded, its job invocations cut off; documents stay
// readable.
func (a *attempt) seal(err *data.ChordError) {
	a.abandon(activation{kind: activationEnded, err: err})
	a.object.documents.Fence(a.token)
	for _, each := range a.jobs {
		each.seal(err)
	}
}

// close tears a sealed attempt down: waits for its running handlers, routine calls and runs up to the host's `closeTimeout`
// (the drain), then closes its natives in reverse with reason. It returns the closes that threw. It blocks, outside a
// section.
func (a *attempt) close(reason NativeCloseReason) []*closeFailed {
	host := a.object.host
	var drain []*nativeWork
	host.enter(func() {
		for _, each := range a.running {
			drain = append(drain, each.work)
		}
	})
	if drained(host.loop(), drain, host.timeouts.close) == withinTimeout {
		host.enter(func() { host.report(HostError{Kind: ErrDrainTimeout, Object: a.object.text}) })
	}
	// The close writer: the persisted runs the seal stopped, and the held state changes (slice 6b §4.2).
	for _, each := range a.jobs {
		each.writeAtClose(a.ctx)
	}
	// Work stuck past the drain keeps running, but no longer counts as a call or holds its `ctx.get` references.
	host.enter(func() {
		stuck := a.running
		a.running = nil
		for _, each := range stuck {
			if each.cleanup != nil {
				each.cleanup()
			}
		}
	})
	var failures []*closeFailed
	for _, native := range slices.Backward(a.natives) {
		if failure := native.close(reason); failure != nil {
			failures = append(failures, failure)
		}
	}
	return failures
}

// detach detaches its derives (once their object no longer needs them: reads fail `released`, their watches end), its
// followed sources (their observers detached, their watches ended) and its writables (their watches ended).
func (a *attempt) detach() {
	ended := a.object.ended()
	for _, derive := range a.derives {
		derive.Detach(ended)
	}
	for _, follow := range a.follows {
		follow.detach()
	}
	if a.registry != nil {
		for _, writable := range a.registry.writables {
			writable.Detach(ended)
		}
	}
}

// releaseReferences releases its references and registry reads.
func (a *attempt) releaseReferences() {
	for _, each := range a.references {
		each.binding.release()
	}
	if a.registry != nil {
		reads := a.registry.reads
		a.registry.reads = nil
		for _, release := range reads {
			release()
		}
	}
}

// activateSteps is `#activate`: the activation steps; each catches its own failures with their kind (the table in slice
// 2's spec). finish gets the outcome.
func (a *attempt) activateSteps(dl *deadline, finish func(activation)) {
	object := a.object
	host := object.host
	var implementation factoryResult
	var dependencies []dependency
	needsLoad := false
	setup := func() error {
		var err error
		if implementation, err = a.declare(); err != nil {
			return err
		}
		// Right after the factory, before anything can fail: a failed attempt keeps its wake signals.
		if a.registry != nil {
			object.retryOn(a.registry.retrySources)
		} else {
			object.retryOn(nil)
		}
		// The object holds the pins from now on.
		if err = object.documents.Hydrate(a.declared, a.takePins(), a.opening); err != nil {
			return err
		}
		for _, definition := range a.declared {
			if definition.Persist {
				needsLoad = true
			}
		}
		return nil
	}
	// bind runs the steps after the awaited loads: the references are bound, once all are bound a dependency made here may
	// have bound back to this object meanwhile.
	bind := func() error {
		for _, each := range a.references {
			if _, err := host.bind(each.binding, each.contract, each.params, a.opening); err != nil {
				return err
			}
			dependencies = append(dependencies, dependency{kind: dependencyReference, binding: each.binding})
		}
		for _, each := range dependencies {
			if each.kind != dependencyReference {
				continue
			}
			if goal := each.binding.target(); goal != nil {
				if err := object.assertNoCycle(goal, a.opening); err != nil {
					return err
				}
			}
		}
		if a.registry != nil {
			for _, value := range a.registry.registries {
				// Read from here until its references are released: an older version's mapping is kept meanwhile.
				a.registry.reads = append(a.registry.reads, value.Read())
				dependencies = append(dependencies, dependency{kind: dependencyRegistry, value: value})
			}
		}
		return nil
	}
	ownSteps := func() { a.ownSteps(implementation, finish) }
	afterBind := func() {
		// Parked only when something is not live yet (most activations find their dependencies live).
		if (len(dependencies) > 0 && checkDependencies(dependencies).kind != checkLive) || a.pinsKeys() {
			a.park(dependencies, dl, func(parked *activation) {
				if parked != nil {
					finish(*parked)
					return
				}
				ownSteps()
			})
			return
		}
		// Its own steps start in a later turn: an object ended in the turn it was made opens nothing.
		a.loop().Queue(ownSteps)
	}
	fail := func(err error) { finish(a.broken(err)) }
	afterLoads := func() {
		if err := bind(); err != nil {
			fail(err)
			return
		}
		afterBind()
	}
	loadJobs := func() {
		a.loadJobs(0, func(err error) {
			if err != nil {
				fail(err)
				return
			}
			afterLoads()
		})
	}
	afterDocuments := func() {
		if len(a.tables) == 0 {
			loadJobs()
			return
		}
		a.prepareTables(dl).then(func(_ struct{}, err error) {
			if err != nil {
				fail(err)
				return
			}
			loadJobs()
		})
	}
	if err := setup(); err != nil {
		fail(err)
		return
	}
	if !needsLoad {
		afterDocuments()
		return
	}
	awaitTask(host, func() (struct{}, error) { return struct{}{}, object.documents.Load(a.opening) }).then(
		func(_ struct{}, err error) {
			if err != nil {
				fail(err)
				return
			}
			afterDocuments()
		})
}

// loadJobs loads a persisted job's runs, once per object: rebuilt before anything runs (`for (const job of jobs) await
// job.load(opening)`): one after the other.
func (a *attempt) loadJobs(index int, done func(err error)) {
	if index == len(a.jobs) {
		done(nil)
		return
	}
	each := a.jobs[index]
	awaitTask(a.object.host, func() (struct{}, error) { return struct{}{}, each.load(a.opening) }).then(
		func(_ struct{}, err error) {
			if err != nil {
				done(err)
				return
			}
			a.loadJobs(index+1, done)
		})
}

// takePins hands the factory's pins to the object (`this.#pins.splice(0)`).
func (a *attempt) takePins() []func() {
	pins := a.pins
	a.pins = nil
	return pins
}

// park parks while a dependency is not live (dependency model §3), with no timer: the deadline paused, `parked_long`'s
// report the only clock. Checks at every change of a dependency (after its batch) and shows what it found on its object;
// once all are live, the keys the factory pinned on them are loaded (a failed load parks it `waiting` until the next change
// or a retry). done gets nil once all are live and pinned, else how it ends: abandoned, or broken (a cycle, an ended
// dependency).
func (a *attempt) park(dependencies []dependency, dl *deadline, done func(*activation)) {
	object := a.object
	dl.pause()
	a.parked = dependencies
	a.isParking = true
	object.waitsFor(dependencies)
	// Waiters on a registry are woken when its value turns usable or its failure changes.
	var waits []func()
	for _, each := range dependencies {
		if each.kind == dependencyRegistry {
			waits = append(waits, each.value.Wait(a))
		}
	}
	long := newParkedLong(a.loop(), object)
	finished := func(outcome *activation) {
		long.stop()
		for _, release := range waits {
			release()
		}
		a.parked = nil
		a.isParking = false
		object.waitsFor(nil)
		dl.resume()
		done(outcome)
	}
	var iterate func()
	waitForChange := func() {
		a.nextChange(func(ok bool) {
			if !ok {
				finished(a.abandoned)
				return
			}
			iterate()
		})
	}
	iterate = func() {
		a.moved = false
		check := checkDependencies(dependencies)
		switch check.kind {
		case checkBroken:
			if a.abandoned != nil {
				finished(a.abandoned)
				return
			}
			finished(&activation{kind: activationFailed, cause: data.FailureCause{Kind: data.FailureBroken, Error: check.err}})
		case checkLive:
			a.pinKeys().then(func(pinned *failed, _ error) {
				if a.abandoned != nil {
					finished(a.abandoned)
					return
				}
				// Changed while the keys loaded: checked again.
				if pinned == nil && !a.moved {
					// Its own steps follow: `loading` until its outcome (a failure shows as the failure, not this).
					object.unparked(a)
					finished(nil)
					return
				}
				if pinned != nil {
					object.parked(a, pinned, true)
				}
				long.stop()
				waitForChange()
			})
		default:
			object.parked(a, check.shown, check.firstPass)
			long.waitingOn(check.remoteLoading)
			waitForChange()
		}
	}
	iterate()
}

// nextChange waits for the next change of a dependency (at once when one came during the check); ok is false when the
// attempt was abandoned meanwhile (its end, the only thing that stops the wait).
func (a *attempt) nextChange(done func(ok bool)) {
	if a.moved {
		a.loop().Queue(func() { done(true) })
		return
	}
	changed := newKeyedPromise[struct{}](a.loop())
	a.changed = changed
	changed.thenWithContext(a.opening, func(_ struct{}, err error) {
		if a.changed == changed {
			a.changed = nil
		}
		done(err == nil)
	})
}

// pinKeys loads the keys the factory pinned on its dependencies' keyed members (`at(key)`); it resolves nil once all are,
// else what the parked attempt shows for the first that failed to load (`waiting` on its reference).
func (a *attempt) pinKeys() *keyedPromise[*failed] {
	result := newKeyedPromise[*failed](a.loop())
	var next func(index int)
	next = func(index int) {
		if index == len(a.references) {
			result.resolve(nil)
			return
		}
		bound := a.references[index].binding
		pinning := bound.pinKeys(a.opening)
		if pinning == nil {
			next(index + 1)
			return
		}
		pinning.then(func(_ struct{}, err error) {
			if err != nil {
				result.resolve(waitingOn(bound.label(), data.AsChordError(err, bound.label())))
				return
			}
			next(index + 1)
		})
	}
	next(0)
	return result
}

// dependencyMoved: a dependency of this parked attempt changed (its state, its cause, its first outcome): it checks again
// after the batch that changed it, once however many changed. A change while it checks or loads pinned keys is remembered
// (`moved`); one outside parking costs a flag.
func (a *attempt) dependencyMoved() {
	if a.moved {
		return
	}
	a.moved = true
	if a.changed != nil {
		a.loop().Queue(func() {
			if a.changed != nil {
				a.changed.resolve(struct{}{})
			}
		})
	}
}

// DependencyMoved implements registries.RegistryWaiter.
func (a *attempt) DependencyMoved() { a.dependencyMoved() }

// Closed implements registries.RegistryWaiter: a registry it is parked on closed with its host: the parked attempt ends
// with err.
func (a *attempt) Closed(err *data.ChordError) {
	a.abandon(activation{kind: activationEnded, err: err})
}

// pinsKeys is whether its factory pinned keys on its dependencies' keyed members (`at(key)`), which load while it is parked.
func (a *attempt) pinsKeys() bool {
	for _, each := range a.references {
		if each.binding.pinsKeys() {
			return true
		}
	}
	return false
}

// retryDependencies is `ref.retry()` on its parked object (`failed { waiting }`): passed on to every failed dependency (a
// broken or transient one retries, a parked one passes it on, a request waiting in an index is left alone), as operation;
// then it checks again (a key that failed to load is loaded again).
func (a *attempt) retryDependencies(operation *retryOperation) {
	for _, each := range a.parked {
		if each.kind == dependencyReference && each.binding.refState().Kind == RefStateFailed {
			each.binding.retry(operation)
		}
	}
	a.dependencyMoved()
}

// ownSteps are the steps after the dependency check (its own: natives, followed sources, the member check, derives, the
// keys references pin on its keyed members), under the deadline. A dependency that leaves `live` meanwhile changes nothing
// (R18 after live).
func (a *attempt) ownSteps(implementation factoryResult, finish func(activation)) {
	host := a.object.host
	failedWith := func(cause data.FailureCause) {
		if a.abandoned != nil {
			finish(*a.abandoned)
			return
		}
		finish(activation{kind: activationFailed, cause: cause})
	}
	afterFollows := func() {
		checked, err := a.checkOwn(implementation)
		if err != nil {
			finish(a.broken(err))
			return
		}
		// The keys references pin on the object's keyed members, loaded on this attempt's before it goes live.
		preparing := a.object.prepareKeys(checked.keyed, a.opening)
		complete := func() {
			if a.abandoned != nil {
				finish(*a.abandoned)
				return
			}
			finish(activation{kind: activationLive})
		}
		if preparing == nil {
			complete()
			return
		}
		preparing.then(func(_ struct{}, err error) {
			if err != nil {
				failedWith(data.FailureCause{Kind: data.FailureTransient, Error: data.AsChordError(err, a.object.text)})
				return
			}
			complete()
		})
	}
	var attachFrom func(index int)
	attachFrom = func(index int) {
		if index == len(a.follows) {
			afterFollows()
			return
		}
		// An attempt abandoned meanwhile attaches nothing.
		if a.abandoned != nil {
			finish(*a.abandoned)
			return
		}
		follow := a.follows[index]
		awaitTask(host, func() (struct{}, error) { return struct{}{}, follow.attach(a.opening) }).
			thenWithContext(a.opening, func(_ struct{}, err error) {
				if err != nil {
					failedWith(data.FailureCause{Kind: data.FailureTransient, Error: data.AsChordError(err, a.object.text)})
					return
				}
				attachFrom(index + 1)
			})
	}
	var openFrom func(index int)
	openFrom = func(index int) {
		if index == len(a.natives) {
			attachFrom(0)
			return
		}
		// An attempt abandoned before this open (ended while it waited) closed its natives already: none opens late.
		if a.abandoned != nil {
			finish(*a.abandoned)
			return
		}
		native := a.natives[index]
		awaitTask(host, func() (struct{}, error) { return struct{}{}, native.open(a.opening) }).
			thenWithContext(a.opening, func(_ struct{}, err error) {
				if err != nil {
					failedWith(data.OpenFailure(err, a.object.text))
					return
				}
				openFrom(index + 1)
			})
	}
	openFrom(0)
}

// checkOwn is the member check and the derives' attachment: what the factory returned is checked against the contract, the
// contributions resolved, the derives attached and computed (a throwing derive fails the attempt, reported by its
// `activation_failed` alone).
func (a *attempt) checkOwn(implementation factoryResult) (*members, error) {
	object := a.object
	variant, checked, err := checkImplementation(object.served.contract, implementation)
	if err != nil {
		return nil, err
	}
	a.members = checked
	a.variant = variant
	if a.registry != nil {
		contributions, err := a.resolveContributions(a.registry)
		if err != nil {
			return nil, err
		}
		a.registry.contributions = contributions
	}
	host, label := object.host, object.text
	err = attachDerives(a.derives, checked.state, func(member string, exposed bool) func(error) {
		return func(err error) {
			// During activation a throwing derive fails the attempt, which `activation_failed` reports alone.
			if !a.isLive {
				return
			}
			failure := HostError{Kind: ErrDeriveFailed, Object: label, Error: err}
			if exposed {
				failure.Member = member
			}
			host.report(failure)
		}
	})
	if err != nil {
		return nil, err
	}
	for _, derive := range a.derives {
		// `void derive.value` throws a failing derive's error: the first one fails the attempt.
		if _, err := derive.Value(); err != nil {
			return nil, err
		}
	}
	return checked, nil
}

// resolveContributions are its declared contributions, as Chord follows them (its references are live now), by its plugin
// and type.
func (a *attempt) resolveContributions(declarations *registryDeclarations) ([]*registries.RegistryContribution, error) {
	object := a.object
	contributor := registries.Contributor{Plugin: object.served.plugin, Type: object.served.contract.Name}
	resolved := make([]*registries.RegistryContribution, 0, len(declarations.declared))
	for _, each := range declarations.declared {
		record := object.host.registryRecord(each.registry.Name)
		contribution, err := contributionOf(record, contributor, each.registry.Version, each.value)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, contribution)
	}
	return resolved, nil
}

// prepareTables brings the declared tables to their definitions before the references are bound (slice 6a §9): created
// fresh or migrated eagerly, with the `readyTimeout` deadline paused meanwhile (resumed with the budget it had left).
func (a *attempt) prepareTables(dl *deadline) *keyedPromise[struct{}] {
	declared := make([]store.DeclaredTable, 0, len(a.tables))
	for _, each := range a.tables {
		declared = append(declared, store.DeclaredTable{Definition: each.handle.Definition, Table: each.sql})
	}
	fence := store.CommitFence{
		Admit: func() bool {
			admitted := false
			a.object.host.enter(func() { admitted = !a.token.Sealed() && a.abandoned == nil })
			return admitted
		},
		Adopt: func(int64, []int64) error { return nil },
	}
	dl.pause()
	prepared := awaitTask(a.object.host, func() (struct{}, error) {
		return struct{}{}, store.PrepareTables(declared, a.tables[0].handle.Store, fence, a.object.text, a.opening)
	})
	result := newKeyedPromise[struct{}](a.loop())
	prepared.then(func(_ struct{}, err error) {
		dl.resume()
		result.settle(struct{}{}, err)
	})
	return result
}

// declare runs the factory with declarations open; the member check establishes what it returned. A throwing factory
// releases its pins.
func (a *attempt) declare() (factoryResult, error) {
	a.declaring = true
	defer func() { a.declaring = false }()
	object := a.object
	value, err := tryPlugin(func() (any, error) {
		if object.served.kind == servedLocal {
			return object.served.local(object.params, a)
		}
		// A remote object's identity has its principal.
		return object.served.remote(object.params, object.principal, a)
	})
	if err != nil {
		for _, release := range a.pins {
			release()
		}
		return factoryResult{}, err
	}
	return factoryResultOf(value.(contracts.Implementation)), nil
}

// factoryResultOf is what a factory returned as the member check takes it: the variant (none for a local contract's) and
// the members by name, state members, methods and keyed members, each in name order.
func factoryResultOf(implementation contracts.Implementation) factoryResult {
	result := factoryResult{}
	if implementation.Variant != "" {
		variant := implementation.Variant
		result.variant = &variant
	}
	members := implementation.Members
	add := func(name string, value any) {
		result.members = append(result.members, implementationMember{name: name, value: unwrapMember(value)})
	}
	for _, name := range sortedKeys(members.State) {
		add(name, members.State[name])
	}
	for _, name := range sortedKeys(members.Methods) {
		add(name, members.Methods[name])
	}
	for _, name := range sortedKeys(members.Keyed) {
		add(name, members.Keyed[name])
	}
	return result
}

func sortedKeys[V any](members map[string]V) []string {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// assertPersistable: persisted data needs a stable identity (P3: a `local` type with empty params, also checked by the
// types) and the host's store; it returns the store.
func (a *attempt) assertPersistable(what string) *store.ChordStore {
	object := a.object
	served := object.served
	contractData, err := contracts.ContractDataOf(served.contract)
	if err != nil {
		panic(err)
	}
	if served.kind != servedLocal || len(contractData.Params.Fields) > 0 {
		panic(declarationError("%s: %s: persist needs a stable identity (a local type with empty params)", object.text, what))
	}
	if object.host.store == nil {
		panic(declarationError("%s: %s: persist needs a store", object.text, what))
	}
	return object.host.store
}

// mustClaim panics with a refused claim (`invalid/args`), which fails the declaring factory.
func (a *attempt) mustClaim(err error) {
	if err != nil {
		panic(err)
	}
}

// admit adds running work to the drain, with cleanup run once: when it settles (a microtask after settle was called), or at
// teardown if it never does. cleanup may be nil.
func (a *attempt) admit(cleanup func()) (settle func(err error)) {
	work := &runningWork{work: newNativeWork(), cleanup: cleanup}
	a.running = append(a.running, work)
	return func(err error) {
		work.work.settle(err)
		// `running.then(settled, settled)`: out of the list already means the teardown ran the cleanup.
		a.loop().Queue(func() {
			if at := slices.Index(a.running, work); at >= 0 {
				a.running = slices.Delete(a.running, at, at+1)
				if work.cleanup != nil {
					work.cleanup()
				}
			}
		})
	}
}

// caller is whom this object's own work acts for: a remote object as its principal, a local one as the host; from this host.
func (a *attempt) caller() caller {
	object := a.object
	if object.served.kind == servedRemote {
		return caller{principal: object.principal, incarnation: object.host.incarnation}
	}
	return caller{principal: object.host.principal, incarnation: object.host.incarnation}
}

// inVariant is ` (variant reader)` for a remote object, for errors.
func (a *attempt) inVariant() string {
	if a.variant == nil {
		return ""
	}
	return " (variant " + *a.variant + ")"
}

// broken is the outcome of an activation step that failed `broken` with err, or the abandonment that came first.
func (a *attempt) broken(err error) activation {
	if a.abandoned != nil {
		return *a.abandoned
	}
	return activation{kind: activationFailed, cause: data.FailureCause{Kind: data.FailureBroken, Error: data.AsChordError(err, a.object.text)}}
}

func (a *attempt) declarations() *registryDeclarations {
	if a.registry == nil {
		a.registry = &registryDeclarations{}
	}
	return a.registry
}

func (a *attempt) assertDeclaring(declaration string) {
	if !a.declaring {
		panic(declarationError("%s: self.%s is valid only in the factory", a.object.text, declaration))
	}
}

// awaitTask is `await work()` for work that blocks (a store, a native, a followed runtime): work runs on an owned task of the
// host's loop, outside a section, and its outcome settles the returned promise in a section (the resumption).
func awaitTask[T any](host *chordHost, work func() (T, error)) *keyedPromise[T] {
	promise := newKeyedPromise[T](host.loop())
	host.loop().Go(func() {
		value, err := work()
		host.enter(func() { promise.settle(value, err) })
	})
	return promise
}

// runParallel runs every work at once and returns when all returned (`Promise.all` of promises that never reject).
func runParallel(works []func()) {
	var wait sync.WaitGroup
	for _, work := range works {
		wait.Go(func() {
			work()
		})
	}
	wait.Wait()
}

// joinArrow joins labels with ` → ` (a dependency cycle's message).
func joinArrow(labels []string) string {
	var text strings.Builder
	for index, label := range labels {
		if index > 0 {
			text.WriteString(" → ")
		}
		text.WriteString(label)
	}
	return text.String()
}

// errNotWatchable is returned for a source that has no Chord watch.
var errNotWatchable = errors.New("not a Chord watchable")

// exposedWatchable is a state source of this package as a `chord.Watchable[delta.JsonValue]`: what `Self.Derive` returns and
// what any state source is as an Implementation's state member (Expose). Its Value is the source's value, nil while it
// fails (Pi's getter throws); Watch is the source's own.
type exposedWatchable struct{ source any }

// Expose is a Chord JSON source of this package (a *documents.Singleton, a derive, a reference member, a followed source, a
// constant) as a `chord.Watchable[delta.JsonValue]`: it is how a factory puts it in Implementation.State, and a source of
// another derive, routine or contribution. Chord follows what it wraps, not the wrapper.
func Expose(source any) chord.Watchable[delta.JsonValue] { return &exposedWatchable{source: source} }

// Value is the source's value; nil while it fails.
func (e *exposedWatchable) Value() delta.JsonValue {
	resolved, err := resolveWatchable(e.source)
	if err != nil || resolved == nil {
		return nil
	}
	value, err := resolved.Value()
	if err != nil {
		return nil
	}
	return value
}

// Watch is the source's watch.
func (e *exposedWatchable) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	watcher, ok := e.source.(JSONSource)
	if !ok {
		return nil, errNotWatchable
	}
	return watcher.Watch(ctx)
}

// exposedKeyed is a keyed document handle (or a reference's keyed member) as a `contracts.KeyedWatchable`.
type exposedKeyed struct{ handle any }

// ExposeKeyed is a keyed document handle (a *documents.Keyed, a reference's keyed member) as a
// `contracts.KeyedWatchable[delta.JsonValue]`: how a factory puts it in Implementation.Keyed.
func ExposeKeyed(handle any) contracts.KeyedWatchable[delta.JsonValue] {
	return &exposedKeyed{handle: handle}
}

func (e *exposedKeyed) keyed() documents.KeyedHandle {
	handle, ok := e.handle.(documents.KeyedHandle)
	if !ok {
		panic(errNotWatchable)
	}
	return handle
}

// Get is the document at key; found is false when absent.
func (e *exposedKeyed) Get(ctx context.Context, key string) (delta.JsonValue, bool, error) {
	value, err := e.keyed().Get(key, ctx)
	if err != nil || value == nil {
		return nil, false, err
	}
	return value, true, nil
}

// Watch is a watch of the document at key that follows its address.
func (e *exposedKeyed) Watch(ctx context.Context, key string) (chord.WatchHandle[delta.JsonValue], error) {
	return e.keyed().Watch(key, ctx)
}

// At is, in a factory only, the document at key as a watchable.
func (e *exposedKeyed) At(key string) chord.Watchable[delta.JsonValue] {
	source, err := e.keyed().At(key)
	if err != nil {
		panic(err)
	}
	return Expose(source)
}

// unwrapMember is what an exposed source wraps: Chord follows the source, not its wrapper; any other value is itself.
func unwrapMember(value any) any {
	switch wrapped := value.(type) {
	case *exposedWatchable:
		return wrapped.source
	case *exposedKeyed:
		return wrapped.handle
	}
	return value
}
