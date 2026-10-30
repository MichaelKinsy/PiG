package host

import (
	"context"
	"math"
	"slices"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/host/object.ts. Objects (§6): one per identity, from its first attempt to its end. Its
// state across attempts (its documents, line and job runs, its member sources, slice 6a §1.2), its holders, retries after
// failures (backoff, `ref.retry()`, passed on while parked), what it shows while its attempt is parked on dependencies
// (dependency model §3), its first outcome (the start barrier, decision (a)), a live object's failure (slice 6a §1.4), the
// waits and cycle checks of activations (Q5, its instance id and the activation chain it was created for), lingering after
// its last reference (§3, D8), the job runs that keep it alive (§15), its contributions to registries while live and its
// wake signals (`retryOn`, slice 8), and its ending, revocation included (§4, R11).
//
// Async contract. Every method but tearDown and stop's teardown runs inside a section of the host's loop; a Promise Pi
// keeps (`firstOutcome`, the endings `host.ending` holds) is a keyedPromise settled in a section, its reactions
// microtasks of the loop. Pi's background promises (`void this.#run(attempt)`, `closing`, `#reported`) are continuations
// on those promises; the steps of them that block (`attempt.close`, `documents.settled`) are owned tasks of the loop
// (`awaitTask`) that settle in a section. `tearDown` and the function `stop` returns block: they are called outside a
// section (the host's close runs them on a task, `end` on an owned one).
//
// Forced Go differences. A `Map`/`Set` Pi iterates (holders, member sources, job records) keeps its insertion order in a
// slice with an index. `host.backoff.until` is `(at, ok)`. Pi's `endingOf` promise is a *keyedPromise (nil is undefined).
// A thrown `ChordError` is a returned error; `retryOperation()` as a default argument is the callers' newRetryOperation().

// servedKind is what a served type is.
type servedKind uint8

const (
	// servedLocal is a `local` type: its factory gets the params and `self`.
	servedLocal servedKind = iota
	// servedRemote is a `remote` type: its factory also gets the principal its object is for.
	servedRemote
)

// served is a type served on a host, by kind: its contract, factory (a remote one gets the object's principal) and
// argument checks. Factories are plugin code: what they return is checked at activation. Pi's `Served`.
type served struct {
	kind servedKind
	// plugin is the plugin whose facet served it: its objects contribute to registries by it.
	plugin   string
	contract *contracts.Contract
	provider *data.Provider
	// linger is how long a live object stays live after its last reference (`serve`'s `linger`, 0 by default).
	linger time.Duration
	// hostLifetime: a type with empty params: its object lives as long as the host (Q-A). Local types only.
	hostLifetime bool
	// local is the factory of a local type, remote of a remote one.
	local  LocalFactory
	remote RemoteFactory
}

// holder is what holds an object: a reference's binding, or a hold arriving over a link.
type holder interface {
	// dependent is the object whose attempt declared the reference: it checks its dependencies again when this object
	// changes; nil for any other holder.
	dependent() *chordObject
	// caller is whom the reference acts for, with its origin's incarnation (`host.references`).
	caller() caller
	// moved: the object's state changed: an attempt started after a failure, it went live, or it failed.
	moved()
	// end: the object ended with err.
	end(err *data.ChordError)
}

// stateMember is a state member of a live object as Chord follows it, in the order the contract lists them.
type stateMember struct {
	name   string
	source state.DeriveSource
}

// liveFailure is a runtime failure of a live attempt: `broken` or `transient`, with its error.
type liveFailure struct {
	kind data.FailureKind
	err  *data.ChordError
}

// objectStateKind is where an object stands.
type objectStateKind uint8

const (
	// objectWaiting: for its identity's backoff window before its first attempt.
	objectWaiting objectStateKind = iota
	// objectActivating has what its references show (`shown`): `loading`, or `failed { waiting }` while its attempt is
	// parked on a failed dependency.
	objectActivating
	objectLive
	// objectFailed has the state its references show (`shown`).
	objectFailed
	objectEnded
)

// objectState is where an object stands: Pi's discriminated union. attempt is of activating and live; shown of activating,
// failed (a failed state) and ended (an ended one).
type objectState struct {
	kind    objectStateKind
	attempt *attempt
	shown   *RefState
}

// teardownKind is what an ended object still has to do once its previous teardowns finished: drain and close its attempt
// (close); then drop its documents and job runs (end alone).
type teardownKind uint8

const (
	teardownNone teardownKind = iota
	teardownEnd
	teardownClose
)

// teardown is what an ended object still has to do once its previous teardowns finished (`previous`, nil when none runs).
type teardown struct {
	kind     teardownKind
	attempt  *attempt
	previous *keyedPromise[struct{}]
}

var (
	closeEnd   = NativeCloseReason{Kind: NativeCloseEnd}
	closeClose = NativeCloseReason{Kind: NativeCloseClose}
)

// retriesKept is how many retry ids an object remembers: a diamond's shortcut only (a ring is stopped by a retry's
// `through`).
const retriesKept = 8

// heldFlush is the one timer storing an object's jobs' held state changes by their earliest deadline (D8).
type heldFlush struct {
	deadline int64
	timer    Timer
}

// chordObject is one object. Each attempt is a fresh `attempt`; the documents, the line, the job runs and the holders
// outlive it. An attempt parked on its dependencies waits for them without a timer; a transient failure (its own) is
// retried after its backoff window, or at once when a reference asks (`retry`); a broken one stays failed until a reference
// asks. The object ends with its last reference, a live one after its type's `linger` (never, for a host-lifetime object),
// or when the host closes.
type chordObject struct {
	host   *chordHost
	served *served
	params *delta.JsonObject
	// principal is the principal in a remote object's identity, which its factory gets; nil for a local object.
	principal data.Principal
	identity  string
	// text is the object in errors, reports and cycles: its type, and its params key when it has params.
	text string
	// chain is the activation chain it was created for (Q5): the instance ids of the activations waiting for it, from the
	// hold that created it (a trusted link's) or the activation here that declared it; empty otherwise.
	chain []string
	// documents are its documents and line, across its attempts.
	documents *documents.Documents
	// ctx is the context of every attempt: its creator's, without the creator's cancellation.
	ctx context.Context
	// jobs are its job runs by job name, across its attempts, in the order they were declared.
	jobs     map[string]*jobRecords
	jobOrder []string
	// instance is this instance's id (uuidv7), minted at its first attempt: a restarted object is a new instance (D4);
	// empty is Pi's undefined.
	instance string
	phase    objectState
	// holderList are the references and holds of this object, in the order they took it.
	holderList []holder
	// waitingFor are the dependencies its parked attempt waits for; their objects are followed to find cycles (Q5).
	waitingFor []dependency
	// firstOutcomeWait is its first outcome's promise, made by the first who waits for it (the start barrier, for
	// host-lifetime objects); hasFirstOutcome says it came.
	firstOutcomeWait *keyedPromise[struct{}]
	hasFirstOutcome  bool
	// retried are the ids of the last retry operations it handled (at most retriesKept, the newest last): a retry reaching
	// it twice through a diamond is handled once.
	retried []string
	// failure is the error of the last failed attempt, or the last cause a parked attempt showed, until one goes live.
	failure *data.ChordError
	// lingering ends the object when its type's `linger` passed after its last reference; cleared by a new one.
	lingering Timer
	// runs are its running and finishing job runs: they keep it alive like references (D4), without being references.
	runs int
	// exposed is the attempt its member sources show: the live one, or the last one that went live while the object is
	// failed or activating again (their last values); nil before the first went live.
	exposed *attempt
	// members are its state members as references and other objects' derives follow them, by name; each made by its first
	// use, in the order they were.
	members     map[string]*memberSource
	memberOrder []string
	// keys are the keys of its keyed members that references hold, across attempts.
	keys *keyedMembers
	// flush is the one timer storing its jobs' held state changes by their earliest deadline (D8), while one is held.
	flush *heldFlush
	// wakes are its wake signals (`retryOn`), the last attempt's declaration; made by the first.
	wakes *retryWakes
}

var (
	_ target                   = (*chordObject)(nil)
	_ store.Claimant           = (*chordObject)(nil)
	_ documents.Clock          = (*objectClock)(nil)
	_ documents.DocumentsOwner = (*objectDocuments)(nil)
)

// newChordObject makes the object of served for params, in principal's identity (nil for a local one): key is its params'
// key, identity its in-process identity, ctx its creator's context and chain the activation chain it was created for.
func newChordObject(
	host *chordHost, entry *served, params *delta.JsonObject, principal data.Principal, key, identity string,
	ctx context.Context, chain []string,
) *chordObject {
	o := &chordObject{
		host: host, served: entry, params: params, principal: principal, identity: identity, chain: chain, ctx: ctx,
		jobs: map[string]*jobRecords{}, members: map[string]*memberSource{},
	}
	o.text = entry.contract.Name
	if key != "" {
		o.text = entry.contract.Name + "(" + key + ")"
	}
	o.keys = newKeyedMembers(host.batch, host.scheduler, host.loop(), ctx)
	o.documents = documents.NewDocuments(&objectDocuments{objectClock: objectClock{object: o}})
	return o
}

// label is the object in errors, reports and cycles.
func (o *chordObject) label() string { return o.text }

// ClaimLabel is store.Claimant's: the claims it holds name it.
func (o *chordObject) ClaimLabel() string { return o.text }

// objectClock is the host's time as an object's documents read it.
type objectClock struct{ object *chordObject }

// Now is the host's Date.now().
func (c *objectClock) Now() int64 { return c.object.host.loop().Now() }

// AfterFunc runs job as a section once d passed; the returned function cancels it.
func (c *objectClock) AfterFunc(d time.Duration, job func()) (cancel func()) {
	timer := c.object.host.loop().AfterFunc(d, job)
	return func() { timer.Stop() }
}

// objectDocuments is the object as the owner of its documents.
type objectDocuments struct{ objectClock }

// Label names the object in errors.
func (d *objectDocuments) Label() string { return d.object.text }

// Batch is the host's batch.
func (d *objectDocuments) Batch() *state.Batch { return d.object.host.batch }

// Context is the object's context.
func (d *objectDocuments) Context() context.Context { return d.object.ctx }

// Store is the host's store and the object's owner name in it, when the host has one.
func (d *objectDocuments) Store() (*store.ChordStore, string, bool) {
	chordStore := d.object.host.store
	if chordStore == nil {
		return nil, "", false
	}
	return chordStore, d.object.served.contract.Name, true
}

// Fail fails the live object `broken` with err (a document the code cannot read); ignored when it is not live.
func (d *objectDocuments) Fail(err *data.ChordError) {
	o := d.object
	if o.phase.kind == objectLive {
		o.failLive(o.phase.attempt, liveFailure{kind: data.FailureBroken, err: err})
	}
}

// Do runs work as one section of the host's loop (the scheduler held, as every section of the host).
func (d *objectDocuments) Do(work func()) { d.object.host.enter(work) }

// refState is the object's state as its references show it (Pi's `state` getter).
func (o *chordObject) refState() *RefState {
	st := o.phase
	switch st.kind {
	case objectFailed, objectEnded:
		return st.shown
	case objectLive:
		return refLive
	case objectActivating:
		return st.shown
	}
	return refLoading
}

// state is the object's state as its references show it (Pi's `state` getter).
func (o *chordObject) state() *RefState { return o.refState() }

// hydrated is whether its state members have values: once live (a local object stays live until it ends).
func (o *chordObject) hydrated() bool { return o.phase.kind == objectLive }

// lastValues is whether its state members have values while it is not live: once an attempt went live, until it ends.
func (o *chordObject) lastValues() bool { return o.exposed != nil && o.phase.kind != objectEnded }

// firstOutcome resolves at its first outcome (the start barrier waits for it); it never rejects.
func (o *chordObject) firstOutcome() *keyedPromise[struct{}] {
	if o.firstOutcomeWait == nil {
		o.firstOutcomeWait = newKeyedPromise[struct{}](o.host.loop())
		if o.hasFirstOutcome {
			o.firstOutcomeWait.resolve(struct{}{})
		}
	}
	return o.firstOutcomeWait
}

// blocksFirstPass is whether it holds up the start barrier: it has no first outcome yet.
func (o *chordObject) blocksFirstPass() bool { return !o.hasFirstOutcome }

// heldOnLink: an object here is not held on a link.
func (o *chordObject) heldOnLink() bool { return false }

// lastFailure is the error of its last failure while none went live since: a failed attempt's, or the cause its parked
// attempt showed last (kept when the dependency falls back to `loading`, so a getter's deadline names it).
func (o *chordObject) lastFailure() *data.ChordError { return o.failure }

// activationId is its instance id while an attempt activates; empty otherwise (a hold's chain names only activations).
func (o *chordObject) activationId() string {
	if o.phase.kind == objectActivating {
		return o.instance
	}
	return ""
}

// activationChain is the chain a hold this object's activation makes over a link carries (Q5): its own chain and its
// instance id, the last 32; nil when it is not activating.
func (o *chordObject) activationChain() []string {
	id := o.activationId()
	if id == "" {
		return nil
	}
	chain := o.chain
	if len(chain) >= link.CHAIN {
		chain = chain[len(chain)-link.CHAIN+1:]
	}
	return append(slices.Clone(chain), id)
}

// holders are its holders, for a presence list made after they took it.
func (o *chordObject) holders() []holder { return o.holderList }

// variant is the variant a live remote object's factory chose; not bound for a local object and before it is live.
func (o *chordObject) variant() (string, bool) {
	if o.phase.kind != objectLive {
		return "", false
	}
	return o.phase.attempt.variantName()
}

// stateMembers are the live object's state members, as Chord follows them.
func (o *chordObject) stateMembers() ([]stateMember, error) {
	attempt, err := o.live()
	if err != nil {
		return nil, err
	}
	return attempt.stateMembers(), nil
}

// attempt starts an attempt when waiting or failed; a trigger while an attempt runs (or once live) is ignored. After a
// failure the holders are told: their references are loading again.
func (o *chordObject) attempt() {
	st := o.phase
	if st.kind != objectWaiting && st.kind != objectFailed {
		return
	}
	if o.instance == "" {
		o.instance = uuidv7()
	}
	next := newAttempt(o, o.ctx)
	o.phase = objectState{kind: objectActivating, attempt: next, shown: refLoading}
	if st.kind == objectFailed {
		o.notify()
	}
	o.run(next)
}

// retry is `ref.retry()` and a `retry` message, as operation (handled once): an attempt now when failed (a broken one too)
// or waiting, its backoff window skipped; passed on to the failed dependencies while parked `failed { waiting }`, with this
// object added to the objects it passed through (at most CHAIN: a retry stops past 32 hops); ignored otherwise (an attempt
// runs, or it is live), and when it passed through this object already (a ring) or was handled here already (a diamond).
func (o *chordObject) retry(operation *retryOperation) {
	instance := o.instance
	if instance != "" && slices.Contains(operation.through, instance) {
		return
	}
	if o.handled(operation.id) {
		return
	}
	st := o.phase
	if st.kind == objectActivating {
		through := operation.through
		if st.shown.Kind != RefStateFailed || instance == "" || len(through) >= link.CHAIN {
			return
		}
		st.attempt.retryDependencies(&retryOperation{id: operation.id, through: append(slices.Clone(through), instance)})
		return
	}
	if st.kind != objectWaiting && st.kind != objectFailed {
		return
	}
	o.host.backoff.skip(o.identity)
	o.attempt()
}

// retryOn follows sources as its wake signals instead of the last attempt's (`self.retryOn`, right after a factory
// returned): a new value retries it while failed, as `ref.retry()` does; one while it activates retries it at once if that
// attempt fails; one while live does nothing.
func (o *chordObject) retryOn(sources []retrySourceType) {
	if o.wakes == nil && len(sources) == 0 {
		return
	}
	if o.wakes == nil {
		o.wakes = &retryWakes{}
	}
	o.wakes.replace(sources, o.wake)
}

// call calls a method of the live object for from: a refusal (not live, ended, failed) rejects the result.
func (o *chordObject) call(method string, args any, ctx context.Context, from caller) *parkedCallResult {
	attempt, err := o.live()
	if err != nil {
		result := newParkedCallResult()
		result.settle(nil, err)
		return result
	}
	return attempt.call(method, args, ctx, from)
}

// member is the state member `member` as references and other objects' derives follow it across attempts: its member
// source, with its last value while the object is not live (`lastValues`).
func (o *chordObject) member(member string) (state.DeriveSource, error) {
	// Made once per member and dropped at the end: a source found here is the one to follow.
	if made := o.members[member]; made != nil {
		return made, nil
	}
	exposed := o.exposed
	if exposed == nil || o.phase.kind == objectEnded {
		attempt, err := o.live()
		if err != nil {
			return nil, err
		}
		return attempt.member(member)
	}
	return o.memberSource(member, exposed)
}

// keyedMember is the keyed member `member` of the live object: its ultimate slot (slice 6a §12).
func (o *chordObject) keyedMember(member string) (*documents.KeyedSlot, error) {
	attempt, err := o.live()
	if err != nil {
		return nil, err
	}
	return attempt.keyedMember(member)
}

// pinKey pins `key` of the live object's keyed member `member` across its attempts (`keyedMembers.pin`). It blocks,
// outside a section.
func (o *chordObject) pinKey(member, key string, ctx context.Context) (pinnedKey, error) {
	var (
		attempt *attempt
		stored  string
		binding *keyedBinding
		err     error
	)
	o.host.enter(func() {
		if attempt, err = o.live(); err != nil {
			return
		}
		var slot *documents.KeyedSlot
		if slot, err = attempt.keyedMember(member); err != nil {
			return
		}
		if stored, err = slot.Key(key); err != nil {
			return
		}
		binding, _ = attempt.keyedMembers().Get(member)
	})
	if err != nil {
		return pinnedKey{}, err
	}
	return o.keys.pin(member, stored, binding, ctx)
}

// prepareKeys: an activating attempt's keyed members are checked: the keys references pin are loaded on them before it goes
// live; nil when none is held.
func (o *chordObject) prepareKeys(members *contracts.NamedMap[*keyedBinding], ctx context.Context) *keyedPromise[struct{}] {
	return o.keys.prepare(members, ctx)
}

// hold counts holder as a reference to this object (the holder reads `state` itself), listed in its type's presence; a
// lingering object stays.
func (o *chordObject) hold(h holder) {
	if !slices.Contains(o.holderList, h) {
		o.holderList = append(o.holderList, h)
	}
	o.stopLingering()
	// Last: a derive over the presence list may close a link synchronously, which may release this holder again.
	o.host.referenced(o, h, o.ctx)
}

// unhold: holder was released: the object may end (`idle`).
func (o *chordObject) unhold(h holder) {
	at := slices.Index(o.holderList, h)
	if at < 0 {
		return
	}
	o.holderList = slices.Delete(o.holderList, at, at+1)
	o.idle()
	// Last: a derive over the presence list may close a link synchronously, which releases other holders again.
	o.host.unreferenced(o, []holder{h}, o.ctx)
}

// jobRecords are the records of job definition's runs, made by its first declaration. A later declaration of another
// version is refused (versions of runs across attempts are slice 7's).
func (o *chordObject) jobRecords(definition *JobDefinition) (*jobRecords, error) {
	records := o.jobs[definition.Name]
	if records == nil {
		chordStore := o.host.store
		owner := jobRecordsOwner{
			label: o.text, host: o.host, context: o.ctx, documents: o.documents,
			countRuns: o.countRuns, held: o.heldUntil,
		}
		if chordStore != nil {
			owner.store, owner.storeOwner = chordStore, o.served.contract.Name
		}
		made := newJobRecords(definition, owner)
		o.jobs[definition.Name] = made
		o.jobOrder = append(o.jobOrder, definition.Name)
		return made, nil
	}
	if records.definition.Version != definition.Version {
		return nil, declarationError(`%s: job "%s" is declared at version %d, its runs are at %d`,
			o.text, definition.Name, definition.Version, records.definition.Version)
	}
	return records, nil
}

// claimTable claims table (its SQL name) in the host's store for this object, until its end (slice 6b claims); held by
// another owner: `invalid/args`, which fails the declaring factory.
func (o *chordObject) claimTable(table string) error { return o.host.store.Claims.Table(table, o) }

// claimDocuments claims this object's documents in namespace of the host's store, as claimTable.
func (o *chordObject) claimDocuments(namespace string) error {
	return o.host.store.Claims.Documents(o.served.contract.Name, namespace, o)
}

// failLive is a runtime failure of the live attempt `attempt`: `broken` (a `migrate` failing on access, a stored version
// newer than the code, a refused commit) or `transient` (a followed runtime failed, R4): the attempt is sealed and the
// object `failed` with cause; calls reject with it, holders are told, the state members keep their values once the admitted
// commits adopted, the natives close in the background (then its references are released), `onError` gets `object_failed`
// once. Documents and job runs stay. Recovery: a broken failure by `ref.retry()`, a transient one after its identity's
// backoff too (a new attempt, whose members the member sources follow once it is live). A failure of a sealed attempt is
// ignored, so several at once fail it once.
func (o *chordObject) failLive(a *attempt, cause liveFailure) {
	st := o.phase
	if st.kind != objectLive || st.attempt != a {
		return
	}
	if cause.kind == data.FailureTransient {
		o.host.backoff.failed(o.identity)
	} else {
		o.host.backoff.delete(o.identity)
	}
	failure := o.failedState(cause.kind, cause.err)
	o.phase = objectState{kind: objectFailed, shown: failure}
	o.failure = cause.err
	o.withdraw(a)
	a.seal(cause.err)
	// Members following another object's sources freeze now: no commit of this object changes them.
	o.freeze(a, freezeForeign)
	// The commits admitted before the seal adopt and publish once (frames included); then the other members keep their
	// values. A retry's factory and hydration wait for both (its ending), so no old commit lands in a new attempt.
	loop := o.host.loop()
	frozen := awaitTask(o.host, func() (struct{}, error) {
		o.documents.Settled()
		return struct{}{}, nil
	})
	frozen.then(func(struct{}, error) { o.freeze(a, freezeAll) })
	closing := awaitTask(o.host, func() ([]*closeFailed, error) { return a.close(closeEnd), nil })
	done := newKeyedPromise[[]*closeFailed](loop)
	closing.then(func(failures []*closeFailed, _ error) {
		frozen.then(func(struct{}, error) {
			a.detach()
			a.releaseReferences()
			done.resolve(failures)
		})
	})
	o.host.ending(o.identity, o.reported(done))
	o.host.report(HostError{Kind: ErrObjectFailed, Object: o.text, Failure: failureCauseOf(cause.kind, cause.err)})
	o.notify()
	o.idle()
}

// failedState is the failed state of an attempt that failed with err of kind: transient ones have the retry time of their
// identity's backoff window.
func (o *chordObject) failedState(kind data.FailureKind, err *data.ChordError) *RefState {
	failure := &RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: kind, Error: err}}
	if at, ok := o.host.backoff.until(o.identity); ok {
		failure.RetryAt = &at
	}
	return failure
}

