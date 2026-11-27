package host

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/host/admission.ts (Pi 7cc21c24f6b5; `revoked` was committed with part B).
//
// Admission (slice 14 §2): the decision made about a caller before anything is built or started. A remote type's provider
// declares it with `ServeOptions.Admit`, run first in every attempt: it reads data about the caller (references with keyed
// pins, registries, derives, live queries) and returns a watchable of `Allow(value)` or `Deny(reason)`; the factory is handed
// the value. A route asks the same way before it resolves or forwards a hold. This file has the app's constructors and one
// `decision`: what one `Admit` declared, what it says now, and the watch its owner follows it with. What follows from a
// decision is its owner's: an attempt parks, goes on or is sealed (`host_attempt.go`, `host_object.go`), a route holds
// back, forwards or releases (`host_route_admission.go`).
//
// Contract of the asynchrony (Go form of Pi's promises): `Admit` is synchronous and declarative, never awaited: `declare`
// runs it in the section that starts the decision, once. `settle` is the one asynchronous step: a promise (keyedPromise on
// the host's loop) that resolves in a section once the pinned keys loaded and the live queries opened (their first runs
// are store reads on owned tasks); the attempt parks on it as on a dependency. `now` is synchronous, in the flush of the
// batch that changed the decision (`Batch.Flushing`): one check per batch however many sources changed. Nothing here is
// ordered against a link's fence: what a denial does to the holders over a link is the hold's (`remote_hold.go`). What
// cancels a decision: the context `declare` and `settle` were given (the attempt's `opening`: its abandonment or seal
// aborts the waits and the keyed reads), and `release` (once, by its owner), which ends its follows, closes its readers
// and lets go of its references.

// Admit is a remote type's admission (`ServeOptions.Admit`): synchronous and declarative, run first in every attempt. It
// returns a Chord watchable of `Allow(value)` or `Deny(reason)` (a JSON object: a derive's value is a JSON value), or the
// error its throw would be. Its attempt parks until what it declared is live and hydrated, then reads the decision and
// follows it: an equal value changes nothing, another one rebuilds the object, a denial shows `failed { denied }`.
type Admit = func(params *delta.JsonObject, principal data.Principal, self AdmitSelf) (chord.Watchable[delta.JsonValue], error)

// AdmitSelf is what `Admit` declares with, valid only while it runs: references (`Get(Type, params)` and `At(key)` on their
// keyed members), registries, derives and live queries. An admission decides and owns nothing: no documents, natives,
// routines or jobs.
type AdmitSelf interface {
	Get(typ *contracts.Contract, params *delta.JsonObject) *Reference
	Registry(registry registries.Registry[any, any]) *registries.RegistryValueSource
	Derive(sources []any, compute func(values []any) (delta.JsonValue, error)) chord.Watchable[delta.JsonValue]
	LiveQuery(query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args any, first int64) chord.Watchable[delta.JsonValue]
}

// AdmittedValue is an object's admitted value as its factory gets it: the admitted data (all fields except `granted`),
// and `Granted` as a set of granted names (grantable members and flags, slice 15 §1.2).
// It also provides Data (the *delta.JsonObject without `granted`) and Get(field) for reading admitted fields.
type AdmittedValue struct {
	Data    *delta.JsonObject
	Granted map[string]bool
}

// Get returns the admitted data field named `field`, or (nil, false) if absent.
func (a AdmittedValue) Get(field string) (delta.JsonValue, bool) {
	if a.Data == nil {
		return nil, false
	}
	return a.Data.Get(field)
}

// Allow allows the caller: value is what the factory is handed about it, plain JSON: the names in `granted` (the
// grantable members and flags the caller gets, slice 15 §1.2) and the data the contract's `admission` schema describes
// (never validated). Small and rarely changing (a role): another value rebuilds the caller's object.
func Allow(value delta.JsonValue) delta.JsonValue {
	return delta.JsonObjectOf("kind", "allow", "value", value)
}

// Deny denies the caller. reason is English, for logs and errors; optional message is the user-facing MessageData
// carried in the `forbidden` error's Details (`{ message }`, slice 15 §2).
func Deny(reason string, message ...contracts.MessageData) delta.JsonValue {
	if len(message) == 0 {
		return delta.JsonObjectOf("kind", "deny", "reason", reason)
	}
	m := message[0]
	params := m.Params
	if params == nil {
		params = []delta.JsonValue{}
	}
	msgObj := delta.JsonObjectOf("namespace", m.Namespace, "key", m.Key, "params", params)
	return delta.JsonObjectOf("kind", "deny", "reason", reason, "message", msgObj)
}

// AdmittedFactory is the factory of an admitted remote type (one whose contract declares an `admission`, grantable
// members or flags): a RemoteFactory that is also handed what its admission allowed (`AdmittedValue`, with `Granted`
// as a set and `Data` holding the admitted fields). An alias of the function type, as LocalFactory and RemoteFactory.
type AdmittedFactory = func(params *delta.JsonObject, principal data.Principal, admitted AdmittedValue, self Self) (contracts.MembersImplementation, error)

// verdictKind is what a decision says at one check.
type verdictKind uint8

