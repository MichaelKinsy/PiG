package host

// pi: packages/chord/src/host/query-reader.ts
// Ports packages/chord/src/host/query-reader.ts (slice 14 §3.2): the readers of named queries, what `self.liveQuery`,
// `self.count` and `self.list` have in common. A reader holds the named query and its arguments as the factory gave them
// (a value, or a watchable the arguments follow), opens once the provider is live, and reads again when the provider has
// another live instance or the arguments change: its member shows the previous value until the new read's first run is
// adopted.
//
// Go forms of Pi's promises (decisions; see ChordS14Reader-STATE.md 'Async contract'):
//   - `await reading.read(...)` in `open` is a blocking call: `reader.Open` blocks (the caller runs it on an owned task,
//     `awaitTail`, off the host's section) until the first run settled; nothing it waits on can fail (Pi's `read` never
//     rejects: a failed run is the source's failure, which the check reports as Opened{waiting}). What Pi's `open` does after
//     that await (the check of the reading) is the func `Open` returns: `awaitTail` runs it in the section that settles the
//     promise, before its reaction. A Go task that blocks and then looks at the reading in a section of its own leaves a gap
//     before the section that settles its promise, where Pi has the next microtask of one turn; an adoption that landed in it
//     would be missed by the follow that comes after the check (read-declarations.ts:43-45).
//   - `void reading.read(...)` in `#moved` is an owned background task (`ReaderSite.Go`, the loop's `Go`): nothing awaits
//     it. Pi's `read` runs up to its first await when it is called (the open query is registered, the previous pending
//     read closed): that part is `Reading.Open`, which a move runs in its own section, in call order. What awaits (the
//     load) is blocking here, so the reader keeps ONE pending load (the latest read wins: an overtaken read's query is
//     closed, its load would do nothing) and one task that drains it. The task may run after the attempt ended: the read a
//     move opened exists by then (a set unbound in between fails the member, as in Pi) and the close drops the load.
//   - the context cancels a read through the store (Read's ctx is the open query's: an aborted context makes the first
//     run a failed run); nothing else in this file waits.
//   - every field of a reader and of a GivenQuery is touched only inside a section of the host (`batch.Section`); `Open`
//     and the drain task enter one for each touch and call the reading outside it.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// The kinds of GivenArguments: a value, or a watchable the arguments follow.
const (
	GivenFixed     = "fixed"
	GivenFollowing = "following"
)

// GivenArguments are the arguments a factory gave a reader: a value (Kind GivenFixed, Value), or a watchable the
// arguments follow (Kind GivenFollowing, Source; a constant source never changes).
type GivenArguments struct {
	Kind   string
	Value  delta.JsonValue
	Source state.DeriveSource
}

// The kinds of Opened.
const (
	// OpenedReady: the reader has a value.
	OpenedReady = "ready"
	// OpenedLoading: its read has not run yet (a table it reads is being created or migrated: no failure, it runs when
	// the table is ready).
	OpenedLoading = "loading"
	// OpenedWaiting: it waits on a failure (the provider, its arguments, its first run); Err is that failure.
	OpenedWaiting = "waiting"
	// OpenedBroken: its own code broke (refused arguments, a builder that threw); Err is that error.
	OpenedBroken = "broken"
)

// Opened is how opening a reader ended.
type Opened struct {
	Kind string
	// Err is the failure of OpenedWaiting and OpenedBroken.
	Err error
}

// Reading is what a reader reads through and feeds: a live query, a count, a list. It is the source that has the
// reader's value: its attempt follows it while it waits. Read, Fail and Detach are Pi's `read`, `fail` and `detach`.
type Reading interface {
	state.DeriveSource
	// Ready reports whether it has a value.
	Ready() bool
	// Failure is its failure; nil: none.
	Failure() error
	// Open is the part of Pi's `read` before its first await: it reads target from now on (its read is open and
	// registered; the previous pending one is closed) and returns the load, which returns once the first run settled
	// (adopted, overtaken or held). It does not fail: a failed run is Failure. Open is called inside a section of the
	// host; the load is not (it blocks on the store).
	Open(target store.LiveTarget, ctx context.Context) (load func())
	// Fail is: what it should read cannot be read; the source fails, and nothing read before may heal it. Called
	// inside a section.
	Fail(err error, ctx context.Context)
	// Detach is: its attempt ended. Called inside a section.
	Detach(err error)
}

// liveReading is a store.LiveQuery as a Reading: Pi's `read` resolves with the first run, which no caller of a reader
// uses (the live query's own `load` looks at its failure), so the load has no result.
type liveReading struct{ *store.LiveQuery }

// newLiveReading is live as the reading of a live query or count reader.
func newLiveReading(live *store.LiveQuery) Reading { return liveReading{live} }