// failureCauseOf is the cause a report names.
func failureCauseOf(kind data.FailureKind, err *data.ChordError) data.FailureCause {
	return data.FailureCause{Kind: kind, Error: err}
}

// heldUntil: a held state change must be stored by deadline: the timer is set for the earliest one.
func (o *chordObject) heldUntil(deadline int64) {
	if o.flush != nil && o.flush.deadline <= deadline {
		return
	}
	if o.flush != nil {
		o.flush.timer.Stop()
	}
	loop := o.host.loop()
	// A foreground timer: what is held is stored before the process may exit.
	wait := max(0, deadline-loop.Now())
	timer := loop.AfterFunc(time.Duration(wait)*time.Millisecond, o.flushHeld)
	o.flush = &heldFlush{deadline: deadline, timer: timer}
}

// flushHeld: the timer fired: every held change past its deadline is stored (on an owned task: the store blocks), and the
// timer set for the next one.
func (o *chordObject) flushHeld() {
	o.flush = nil
	if o.phase.kind == objectEnded {
		return
	}
	now := o.host.loop().Now()
	records := o.jobRecordsInOrder()
	loop := o.host.loop()
	loop.Go(func() {
		var wait []func()
		for _, each := range records {
			wait = append(wait, func() { each.flushHeld(now) })
		}
		runParallel(wait)
		o.host.enter(func() {
			next := int64(math.MaxInt64)
			for _, each := range records {
				next = min(next, each.nextDeadline())
			}
			if next != math.MaxInt64 {
				o.heldUntil(next)
			}
		})
	})
}