const (
	// verdictAllow is Pi's `Allowed`: allowed with a value.
	verdictAllow verdictKind = iota
	// verdictDeny is Pi's `Denied`: denied with an error.
	verdictDeny
	// verdictParked: not decided, parked on what it reads: `loading`, or `failed { waiting }` on a source that failed or a
	// decision that cannot be read.
	verdictParked
	// verdictBroken: a cycle, an ended source, a value that is no decision.
	verdictBroken
)

// verdict is what a decision says at one check (Pi's `Verdict`: `Allowed | Denied | parked | broken`). Compared by IDENTITY,
// as Pi does: an allow with an equal value and a denial with the same reason are the same object for as long as the
// decision keeps them (`decide`), so the same object as before means nothing changed. Only the fields of its kind are set.
type verdict struct {
	kind verdictKind
	// value is a route allow's raw value (or an object allow's Data).
	value delta.JsonValue
	// admitted is an object allow's AdmittedValue (with Granted set).
	admitted AdmittedValue
	// granted is an allow's granted set (contracts.NoGrants for routes or when empty).
	granted map[string]bool
	// err is a denial's `forbidden` error (the denied object shows it as it is; its calls get `revoked(label, err)`), or what
	// a broken verdict broke with.
	err *data.ChordError
	// shown, firstPass and cause are a parked verdict's (as `parkResult`'s).
	shown     *RefState
	firstPass bool
	cause     *data.FailureCause
}

// decisionOwner is who a decision is made for and what it tells.
type decisionOwner struct {
	host *chordHost
	// label is what it decides about, in errors and reports: an object's label, or a route's address.
	label string
	// caller is whom its references act as: a remote object's principal, the host for a route.
	caller caller
	// dependent is the object whose attempt it admits (its references' dependent: cycles, the start barrier); nil for a route.
	dependent *chordObject
	// grants is an object's contract grants (grantable members and flags, slice 15 §1.2), against which an allowed value
	// is read; nil for a route (hasGrants == false), whose value is passed on as it is.
	hasGrants bool
	grants    []string
	// moved: something it reads changed (a source's state, the decision's value, in that change's batch): check it again.
	moved func()
}

// decided is the last allow or denial a decision read: an equal one returns the same *verdict object.
type decided struct {
	kind    verdictKind
	verdict *verdict
	value   delta.JsonValue
	names   []string
	reason  string
	message delta.JsonValue
}

// Decision is Pi's `Decision` (the name `decision` is the reference's `until`): one decision: the `self` its `Admit` declares with, the requests it made (held until `release`), and what it
// says. `settle` brings it to a verdict (its pinned keys loaded, its live queries opened); `now` reads it again, in the flush
// of the batch that changed it. A source that only loads keeps the last decision; one that failed or ended, and a decision
// that throws when read, do not (fail closed: slice 14 §2.2). It is state.Dependent (its source's changes) and
// registries.RegistryWaiter (its registries'). Used inside the host's sections.
type Decision struct {
	owner decisionOwner
	// declaring is the context `Admit` declares under, while it runs (its params are checked with it); nil outside.
	declaring  context.Context
	references []declaredReference
	// dependencies are its requests in declaration order: what its owner parks on.
	dependencies []dependency
	derives      []*state.Derive
	readers      []*reader
	// following ends its follows of its references and registries: `release`; nil until `declare` made it.
	following func(cause error)
	releases  []func()
	// source is the watchable `Admit` returned, as Chord follows it.
	source state.DeriveSource
	// decided is the last allow or denial it read.
	decided *decided
	// flushing is whether a change of its source waits for the current batch's flush: one check per batch.
	flushing bool
	// readerWake follows a live query it waits on: its next adoption checks again.
	readerWake *decisionWake
	released   bool
	// lifetime is the context its follows run under, cancelled by `release`: what a waiter outside a section ends with
	// (`settleWait`). Nil until `declare` made it.
	lifetime context.Context
}

// decisionWake is the dependent a decision's waiting live queries are followed with (Pi's `#readerWake`).
type decisionWake struct{ decision *Decision }

func (w *decisionWake) Invalidate()                                { w.decision.owner.moved() }
func (w *decisionWake) SourceRetired(map[state.Dependent]struct{}) {}

func newDecision(owner decisionOwner) *Decision {
	d := &Decision{owner: owner}
	d.readerWake = &decisionWake{decision: d}
	return d
}

var (
	_ AdmitSelf                 = (*Decision)(nil)
	_ state.Dependent           = (*Decision)(nil)
	_ registries.RegistryWaiter = (*Decision)(nil)
)

// noMembers: a decision's derives are exposed by none.
var noMembers = contracts.NewNamedMap[state.DeriveSource](0)

// Get implements AdmitSelf.
func (d *Decision) Get(typ *contracts.Contract, params *delta.JsonObject) *Reference {
	ctx := d.assertDeclaring("get")
	owner := d.owner
	checked, err := checkParams(typ, params, ctx)
	if err != nil {
		panic(err)
	}
	bound, err := owner.host.reference(typ, owner.dependent, owner.caller, GetOptions{})
	if err != nil {
		panic(err)
	}
	d.references = append(d.references, declaredReference{binding: bound, contract: typ, params: checked})
	d.dependencies = append(d.dependencies, dependency{kind: dependencyReference, binding: bound})
	return bound.reference
}

