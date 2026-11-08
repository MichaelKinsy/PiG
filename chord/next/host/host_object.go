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
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/host/object.ts. Objects (§6): one per identity, from its first attempt to its end. Its state
// across attempts (its documents, line and job runs), the instance its references follow (`followed`: decision "References
// are the only indirection"), its holders, retries after failures (backoff, `ref.retry()`, passed on while parked), what it
// shows while its attempt is parked on dependencies (dependency model §3), its first outcome (the start barrier, decision
// (a)), a live object's failure (slice 6a §1.4), the waits and cycle checks of activations (Q5, its instance id and the
// activation chain it was created for), lingering after its last reference (§3, D8), the job runs that keep it alive (§15),
// its contributions to registries while live and its wake signals (`retryOn`, slice 8), its ending, revocation included (§4,
// R11), and what a reload does to it (slice 7b §4): a pure swap in place, a native restart after the drain, a retry on the
// new code, or its end by its type's removal, after which its references wait with their last values and follow the next
// object of the identity.
//
// Async contract. Every method but tearDown and stop's teardown runs inside a section of the host's loop; a Promise Pi
// keeps (`firstOutcome`, the endings `host.ending` holds, a swap's `done`, a retirement) is a keyedPromise settled in a
// section, its reactions microtasks of the loop (`Promise.all` is promiseAll2 / promiseAllVoid: the reaction of the last
// one settles it, so a reaction on it runs one microtask later). Pi's background promises (`void this.#run(attempt)`,
// `closing`, `#reported`, `#retire`) are continuations on those promises; the steps of them that block (`attempt.close`,
// `documents.settled`) are owned tasks of the loop (`awaitTask`) that settle in a section. `tearDown` and the function
// `stop` returns block: they are called outside a section (the host's close runs them on a task, `end` on an owned one). A
// call waiting at a swap's gate (`call`) is a parkedCallResult settled by the continuation of the gate.
//
// Forced Go differences. A `Map`/`Set` Pi iterates (holders, retiring attempts, job records) keeps its insertion order in a
// slice with an index. `host.backoff.until` is `(at, ok)`. Pi's `endingOf` promise is a *keyedPromise (nil is undefined). A
// thrown `ChordError` is a returned error. `Ending`'s optional `end` default (`this.ended()`) is each caller's. A reference
// member's `Followed.instance` is `any` (a pointer or nil, compared by identity).

// servedKind is what a served type is.
type servedKind uint8

const (
	// servedLocal is a `local` type: its factory gets the params and `self`.
	servedLocal servedKind = iota
	// servedRemote is a `remote` type: its factory also gets the principal its object is for.
	servedRemote
)

// served is a type served on a host, by kind: its contract, factory (a remote one gets the object's principal) and argument
// checks. Factories are plugin code: what they return is checked at activation. Pi's `Served`.
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
	// contract is the consumer's contract version and fingerprint (its request's): the provider binds it or it waits
	// unbound.
	contract() contractRef
	// retarget runs inside a batch, before moved: what it follows of the object changed (`chordObject.followed`): a
	// reference moves its members' subscriptions to the new instance, or keeps their last values; a hold over a link has
	// nothing to do here.
	retarget()
	// following is whether retarget moves anything (a reference whose members or keys were used): it then runs in a batch.
	following() bool
	// moved: the object's state changed: an attempt started after a failure, it went live, or it failed.
	moved()
	// end: the object ended with err.
	end(err *data.ChordError)
	// removed: the object ended because a reload removed its type (slice 7b §6): the holder's request is not ended; it
	// waits unbound with removal.cause (`not_served`) until a publication serves the name again.
	removed(r removal)
}

// removal is what an object tells holders it leaves: why they wait (`not_served` after a removal, `contract_mismatch` after
// a version refusal), its params (they place their requests again) and its context. A reference keeps its last values
// meanwhile and moves its subscriptions to the next object of the identity that serves its version (one frame).
type removal struct {
	cause  data.FailureCause
	host   *chordHost
	params checkedParams
	ctx    context.Context
}

// reloadFate is what a reload does to a live, activating, parked or failed object whose type is still served (slice 7b §4).
type reloadFate string

const (
	reloadSwap    reloadFate = "swap"
	reloadRestart reloadFate = "restart"
	reloadRetry   reloadFate = "retry"
)