// jobRecordsInOrder are its job records, in the order they were declared.
func (o *chordObject) jobRecordsInOrder() []*jobRecords {
	records := make([]*jobRecords, 0, len(o.jobOrder))
	for _, name := range o.jobOrder {
		records = append(records, o.jobs[name])
	}
	return records
}

// countRuns adds delta to its running and finishing job runs (and `host.activity.jobs`). A new run keeps a lingering
// object; the last one settling on an object without references ends it as its last release would (`idle`).
func (o *chordObject) countRuns(delta int) {
	o.runs += delta
	o.host.count(activityJobs, delta)
	// A run started while the object lingers keeps it, like a new reference.
	if delta > 0 {
		o.stopLingering()
	}
	if o.runs == 0 {
		o.idle()
	}
}

// assertNoCycle returns `invalid/cycle` when waiting for dependency closes a chain of waits back to this object (Q5). Every
// other object on the chain fails broken with the same error, so no part of a cycle waits for a retry. Counts the objects it
// visits (`chord.cycle.visits`). Otherwise, a dependency whose activation is in this object's chain closes a cycle through
// other hosts (they fail through their dependency on this one).
func (o *chordObject) assertNoCycle(dependency target, ctx context.Context) error {
	visited := map[target]struct{}{}
	cycle := dependency.pathTo(o, visited)
	chordctx.Telemetry(ctx).Metrics().Counter("chord.cycle.visits", telemetry.InstrumentOptions{}).Add(float64(len(visited)), nil)
	if cycle == nil {
		across := dependency.activationId()
		if across == "" || !slices.Contains(o.chain, across) {
			return nil
		}
		return o.host.cycle(o.chain, across, []string{o.text, dependency.label()})
	}
	labels := make([]string, 0, len(cycle)+1)
	labels = append(labels, o.text)
	for _, each := range cycle {
		labels = append(labels, each.text)
	}
	err := data.Invalid(data.ReasonCycle, "dependency cycle: "+joinArrow(labels))
	for _, each := range cycle {
		st := each.phase
		if each != o && st.kind == objectActivating {
			st.attempt.abandon(activation{kind: activationFailed, cause: data.FailureCause{Kind: data.FailureBroken, Error: err}})
		}
	}
	return err
}