// Registry implements AdmitSelf.
func (d *Decision) Registry(registry registries.Registry[any, any]) *registries.RegistryValueSource {
	d.assertDeclaring("registry")
	value := d.owner.host.registryRecord(registry.Name).Value(registry.Version)
	d.dependencies = append(d.dependencies, dependency{kind: dependencyRegistry, value: value})
	return value
}

// Derive implements AdmitSelf.
func (d *Decision) Derive(sources []any, compute func(values []any) (delta.JsonValue, error)) chord.Watchable[delta.JsonValue] {
	d.assertDeclaring("derive")
	unwrapped := make([]any, len(sources))
	for index, source := range sources {
		unwrapped[index] = unwrapMember(source)
	}
	derive := state.NewDerive(unwrapped, compute, d.owner.host.batch)
	d.derives = append(d.derives, derive)
	return Expose(derive)
}

// LiveQuery implements AdmitSelf.
func (d *Decision) LiveQuery(query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args any, first int64) chord.Watchable[delta.JsonValue] {
	d.assertDeclaring("liveQuery")
	owner := d.owner
	at, err := readerSite(owner.host, owner.label, func() { owner.moved() }, owner.host.loop().Go)
	if err != nil {
		panic(err)
	}
	reader, source, err := rowsReader(at, query, args, first)
	if err != nil {
		panic(err)
	}
	d.readers = append(d.readers, reader)
	// Its value is what `present` builds from the query's items.
	return Expose(source)
}

// declare runs admit with declarations open, then binds what it declared under ctx and follows it: its references (bound
// in declaration order, cycles checked for an attempt's), its registries, its derives, and the watchable it returned. It
// returns what admit threw, a refused request's error, and the error for a result that is no Chord watchable; its caller
// then releases the decision.
func (d *Decision) declare(admit func(self AdmitSelf) (any, error), ctx context.Context) error {
	owner := d.owner
	d.declaring = ctx
	returned, err := tryPlugin(func() (any, error) { return admit(d) })
	d.declaring = nil
	if err != nil {
		return err
	}
	source, err := resolveWatchable(unwrapMember(returned))
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("%s: admit returns a JSON watchable of allow(value) or deny(reason)", owner.label)
	}
	d.source = source
	for _, each := range d.references {
		if _, err := owner.host.bind(each.binding, each.contract, each.params, ctx); err != nil {
			return err
		}
	}
	// Once all are bound: a dependency made here may have bound back to the admitted object meanwhile.
	if owner.dependent != nil {
		for _, each := range d.references {
			if goal := each.binding.target(); goal != nil {
				if err := owner.dependent.assertNoCycle(goal, ctx); err != nil {
					return err
				}
			}
		}
	}
	following, cancel := chordctx.WithCancel(ctx)
	d.lifetime, d.following = following, cancel
	for _, each := range d.dependencies {
		if each.kind == dependencyRegistry {
			// Read until its release: an older version's mapping is kept meanwhile.
			d.releases = append(d.releases, each.value.Read(), each.value.Wait(d))
			continue
		}
		// Checked at every change of the reference until the release (never done): its owner checks it again.
		moved := func(*RefState) decision {
			owner.moved()
			return decision{kind: decisionWait}
		}
		each.binding.until(moved, following).then(func(struct{}, error) {})
	}
	err = attachDerives(d.derives, noMembers, func(string, bool) func(error) {
		return func(err error) {
			owner.host.report(HostError{Kind: ErrDeriveFailed, Object: owner.label, Error: err})
		}
	})
	if err != nil {
		return err
	}
	if dependents := source.DependentSet(); dependents != nil {
		dependents.Add(d)
	}
	return nil
}

// settle brings it to a verdict: once every request is live, the keys it pinned are loaded and its live queries opened (each
// named to long while it blocks), then the decision is read. Nothing blocks long for an allow or a denial: `parked_long`
// reports no denial. The promise resolves in a section; it never rejects. What it waits for ends with ctx (a pinned key's
// load, a live query's first run).
func (d *Decision) settle(long blocked, ctx context.Context) *keyedPromise[*verdict] {
	result := newKeyedPromise[*verdict](d.owner.host.loop())
	if d.released {
		result.resolve(&verdict{kind: verdictBroken, err: d.endedError()})
		return result
	}
	check := checkDependencies(d.dependencies)
	if check.kind != checkLive {
		unsettled := d.unsettled(check)
		var found *blocker
		if unsettled.kind == verdictParked && check.kind == checkParked {
			found = check.blocker
		}
		long.blockedBy(found)
		result.resolve(unsettled)
		return result
	}
	bindings := make([]*binding, len(d.references))
	for index, each := range d.references {
		bindings[index] = each.binding
	}
	pinKeys(d.owner.host.loop(), bindings, long, ctx).then(func(pinned *failed, _ error) {
		if pinned != nil {
			result.resolve(&verdict{kind: verdictParked, shown: pinned, firstPass: true, cause: &pinned.Cause})
			return
		}
		label := d.owner.label + ": a live query of its admission"
		openReaders(d.owner.host, d.readers, d.readerWake, long, label, ctx, func(reading parkResult) {
			if reading.kind != parkingReady {
				// A live query whose read has not run yet keeps the last decision, as a source that only loads does.
				loading := reading.kind == parkingParked && reading.shown.Kind == RefStateLoading
				if loading && d.decided != nil {
					result.resolve(d.decided.verdict)
					return
				}
				result.resolve(verdictOfParking(reading))
				return
			}
			read := d.read(ctx)
			long.blockedBy(nil)
			result.resolve(read)
		})
	})
	return result
}