// Open opens target's read (store.LiveQuery.Open); its load drops the first run.
func (r liveReading) Open(target store.LiveTarget, ctx context.Context) func() {
	load := r.LiveQuery.Open(target, ctx)
	return func() { load() }
}

// pendingRead is the load a reader has queued: the latest read's, and the calls of Open waiting for its first run.
type pendingRead struct {
	load    func()
	waiters []chan struct{}
}

// reader is one reader of an attempt: opened before the attempt goes live, closed at its end. It opens its reading once
// the provider is live, and reads again whenever what it reads moves.
type reader struct {
	given   *GivenQuery
	reading Reading
	// wake tells its attempt, waiting on it, that what it reads moved before it could read anything.
	wake func()
	// spawn runs a background task of the host's loop (an owned, unawaited Promise).
	spawn func(task func())
	// ctx is the context its reads run under: its attempt's, set by its first Open.
	ctx context.Context
	// started is whether a read was opened: from then on a move reads again, before it only wakes the attempt.
	started bool
	closed  bool
	// pending is the read to run next; draining is whether a task drains it.
	pending  *pendingRead
	draining bool
	// drains counts the drain tasks it spawned that have not returned: settle waits for them.
	drains sync.WaitGroup
}

// newReader is the reader of given through reading; wake and spawn are the attempt's.
func newReader(given *GivenQuery, reading Reading, wake func(), spawn func(task func())) *reader {
	return &reader{given: given, reading: reading, wake: wake, spawn: spawn}
}

// source is the source that changes when the reader gets a value.
func (r *reader) source() Reading { return r.reading }

// Open opens the read (the provider is live: its attempt's dependencies are) and returns once its first run settled: Pi's `open`
// up to its `await this.#reading.read(...)`. Called again while its attempt waits on it: a reader that read has nothing to wait
// for, one that never read tries again. It blocks: call it off the host's section. It returns what Pi's `open` does after that
// await (query-reader.ts:91-94: the refusal it returns before it reads, else the check of the reading) as a func to run in a
// section of the host: the one that settles the promise of the await (awaitTail), so that the check and what follows it
// (read-declarations.ts:43-45) are one turn, as in Pi.
func (r *reader) Open(ctx context.Context) (opened func() Opened) {
	var refused *Opened
	var done chan struct{}
	r.given.batch.Section(func() {
		if r.ctx == nil {
			r.ctx = ctx
			r.given.follow(r.moved)
		}
		if r.started {
			return
		}
		target, err := r.given.target(ctx)
		if err != nil {
			// Refused arguments and a throwing builder are the reader's own code (`invalid`, a plain error); a provider or
			// arguments not ready yet are waited for.
			kind := OpenedWaiting
			if isOwnCode(err) {
				kind = OpenedBroken
			}
			refused = &Opened{Kind: kind, Err: err}
			return
		}
		r.started = true
		done = r.enqueue(r.reading.Open(target, ctx))
	})
	if done != nil {
		<-done
	}
	return func() Opened {
		if refused != nil {
			return *refused
		}
		switch failure := r.reading.Failure(); {
		case r.reading.Ready():
			return Opened{Kind: OpenedReady}
		case failure == nil:
			return Opened{Kind: OpenedLoading}
		default:
			return Opened{Kind: OpenedWaiting, Err: failure}
		}
	}
}

// Close is: its attempt ended: no commit runs its reads again, its member's reads return err. Inside a section.
func (r *reader) Close(err error) {
	r.closed = true
	r.given.release()
	r.drop()
	r.reading.Detach(err)
}

// moved is: what it reads moved: the new read replaces the current one once its first run is adopted. Before its first
// read its attempt waits on it and tries again. Inside a section.
func (r *reader) moved() {
	if r.closed {
		return
	}
	if !r.started {
		r.wake()
		return
	}
	target, err := r.given.target(r.ctx)
	if err != nil {
		r.drop()
		r.reading.Fail(err, r.ctx)
		return
	}
	// A failure of the run is the member's (a failed member with its last value); nothing awaits it.
	r.enqueue(r.reading.Open(target, r.ctx))
}

// enqueue makes load, the load of a read just opened, the one to run next (it replaces a queued one: that read was
// overtaken before its load ran) and returns the channel closed once a load at or after it settled. Inside a section.
func (r *reader) enqueue(load func()) chan struct{} {
	if r.pending == nil {
		r.pending = &pendingRead{}
	}
	r.pending.load = load
	done := make(chan struct{})
	r.pending.waiters = append(r.pending.waiters, done)
	if !r.draining {
		r.draining = true
		r.drains.Add(1)
		r.spawn(r.drain)
	}
	return done
}

// drop forgets the queued read: what waited for it is released. Inside a section.
func (r *reader) drop() {
	if r.pending == nil {
		return
	}
	for _, each := range r.pending.waiters {
		close(each)
	}
	r.pending = nil
}