// waitsFor: its parked attempt waits for dependencies (none once it leaves parking): the waits a cycle check follows.
func (o *chordObject) waitsFor(dependencies []dependency) { o.waitingFor = dependencies }

// dependencyMoved: a dependency of its current attempt changed (its state, or its first outcome): a parked attempt checks
// again; an attempt past its dependency check ignores it (R18 after live), and so does any other state.
func (o *chordObject) dependencyMoved() {
	if st := o.phase; st.kind == objectActivating {
		st.attempt.dependencyMoved()
	}
}

// parked: its attempt a is parked showing shown (`loading` or `failed { waiting }`): holders are told when that changed (a
// cause compared by value). firstPass: no blocker here is still without its first outcome, so this object has its first
// outcome now.
func (o *chordObject) parked(a *attempt, shown *RefState, firstPass bool) {
	st := o.phase
	if st.kind != objectActivating || st.attempt != a {
		return
	}
	if !sameState(st.shown, shown) {
		o.phase = objectState{kind: objectActivating, attempt: a, shown: shown}
		if shown.Kind == RefStateFailed {
			o.failure = shown.Cause.Error
		}
		o.notify()
	}
	if firstPass {
		o.settleFirstOutcome()
	}
}

// unparked: its attempt a left parking for its own steps: it shows `loading` until its outcome.
func (o *chordObject) unparked(a *attempt) { o.parked(a, refLoading, false) }