// now is what it says now, after it settled once (its keys loaded, its live queries open): read synchronously, in the flush
// of the batch that changed it. The same object as before means nothing changed.
func (d *Decision) now(ctx context.Context) *verdict {
	check := checkDependencies(d.dependencies)
	if check.kind == checkLive {
		return d.read(ctx)
	}
	return d.unsettled(check)
}

// ErrDecisionReleased is the cause of the broken verdict a released decision's waiters get (`settle`, `settleWait`): the
// decision ended under them, nothing it asked failed. A caller tells a release from a genuinely broken admit by
// `errors.Is(verdict.err, ErrDecisionReleased)`; neither an allow nor a denial is made of it, and a verdict that resolves in the
// turn of the release (the readers the release closes end the promise) is not what a waiter gets. (Pi's promise of a released
// decision never settles: there is no Pi cause to port.)
var ErrDecisionReleased = errors.New("admission decision released")

// endedError is what a released decision's waiters get: `released`, with ErrDecisionReleased as its cause.
func (d *Decision) endedError() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: d.owner.label + ": its admission ended"}, ErrDecisionReleased)
}

// settleWait is `settle` for a caller outside a section (a route's task: Pi's `await decision.settle(...)`): it BLOCKS until
// the verdict is there, or until ctx is cancelled or the decision is released, which cancels what it waits under (a broken
// verdict with the reason: ctx's cause, or `released`). A release wins over a verdict that resolves in the same turn: the
// readers it closes end the promise (parked, waiting on a closed reader) as it cancels the wait, and the waiter of a released
// decision gets `released`, not that verdict. It takes its own sections; never call it inside one. Part D's `settle`. Pi's
// promise of a released decision never settles; a Go task must not block for ever, so the wait ends the way every wait of an
// attempt does, through a context.
func (d *Decision) settleWait(long blocked, ctx context.Context) *verdict {
	waiting, stop := chordctx.WithCancel(ctx)
	defer stop(nil)
	var promise *keyedPromise[*verdict]
	d.owner.host.enter(func() {
		if d.released {
			stop(d.endedError())
			return
		}
		promise = d.settle(long, waiting)
	})
	if promise == nil {
		return &verdict{kind: verdictBroken, err: data.AsChordError(context.Cause(waiting), d.owner.label)}
	}
	// Released: its requests are let go and what it waits for may never come. The decision's end ends this wait inside the cancel
	// that releases it (Go-only: Pi's settle promise of a released decision never settles, admission.ts:238-258, release at :279
	// `#following.abort(ended)`); onAbortNow only cancels `waiting`, and its stop ends the registration when the wait is over.
	stopEnding := onAbortNow(d.lifetime, func() { stop(d.endedError()) })
	defer stopEnding()
	parking.Park(waiting)
	select {
	case <-promise.done:
		// Both are ready when the release came in the turn that resolved the verdict (the readers it closes end the promise), and
		// select picks one at random: the release wins, so the waiter never gets the verdict of a decision that ended under it.
		if cause := context.Cause(waiting); errors.Is(cause, ErrDecisionReleased) {
			return &verdict{kind: verdictBroken, err: data.AsChordError(cause, d.owner.label)}
		}
		return promise.value
	case <-waiting.Done():
		return &verdict{kind: verdictBroken, err: data.AsChordError(context.Cause(waiting), d.owner.label)}
	}
}

// release lets go of everything it declared (once): its references and registry reads released, its derives and live queries
// detached. Its owner calls it once the decision that replaces it has bound its own requests, so a source held only by
// decisions never loses its last holder across a rebuild (slice 14 §2.2, rule 4).
func (d *Decision) release() {
	if d.released {
		return
	}
	d.released = true
	ended := d.endedError()
	if d.following != nil {
		d.following(ended)
	}
	if d.source != nil {
		if dependents := d.source.DependentSet(); dependents != nil {
			dependents.Delete(d)
		}
	}
	d.source = nil
	for _, each := range d.readers {
		each.source().DependentSet().Delete(d.readerWake)
		each.Close(ended)
	}
	for _, each := range d.derives {
		each.Detach(ended)
	}
	releases := d.releases
	d.releases = nil
	for _, release := range releases {
		release()
	}
	references := d.references
	d.references = nil
	for _, each := range references {
		each.binding.release()
	}
	d.dependencies = nil
}