// swap is a pure swap in progress (slice 7b §5): the attempt it replaces (its calls run on, then it is sealed), whether the
// new attempt parked (holders then see what it shows, and the old one's contributions left the tables), and the end of the
// swap (its new attempt live, parked or failed, or the object ended), which calls arriving meanwhile wait for.
type swap struct {
	old    *attempt
	parked bool
	done   *keyedPromise[struct{}]
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
// failed (a failed state) and ended (an ended one); swap of activating while a reload's pure swap replaces the live attempt
// (nil otherwise).
type objectState struct {
	kind    objectStateKind
	attempt *attempt
	shown   *RefState
	swap    *swap
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

// endingKind is how an object ends: by an end (its last reference, revocation, the host closing) or by its type's removal.
type endingKind uint8

const (
	endingEnd endingKind = iota
	endingRemoved
)

// ending is how an object ends, with the error its calls and holders get; cause is of a removal.
type ending struct {
	kind  endingKind
	err   *data.ChordError
	cause data.FailureCause
}

var (
	closeEnd    = NativeCloseReason{Kind: NativeCloseEnd}
	closeClose  = NativeCloseReason{Kind: NativeCloseClose}
	closeReload = NativeCloseReason{Kind: NativeCloseReload}
)

// heldFlush is the one timer storing an object's jobs' held state changes by their earliest deadline (D8).
type heldFlush struct {
	deadline int64
	timer    Timer
}

// retiringAttempt is a swapped-out attempt while it retires, with the promise of its retirement.
type retiringAttempt struct {
	old  *attempt
	done *keyedPromise[struct{}]
}

// chordObject is one object. Each attempt is a fresh `attempt`; the documents, the line, the job runs and the holders
// outlive it. An attempt parked on its dependencies waits for them without a timer; a transient failure (its own) is retried
// after its backoff window, or at once when a reference asks (`retry`); a broken one stays failed until a reference asks. The
// object ends with its last reference, a live one after its type's `linger` (never, for a host-lifetime object), or when the
// host closes.
type chordObject struct {
	host *chordHost
	// served is its served type: replaced by a reload that swaps, restarts or retries it.
	served *served
	params *delta.JsonObject
	// key is its params' identity key (empty for empty params).
	key string
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
	// liveQueries are its live queries by table, across its attempts; made by the first table it declares.
	liveQueries *store.LiveQueries
	// ctx is the context of every attempt: its creator's, without the creator's cancellation.
	ctx context.Context
	// jobs are its job runs by job name, across its attempts, in the order they were declared.
	jobs     map[string]*jobRecords
	jobOrder []string
	// instanceID is this instance's id (uuidv7), minted at its first attempt: a restarted object is a new instance (D4);
	// empty is Pi's undefined.
	instanceID string
	phase      objectState
	// holderList are the references and holds of this object, in the order they took it.
	holderList []holder
	// waitingFor are the dependencies its parked attempt waits for; their objects are followed to find cycles (Q5).
	waitingFor []dependency
	// firstOutcomeWait is its first outcome's promise, made by the first who waits for it (the start barrier, for
	// host-lifetime objects); hasFirstOutcome says it came.
	firstOutcomeWait *keyedPromise[struct{}]
	hasFirstOutcome  bool
	// failure is the error of the last failed attempt, or the last cause a parked attempt showed, until one goes live.
	failure *data.ChordError
	// lingering ends the object when its type's `linger` passed after its last reference; cleared by a new one.
	lingering Timer
	// runs are its running and finishing job runs: they keep it alive like references (D4), without being references.
	runs int
	// exposed is the instance its references follow (`followed`): the live one, the old one while a swap brings its
	// successor in place, or the last one that went live while it still changes after a failure or a restart (references
	// take their last values from it when it stops); nil before the first went live and once it stopped changing.
	exposed *attempt
	// following is whether references still follow exposed, which may still change: while it is live, drains (a restart),
	// retires (a swap's old instance until its successor is live or it retired), or failed live until the commits admitted
	// before it published (every member, its own and re-exposed ones: audit A10); not once it stopped changing.
	following bool
	// flush is the one timer storing its jobs' held state changes by their earliest deadline (D8), while one is held.
	flush *heldFlush
	// wakes are its wake signals (`retryOn`), the last attempt's declaration; made by the first.
	wakes *retryWakes
	// retiring are its swapped-out attempts while they retire (bounded by `closeTimeout`), in the order they were swapped.
	retiring []retiringAttempt
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
		host: host, served: entry, params: params, key: key, principal: principal, identity: identity, chain: chain,
		ctx: ctx, jobs: map[string]*jobRecords{},
	}
	o.text = entry.contract.Name
	if key != "" {
		o.text = entry.contract.Name + "(" + key + ")"
	}
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

// lastFailure is the error of its last failure while none went live since: a failed attempt's, or the cause its parked
// attempt showed last (kept when the dependency falls back to `loading`, so a getter's deadline names it).
func (o *chordObject) lastFailure() *data.ChordError { return o.failure }

// activationId is its instance id while an attempt activates; empty otherwise (a hold's chain names only activations).
func (o *chordObject) activationId() string {
	if o.phase.kind == objectActivating {
		return o.instanceID
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
	live, err := o.live()
	if err != nil {
		return nil, err
	}
	return live.stateMembers(), nil
}

// attempt starts an attempt when waiting or failed and returns it; a trigger while an attempt runs (or once live) is ignored
// (nil). After a failure the holders are told: their references are loading again.
func (o *chordObject) attempt() *attempt {
	st := o.phase
	if st.kind != objectWaiting && st.kind != objectFailed {
		return nil
	}
	if o.instanceID == "" {
		o.instanceID = uuidv7()
	}
	next := newAttempt(o, o.ctx)
	o.phase = objectState{kind: objectActivating, attempt: next, shown: refLoading}
	if st.kind == objectFailed {
		o.notify()
	}
	o.run(next, o.host.endingOf(o.identity))
	return next
}

// retry is `ref.retry()` and a `retry` message, as a step of walk (handled once per walk: a diamond reaches it twice): an
// attempt now when failed (a broken one too) or waiting, its backoff window skipped; passed on to the failed dependencies
// while parked `failed { waiting }`; ignored otherwise (an attempt runs, or it is live).
func (o *chordObject) retry(walk *retryWalk) {
	if _, seen := walk.visited[o]; seen {
		return
	}
	walk.visited[o] = struct{}{}
	st := o.phase
	if st.kind == objectActivating {
		if st.shown.Kind == RefStateFailed {
			st.attempt.retryDependencies(walk)
		}
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

// call calls a method of the live object for from, bound at version (an older one maps its arguments and result, Q10). While
// a reload's pure swap runs, the call waits for its new attempt (the gate), bounded by ctx; a refusal (not live, ended,
// failed) rejects the result.
func (o *chordObject) call(method string, args any, ctx context.Context, from caller, version int) *parkedCallResult {
	if st := o.phase; st.kind == objectActivating && st.swap != nil {
		return o.gated(st.swap, method, args, ctx, from, version)
	}
	live, err := o.live()
	if err != nil {
		result := newParkedCallResult()
		result.settle(nil, err)
		return result
	}
	return live.call(method, args, ctx, from, version)
}

// gated is a call that waits until sw ended (its new attempt live, parked, failed, or the object ended), then goes to the live
// attempt. No timer of its own: the swap ends when its new attempt is live, parked or failed, and `readyTimeout` bounds the
// new attempt's own steps (Fable).
func (o *chordObject) gated(sw *swap, method string, args any, ctx context.Context, from caller, version int) *parkedCallResult {
	result := newParkedCallResult()
	loop := o.host.loop()
	sw.done.thenWithContext(ctx, func(_ struct{}, err error) {
		if err != nil {
			result.settle(nil, err)
			return
		}
		live, err := o.live()
		if err != nil {
			result.settle(nil, err)
			return
		}
		live.call(method, args, ctx, from, version).then(loop, result.settle)
	})
	return result
}

// swapsInPlace is whether a reload's pure swap runs in place: the new attempt neither parked nor failed yet. Holds stay bound
// meanwhile (their calls wait at the gate) and get a fresh `bound` once it is live.
func (o *chordObject) swapsInPlace() bool {
	st := o.phase
	return st.kind == objectActivating && st.swap != nil && !st.swap.parked
}

// checkedParams are its params as checked, with their identity key.
func (o *chordObject) checkedParams() checkedParams {
	return checkedParams{params: o.params, key: o.key}
}

// followed is what a reference of version follows for state member `member` now (decision "References are the only
// indirection"): the shown instance's member (its mapping at an older version) while that instance is live, while a swap
// brings its successor in place, and after a live failure until its admitted commits published; else nothing: `failed` with
// the cause's error, or `loading`, with that member to take a last value from.
func (o *chordObject) followed(member string, version int) followed {
	exposed := o.exposed
	var shown state.DeriveSource
	var lacking *data.ChordError
	var instance any
	if exposed != nil {
		instance = exposed
		source, err := exposed.memberAt(member, version)
		if err == nil {
			shown = source
		} else if failure := data.AsChordError(err, o.text); failure.Code != data.CodeContractMismatch {
			// The shown instance does not serve this version (a swap's old one, while a reload brings it): nothing yet.
			lacking = failure
		}
	}
	if exposed != nil && o.following {
		if shown != nil {
			return followed{kind: followedLive, source: shown, instance: instance}
		}
		if lacking != nil {
			return followed{kind: followedLacking, err: lacking}
		}
		return followed{kind: followedLoading}
	}
	st := o.phase
	var failure *RefState
	switch {
	case st.kind == objectFailed:
		failure = st.shown
	case st.kind == objectActivating && st.shown.Kind == RefStateFailed:
		failure = st.shown
	}
	if failure != nil {
		return followed{kind: followedFailed, err: failure.Cause.Error, last: shown, instance: instance}
	}
	return followed{kind: followedLoading, last: shown, instance: instance}
}

// attachable is the instance its references follow now (they attach to it; they take last values only from an instance they
// attached to); nil once it stopped changing.
func (o *chordObject) attachable() attachable {
	if o.following && o.exposed != nil {
		return o.exposed
	}
	return nil
}

// instance is the live instance, which references pin their keys on (a new one: they pin again); nil when not live.
func (o *chordObject) instance() any {
	if o.phase.kind == objectLive {
		return o.phase.attempt
	}
	return nil
}

// liveMember is the live instance's state member `member` as a consumer of version follows it (a hold over a link).
func (o *chordObject) liveMember(member string, version int) (state.DeriveSource, error) {
	live, err := o.live()
	if err != nil {
		return nil, err
	}
	return live.memberAt(member, version)
}

// keyedMapping is how a consumer of version reads keyed member `member`'s documents: mapped back when older (nil: as they
// are).
func (o *chordObject) keyedMapping(member string, version int) (contracts.Mapping, error) {
	live, err := o.live()
	if err != nil {
		return nil, err
	}
	return live.keyMapping(member, version)
}

// keyedMember is the keyed member `member` of the live object: its ultimate slot (slice 6a §12).
func (o *chordObject) keyedMember(member string) (*documents.KeyedSlot, error) {
	live, err := o.live()
	if err != nil {
		return nil, err
	}
	return live.keyedMember(member)
}

// pinKey pins `key` of keyed member `member` on the live instance, as a consumer of version reads it (`attempt.pinKey`). It
// blocks, outside a section.
func (o *chordObject) pinKey(member, key string, version int, ctx context.Context) (pinnedKey, error) {
	var live *attempt
	var err error
	o.host.enter(func() { live, err = o.live() })
	if err != nil {
		return pinnedKey{}, err
	}
	return live.pinKey(member, key, version, ctx)
}

// hold counts holder as a reference to this object (the holder reads `state` itself), listed in its type's presence; a
// lingering object stays. A version it binds whose member the live provider refuses (an older consumer): it waits unbound
// instead.
func (o *chordObject) hold(h holder) {
	if st := o.phase; st.kind == objectLive {
		if unfit := o.unfit(h, st.attempt); unfit != nil {
			o.unbound(h, unfit)
			return
		}
	}
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
	o.dropUnheld(h.contract().version)
	o.idle()
	// Last: a derive over the presence list may close a link synchronously, which releases other holders again.
	o.host.unreferenced(o, []holder{h}, o.ctx)
}

// jobRecords are the records of job definition's runs, made by its first declaration. A later declaration (a new attempt's,
// after a reload) at another version takes the records over (each run keeps its version, slice 7b §8); one at the same
// version with another fingerprint is refused (checkJob).
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
	if err := o.checkJob(definition); err != nil {
		return nil, err
	}
	if err := records.redefine(definition); err != nil {
		return nil, err
	}
	return records, nil
}

// checkJob refuses a declaration of a job its records cannot take (`jobRecords.check`); a reload's staging checks it too.
func (o *chordObject) checkJob(definition *JobDefinition) error {
	if records := o.jobs[definition.Name]; records != nil {
		return records.check(definition)
	}
	return nil
}

// claimTable claims table (its SQL name) in the host's store for this object, until its end (slice 6b claims); held by
// another owner: `invalid/args`, which fails the declaring factory.
func (o *chordObject) claimTable(table string) error { return o.host.store.Claims.Table(table, o) }

// queries are its live queries by table (`self.query`), across its attempts: what its commits writing a table re-run.
func (o *chordObject) queries() *store.LiveQueries {
	if o.liveQueries == nil {
		o.liveQueries = store.NewLiveQueries()
	}
	return o.liveQueries
}

// claimDocuments claims this object's documents in namespace of the host's store, as claimTable.
func (o *chordObject) claimDocuments(namespace string) error {
	return o.host.store.Claims.Documents(o.served.contract.Name, namespace, o)
}

// failLive is a runtime failure of the live attempt `a`: `broken` (a `migrate` failing on access, a stored version newer than
// the code, a refused commit) or `transient` (a followed runtime failed, R4): the attempt is sealed and the object `failed`
// with cause; calls reject with it, holders are told, its references keep their last values once the admitted commits
// adopted (`followed`), the natives close in the background (then its references are released), `onError` gets
// `object_failed` once. Documents and job runs stay. Recovery: a broken failure by `ref.retry()`, a transient one after its
// identity's backoff too (a new attempt, whose members its references follow once live). A failure of a sealed attempt is
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
	a.seal(cause.err, cutRestart)
	// Its references follow every member on until the commits admitted before the seal adopted and published once (frames
	// included), then stop. A retry's factory and hydration wait for both (its ending), so no old commit lands in a new
	// attempt.
	loop := o.host.loop()
	settled := awaitTask(o.host, func() (struct{}, error) {
		o.documents.Settled()
		return struct{}{}, nil
	})
	frozen := newKeyedPromise[struct{}](loop)
	settled.then(func(struct{}, error) {
		o.stopFollowing(a)
		frozen.resolve(struct{}{})
	})
	closing := promiseAll2(loop, awaitTask(o.host, func() ([]*closeFailed, error) { return a.close(closeEnd), nil }), frozen)
	done := newKeyedPromise[[]*closeFailed](loop)
	closing.then(func(failures []*closeFailed, _ error) {
		a.detach()
		a.releaseReferences()
		done.resolve(failures)
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
	sw := st.swap
	// A swap's new attempt parks (decision 4): no overlap of old and new code, holders see what it shows.
	parks := sw != nil && !sw.parked
	if parks {
		o.swapParked(sw)
	}
	if parks || !sameState(st.shown, shown) {
		o.phase = objectState{kind: objectActivating, attempt: a, shown: shown, swap: sw}
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
func (o *chordObject) unparked(a *attempt) {
	st := o.phase
	if st.kind != objectActivating || st.attempt != a {
		return
	}
	if !sameState(st.shown, refLoading) {
		o.parked(a, refLoading, false)
	}
}

// swapParked: a swap's new attempt parked: the old attempt's contributions leave the tables (it is not live any more).
func (o *chordObject) swapParked(sw *swap) {
	sw.parked = true
	o.withdraw(sw.old)
	sw.done.resolve(struct{}{})
}

// end ends the object (once): sealed now, its holders and running calls told err (`released`: ended()), torn down in the
// background with reason `end`, close failures reported.
func (o *chordObject) end(err *data.ChordError) {
	td := o.seal(ending{kind: endingEnd, err: err})
	if td.kind == teardownNone {
		return
	}
	torn := awaitTask(o.host, func() ([]*closeFailed, error) { return o.tearDown(td, closeEnd), nil })
	o.host.ending(o.identity, o.reported(torn))
}

// stop ends the object for `host.close`: sealed now; returns its teardown (reason `close`), which the host runs once every
// object is sealed, so no reference it releases starts an ending in the background. The teardown blocks.
func (o *chordObject) stop() func() []*closeFailed {
	td := o.seal(ending{kind: endingEnd, err: o.ended()})
	return func() []*closeFailed { return o.tearDown(td, closeClose) }
}

// remove ends the object because a reload removed its type (slice 7b §4): as `end` (natives close with reason `end`, running
// calls' callers rejected `released`, handlers drained), but its holders' requests are not ended: they wait unbound with
// cause (`not_served`), references with their last values, until the next object of its identity binds them (`removal`).
func (o *chordObject) remove(cause data.FailureCause) {
	td := o.seal(ending{kind: endingRemoved, err: o.ended(), cause: cause})
	if td.kind == teardownNone {
		return
	}
	torn := awaitTask(o.host, func() ([]*closeFailed, error) { return o.tearDown(td, closeEnd), nil })
	o.host.ending(o.identity, o.reported(torn))
}

// reloadFate is what a reload with recorded (the new factory's declarations) does to this object, whose type is still
// served: a live object swaps in place when neither its live attempt nor the new factory opens natives and no table of the
// new factory is newer than the store's, else it restarts; any other is retried.
func (o *chordObject) reloadFate(recorded Recorded) reloadFate {
	st := o.phase
	if st.kind != objectLive {
		return reloadRetry
	}
	if st.attempt.opensNatives() || recorded.natives > 0 || migratesTables(o, recorded) {
		return reloadRestart
	}
	return reloadSwap
}

// reload takes fate with its new served type (after the reload's commit); it returns the new attempt, whose first dependency
// check the reload's report waits for.
func (o *chordObject) reload(entry *served, fate reloadFate) *attempt {
	// Its members per version stay: the previous version's are mapped once the new attempt goes live.
	o.served = entry
	var next *attempt
	switch fate {
	case reloadSwap:
		next = o.swap()
	case reloadRestart:
		next = o.restart()
	default:
		next = o.retryReloaded()
	}
	next.startedByReload()
	return next
}

// swap is a pure swap (slice 7b §5): a new attempt of the same object (its documents, line and job records, its instance
// id), its activation at once; the live attempt keeps its running calls, its routines aborted, its jobs handed over, and is
// sealed once its calls settled and the new attempt's requests bound (`retire`). Calls arriving meanwhile wait at the gate.
func (o *chordObject) swap() *attempt {
	// A swap starts from a live object (`reloadFate`).
	old := o.phase.attempt
	ending := o.host.endingOf(o.identity)
	next := newAttempt(o, o.ctx)
	sw := &swap{old: old, done: newKeyedPromise[struct{}](o.host.loop())}
	o.phase = objectState{kind: objectActivating, attempt: next, shown: refLoading, swap: sw}
	old.handOver()
	o.retiringAdd(old, o.retire(sw))
	o.notify()
	o.run(next, ending)
	return next
}

// retiringAdd keeps retire (a swapped-out attempt's) until it settled: a restart, an end and the host's close wait for it; a
// later swap does not (it holds no natives, its documents are the object's: Fable, Opus F1), so a second reload is never held
// up by the first one's old calls.
func (o *chordObject) retiringAdd(old *attempt, retire *keyedPromise[struct{}]) {
	o.retiring = append(o.retiring, retiringAttempt{old: old, done: retire})
	retire.then(func(struct{}, error) {
		for at, each := range o.retiring {
			if each.old == old {
				o.retiring = slices.Delete(o.retiring, at, at+1)
				return
			}
		}
	})
}

// retiringNow is its swapped-out attempts still retiring, as one wait; nil when none is.
func (o *chordObject) retiringNow() *keyedPromise[struct{}] {
	if len(o.retiring) == 0 {
		return nil
	}
	waits := make([]*keyedPromise[struct{}], 0, len(o.retiring))
	for _, each := range o.retiring {
		waits = append(waits, each.done)
	}
	loop := o.host.loop()
	result := newKeyedPromise[struct{}](loop)
	promiseAllVoid(loop, waits).then(func(struct{}, error) { result.resolve(struct{}{}) })
	return result
}

// retire seals a swapped attempt once its running calls (and routine calls) settled, bounded by `closeTimeout`, and the swap
// ended (the new attempt live, parked, failed: its requests bound, so no request it shares with the old one ends in between,
// M§3.9, and its holders compared the old members with the new); a parked swap's references take their last values from it
// once the commits it admitted before the seal published; then its references are released. Its job invocations run on, on
// their old code.
func (o *chordObject) retire(sw *swap) *keyedPromise[struct{}] {
	host := o.host
	loop := host.loop()
	old := sw.old
	retired := newKeyedPromise[struct{}](loop)
	promiseAll2(loop, old.drainCalls(), sw.done).then(func(struct{}, error) {
		old.seal(o.reloaded(), cutRestart)
		// A parked swap's old instance stops changing once its admitted commits published (its successor, live, took over its
		// references before).
		awaitTask(host, func() (struct{}, error) {
			o.documents.Settled()
			return struct{}{}, nil
		}).then(func(struct{}, error) {
			o.stopFollowing(old)
			awaitTask(host, func() ([]*closeFailed, error) { return old.close(closeEnd), nil }).then(
				func(failures []*closeFailed, _ error) {
					old.detach()
					old.releaseReferences()
					for _, failure := range failures {
						host.report(*failure)
					}
					retired.resolve(struct{}{})
				})
		})
	})
	return retired
}

// restart is a native restart (decision "A native restart drains first"): new calls park (it shows `loading`), its
// contributions leave, the live attempt's running calls drain (bounded by `closeTimeout`), then it is sealed (its in-memory
// job runs end `aborted { reload }`), its references take their last values once its admitted commits published, its natives
// close with reason `reload`; the new attempt, a new instance (D4), activates after that teardown.
func (o *chordObject) restart() *attempt {
	host := o.host
	loop := host.loop()
	old := o.phase.attempt
	o.withdraw(old)
	old.stopRoutines()
	o.instanceID = uuidv7()
	teardown := newKeyedPromise[[]*closeFailed](loop)
	drain := old.drainCalls()
	retiring := o.retiringNow()
	if retiring == nil {
		retiring = newKeyedPromise[struct{}](loop)
		retiring.resolve(struct{}{})
	}
	promiseAll2(loop, drain, retiring).then(func(struct{}, error) {
		old.seal(o.reloaded(), cutReload)
		// Drained and sealed: its references take their last values once the commits it admitted before published (a store
		// write is not a running call's promise; Sol).
		awaitTask(host, func() (struct{}, error) {
			o.documents.Settled()
			return struct{}{}, nil
		}).then(func(struct{}, error) {
			o.stopFollowing(old)
			awaitTask(host, func() ([]*closeFailed, error) { return old.close(closeReload), nil }).then(
				func(failures []*closeFailed, _ error) {
					old.detach()
					old.releaseReferences()
					teardown.resolve(failures)
				})
		})
	})
	host.ending(o.identity, o.reported(teardown))
	next := o.replace()
	o.run(next, host.endingOf(o.identity))
	return next
}

// retryReloaded: a reload retries an object that is not live (slice 7b §9, decision 1): a parked attempt is replaced in one
// handover (it opened nothing: the new attempt's requests bind before the old ones are released, M§3.9); an attempt in its
// own steps is sealed, its natives close with reason `reload`, and the new one activates after that teardown; a failed or
// waiting one gets an attempt now, its backoff skipped.
func (o *chordObject) retryReloaded() *attempt {
	st := o.phase
	if st.kind != objectActivating {
		o.host.backoff.skip(o.identity)
		// Waiting or failed (`reloadFate`): an attempt starts.
		return o.attempt()
	}
	host := o.host
	loop := host.loop()
	old, sw := st.attempt, st.swap
	if sw != nil && !sw.parked {
		o.swapParked(sw)
	}
	if sw != nil {
		sw.done.resolve(struct{}{})
	}
	parked := old.isParked()
	// A parked attempt's successor waits for no teardown of it (its references are released after it bound).
	ending := host.endingOf(o.identity)
	old.seal(o.reloaded(), cutReload)
	next := o.replace()
	if parked {
		o.run(next, ending)
	}
	closing := newKeyedPromise[[]*closeFailed](loop)
	closeOld := func(struct{}, error) {
		awaitTask(host, func() ([]*closeFailed, error) { return old.close(closeReload), nil }).then(
			func(failures []*closeFailed, _ error) {
				old.detach()
				old.releaseReferences()
				closing.resolve(failures)
			})
	}
	if parked {
		next.checked().then(func(*data.FailureCause, error) { closeOld(struct{}{}, nil) })
	} else {
		now := newKeyedPromise[struct{}](loop)
		now.resolve(struct{}{})
		now.then(closeOld)
	}
	host.ending(o.identity, o.reported(closing))
	if !parked {
		o.run(next, host.endingOf(o.identity))
	}
	return next
}

// replace is a new attempt replacing the current one, holders told; its caller starts it (`run`) after what it waits for.
func (o *chordObject) replace() *attempt {
	next := newAttempt(o, o.ctx)
	o.phase = objectState{kind: objectActivating, attempt: next, shown: refLoading}
	o.notify()
	return next
}

// reloaded is the `released` error of an attempt a reload replaced.
func (o *chordObject) reloaded() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: o.text + ": replaced by a reload"}, nil)
}

// ended is the `released` error of this object.
func (o *chordObject) ended() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: o.text + ": ended"}, nil)
}

// run runs a's activation once ending (its predecessors' teardowns, if any; nil when none) finished; takes its outcome.
func (o *chordObject) run(a *attempt, ending *keyedPromise[struct{}]) {
	a.activate(ending).then(func(outcome activation, _ error) {
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
	sw := o.swapOf(a)
	// Before the new instance is shown: holders of a version its provider does not bind leave with their last values, which
	// their references take from the instance shown so far.
	unbound := o.unbindUnfit(a)
	o.phase = objectState{kind: objectLive, attempt: a}
	o.exposed = a
	o.following = true
	contributions := a.contributionList()
	// The old attempt's contributions leave in the batch the new ones enter (slice 7b §5), so no reader sees a gap.
	var leaving []*registries.RegistryContribution
	if sw != nil && !sw.parked {
		leaving = sw.old.contributionList()
	}
	// In the registries' tables before anyone sees it live (a getter released now reads them recomposed).
	if len(contributions) > 0 || len(leaving) > 0 {
		o.within(func() {
			for _, contribution := range leaving {
				contribution.Record.Remove(contribution)
			}
			for _, contribution := range contributions {
				contribution.Record.Add(contribution)
			}
		})
	}
	o.failure = nil
	o.host.wentLive(o)
	// Its references move to the new instance (one frame each); holds get a fresh `bound`.
	o.notify()
	o.settleFirstOutcome()
	var previous *attempt
	if sw != nil {
		sw.done.resolve(struct{}{})
		previous = sw.old
	}
	// A swap's routines start once the old attempt's routine calls finished (the handover, §14).
	a.live(previous)
	o.takeJobs(a)
	// Its last holders may have left for their version: it ends as at their release.
	if unbound {
		o.idle()
	}
}

// takeJobs: after a went live: the jobs it does not declare block their runs (slice 7b §8); after a reload, the blocked runs
// of the jobs it declares are retried.
func (o *chordObject) takeJobs(a *attempt) {
	if len(o.jobs) == 0 {
		return
	}
	reloaded := a.reloadStarted()
	for _, name := range o.jobOrder {
		records := o.jobs[name]
		if !a.declaresJob(name) {
			records.undeclare()
		} else if reloaded {
			// Pi filters synchronously inside the call, before its first await: the task gets the runs taken now.
			if retried := records.blockedToRetry(); len(retried) != 0 {
				o.host.loop().Go(func() { records.retryBlocked(retried, o.ctx) })
			}
		}
	}
}

// unbindUnfit: its holders whose version a's provider (going live) does not bind (a reload dropped it, slice 7b §6), or of
// which it refuses a member (`contract_mismatch`), leave it: they wait unbound `version` (`failed { waiting }`), references
// with their last values, until a publication serves their version again. The object and its other holders stay.
func (o *chordObject) unbindUnfit(a *attempt) bool {
	type unfitHolder struct {
		holder holder
		err    *data.ChordError
	}
	var unfit []unfitHolder
	for _, each := range o.holderList {
		if err := o.unfit(each, a); err != nil {
			unfit = append(unfit, unfitHolder{holder: each, err: err})
		}
	}
	if unfit == nil {
		return false
	}
	left := make([]holder, 0, len(unfit))
	for _, each := range unfit {
		left = append(left, each.holder)
		if at := slices.Index(o.holderList, each.holder); at >= 0 {
			o.holderList = slices.Delete(o.holderList, at, at+1)
		}
	}
	o.host.unreferenced(o, left, o.ctx)
	for _, each := range unfit {
		o.unbound(each.holder, each.err)
	}
	return true
}

// heldAt is whether a holder of version holds it.
func (o *chordObject) heldAt(version int) bool {
	for _, each := range o.holderList {
		if each.contract().version == version {
			return true
		}
	}
	return false
}

// unfit is why h cannot bind to a (live): its version is not one the provider binds, or the provider refuses a member of it
// (`contract_mismatch`); nil when it binds.
func (o *chordObject) unfit(h holder, a *attempt) *data.ChordError {
	contract, provider := o.served.contract, o.served.provider
	consumer := h.contract()
	if consumer.version == contract.Version && consumer.fingerprint == contract.Fingerprint {
		return nil
	}
	if data.Binds(provider, consumer.version, consumer.fingerprint) {
		return refusal(o.served, consumer.version, a.variant)
	}
	return notServedHere(contract, consumer.version)
}

// unbound: h (no longer held) waits unbound with err (`failed { waiting }`) until a publication fits it.
func (o *chordObject) unbound(h holder, err *data.ChordError) {
	cause := data.FailureCause{Kind: data.FailureWaiting, Error: err}
	h.removed(removal{cause: cause, host: o.host, params: o.checkedParams(), ctx: o.ctx})
}

// swapOf is the swap a (still the current one) ends: nil when it is no swap's new attempt.
func (o *chordObject) swapOf(a *attempt) *swap {
	st := o.phase
	if st.kind == objectActivating && st.attempt == a {
		return st.swap
	}
	return nil
}

// failedAttempt records a failed attempt: reported, torn down in the background (reason `end`), its references released once
// its natives closed, its holders told. Transient: a backoff window, whose end is the failure's `retryAt`; broken: no
// automatic retry.
func (o *chordObject) failedAttempt(a *attempt, cause data.FailureCause) {
	sw := o.swapOf(a)
	if sw != nil && !sw.parked {
		o.swapParked(sw)
	}
	if sw != nil {
		sw.done.resolve(struct{}{})
	}
	wokenBy := o.wakes != nil && o.wakes.during == a
	if o.wakes != nil {
		o.wakes.during = nil
	}
	if cause.Kind == data.FailureTransient {
		o.host.backoff.failed(o.identity)
	} else {
		o.host.backoff.delete(o.identity)
	}
	o.phase = objectState{kind: objectFailed, shown: o.failedState(cause.Kind, cause.Error)}
	o.failure = cause.Error
	a.seal(o.ended(), cutRestart)
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
		o.retry(newRetryWalk(link.RETRY_HOPS))
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
	o.host.loop().Queue(func() { o.retry(newRetryWalk(link.RETRY_HOPS)) })
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

// within runs work in a batch of the object's context. The adoptions return no error.
func (o *chordObject) within(work func()) {
	_ = o.host.batch.Within(func() error {
		work()
		return nil
	}, o.ctx)
}

// stopFollowing: a, the instance shown, stopped changing (a live failure's admitted commits published, a restart's drain, a
// swapped or parked old instance retiring): its references take their last values from it, holds leave it, and the object
// lets it go (a reference made later never attached to it: it takes nothing from it).
func (o *chordObject) stopFollowing(a *attempt) {
	if o.exposed != a || !o.following {
		return
	}
	o.following = false
	o.notify()
	// Its references took their values (`notify` retargets them at once): a failed object keeps no ended instance.
	o.exposed = nil
}

// notify tells every holder that the state changed: references first follow what it shows now (`followed`).
func (o *chordObject) notify() {
	holders := slices.Clone(o.holderList)
	// In one batch when some reference's members move (one frame each, a diamond over several sees one change); no commit
	// at all for references that used none (activation churn).
	retarget := func() {
		for _, each := range holders {
			each.retarget()
		}
	}
	moving := false
	for _, each := range holders {
		if each.following() {
			moving = true
			break
		}
	}
	if moving {
		o.within(retarget)
	} else {
		retarget()
	}
	for _, each := range holders {
		each.moved()
	}
}

// seal marks the object ended: forgotten by the host, waits rejected, references to it ended with err (and gone from its
// type's presence), its job runs ended (signals fired, waiters rejected), its transactions refused, its attempt sealed.
// Returns what is left to tear down; it releases nothing itself.
func (o *chordObject) seal(by ending) teardown {
	err := by.err
	st := o.phase
	if st.kind == objectEnded {
		return teardown{kind: teardownNone}
	}
	endings := o.host.endingOf(o.identity)
	// Its ending rejects the callers of its swapped-out attempts' calls too, at once.
	for _, each := range o.retiring {
		each.old.seal(err, cutRestart)
	}
	// Its swapped-out attempts end before its teardown completes (they share its documents).
	previous := endings
	if retiring := o.retiringNow(); retiring != nil {
		loop := o.host.loop()
		if endings == nil {
			endings = newKeyedPromise[struct{}](loop)
			endings.resolve(struct{}{})
		}
		previous = newKeyedPromise[struct{}](loop)
		both := promiseAll2(loop, endings, retiring)
		both.then(func(struct{}, error) { previous.resolve(struct{}{}) })
	}
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
	// Sealed: its instance stops changing; its references take their last values from it.
	o.following = false
	holders := slices.Clone(o.holderList)
	o.holderList = nil
	o.host.unreferenced(o, holders, o.ctx)
	// Its type removed: its references wait with their last values, which they take from the instance shown (still here);
	// otherwise they end with it.
	var removed *removal
	if by.kind == endingRemoved {
		removed = &removal{cause: by.cause, host: o.host, params: o.checkedParams(), ctx: o.ctx}
	}
	for _, each := range holders {
		if removed == nil {
			each.end(err)
		} else {
			each.removed(*removed)
		}
	}
	o.exposed = nil
	if st.kind == objectActivating && st.swap != nil {
		if !st.swap.parked {
			o.withdraw(st.swap.old)
		}
		st.swap.old.seal(err, cutRestart)
		st.swap.done.resolve(struct{}{})
	}
	// A failed attempt is closed (or closing) since it failed.
	if st.kind == objectWaiting || st.kind == objectFailed {
		return teardown{kind: teardownEnd, previous: previous}
	}
	if st.kind == objectLive {
		o.withdraw(st.attempt)
	}
	st.attempt.seal(err, cutRestart)
	return teardown{kind: teardownClose, attempt: st.attempt, previous: previous}
}

// dropUnheld: the last holder of version left: the shown instance's shared mapping for it goes (Opus F2), so a long-lived
// object keeps no mapped value nobody reads.
func (o *chordObject) dropUnheld(version int) {
	if o.heldAt(version) {
		return
	}
	if o.exposed != nil {
		o.exposed.dropVersion(version)
	}
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
		_, _ = td.previous.await(chordctx.Background())
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

// promiseAll2 is `Promise.all([first, second])` of promises that never reject, as first's value: it settles in the reaction
// of the last one to settle, so a reaction on it runs one microtask after that.
func promiseAll2[A, B any](loop sectionLoop, first *keyedPromise[A], second *keyedPromise[B]) *keyedPromise[A] {
	all := newKeyedPromise[A](loop)
	var value A
	waiting := 2
	arrived := func(err error) {
		if err != nil {
			all.reject(err)
			return
		}
		if waiting--; waiting == 0 {
			all.resolve(value)
		}
	}
	first.then(func(found A, err error) {
		value = found
		arrived(err)
	})
	second.then(func(_ B, err error) { arrived(err) })
	return all
}

// promiseAllVoid is `Promise.all(promises)` of promises that never reject (settled at once when there is none), as promiseAll2.
func promiseAllVoid(loop sectionLoop, promises []*keyedPromise[struct{}]) *keyedPromise[struct{}] {
	all := newKeyedPromise[struct{}](loop)
	waiting := len(promises)
	if waiting == 0 {
		all.resolve(struct{}{})
		return all
	}
	for _, each := range promises {
		each.then(func(_ struct{}, err error) {
			if err != nil {
				all.reject(err)
				return
			}
			if waiting--; waiting == 0 {
				all.resolve(struct{}{})
			}
		})
	}
	return all
}