// end ends the object (once): sealed now, its holders and running calls told err (`released` by default: ended()), torn
// down in the background with reason `end`, close failures reported.
func (o *chordObject) end(err *data.ChordError) {
	td := o.seal(err)
	if td.kind == teardownNone {
		return
	}
	torn := awaitTask(o.host, func() ([]*closeFailed, error) { return o.tearDown(td, closeEnd), nil })
	o.host.ending(o.identity, o.reported(torn))
}

// stop ends the object for `host.close`: sealed now; returns its teardown (reason `close`), which the host runs once every
// object is sealed, so no reference it releases starts an ending in the background. The teardown blocks.
func (o *chordObject) stop() func() []*closeFailed {
	td := o.seal(o.ended())
	return func() []*closeFailed { return o.tearDown(td, closeClose) }
}

// ended is the `released` error of this object.
func (o *chordObject) ended() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: o.text + ": ended"}, nil)
}

// run is `#run`: the attempt activates once the previous instance's teardown finished; the outcome lands here unless the
// object ended meanwhile.
func (o *chordObject) run(a *attempt) {
	a.activate(o.host.endingOf(o.identity)).then(func(outcome activation, _ error) {
		// The object may have ended after the activation's last step.
		if o.phase.kind != objectActivating || o.phase.attempt != a {
			return
		}
		switch outcome.kind {
		case activationLive:
			o.wentLive(a)
		case activationFailed:
			o.failedAttempt(a, outcome.cause)
		}
	})
}