// Invalidate is its source changing in the current batch: its owner checks it in that batch's flush, once however many changed.
func (d *Decision) Invalidate() {
	if d.flushing || d.released {
		return
	}
	d.flushing = true
	d.owner.host.batch.Flushing(func(context.Context) error {
		d.flushing = false
		if !d.released {
			d.owner.moved()
		}
		return nil
	})
}

// SourceRetired: a retired document under its source is a change of its value, which Invalidate tells.
func (d *Decision) SourceRetired(map[state.Dependent]struct{}) {}

// DependencyMoved: a registry it reads changed whether its value is usable.
func (d *Decision) DependencyMoved() { d.owner.moved() }

// Closed: a registry it reads closed with its host: the host ends its owner.
func (d *Decision) Closed(*data.ChordError) {}

// unsettled is what a check that found a request not live says: broken; parked on a failure; or, only loading, the last
// decision (a provider restarting or a link down changes no access), `loading` before the first.
func (d *Decision) unsettled(found check) *verdict {
	if found.kind == checkBroken {
		return &verdict{kind: verdictBroken, err: found.err}
	}
	if found.shown.Kind == RefStateLoading && d.decided != nil {
		return d.decided.verdict
	}
	cause := parkedCause(found)
	return &verdict{kind: verdictParked, shown: found.shown, firstPass: found.firstPass, cause: &cause}
}

// read reads the decision. A read that throws (a derive of `Admit`, a failed live query) is a failed source: parked
// `failed { waiting }` on it. Plugin code is not type-checked at runtime (declaration policy): a value that is neither
// `Allow(value)` nor `Deny(reason)` breaks.
func (d *Decision) read(ctx context.Context) *verdict {
	label := d.owner.label
	var raw delta.JsonValue
	var err error
	if d.source != nil {
		raw, err = d.source.Value()
	}
	if err != nil {
		shown := waitingOn(label+": its admission", data.AsChordError(err, label))
		return &verdict{kind: verdictParked, shown: shown, firstPass: true, cause: &shown.Cause}
	}
	if object, ok := raw.(*delta.JsonObject); ok {
		kind, _ := object.Get("kind")
		value, hasValue := object.Get("value")
		reason, _ := object.Get("reason")
		message, hasMessage := object.Get("message")
		if kind == "allow" && hasValue {
			if !d.owner.hasGrants {
				d.countWalk()
				return d.allowRoute(value)
			}
			return d.allowObject(value, label)
		}
		if text, ok := reason.(string); ok && kind == "deny" {
			if hasMessage && message != nil && !contracts.IsMessageData(message) {
				return brokenRead(fmt.Sprintf("its denial's message is no message as data (MessageSchema): %s", text), label)
			}
			d.countWalk()
			if !hasMessage {
				message = nil
			}
			return d.deny(text, message)
		}
	}
	return brokenRead("its admission's value is neither allow(value) nor deny(reason)", label)
}

func (d *Decision) countWalk() {
	if d.owner.host != nil && d.owner.host.metrics != nil {
		d.owner.host.metrics.Counter("chord.data.walks").Add(1, nil)
	}
}

func brokenRead(message, label string) *verdict {
	return &verdict{kind: verdictBroken, err: data.AsChordError(errors.New(message), label)}
}

func (d *Decision) allowObject(value delta.JsonValue, label string) *verdict {
	obj, ok := value.(*delta.JsonObject)
	if !ok || obj == nil {
		return brokenRead("its admission's value is an object (allow({ granted, ...data }))", label)
	}
	grants := d.owner.grants
	var names []string
	if listedRaw, hasGranted := obj.Get("granted"); hasGranted && listedRaw != nil {
		listed, ok := listedRaw.([]delta.JsonValue)
		if !ok {
			return brokenRead("its admission's granted is a list of names", label)
		}
		if len(listed) > 0 {
			seen := make(map[string]struct{}, len(listed))
			for _, item := range listed {
				name, ok := item.(string)
				if !ok || !slices.Contains(grants, name) {
					return brokenRead(fmt.Sprintf("its admission granted %s: no grantable member or flag of its contract", jsStringifyAny(item)), label)
				}
				if _, dup := seen[name]; !dup {
					seen[name] = struct{}{}
					names = append(names, name)
				}
			}
			slices.Sort(names)
		}
	}
	d.countWalk()
	if d.decided != nil && d.decided.kind == verdictAllow && slices.Equal(d.decided.names, names) && data.SameJson(d.decided.value, obj, "granted") {
		d.decided.value = obj
		return d.decided.verdict
	}
	// Build Data without "granted"
	dataObj := delta.NewJsonObject(obj.Len())
	for k, v := range obj.All() {
		if k != "granted" {
			dataObj.Set(k, v)
		}
	}
	granted := contracts.NoGrants
	if len(names) > 0 {
		granted = make(map[string]bool, len(names))
		for _, name := range names {
			granted[name] = true
		}
	}
	admitted := AdmittedValue{Data: dataObj, Granted: granted}
	v := &verdict{kind: verdictAllow, value: dataObj, admitted: admitted, granted: granted}
	d.decided = &decided{kind: verdictAllow, verdict: v, value: obj, names: names}
	return v
}

