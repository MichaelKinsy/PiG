package host

import (
	"context"
	"fmt"
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
// R11), and what a reload does to it (decision "Reload restarts every affected object"): a restart whose old attempt serves old work, a retry on the
// new code, or its end by its type's removal, after which its references wait with their last values and follow the next
// object of the identity.
//
// Async contract. Every method but tearDown and stop's teardown runs inside a section of the host's loop; a Promise Pi
// keeps (`firstOutcome`, the endings `host.ending` holds, a restart's `ended`) is a keyedPromise settled in a
// section, its reactions microtasks of the loop (`Promise.all` is promiseAll2 / promiseAllVoid: the reaction of the last
// one settles it, so a reaction on it runs one microtask later). Pi's background promises (`void this.#run(attempt)`,
// `closing`, `#reported`, `#endOld`) are continuations on those promises; the steps of them that block (`attempt.close`,
// `documents.settled`) are owned tasks of the loop (`awaitTask`) that settle in a section. `tearDown` and the function
// `stop` returns block: they are called outside a section (the host's close runs them on a task, `end` on an owned one). A
// call waiting at the gate (`call`) is a parkedCallResult settled by the continuation of the gate.
//
// Forced Go differences. A `Map`/`Set` Pi iterates (holders, job records) keeps its insertion order in a
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
	// listed is whether presence lists show it (§19): not a restart's readiness binding, which is no consumer of its own.
	listed() bool
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

// reloadFate is what a reload does to an object whose type is still served: a live one restarts, any other is retried.
type reloadFate string

const (
	reloadRestart reloadFate = "restart"
	reloadRetry   reloadFate = "retry"
)

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

// chordObject is one object. Each attempt is a fresh `attempt`; the documents, the line, the job runs and the holders
// outlive it. An attempt parked on its dependencies waits for them without a timer; a transient failure (its own) is retried
// after its backoff window, or at once when a reference asks (`retry`); a broken one stays failed until a reference asks. The
// object ends with its last reference, a live one after its type's `linger` (never, for a host-lifetime object), or when the
// host closes.
type chordObject struct {
	host *chordHost
	// served is its served type: replaced by a reload that restarts or retries it.
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
	// changed resolves at its next change of state, for calls waiting at the gate; made by the first one.
	changed *keyedPromise[struct{}]
	// runs are its running and finishing job runs: they keep it alive like references (D4), without being references.
	runs int
	// exposed is the instance its references follow (`followed`): the live one, or the last one that went live while it still
	// changes after a failure or during a restart (references take their last values from it when it stops); nil before the
	// first went live and once it stopped changing.
	exposed *attempt
	// following is whether references still follow exposed, which may still change: while it is live, while a restart keeps
	// it open for old work, or failed live until the commits admitted before it published (every member, its own and
	// re-exposed ones: audit A10); not once it stopped changing.
	following bool
	// flush is the one timer storing its jobs' held state changes by their earliest deadline (D8), while one is held.
	flush *heldFlush
	// wakes are its wake signals (`retryOn`), the last attempt's declaration; made by the first.
	wakes *retryWakes
	// old is a restart's old attempt while it is open (until it stopped changing): old work runs on it, and consumers whose
	// version it binds see it live (decision "Reload restarts every affected object", rules 2 and 5).
	old *attempt
	// kept is a restart's old attempt without natives whose contributions stay in the tables until its successor is live
	// (F-c), replaced there in one batch; they leave if the successor fails, parks on a failure or the object ends.
	kept *attempt
	// readiness is a restart's readiness, until its successor's first dependency check.
	readiness *readiness
	// showsReadiness is a restart's successor that shows its readiness's failures, until the old attempt ended.
	showsReadiness *attempt
	// startSuccessorFn starts a restart's readiness and successor, once every fate of its reload started (`startSuccessor`).
	startSuccessorFn func()
	// gateWaits are the objects whose gate its activation's opener waits at now: its wait edges for reload cycles.
	gateWaits map[*chordObject]struct{}
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

// kind is where it is: an object on this host (a reference's target that is not held over a link).
func (o *chordObject) kind() string { return "local" }

// hydrated is whether its state members have values: once live (a local object stays live until it ends), and while a
// restart's old attempt is open (its consumers check their version through `stateFor`).
func (o *chordObject) hydrated() bool { return o.phase.kind == objectLive || o.openOldAttempt() != nil }

// stateFor is the state a same-host consumer of version and fingerprint sees (rule 5): `live` while a restart's open old
// attempt binds that version and its successor is not parked on a failure; otherwise `state`.
func (o *chordObject) stateFor(version int, fingerprint string) *RefState {
	st := o.phase
	if st.kind == objectActivating && st.shown.Kind != RefStateFailed && o.oldServes(version, fingerprint) {
		return refLive
	}
	return o.state()
}

// oldServes is whether a restart's open old attempt binds version and fingerprint: its consumers of that version may use it.
func (o *chordObject) oldServes(version int, fingerprint string) bool {
	old := o.openOldAttempt()
	return old != nil && data.Binds(old.served.provider, version, fingerprint)
}

// openOldAttempt is a restart's old attempt while it is open (draining, not sealed yet): old work runs on it, and consumers
// whose version it binds see it as the live instance; nil otherwise.
func (o *chordObject) openOldAttempt() *attempt {
	if old := o.old; old != nil && old.draining() {
		return old
	}
	return nil
}

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

// variant is the variant a live remote object's factory chose (a restart's open old attempt's meanwhile); not bound for a
// local object and before it is live.
func (o *chordObject) variant() (string, bool) {
	if o.phase.kind == objectLive {
		return o.phase.attempt.variantName()
	}
	if old := o.openOldAttempt(); old != nil {
		return old.variantName()
	}
	return "", false
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
// an attempt activates (a reload's restart, a retry), new work waits for its outcome (the gate); old work runs on a restart's
// open old attempt. A refusal (not live, ended, failed) rejects the result.
func (o *chordObject) call(method string, args any, ctx context.Context, from caller, version int) *parkedCallResult {
	if o.phase.kind == objectActivating {
		// Old work runs on the open old attempt (rule 2): work begun before the reload finishes on old code, and a restart's
		// drain never waits on itself. The old attempt checks the call's version against its own contract.
		if old := o.openOldAttempt(); old != nil && o.host.oldWork.of(ctx) {
			return old.call(method, args, ctx, from, version)
		}
		return o.gated(method, args, ctx, from, version)
	}
	live, err := o.live()
	if err != nil {
		result := newParkedCallResult()
		result.settle(nil, err)
		return result
	}
	return live.call(method, args, ctx, from, version)
}

// gated is a call that waits while an attempt activates until the object is live, failed or ended (`live` then calls or
// returns the error); it is rejected with the cause once the attempt parks on a failed dependency (`failed { waiting }`), and
// with `unavailable/park_timeout` after `readyTimeout`, as a call parked on a reference. A hold over a link stays bound while
// a restart's old attempt is open, so its consumer sees `live` and sends calls that arrive here: new work waits for the new
// attempt, never `not_ready`. Old work that found no open old attempt (else it was routed in `call`) waits as old work: cut at
// once when it cycles through its own drain.
func (o *chordObject) gated(method string, args any, ctx context.Context, from caller, version int) *parkedCallResult {
	result := newParkedCallResult()
	loop := o.host.loop()
	o.host.waitAsOldWork(o.openerGate(ctx), o, o.served.contract, ctx).then(func(_ struct{}, err error) {
		if err != nil {
			result.settle(nil, err)
			return
		}
		live, liveErr := o.live()
		if liveErr != nil {
			result.settle(nil, liveErr)
			return
		}
		live.call(method, args, ctx, from, version).then(loop, result.settle)
	})
	return result
}

// openerGate is the gate with an activation's opener waiting here recorded as an edge of its object (a wait of old work on
// that object then sees it), the waits of old work checked again when one starts. It resolves once the object is no longer
// activating (live, failed, ended) or rejects as `gated` does.
func (o *chordObject) openerGate(ctx context.Context) *keyedPromise[struct{}] {
	loop := o.host.loop()
	result := newKeyedPromise[struct{}](loop)
	opener, _ := chordctx.Value(ctx, openingKey)
	if opener != nil {
		if opener.gateWaits == nil {
			opener.gateWaits = map[*chordObject]struct{}{}
		}
		opener.gateWaits[o] = struct{}{}
		o.host.reloadCycles.recheck()
	}
	o.gateWait(ctx).then(func(_ struct{}, err error) {
		if opener != nil {
			delete(opener.gateWaits, o)
		}
		result.settle(struct{}{}, err)
	})
	return result
}

// gateWait is `#gate`: it resolves once the object is no longer activating, rejects with the cause once its attempt parks on a
// failed dependency, with `unavailable/park_timeout` after `readyTimeout`, with ctx's abort.
func (o *chordObject) gateWait(ctx context.Context) *keyedPromise[struct{}] {
	loop := o.host.loop()
	result := newKeyedPromise[struct{}](loop)
	ready := o.host.timeouts.ready
	due := loop.Now() + ready.Milliseconds()
	var step func()
	step = func() {
		st := o.phase
		if st.kind != objectActivating {
			result.resolve(struct{}{})
			return
		}
		if st.shown.Kind == RefStateFailed {
			result.reject(st.shown.Cause.Error)
			return
		}
		left := due - loop.Now()
		if left <= 0 {
			message := fmt.Sprintf("%s: a call waited %d ms for its object to go live", o.text, ready.Milliseconds())
			result.reject(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonParkTimeout, Message: message}, nil))
			return
		}
		if o.changed == nil {
			o.changed = newKeyedPromise[struct{}](loop)
		}
		woken := newKeyedPromise[struct{}](loop)
		timer := loop.AfterFunc(time.Duration(left)*time.Millisecond, func() { woken.resolve(struct{}{}) })
		o.changed.then(func(struct{}, error) { woken.resolve(struct{}{}) })
		woken.thenWithContext(ctx, func(_ struct{}, err error) {
			timer.Stop()
			if err != nil {
				result.reject(err)
				return
			}
			step()
		})
	}
	step()
	return result
}

// waitEdges is what its activation waits for now (reload cycles, rule 6): the objects its parked attempt's dependencies are
// bound to, and those whose gate its opener waits at.
func (o *chordObject) waitEdges() []*chordObject {
	edges := make([]*chordObject, 0, len(o.gateWaits)+len(o.waitingFor))
	for edge := range o.gateWaits {
		edges = append(edges, edge)
	}
	for _, dependency := range o.waitingFor {
		if dependency.kind != dependencyReference {
			continue
		}
		if goal, ok := dependency.binding.target().(*chordObject); ok {
			edges = append(edges, goal)
		}
	}
	return edges
}

// wakeGate is that calls waiting at the gate check the state again.
func (o *chordObject) wakeGate() {
	if o.changed != nil {
		changed := o.changed
		o.changed = nil
		changed.resolve(struct{}{})
	}
}

// checkedParams are its params as checked, with their identity key.
func (o *chordObject) checkedParams() checkedParams {
	return checkedParams{params: o.params, key: o.key}
}

// followed is what a reference of version follows for state member `member` now (decision "References are the only
// indirection"): the shown instance's member (its mapping at an older version) while that instance is live, while a restart
// keeps it open, and after a live failure until its admitted commits published; else nothing: `failed` with
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
			// The shown instance does not serve this version (a restart's old one, while a reload brings it): nothing yet.
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

// instance is the live instance, which references pin their keys on (a new one: they pin again), or a restart's open old
// attempt; nil otherwise.
func (o *chordObject) instance() any {
	if o.phase.kind == objectLive {
		return o.phase.attempt
	}
	if old := o.openOldAttempt(); old != nil {
		return old
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
	if !sameState(st.shown, shown) {
		o.phase = objectState{kind: objectActivating, attempt: a, shown: shown}
		if shown.Kind == RefStateFailed {
			o.failure = shown.Cause.Error
			// A restart's successor parked on a failure: the old attempt's kept contributions leave (F-c).
			o.dropKept()
		}
		o.notify()
	}
	if firstPass {
		o.settleFirstOutcome()
	}
	// A park may close a cycle with a wait of old work (rule 6).
	o.host.reloadCycles.recheck()
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

// dropKept: the contributions a restart kept for its successor leave the tables (F-c): it will not take them over now.
func (o *chordObject) dropKept() {
	kept := o.kept
	if kept == nil {
		return
	}
	o.kept = nil
	o.withdraw(kept)
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

// reloadFate is what a reload does to this object, whose type is still served: a live object restarts, any other is retried.
func (o *chordObject) reloadFate() reloadFate {
	if o.phase.kind == objectLive {
		return reloadRestart
	}
	return reloadRetry
}

// reload takes fate with its new served type (after the reload's commit) and, for a restart, its new factory's requests as
// staging recorded them; it returns the new attempt, whose first dependency check the reload's report waits for (a retry's).
func (o *chordObject) reload(entry *served, fate reloadFate, requests []request) *attempt {
	// Its members per version stay: the previous version's are mapped once the new attempt goes live.
	o.served = entry
	var next *attempt
	if fate == reloadRestart {
		next = o.restart(requests)
	} else {
		next = o.retryReloaded()
	}
	next.startedByReload()
	return next
}

// restart is a restart (decision "Reload restarts every affected object; old work finishes on old code"): its routines stop,
// new calls wait at the gate, old work runs on the old attempt, and consumers whose version it binds see it live; its
// contributions stay (until its seal with natives, F-b; until its successor is live without, F-c). It is sealed once its
// successor's recorded requests are ready and its calls, routed old work included, drained, bounded by the reload drain bound;
// then its natives close with reason `reload` and the successor, a new instance (D4), runs its factory. Readiness and
// successor start once every fate of the reload started (`startSuccessor`).
func (o *chordObject) restart(requests []request) *attempt {
	host := o.host
	// A restart starts from a live object (`reloadFate`).
	old := o.phase.attempt
	o.old = old
	old.beginDrain()
	old.stopRoutines()
	if !old.opensNatives() {
		o.kept = old
	}
	o.instanceID = uuidv7()
	next := o.replace()
	ready := newReadiness(host, o.callerOf(), requests, func(cause *data.FailureCause) { o.readinessShown(next, cause) }, o.ctx)
	o.readiness = ready
	due := host.loop().Now() + host.timeouts.reload.Milliseconds()
	ended := o.endOld(old, ready, due)
	host.ending(o.identity, o.reported(ended))
	ending := host.endingOf(o.identity)
	o.startSuccessorFn = func() { o.startRestarted(next, old, ready, ended, ending) }
	return next
}

// endOld ends a restart's old attempt: once its successor's requests are ready and its calls drained (routed old work joins
// while the drain runs), by due; sealed (in-memory job runs end `aborted { reload }`), its references take their last values
// once its admitted commits published (a store write is not a running call's promise), its natives close with reason
// `reload`. The promise is of the closes that threw.
func (o *chordObject) endOld(old *attempt, ready *readiness, due int64) *keyedPromise[[]*closeFailed] {
	host := o.host
	ended := newKeyedPromise[[]*closeFailed](host.loop())
	ready.ready(due).then(func(struct{}, error) {
		old.drainCalls(due).then(func(struct{}, error) {
			// With natives its contributions leave at the seal (F-b); without, they stay for its successor (F-c), unless a
			// failure dropped them already.
			if old.opensNatives() {
				o.withdraw(old)
			}
			old.seal(o.reloaded(), cutReload)
			// Sealed, it is no longer the instance its holders bind (`instance`, `stateFor`): a hold waiting for a window
			// slot unbinds now, before an `ack` could bind it to nothing; bound holds and references follow it on until it
			// stopped changing.
			o.notify()
			awaitTask(host, func() (struct{}, error) {
				o.documents.Settled()
				return struct{}{}, nil
			}).then(func(struct{}, error) {
				o.stopFollowing(old)
				awaitTask(host, func() ([]*closeFailed, error) { return old.close(closeReload), nil }).then(
					func(failures []*closeFailed, _ error) {
						old.detach()
						ended.resolve(failures)
					})
			})
		})
	})
	return ended
}

// startSuccessor starts a restart's readiness and its successor, once every fate of the reload started (its served types are
// new).
func (o *chordObject) startSuccessor() {
	start := o.startSuccessorFn
	o.startSuccessorFn = nil
	if start != nil {
		start()
	}
}

// startRestarted binds the readiness, whose failures the successor shows until the old attempt ended (`readinessShown`). The
// successor runs after ending; once the old attempt ended (ended) and the successor's own requests bound, the old attempt's
// references and the readiness bindings are released (F-a: a dependency held only by them never ends; a successor a later
// reload replaces never releases them early).
func (o *chordObject) startRestarted(next, old *attempt, ready *readiness, ended *keyedPromise[[]*closeFailed], ending *keyedPromise[struct{}]) {
	loop := o.host.loop()
	o.showsReadiness = next
	// Before the successor's own steps (`ending` settles after `ended`): its own checks show what it waits for then.
	ended.then(func([]*closeFailed, error) {
		if o.showsReadiness == next {
			o.showsReadiness = nil
		}
	})
	ready.start()
	o.run(next, ending)
	promiseAll2(loop, ended, next.checked()).then(func([]*closeFailed, error) {
		old.releaseReferences()
		ready.release()
		if o.readiness == ready {
			o.readiness = nil
		}
	})
}

// readinessCause is what a restart's readiness finds now: a failed request the successor waits for, if any (the report's).
func (o *chordObject) readinessCause() *data.FailureCause {
	if o.readiness == nil {
		return nil
	}
	return o.readiness.cause()
}

// readinessShown: a restart's readiness found cause (a failed request), or none any more: while the old attempt has not
// ended, the successor next shows it (new calls get the cause, consumers see the failure; old work still runs on the old
// attempt), and shows `loading` again once the request recovered. Its kept contributions stay: only the successor's own
// failure or park drops them (F-c).
func (o *chordObject) readinessShown(next *attempt, cause *data.FailureCause) {
	st := o.phase
	if o.showsReadiness != next || st.kind != objectActivating || st.attempt != next {
		return
	}
	shown := refLoading
	if cause != nil {
		shown = &RefState{Kind: RefStateFailed, Cause: *cause}
	}
	if sameState(st.shown, shown) {
		return
	}
	o.phase = objectState{kind: objectActivating, attempt: next, shown: shown}
	if cause != nil {
		o.failure = cause.Error
	}
	o.notify()
	o.host.reloadCycles.recheck()
}

// callerOf is whom its own requests act as: the host for a local object, its principal for a remote one.
func (o *chordObject) callerOf() caller {
	if o.served.kind == servedRemote {
		return caller{principal: o.principal, incarnation: o.host.incarnation}
	}
	return caller{principal: o.host.principal, incarnation: o.host.incarnation}
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
	old := st.attempt
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
	// Before the new instance is shown: holders of a version its provider does not bind leave with their last values, which
	// their references take from the instance shown so far.
	unbound := o.unbindUnfit(a)
	o.phase = objectState{kind: objectLive, attempt: a}
	o.exposed = a
	o.following = true
	contributions := a.contributionList()
	// A restart's kept contributions leave in the batch the new ones enter (F-c), so no reader sees a gap.
	var leaving []*registries.RegistryContribution
	if o.kept != nil {
		leaving = o.kept.contributionList()
		o.kept = nil
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
	a.live()
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

// failedAttempt records a failed attempt: reported, torn down in the background (reason `end`), its references released once
// its natives closed, its holders told. Transient: a backoff window, whose end is the failure's `retryAt`; broken: no
// automatic retry.
func (o *chordObject) failedAttempt(a *attempt, cause data.FailureCause) {
	o.dropKept()
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

// stopFollowing: a, the instance shown, stopped changing (a live failure's admitted commits published, a restart's old
// attempt sealed): its references take their last values from it, holds leave it, and the object
// lets it go (a reference made later never attached to it: it takes nothing from it).
func (o *chordObject) stopFollowing(a *attempt) {
	if o.old == a {
		o.old = nil
	}
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
	o.wakeGate()
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
	// A restart's teardown (its old attempt's end) finishes before this one (they share its documents).
	previous := o.host.endingOf(o.identity)
	// Its ending rejects the callers of a restart's old attempt too, at once; that attempt's end closes it.
	if o.old != nil {
		o.old.seal(err, cutRestart)
	}
	o.dropKept()
	if o.readiness != nil {
		o.readiness.release()
	}
	o.readiness = nil
	o.showsReadiness = nil
	o.startSuccessorFn = nil
	o.phase = objectState{kind: objectEnded, shown: &RefState{Kind: RefStateEnded, Error: err}}
	o.wakeGate()
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

// live is the live attempt, or a restart's open old attempt (its consumers saw it live); `not_ready` before the object went
// live and while it activates again, the cause while failed, `released` once it ended.
func (o *chordObject) live() (*attempt, error) {
	st := o.phase
	switch st.kind {
	case objectLive:
		return st.attempt, nil
	case objectActivating:
		if old := o.openOldAttempt(); old != nil {
			return old, nil
		}
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