func (o *chordObject) wentLive(a *attempt) {
	if o.wakes != nil {
		o.wakes.during = nil
	}
	o.phase = objectState{kind: objectLive, attempt: a}
	o.exposed = a
	if len(o.members) > 0 || !o.keys.empty() {
		o.within(func() { o.rebind(a) })
	}
	contributions := a.contributionList()
	// In the registries' tables before anyone sees it live (a getter released now reads them recomposed).
	if len(contributions) > 0 {
		o.within(func() {
			for _, contribution := range contributions {
				contribution.Record.Add(contribution)
			}
		})
	}
	o.failure = nil
	o.host.wentLive(o)
	o.notify()
	o.settleFirstOutcome()
	a.live()
}

// within runs work in a batch of the object's context. The adoptions return no error.
func (o *chordObject) within(work func()) {
	_ = o.host.batch.Within(func() error {
		work()
		return nil
	}, o.ctx)
}

// failedAttempt records a failed attempt: reported, torn down in the background (reason `end`), its references released
// once its natives closed, its holders told. Transient: a backoff window, whose end is the failure's `retryAt`; broken: no
// automatic retry.
func (o *chordObject) failedAttempt(a *attempt, cause data.FailureCause) {
	wokenBy := o.wakes != nil && o.wakes.during == a
	if o.wakes != nil {
		o.wakes.during = nil
	}
	// What its activation loaded for the pinned keys goes; they stay frozen.
	o.keys.discard()
	if cause.Kind == data.FailureTransient {
		o.host.backoff.failed(o.identity)
	} else {
		o.host.backoff.delete(o.identity)
	}
	o.phase = objectState{kind: objectFailed, shown: o.failedState(cause.Kind, cause.Error)}
	o.failure = cause.Error
	a.seal(o.ended())
	loop := o.host.loop()
	closing := awaitTask(o.host, func() ([]*closeFailed, error) { return a.close(closeEnd), nil })
	done := newKeyedPromise[[]*closeFailed](loop)
	closing.then(func(failures []*closeFailed, _ error) {
		a.detach()
		// Released once its natives closed (a close may use them, §6, §13).
		a.releaseReferences()
		done.resolve(failures)
	})
	o.host.ending(o.identity, o.reported(done))
	o.host.report(HostError{Kind: ErrActivationFailed, Object: o.text, Failure: cause})
	o.notify()
	o.settleFirstOutcome()
	// Job runs kept it while it activated; failed, they keep it no longer.
	o.idle()
	// A wake signal arrived while it activated: it retries it now.
	if wokenBy {
		o.retry(newRetryOperation())
	}
}