func (d *Decision) allowRoute(value delta.JsonValue) *verdict {
	if d.decided != nil && d.decided.kind == verdictAllow && data.SameJson(d.decided.value, value, "") {
		d.decided.value = value
		return d.decided.verdict
	}
	v := &verdict{kind: verdictAllow, value: value, granted: contracts.NoGrants}
	d.decided = &decided{kind: verdictAllow, verdict: v, value: value}
	return v
}

func (d *Decision) deny(reason string, message delta.JsonValue) *verdict {
	if d.decided != nil && d.decided.kind == verdictDeny && d.decided.reason == reason && data.SameJson(d.decided.message, message, "") {
		d.decided.message = message
		return d.decided.verdict
	}
	var details *delta.JsonObject
	if message != nil {
		details = delta.JsonObjectOf("message", message)
	}
	v := &verdict{kind: verdictDeny, err: data.Forbidden(reason, details)}
	d.decided = &decided{kind: verdictDeny, verdict: v, reason: reason, message: message}
	return v
}

func jsStringifyAny(value any) string {
	if s, ok := value.(string); ok {
		return jsStringify(s)
	}
	return data.CanonicalJson(value, context.Background())
}

// assertDeclaring is the context `Admit` runs under; outside it, a declaration is refused.
func (d *Decision) assertDeclaring(declaration string) context.Context {
	if d.declaring == nil {
		panic(declarationError("%s: self.%s is valid only in admit", d.owner.label, declaration))
	}
	return d.declaring
}

// verdictOfParking is a parked or broken parkResult as a verdict.
func verdictOfParking(p parkResult) *verdict {
	if p.kind == parkingBroken {
		return &verdict{kind: verdictBroken, err: p.err}
	}
	return &verdict{kind: verdictParked, shown: p.shown, firstPass: p.firstPass, cause: p.cause}
}

// parkingOfVerdict is a parked or broken verdict as the parkResult an attempt's park takes (Pi hands the verdict itself: the
// shapes agree). An allow and a denial are the attempt's own (`decide`).
func parkingOfVerdict(v *verdict) parkResult {
	if v.kind == verdictBroken {
		return parkResult{kind: parkingBroken, err: v.err}
	}
	return parkResult{kind: parkingParked, shown: v.shown, firstPass: v.firstPass, cause: v.cause}
}

// refuseAdmittedJob refuses `self.job` in an admitted type's factory (slice 14 §2.1), in an attempt and in staging: work for a
// person outlives the person's access, so it lives in a `local` store, carries the person in its arguments and asks the
// person's access again on every run. It returns nil for any other type.
func refuseAdmittedJob(s *served, label string) error {
	if s.kind != servedRemote || s.admission == nil {
		return nil
	}
	return fmt.Errorf("%s: self.job is not declared in an admitted object: work for a person lives in a local store", label)
}

// revoked is what a denial rejects the calls running for the denied caller with (slice 14 §2.2): `forbidden/revoked`,
// naming label and the denial's reason. The denied object itself shows denial, `forbidden` with the reason alone.
func revoked(label string, denial *data.ChordError) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: label + ": " + denial.Message, Details: denial.Details}, nil)
}

// What follows is the admission half of Pi's `Attempt` (attempt.ts, `#runAdmission` .. `releaseAdmission`) and `ChordObject`
// (object.ts, `admitting` .. `#disallow`, `get admitted`): methods of `attempt` and `chordObject` kept here, with the decision
// they follow.

// runAdmission is the admission phase (slice 14 §2.2; Pi `#runAdmission`): one microtask after its start (a seal's flush has
// ended by then), `Admit` declares against this attempt's decision, whose requests are bound at once (its object then lets
// its predecessors' go). The attempt parks on them, and on a denial showing `failed { denied }`, with no timer (the deadline
// paused), and goes on with the first allow. The promise resolves nil once allowed, else with how the attempt ends. It never
// rejects.
func (a *attempt) runAdmission(admission *servedAdmission, dl *deadline) *keyedPromise[*activation] {
	result := newKeyedPromise[*activation](a.loop())
	// `await undefined`.
	a.admissionHop(func() {
		if a.abandoned != nil {
			result.resolve(a.abandoned)
			return
		}
		object := a.object
		curVer, err := contracts.CurrentVersion(object.served.contract)
		if err != nil {
			broken := a.broken(err)
			result.resolve(&broken)
			return
		}
		decision := newDecision(decisionOwner{
			host: object.host, label: object.text, caller: a.caller(), dependent: object, hasGrants: true, grants: curVer.Grants, moved: a.admissionMoved,
		})
		// A remote object's identity has its principal.
		err = decision.declare(func(self AdmitSelf) (any, error) { return admission.admit(object.params, object.principal, self) }, a.opening)
		if err != nil {
			decision.release()
			broken := a.broken(err)
			result.resolve(&broken)
			return
		}
		a.decision = decision
		object.admitting(a)
		a.park(decision.dependencies, func(long *parkedLong, settle func(parkResult)) { a.decide(decision, long, settle) }, dl,
			func(outcome *activation) { result.resolve(outcome) })
	})
	return result
}

