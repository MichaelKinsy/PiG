package documents

import (
	"context"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/documents/documents.ts
//
// An object's documents (§9, P2, slice 6a §1.2, §10): its slots across attempts, its line (its transactions and line
// jobs run one at a time), the seal fence over the transaction holding the line, the factory's pins between attempts,
// the activation's loads and the residency of keyed documents.
//
// Async contract: OnLine, Tx, Load and Settled block (the line, the store); everything else is a section the host
// serializes (see the package documentation). A section never blocks. A blocking call that is given a handler's ctx (or a
// context derived from it) parks the handler where it starts to wait (chord/internal/parking), so the section that admitted
// the handler's call goes on: Pi's handler runs up to its first `await` inside that section.

// DocumentsOwner is who owns an object's documents: the object's name, batch and context, its store address, its live
// failure and the host's serialization and time.
type DocumentsOwner interface {
	// Label is the owner's name in errors.
	Label() string
	Batch() *state.Batch
	// Context is the object's context: shared cold loads run under it (rule 11).
	Context() context.Context
	// Store is the host's store and the object's owner name in it (its type, R2), when it has one.
	Store() (chordStore *store.ChordStore, owner string, ok bool)
	// Fail fails the live object `broken` with error (a document the code cannot read, slice 6a §1.4); ignored when it
	// is not live.
	Fail(err *data.ChordError)
	// Do runs work as one section of the host's serialization: it does not block, and is inline when the caller is in
	// a section already.
	Do(work func())
	Clock
}

// CancelReporter is a DocumentsOwner that reports a transaction that did not commit while its context was cancelled
// (`transaction_cancelled`): an empty `catch` cannot hide a change that never happened. cause is the context's cause.
type CancelReporter interface {
	TransactionCancelled(cause error)
}

// DeclaredSlot is what Documents needs of a slot of either kind.
type DeclaredSlot interface {
	Definition() *SlotDefinition
	Persisted() PersistedSlot
	Hydrate(definition AnyDefinition, ctx context.Context) error
	LoadDeclared(ctx context.Context) error
	Drop()
}

type documentsState int

const (
	documentsOpen documentsState = iota
	// documentsSealed: the object is ending; its documents stay readable, its transactions return `released`.
	documentsSealed
	documentsEnded
)

// Documents are the documents of one object, from its first activation to its end: one slot per declared name, kept
// across attempts, and the line. Each attempt declares its handles under its own seal token; the object's own
// transactions (job outcomes) run under Own, sealed only by the object's end.
type Documents struct {
	owner     DocumentsOwner
	line      *Line
	residency *Residency
	// own is the token of the object's own transactions: sealed with the object (Seal).
	own   *SealToken
	slots map[string]DeclaredSlot
	order []string
	// holder is the transaction holding the line, until it settles or its token is sealed.
	holder *Transaction
	state  documentsState
	// pins are the pins the last factory that returned declared (`at(key)`): held until the next one returns (rule 6).
	pins []func()
}

// NewDocuments is the documents of owner's object.
func NewDocuments(owner DocumentsOwner) *Documents {
	d := &Documents{owner: owner, line: NewLine(), own: NewSealToken(), slots: map[string]DeclaredSlot{}}
	// A sweep's only failure is OnLine's `released` once the object ended (a sweep itself drops trackers in memory and
	// does not fail): nothing to report.
	d.residency = NewResidency(owner, func(sweep func()) {
		// Pi's `onLine` refuses `released` before it queues, and takes its place on the line at the call.
		if d.refusedOnLine(nil) != nil {
			return
		}
		place := d.line.reserve()
		//portlint:allow asyncorder Pi fires the sweep unawaited (`void this.onLine(async () => sweep()).catch(() => {})`, documents.ts:48) and onLine's `this.line.acquire()` (documents.ts:137) takes its place at the call: the place is reserved above, so the order of the line is the order of the calls
		go func() { //portlint:allow golifetime the sweep is a floating promise in Pi (documents.ts:48); it ends once its place on the line is handed over and the job ran or was refused `released`
			//portlint:allow ctxbackground Pi's `onLine` takes no signal (documents.ts:135) and `line.acquire()` none (line.ts `acquire()`): a line job has no caller to cancel it
			_ = d.runOnLine(place, context.Background(), func() error {
				d.Do(sweep)
				return nil
			}, nil)
		}()
	})
	return d
}

// Label is the owner's name in errors.
func (d *Documents) Label() string { return d.owner.Label() }

// Batch is the object's batch.
func (d *Documents) Batch() *state.Batch { return d.owner.Batch() }

// Context is the object's context.
func (d *Documents) Context() context.Context { return d.owner.Context() }

// Line is the object's line.
func (d *Documents) Line() *Line { return d.line }

// Residency is the idle persisted keyed documents and their eviction.
func (d *Documents) Residency() *Residency { return d.residency }

// Own is the token of the object's own transactions: sealed with the object.
func (d *Documents) Own() *SealToken { return d.own }

// Do runs work as one section of the host's serialization.
func (d *Documents) Do(work func()) { d.owner.Do(work) }

// Declare is a handle to definition's slot for an attempt sealed by token, whose `at(key)` pins declarer takes: the slot
// made by the first declaration of its name, checked against the values for later ones (SlotDefinition.Check). The
// handle is a *Keyed for a keyed definition, a *Singleton otherwise. "Declared twice" is the attempt's check. A
// section.
func (d *Documents) Declare(definition AnyDefinition, token *SealToken, declarer KeyDeclarer) (any, error) {
	slot := d.slots[definition.Name]
	if slot != nil {
		if err := slot.Definition().Check(definition); err != nil {
			return nil, err
		}
	}
	var persistence *Persistence
	if slot == nil && definition.Persist {
		persistence = d.persistence(definition.Name)
	}
	// The definition's `keyed` decides the slot's class and the handle's.
	if definition.Keyed {
		if slot == nil {
			slot = d.add(NewKeyedSlot(d, definition, persistence))
		}
		return NewKeyed(slot.(*KeyedSlot), token, definition.Version, declarer), nil
	}
	if slot == nil {
		slot = d.add(NewSingletonSlot(d, definition, persistence))
	}
	return NewSingleton(slot.(*SingletonSlot), token, definition.Version), nil
}

// Check checks a declaration of definition as Declare would, changing nothing (a reload's staging: a recording factory
// declares against the documents the object has).
func (d *Documents) Check(definition AnyDefinition) error {
	if slot := d.slots[definition.Name]; slot != nil {
		return slot.Definition().Check(definition)
	}
	return nil
}

// Hydrate takes an attempt's declarations once its factory returned: its `at(key)` pins replace the previous factory's,
// the slots it no longer declares are dropped (their watches end; persisted ones load again when declared again),
// every declared one hydrated (`initial()`, migrations; a failure fails the activation). declared is in declaration
// order. A section.
func (d *Documents) Hydrate(declared []AnyDefinition, pins []func(), ctx context.Context) error {
	previous := d.pins
	d.pins = pins
	for _, release := range previous {
		release()
	}
	names := make(map[string]struct{}, len(declared))
	for _, definition := range declared {
		names[definition.Name] = struct{}{}
	}
	// A dropped persisted slot goes whole, its loaded documents too: observably the same as keeping them, since every
	// committed write is in the store and a later declaration reloads from there.
	kept := d.order[:0]
	for _, name := range d.order {
		if _, ok := names[name]; ok {
			kept = append(kept, name)
			continue
		}
		slot := d.slots[name]
		delete(d.slots, name)
		slot.Drop()
	}
	d.order = kept
	for _, definition := range declared {
		if err := d.slots[definition.Name].Hydrate(definition, ctx); err != nil {
			return err
		}
	}
	return nil
}

// Load loads, in one line job, every persisted singleton not loaded yet and every pinned key not loaded since it was
// pinned (an attempt's activation, before it goes live, rule 6); it returns at once when nothing is persisted. A
// failure fails the activation.
func (d *Documents) Load(ctx context.Context) error {
	var persisted []DeclaredSlot
	d.Do(func() {
		for _, name := range d.order {
			if slot := d.slots[name]; slot.Persisted() != nil {
				persisted = append(persisted, slot)
			}
		}
	})
	// In memory only: nothing to wait for.
	if len(persisted) == 0 {
		return nil
	}
	return d.OnLine(ctx, func() error {
		for _, slot := range persisted {
			if err := slot.LoadDeclared(ctx); err != nil {
				return err
			}
		}
		return nil
	}, nil)
}

// OnLine runs job on the line (a cold load, a page of keys, an eviction sweep); refused `released` once the object is
// ending or token (the handle's that asks, when the job is one handle's; nil otherwise) is sealed, a job queued before
// either included (anything taking the line after the seal is refused, rule 4).
func (d *Documents) OnLine(ctx context.Context, job func() error, token *SealToken) error {
	if err := d.refusedOnLine(token); err != nil {
		return err
	}
	return d.runOnLine(d.line.reserve(), ctx, job, token)
}

// runOnLine is the rest of OnLine once its place on the line is taken: waits for the place, checks the refusals again
// and runs job.
func (d *Documents) runOnLine(place linePlace, ctx context.Context, job func() error, token *SealToken) error {
	release := place.wait(ctx)
	defer release()
	if err := d.refusedOnLine(token); err != nil {
		return err
	}
	return job()
}

func (d *Documents) refusedOnLine(token *SealToken) error {
	var err error
	d.Do(func() {
		if d.state != documentsOpen || (token != nil && token.Sealed()) {
			err = d.Released()
		}
	})
	return err
}

// Tx runs fn as one transaction on the line under token and commits what it drafted (§9): an error, an abort of ctx,
// the token's seal or the object's end discards everything. persistence defers storing a run state's change (D8).
func (d *Documents) Tx(fn func(tx *Transaction) (any, error), token *SealToken, ctx context.Context, persistence ...CommitPersistence) (any, error) {
	commitPersistence := Immediate
	if len(persistence) > 0 {
		commitPersistence = persistence[0]
	}
	var err error
	d.Do(func() {
		if err = d.Batch().AssertOutside("tx"); err == nil {
			err = d.AssertAdmitted(token)
		}
	})
	if err != nil {
		return nil, err
	}
	release := d.line.Acquire(ctx)
	var tx *Transaction
	defer func() {
		// Pi's `finally { holder = undefined; release(); tx.settle(); }` is one synchronous section: no other transaction
		// takes the line, and nothing else of the object runs, between the line's release and the staged changes hearing
		// of the commit. A job start's `committed` queues the run's invocation (`need`); an abort of that run whose
		// marking transaction waited on the line must not commit ahead of it, or the run ends `aborted` without ever
		// being invoked.
		d.Do(func() {
			if d.holder == tx {
				d.holder = nil
			}
			release()
			if tx != nil {
				tx.Settle()
			}
		})
	}()
	d.Do(func() {
		tx = NewTransaction(d, token, release, ctx, commitPersistence)
		if err = d.AssertAdmitted(token); err == nil {
			d.holder = tx
		}
	})
	var result any
	var started time.Time
	// Timed only when something records: the commit's duration metric, its event on the enclosing span.
	metrics := d.Batch().Metrics()
	timed := metrics != nil || chordctx.Telemetry(ctx) != telemetry.NoopTelemetryContext
	if err == nil {
		result, err = fn(tx)
	}
	if err == nil {
		if timed {
			started = time.Now()
		}
		// In-memory-only commits are synchronous (W12): the store's step is taken only for persisted writes.
		err = tx.Commit()
	}
	if err != nil {
		d.Do(tx.Discard)
		// Only a transaction that did not commit reads this; Chord's own run under contexts that never abort.
		if ctx.Err() != nil {
			if reporter, ok := d.owner.(CancelReporter); ok {
				cause := context.Cause(ctx)
				d.Do(func() { reporter.TransactionCancelled(cause) })
			}
		}
		return nil, err
	}
	if timed {
		d.Do(func() { d.recordCommit(tx, time.Since(started), ctx, metrics) })
	}
	return result, nil
}

// recordCommit records a commit of tx that took duration (from its start to its adoption): its event on ctx's span (the
// method, routine or job it ran in) and the host's commit duration metric; a commit that changed nothing records
// nothing. A section.
func (d *Documents) recordCommit(tx *Transaction, duration time.Duration, ctx context.Context, metrics telemetry.TelemetryMetrics) {
	documents, ops, writes, rows := tx.Committed()
	if documents == 0 && writes == 0 {
		return
	}
	ms := float64(duration) / float64(time.Millisecond)
	if span := chordctx.Telemetry(ctx); span != telemetry.NoopTelemetryContext {
		span.AddEvent("chord.commit", telemetry.SpanAttributes{
			telemetry.Attr("documents", float64(documents)), telemetry.Attr("ops", float64(ops)),
			telemetry.Attr("rows", float64(rows)), telemetry.Attr("duration", ms),
		})
	}
	if metrics != nil {
		unit := "ms"
		metrics.Histogram("chord.commit.duration", telemetry.HistogramOptions{InstrumentOptions: telemetry.InstrumentOptions{Unit: &unit}}).Record(ms, nil, nil)
	}
}

// Fence seals token (the seal fence): its transactions are refused from now on, and the one holding the line is
// discarded and the line released. A section.
func (d *Documents) Fence(token *SealToken) {
	token.Seal()
	if d.holder != nil && d.holder.Token() == token {
		d.cut()
	}
}

// AssertLive returns `released` once the object ended.
func (d *Documents) AssertLive() error {
	if d.state == documentsEnded {
		return d.Released()
	}
	return nil
}

// AssertAdmitted returns `released` once token is sealed or the object is ending: no new work takes the line.
func (d *Documents) AssertAdmitted(token *SealToken) error {
	if token.Sealed() || d.state != documentsOpen {
		return d.Released()
	}
	return nil
}

// Fail fails the live object `broken` with err (a document the code cannot read); ignored when it is not live.
func (d *Documents) Fail(err *data.ChordError) { d.owner.Fail(err) }

// failWith fails the owner with err as a ChordError and returns that error.
func (d *Documents) failWith(err error) error {
	failure := data.AsChordError(err, d.Label())
	d.Fail(failure)
	return failure
}

// Seal is: the object is ending: every transaction is refused, the one holding the line discarded; documents stay
// readable. A section.
func (d *Documents) Seal() {
	if d.state != documentsOpen {
		return
	}
	d.state = documentsSealed
	d.own.Seal()
	d.cut()
}

// Settled blocks until the line is free. After Seal only an admitted step can hold it (every other holder was cut):
// the object's teardown waits here, so the step adopts into documents that still exist (slice 6a §1.3).
func (d *Documents) Settled() {
	//portlint:allow ctxbackground Pi's `settled()` takes no signal and `line.acquire()` none (documents.ts:215-216, line.ts `acquire()`): the teardown's wait is not cancellable and no handler is parked here
	d.line.Acquire(context.Background())()
}

// End drops every document and pin; watches end `ended`, and every later use returns `released`. A section.
func (d *Documents) End() {
	d.state = documentsEnded
	d.residency.End()
	for _, name := range d.order {
		d.slots[name].Drop()
	}
	clear(d.slots)
	d.order = nil
	d.pins = nil
}

// Released is the `released` error of this object's documents.
func (d *Documents) Released() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: d.Label() + ": ended"}, nil)
}

// cut discards the transaction holding the line and releases the line.
func (d *Documents) cut() {
	holder := d.holder
	d.holder = nil
	if holder != nil {
		holder.Cut()
	}
}

func (d *Documents) add(slot DeclaredSlot) DeclaredSlot {
	name := slot.Definition().Current().Name
	d.slots[name] = slot
	d.order = append(d.order, name)
	return slot
}

// persistence is the store side of persisted documents named name; the attempt checked the store first (P3).
func (d *Documents) persistence(name string) *Persistence {
	chordStore, owner, _ := d.owner.Store()
	return NewPersistence(chordStore, owner, "chord", name)
}