// wake is a wake signal's new value (inside its batch): retries a failed object (after the batch: a factory may not run in
// one), remembered while it activates, ignored while live and when its own contributions' leaving made it.
func (o *chordObject) wake() {
	st := o.phase
	// Called by its wakes only.
	wakes := o.wakes
	if st.kind == objectActivating {
		// While parked its dependencies decide (dependency model §3.8).
		if !st.attempt.isParked() {
			wakes.during = st.attempt
		}
		return
	}
	if st.kind != objectFailed || wakes.leaving {
		return
	}
	o.host.loop().Queue(func() { o.retry(newRetryOperation()) })
}

// withdraw: the live attempt a leaves the registries' tables (it fails or ends), inside a batch; a composition this
// leaving makes wakes no wake signal of this object.
func (o *chordObject) withdraw(a *attempt) {
	contributions := a.contributionList()
	if len(contributions) == 0 {
		return
	}
	batch := o.host.batch
	wakes := o.wakes
	o.within(func() {
		if wakes != nil {
			wakes.leaving = true
			batch.Flushing(func(context.Context) error {
				wakes.leaving = false
				return nil
			})
		}
		for _, contribution := range contributions {
			contribution.Record.Remove(contribution)
		}
	})
}

// memberSource is the member source of member, made bound to exposed's member by its first use.
func (o *chordObject) memberSource(member string, exposed *attempt) (*memberSource, error) {
	if source := o.members[member]; source != nil {
		return source, nil
	}
	found, err := exposed.member(member)
	if err != nil {
		return nil, err
	}
	source := newMemberSource(found, o.host.batch, o.host.scheduler)
	o.members[member] = source
	o.memberOrder = append(o.memberOrder, member)
	return source, nil
}

// freeze: the attempt a failed live: its state members and pinned keys keep their values from now on (each made now when
// nothing used it yet, so reads while failed find it). foreign: those following another object's sources (at once); all:
// every one (once the admitted commits adopted).
func (o *chordObject) freeze(a *attempt, which freezeScope) {
	if o.exposed != a || o.phase.kind == objectEnded {
		return
	}
	for _, each := range a.stateMembers() {
		if which == freezeAll || !a.owns(each.source) {
			// The member exists in the attempt that exposed it.
			if source, err := o.memberSource(each.name, a); err == nil {
				source.freeze()
			}
		}
	}
	// A pin still loading is given up with the failure: a consumer parked on it shows `failed { waiting }`.
	failure := o.failure
	if failure == nil {
		failure = o.ended()
	}
	o.keys.freeze(which, failure)
}

// rebind: a new attempt went live, inside a batch: every member source follows its member of the same name, every pinned
// key its keyed member's (loaded by its activation).
func (o *chordObject) rebind(a *attempt) {
	for _, member := range o.memberOrder {
		source := o.members[member]
		if found, err := a.member(member); err == nil {
			source.bind(found)
			continue
		}
		variant := ""
		if name, ok := a.variantName(); ok {
			variant = " (variant " + name + ")"
		}
		source.vanish(data.Invalid(data.ReasonMember, o.text+variant+` has no state member "`+member+`"`), o.ctx)
	}
	o.keys.rebind()
}

// notify tells every holder that the state changed.
func (o *chordObject) notify() {
	for _, h := range slices.Clone(o.holderList) {
		h.moved()
	}
}

// seal marks the object ended: forgotten by the host, waits rejected, references to it ended with err (and gone from its
// type's presence), its job runs ended (signals fired, waiters rejected), its transactions refused, its attempt sealed.
// Returns what is left to tear down; it releases nothing itself.
func (o *chordObject) seal(err *data.ChordError) teardown {
	st := o.phase
	if st.kind == objectEnded {
		return teardown{kind: teardownNone}
	}
	previous := o.host.endingOf(o.identity)
	o.phase = objectState{kind: objectEnded, shown: &RefState{Kind: RefStateEnded, Error: err}}
	for _, records := range o.jobRecordsInOrder() {
		records.end(err)
	}
	if o.flush != nil {
		o.flush.timer.Stop()
	}
	o.flush = nil
	o.documents.Seal()
	o.stopLingering()
	if o.wakes != nil {
		o.wakes.stop()
	}
	o.host.forget(o)
	// An object of the start barrier's cohort that ends before its first outcome settles it as ended.
	o.settleFirstOutcome()
	// In a batch: the derives over its members are marked, so none keeps a cached value.
	o.within(func() {
		for _, member := range o.memberOrder {
			o.members[member].objectEnded(err)
		}
		o.keys.end(err)
	})
	clear(o.members)
	o.memberOrder = nil
	o.exposed = nil
	holders := slices.Clone(o.holderList)
	o.holderList = nil
	o.host.unreferenced(o, holders, o.ctx)
	for _, h := range holders {
		h.end(err)
	}
	// A failed attempt is closed (or closing) since it failed.
	if st.kind == objectWaiting || st.kind == objectFailed {
		return teardown{kind: teardownEnd, previous: previous}
	}
	if st.kind == objectLive {
		o.withdraw(st.attempt)
	}
	st.attempt.seal(err)
	return teardown{kind: teardownClose, attempt: st.attempt, previous: previous}
}