// admissionHop starts job one microtask later (Pi's `await undefined` before `#runAdmission`): `a.loop().Queue(job)`. Test seam
// (internal/admitseam): while its host's admissionHold is set, the hop is handed to it, which starts it when the test lets it
// go; no production code sets it, and unset (always, outside tests) this is exactly the Queue call.
func (a *attempt) admissionHop(job func()) {
	if hold := a.object.host.admissionHold; hold != nil {
		hold(func() { a.loop().Queue(job) })
		return
	}
	a.loop().Queue(job)
}

// decide is one check of its admission while it parks on it (Pi `#decide`): allowed, it goes on; denied, it shows
// `failed { denied }`; anything else is what the decision's settle found (parked, broken).
func (a *attempt) decide(decision *Decision, long *parkedLong, settle func(parkResult)) {
	a.allowed = nil
	decision.settle(long, a.opening).then(func(found *verdict, _ error) {
		switch found.kind {
		case verdictAllow:
			a.allowed = found
			a.followedVerdict = found
			settle(parkResult{kind: parkingReady})
		case verdictDeny:
			cause := data.FailureCause{Kind: data.FailureDenied, Error: found.err}
			a.admitEvent("deny")
			settle(parkResult{kind: parkingParked, shown: &RefState{Kind: RefStateFailed, Cause: cause}, firstPass: true, cause: &cause})
		default:
			settle(parkingOfVerdict(found))
		}
	})
}

// admitEvent records the outcome of its admission on its activation span (`chord.admit`).
func (a *attempt) admitEvent(outcome string) {
	if span := a.activation; span != nil && !span.Ended() {
		span.Span().AddEvent("chord.admit", telemetry.SpanAttributes{telemetry.Attr("outcome", outcome)})
	}
}

// admissionMoved: something its admission reads changed (Pi `#admissionMoved`). While it still decides, its parked check runs
// again. From its allow on it follows the decision (slice 14 §2.2, "Watch"), live, opening its natives or draining for a
// reload alike: an equal decision (the same object) changes nothing; anything else (another value, a denial, a source that
// failed or ended) is its object's to act on, which seals it at once and admits afresh. It goes on following after its own
// failure, so its failed object still shows a denial; a later attempt's admission takes over once it bound.
func (a *attempt) admissionMoved() {
	if a.allowed == nil {
		a.dependencyMoved()
		return
	}
	found := a.decision.now(a.opening)
	if found == a.followedVerdict {
		return
	}
	a.followedVerdict = found
	a.object.readmit(a, found)
}

// admittedValue is what its admission allowed (its factory's value); nil before its first allow and without an admission.
func (a *attempt) admittedValue() *verdict { return a.allowed }

// sealed is whether it was sealed: its object lets a sealed attempt's admission go once a later one bound its requests.
func (a *attempt) sealed() bool { return a.token.Sealed() }

// releaseAdmission releases what its admission declared (its references, registry reads, derives and live queries); once.
func (a *attempt) releaseAdmission() {
	if a.decision != nil {
		a.decision.release()
	}
}

// admitting: attempt's admission bound its requests: the admissions of the sealed attempts before it are let go (slice 14 §2.2,
// rule 4).
func (o *chordObject) admitting(a *attempt) {
	o.admissions = append(o.admissions, a)
	o.releaseSuperseded()
}

// releaseSuperseded releases the admission of every sealed attempt a later one's followed (its requests are bound: a source
// held only by them stays held). The last one's stays, sealed or not, until a later attempt's binds or the object ends.
func (o *chordObject) releaseSuperseded() {
	if len(o.admissions) == 0 {
		return
	}
	last := o.admissions[len(o.admissions)-1]
	for _, each := range slices.Clone(o.admissions) {
		if each == last || !each.sealed() {
			continue
		}
		each.releaseAdmission()
		o.admissions = slices.DeleteFunc(o.admissions, func(other *attempt) bool { return other == each })
	}
}

// readmit: the admission of a no longer says what it allowed (slice 14 §2.2, "Watch"): found is another value, a denial, or no
// decision (a source that failed or ended). What follows depends on where the attempt stands: the object's current attempt is
// sealed and a new one admits (`rebuild`); a restart's old attempt that still drains is sealed (`sealDraining`); the last
// attempt of a failed object makes the object follow a denial without running its failed factory again (`followFailed`). An
// attempt a later one replaced tells nothing.
func (o *chordObject) readmit(a *attempt, found *verdict) {
	var denial *data.ChordError
	if found.kind == verdictDeny {
		denial = found.err
	}
	st := o.phase
	switch {
	case (st.kind == objectLive || st.kind == objectActivating) && st.attempt == a:
		o.rebuild(a, denial)
	case o.old == a && a.draining():
		o.sealDraining(a, denial)
	case st.kind == objectFailed && len(o.admissions) > 0 && o.admissions[len(o.admissions)-1] == a:
		o.followFailed(a, found)
	}
}