// drain runs the queued reads one after the other until none is left; the owned task of a reader.
func (r *reader) drain() {
	defer r.drains.Done()
	for {
		var next *pendingRead
		r.given.batch.Section(func() {
			next = r.pending
			r.pending = nil
			if next == nil {
				r.draining = false
			}
		})
		if next == nil {
			return
		}
		next.load()
		for _, each := range next.waiters {
			close(each)
		}
	}
}

// settle blocks until the reads it queued ran: what Pi's `await flushed()` after a move waits for. Not inside a section.
func (r *reader) settle() { r.drains.Wait() }

// isOwnCode reports whether err is of the reader's own code: arguments the schema refused, or a builder that threw.
func isOwnCode(err error) bool {
	if chordErr, ok := errors.AsType[*data.ChordError](err); ok {
		return chordErr.Code == data.CodeInvalid
	}
	return true
}

// GivenQuery is a named query with the arguments a factory gave it: what it reads now (target), and a call whenever that
// changes (another live instance of the provider, other arguments by canonical JSON), made after the batch that changed
// it. It is a state.Dependent of the arguments' source.
type GivenQuery struct {
	Handle NamedQueryHandle
	args   GivenArguments
	batch  *state.Batch
	// read is the canonical JSON of the arguments read last; hasRead is false before the first read and after a failed one.
	read    string
	hasRead bool
	moved   func()
	release func()
	// checking is set from an invalidation until the batch's flush compares the arguments.
	checking bool
}

var _ state.Dependent = (*GivenQuery)(nil)

// NewGivenQuery is handle with args, in batch.
func NewGivenQuery(handle NamedQueryHandle, args GivenArguments, batch *state.Batch) *GivenQuery {
	return &GivenQuery{Handle: handle, args: args, batch: batch, release: func() {}}
}

// target is what reading the query reads now: the provider's declaration for the arguments' value. It fails while the
// provider is not live or the arguments have no value; arguments the provider refuses fail with its `invalid/args`.
// Inside a section.
func (g *GivenQuery) target(ctx context.Context) (store.LiveTarget, error) {
	g.hasRead = false
	args, err := g.value()
	if err != nil {
		return store.LiveTarget{}, err
	}
	provided, err := g.Handle.Provided()
	if err != nil {
		return store.LiveTarget{}, err
	}
	target, err := provided.Target(args, ctx)
	if err != nil {
		return store.LiveTarget{}, err
	}
	g.read, g.hasRead = data.CanonicalJson(args, ctx), true
	return target, nil
}

// follow calls moved from now on whenever target would read something else, until release. Inside a section.
func (g *GivenQuery) follow(moved func()) {
	g.moved = moved
	releaseInstance := g.Handle.Follow(moved)
	// A constant never changes: only a source is followed.
	var dependents *state.DependentSet
	if g.args.Kind == GivenFollowing {
		dependents = g.args.Source.DependentSet()
	}
	if dependents != nil {
		dependents.Add(g)
	}
	g.release = func() {
		releaseInstance()
		if dependents != nil {
			dependents.Delete(g)
		}
	}
}

// Release stops following. Inside a section.
func (g *GivenQuery) Release() {
	g.release()
	g.release = func() {}
	g.moved = nil
}

// Invalidate is: the arguments' source changed in the current batch: compared once the batch delivered (no compute here).
func (g *GivenQuery) Invalidate() {
	if g.checking {
		return
	}
	g.checking = true
	g.batch.Flushing(func(ctx context.Context) error {
		g.checking = false
		now, hasNow := "", false
		if args, err := g.value(); err == nil {
			now, hasNow = data.CanonicalJson(args, ctx), true
		}
		// Arguments that cannot be computed: the reader tries to read and shows that failure.
		if (!hasNow || now != g.read || !g.hasRead) && g.moved != nil {
			g.moved()
		}
		return nil
	})
}

// SourceRetired is a no-op: a retired source changes nothing here.
func (g *GivenQuery) SourceRetired(map[state.Dependent]struct{}) {}

// value is the arguments' value. A reader opens before its attempt's natives, followed sources and pinned keys (an
// attempt waiting on a read holds none): arguments computed from one have no value then and would get none while the
// attempt waits. That is the reader's own code, refused as such, not waited for.
func (g *GivenQuery) value() (delta.JsonValue, error) {
	if g.args.Kind == GivenFixed {
		return g.args.Value, nil
	}
	value, err := g.args.Source.Value()
	if err == nil {
		return value, nil
	}
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeNotReady {
		return nil, err
	}
	rule := "a reader's arguments read references, registries, writables and earlier reads"
	return nil, data.Invalid(data.ReasonArgs,
		fmt.Sprintf(`query "%s": %s, not a followed source or a native (%s)`, g.Handle.Label(), rule, chordErr.Message))
}