// idle: a remote object or a local one with params ends once it has no references and no running or finishing job runs: a
// live one once its type's `linger` passed without a new reference, any other at once.
func (o *chordObject) idle() {
	kind := o.phase.kind
	// A run waiting for a live attempt keeps its object only while one is live or activating.
	running := o.runs > 0 && (kind == objectLive || kind == objectActivating)
	kept := len(o.holderList) > 0 || running || (o.served.kind == servedLocal && o.served.hostLifetime)
	if kept || kind == objectEnded || o.lingering != nil {
		return
	}
	if kind != objectLive || o.served.linger == 0 {
		o.end(o.ended())
		return
	}
	// A new holder clears the timer (`hold`), so an expiry finds none.
	o.lingering = o.host.loop().AfterFunc(o.served.linger, func() {
		o.stopLingering()
		o.end(o.ended())
	})
	o.host.count(activityLingering, 1)
}

// stopLingering: its linger timer stops (a new holder, its expiry, its end): no longer counted as lingering.
func (o *chordObject) stopLingering() {
	if o.lingering == nil {
		return
	}
	o.lingering.Stop()
	o.lingering = nil
	o.host.count(activityLingering, -1)
}

// tearDown, once the previous teardowns finished (a failed attempt's closes may read the documents): drains and closes a
// sealed attempt, then releases its references (after the closes, which may use them), then waits for an admitted commit
// step to adopt and drops the documents and job runs. It returns the closes that threw. It blocks: it is called outside a
// section.
func (o *chordObject) tearDown(td teardown, reason NativeCloseReason) []*closeFailed {
	if td.kind == teardownNone {
		return nil
	}
	if td.previous != nil {
		// An ending never rejects.
		_, _ = td.previous.await()
	}
	var failures []*closeFailed
	if td.kind == teardownClose {
		failures = td.attempt.close(reason)
		o.host.enter(func() {
			td.attempt.detach()
			td.attempt.releaseReferences()
		})
	}
	o.documents.Settled()
	o.host.enter(func() {
		o.documents.End()
		if o.host.store != nil {
			o.host.store.Claims.Release(o)
		}
		for _, records := range o.jobRecordsInOrder() {
			records.drop()
		}
		clear(o.jobs)
		o.jobOrder = nil
	})
	return failures
}

// reported reports the close failures of a teardown nobody waits for.
func (o *chordObject) reported(closing *keyedPromise[[]*closeFailed]) *keyedPromise[struct{}] {
	reported := newKeyedPromise[struct{}](o.host.loop())
	closing.then(func(failures []*closeFailed, _ error) {
		for _, failure := range failures {
			o.host.report(*failure)
		}
		reported.resolve(struct{}{})
	})
	return reported
}

// live is the live attempt; `not_ready` before the object went live and while it activates again, the cause while failed,
// `released` once it ended.
func (o *chordObject) live() (*attempt, error) {
	st := o.phase
	switch st.kind {
	case objectLive:
		return st.attempt, nil
	case objectEnded:
		return nil, st.shown.Error
	case objectFailed:
		return nil, st.shown.Cause.Error
	}
	return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: o.text + ": not live yet"}, nil)
}

// handled is whether the retry operation id reached it before (among the last few: the diamond's shortcut; a ring is
// stopped by `through`); records it otherwise.
func (o *chordObject) handled(id string) bool {
	if slices.Contains(o.retried, id) {
		return true
	}
	if len(o.retried) == retriesKept {
		o.retried = o.retried[1:]
	}
	o.retried = append(o.retried, id)
	return false
}

// settleFirstOutcome is the settlement of its first outcome (once): dependents parked on it check their first pass again.
func (o *chordObject) settleFirstOutcome() {
	if o.hasFirstOutcome {
		return
	}
	o.hasFirstOutcome = true
	if o.firstOutcomeWait != nil {
		o.firstOutcomeWait.resolve(struct{}{})
	}
	for _, h := range o.holderList {
		if dependent := h.dependent(); dependent != nil {
			dependent.dependencyMoved()
		}
	}
}

// pathTo is the chain of waits from this activation to goal (see target), through its parked attempt's dependencies.
func (o *chordObject) pathTo(goal *chordObject, visited map[target]struct{}) []*chordObject {
	if o == goal {
		return []*chordObject{o}
	}
	visited[o] = struct{}{}
	for _, each := range o.waitingFor {
		if each.kind != dependencyReference {
			continue
		}
		next := each.binding.target()
		if next == nil {
			continue
		}
		if _, seen := visited[next]; seen {
			continue
		}
		if rest := next.pathTo(goal, visited); rest != nil {
			return append([]*chordObject{o}, rest...)
		}
	}
	return nil
}

// sameState is whether two states of a reference say the same: the same object, or failures with the same cause (by value).
func sameState(a, b *RefState) bool {
	if a == b {
		return true
	}
	if a.Kind != RefStateFailed || b.Kind != RefStateFailed {
		return false
	}
	sameRetry := (a.RetryAt == nil && b.RetryAt == nil) || (a.RetryAt != nil && b.RetryAt != nil && *a.RetryAt == *b.RetryAt)
	return sameRetry && data.SameCause(a.Cause, b.Cause)
}