// rebuild seals the object's attempt a at once, with no drain, and admits afresh: its running calls are rejected
// `forbidden/revoked` for a denial (denial, its `forbidden` error) and `unavailable` otherwise, nothing of it is published from
// here on (its holders stop following it in this flush: they keep their last values while it loads, none for a denial, which
// the object shows at once), and it is torn down in the background. A new attempt admits, one microtask later, and a call that
// crossed the seal waits for it at the gate. A lingering object without holders ends instead. It does not use `replace`: that
// notifies, and a denial must be shown without a `loading` in between.
func (o *chordObject) rebuild(a *attempt, denial *data.ChordError) {
	if o.lingering != nil {
		o.end(o.ended())
		return
	}
	a.seal(o.readmitted(denial), cutRestart)
	next := newAttempt(o, o.ctx)
	o.attempts++
	next.trace = attemptTrace{trigger: o.trigger, attempt: o.attempts}
	o.phase = objectState{kind: objectActivating, attempt: next, shown: refLoading}
	if denial != nil {
		o.showDenied(denial)
	}
	o.cut(a)
	host := o.host
	closing := awaitTask(host, func() ([]*closeFailed, error) { return a.close(closeEnd), nil })
	done := newKeyedPromise[[]*closeFailed](host.loop())
	closing.then(func(failures []*closeFailed, _ error) {
		a.detach()
		a.releaseReferences()
		done.resolve(failures)
	})
	host.ending(o.identity, o.reported(done))
	o.run(next, host.endingOf(o.identity))
}

// sealDraining: a restart's old attempt a, still draining, is sealed at once (its successor admits by itself): the old work on
// it is rejected as rebuild rejects calls, and its holders leave it in this flush; a denial is shown now, not when the
// successor's own admission finds it. Its end (`endOld`) goes on from here: it closes the attempt and lets its admission's
// requests go (never here: a reference is not released inside a batch).
func (o *chordObject) sealDraining(a *attempt, denial *data.ChordError) {
	a.seal(o.readmitted(denial), cutReload)
	if denial != nil {
		o.showDenied(denial)
	}
	o.cut(a)
}

// followFailed: a failed object follows its last attempt's admission (the factory of a broke, or it failed live): a denial is
// shown at once, `failed { denied }` in place of its failure, with no value kept and its holders out of presence, and the
// failed factory is not run for it; another allowed value retries the object, as a wake signal does; a source that failed
// changes nothing.
func (o *chordObject) followFailed(a *attempt, found *verdict) {
	switch found.kind {
	case verdictAllow:
		o.retry(newRetryWalk(link.RETRY_HOPS), nil)
	case verdictDeny:
		// Denied: no backoff, woken by the decision.
		o.host.backoff.delete(o.identity)
		o.showDenied(found.err)
		o.cut(a)
	}
}

// readmitted is what a change of its admission rejects running calls with: `forbidden/revoked` for a denial, else `unavailable`.
func (o *chordObject) readmitted(denial *data.ChordError) *data.ChordError {
	if denial != nil {
		return revoked(o.text, denial)
	}
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: o.text + ": its admission changed"}, nil)
}

// showDenied: the object shows `failed { denied }` with denial from now on, in place of what it shows while it activates or of
// its failure, and presence lists its holders no more. Its caller tells the holders (`cut`).
func (o *chordObject) showDenied(denial *data.ChordError) {
	shown := &RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: data.FailureDenied, Error: denial}}
	st := o.phase
	switch st.kind {
	case objectActivating:
		o.phase = objectState{kind: objectActivating, attempt: st.attempt, shown: shown}
	case objectFailed:
		o.phase = objectState{kind: objectFailed, shown: shown}
	default:
		return
	}
	o.failure = denial
	o.disallow()
}

// cut: nothing of a is published from here on: the references that follow it leave it now, taking what the object shows
// (their last values while it loads, none for a denial), and every holder is told.
func (o *chordObject) cut(a *attempt) {
	exposed := o.exposed == a
	if exposed {
		o.following = false
	}
	o.notify()
	if exposed {
		o.exposed = nil
	}
}

// disallow: an admitted object is denied: presence lists its holders no more, until its next allow (§19).
func (o *chordObject) disallow() {
	if !o.allowed {
		return
	}
	o.host.unreferenced(o, o.holderList, o.ctx)
	o.allowed = false
}

// admitted is what its live attempt's admission allowed, with the fingerprint of the admission schema it was decided under: a
// reload's staging rehearses its new factory with it (slice 14 §2.5); nil without one. (Pi `get admitted`; the Go name is
// `admittedValue`, the object's `admitted` being the factory field of `served`.)
func (o *chordObject) admittedValue() *objectAdmitted {
	st := o.phase
	if st.kind != objectLive {
		return nil
	}
	allowed, entry := st.attempt.admittedValue(), st.attempt.served
	if allowed == nil || entry.kind != servedRemote || entry.admission == nil {
		return nil
	}
	return &objectAdmitted{admitted: allowed.admitted, granted: allowed.granted, fingerprint: entry.admission.fingerprint}
}

// objectAdmitted is `ChordObject.admitted`'s result.
type objectAdmitted struct {
	admitted    AdmittedValue
	granted     map[string]bool
	fingerprint string
}
