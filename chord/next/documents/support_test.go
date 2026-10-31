package documents

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/support.ts (the helpers the documents tests use).
//
// Shared test helpers of package documents. Pi's documents tests reach their objects through a `createHost` host
// (`loaded`, `restartable`, `storeRestartable`, `host.get`), which lives in another package (D5). The Go counterpart
// here is the part of a host's object the documents need and nothing more: object is a DocumentsOwner over one batch,
// a serialized loop (Do), a fake clock and an optional store; attempt is what an attempt does with the documents
// (declare handles, hydrate after the factory, load, fail live, seal, end). Tests that need the host's own behavior
// (references, `activation_failed` reports, the factory's thrown message) are not ported here.

var background = chordctx.Background()

// loop is the host's serialization: one section at a time, inline when the caller is in a section already.
type loop struct {
	mu     sync.Mutex
	holder atomic.Uint64
	// scheduler is the delivery scheduler of the object's watches: an outermost section holds it, as Pi's microtask
	// cannot run inside one synchronous commit (frames advanced inside a section fold into one delivery).
	scheduler *chord.DeliveryScheduler
	// stall, when set, parks every section its goroutine enters from outside one (and that its `when`, if any, accepts at
	// the entry) until release closes: a test forces the interleaving of a waiter's resumption against other goroutines'
	// sections.
	stall atomic.Pointer[loopStall]
}

type loopStall struct {
	id      uint64
	release chan struct{}
	when    func() bool
}

func goroutineID() uint64 {
	var buffer [64]byte
	text := strings.TrimPrefix(string(buffer[:runtime.Stack(buffer[:], false)]), "goroutine ")
	var id uint64
	for _, c := range text {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

func (l *loop) do(work func()) {
	id := goroutineID()
	// The holder is read without mu, which the outermost section keeps locked for its whole work.
	if l.holder.Load() == id {
		work()
		return
	}
	if stall := l.stall.Load(); stall != nil && stall.id == id && (stall.when == nil || stall.when()) {
		<-stall.release
	}
	l.mu.Lock()
	l.holder.Store(id)
	release := func() {}
	if l.scheduler != nil {
		release = l.scheduler.Hold()
	}
	func() {
		defer func() {
			l.holder.Store(0)
			l.mu.Unlock()
		}()
		work()
	}()
	release()
}

// fakeClock is a clock a test advances; AfterFunc jobs run as sections of the object's loop.
type fakeClock struct {
	mu     sync.Mutex
	now    int64
	timers map[*fakeTimer]struct{}
	loop   *loop
}

type fakeTimer struct {
	at  int64
	job func()
}

func newFakeClock(l *loop) *fakeClock {
	return &fakeClock{timers: map[*fakeTimer]struct{}{}, loop: l}
}

func (c *fakeClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, job func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.now + d.Milliseconds(), job: job}
	c.timers[timer] = struct{}{}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.timers, timer)
	}
}

// TimerCount is the timers pending.
func (c *fakeClock) TimerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Advance moves time forward by ms, firing the timers that come due in time order (vi.advanceTimersByTime).
func (c *fakeClock) Advance(ms int64) {
	target := func() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.now + ms }()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for timer := range c.timers {
			if timer.at <= target && (next == nil || timer.at < next.at) {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		delete(c.timers, next)
		if next.at > c.now {
			c.now = next.at
		}
		c.mu.Unlock()
		c.loop.do(next.job)
	}
}

// object is a documents owner: the object's name, batch, context, store and live failures.
type object struct {
	t         *testing.T
	label     string
	loop      *loop
	clock     *fakeClock
	batch     *state.Batch
	scheduler *chord.DeliveryScheduler
	ctx       context.Context
	store     *store.ChordStore
	owner     string
	documents *Documents
	mu        sync.Mutex
	failures  []*data.ChordError
	reports   []state.BatchError
	// onFail runs inside Fail (a host fails the live attempt, whose token it seals).
	onFail func(err *data.ChordError)
}

type objectOptions struct {
	label string
	store *store.ChordStore
	owner string
	ctx   context.Context
}

// newObject is an object with fresh documents; its store is options.store (nil: no store).
func newObject(t *testing.T, options objectOptions) *object {
	t.Helper()
	l := &loop{scheduler: chord.NewDeliveryScheduler()}
	o := &object{t: t, label: "app.owner(1)", loop: l, clock: newFakeClock(l), scheduler: l.scheduler, ctx: background, store: options.store, owner: "app.owner"}
	if options.label != "" {
		o.label = options.label
	}
	if options.owner != "" {
		o.owner = options.owner
	}
	if options.ctx != nil {
		o.ctx = options.ctx
	}
	o.batch = state.NewBatch(func(err state.BatchError) {
		o.mu.Lock()
		o.reports = append(o.reports, err)
		o.mu.Unlock()
	}, nil).UseScheduler(o.scheduler).UseMicrotasks(func(job func()) { go o.loop.do(job) })
	o.documents = NewDocuments(o)
	return o
}

func (o *object) Label() string            { return o.label }
func (o *object) Batch() *state.Batch      { return o.batch }
func (o *object) Context() context.Context { return o.ctx }
func (o *object) Do(work func())           { o.loop.do(work) }
func (o *object) Now() int64               { return o.clock.Now() }
func (o *object) AfterFunc(d time.Duration, job func()) func() {
	return o.clock.AfterFunc(d, func() { job() })
}

func (o *object) Store() (*store.ChordStore, string, bool) {
	return o.store, o.owner, o.store != nil
}

func (o *object) Fail(err *data.ChordError) {
	o.mu.Lock()
	o.failures = append(o.failures, err)
	hook := o.onFail
	o.mu.Unlock()
	if hook != nil {
		hook(err)
	}
}

// Failures is the live failures the documents reported.
func (o *object) Failures() []*data.ChordError {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*data.ChordError{}, o.failures...)
}

// delivered waits until queued watch frames have been delivered (support.ts `delivered()`).
func (o *object) delivered() { o.scheduler.Wait() }

// attempt is what one attempt of an object does with its documents, under its own seal token.
type attempt struct {
	object    *object
	token     *SealToken
	declared  []AnyDefinition
	pins      []func()
	inFactory bool
}

// newAttempt starts an attempt: its factory is running until Activate.
func (o *object) newAttempt() *attempt {
	return &attempt{object: o, token: NewSealToken(), inFactory: true}
}

// State is `self.state(definition)`: the handle (a *Singleton or a *Keyed) of the attempt's declaration.
func (a *attempt) State(definition AnyDefinition) any {
	a.object.t.Helper()
	var handle any
	var err error
	a.object.Do(func() {
		for _, declared := range a.declared {
			if declared.Name == definition.Name {
				err = errors.New(definition.Name + ": declared twice")
				return
			}
		}
		handle, err = a.object.documents.Declare(definition, a.token, a)
		if err == nil {
			a.declared = append(a.declared, definition)
		}
	})
	if err != nil {
		a.object.t.Fatalf("self.state(%s): %v", definition.Name, err)
	}
	return handle
}

// TryState is State returning the declaration's error.
func (a *attempt) TryState(definition AnyDefinition) (handle any, err error) {
	a.object.Do(func() {
		handle, err = a.object.documents.Declare(definition, a.token, a)
		if err == nil {
			a.declared = append(a.declared, definition)
		}
	})
	return handle, err
}

// Singleton is State of a singleton definition.
func (a *attempt) Singleton(definition AnyDefinition) *Singleton {
	a.object.t.Helper()
	return a.State(definition).(*Singleton)
}

// Keyed is State of a keyed definition.
func (a *attempt) Keyed(definition AnyDefinition) *Keyed {
	a.object.t.Helper()
	return a.State(definition).(*Keyed)
}

// At is KeyDeclarer: the factory alone pins a key.
func (a *attempt) At(slot *KeyedSlot, key string) (*KeySource, error) {
	var source *KeySource
	var err error
	a.object.Do(func() {
		if !a.inFactory {
			err = errors.New("at(key) is declared in the factory")
			return
		}
		var release func()
		if release, err = slot.Pin(key, a.object.ctx); err != nil {
			return
		}
		a.pins = append(a.pins, release)
		source = slot.KeySourceOf(key)
	})
	return source, err
}

// Activate is what the host does once the factory returned: the documents take the declarations and load.
func (a *attempt) Activate() error {
	var err error
	a.object.Do(func() {
		a.inFactory = false
		err = a.object.documents.Hydrate(a.declared, a.pins, a.object.ctx)
	})
	if err != nil {
		return err
	}
	return a.object.documents.Load(a.object.ctx)
}

// Tx is `self.tx`.
func (a *attempt) Tx(fn func(tx *Transaction) (any, error), ctx context.Context) (any, error) {
	return a.object.documents.Tx(fn, a.token, ctx)
}

// Fail is the attempt failing live: its token is sealed and its open transaction discarded; the object stays.
func (a *attempt) Fail() { a.object.Do(func() { a.object.documents.Fence(a.token) }) }

// End is the object ending: its documents are sealed, the line settles, the documents end.
func (o *object) End() {
	o.Do(func() { o.documents.Seal() })
	o.documents.Settled()
	o.Do(func() { o.documents.End() })
}

// activated is an object whose first attempt ran factory and activated.
func activated(t *testing.T, options objectOptions, factory func(a *attempt)) (*object, *attempt) {
	t.Helper()
	o := newObject(t, options)
	a := o.newAttempt()
	factory(a)
	if err := a.Activate(); err != nil {
		t.Fatalf("activate: %v", err)
	}
	return o, a
}

// restart is a failed attempt's successor: a new attempt that ran factory and activated.
func (o *object) restart(factory func(a *attempt)) (*attempt, error) {
	a := o.newAttempt()
	factory(a)
	return a, a.Activate()
}

// memoryStore is a store on a fresh SQLite `:memory:` database, as Chord's layers see it.
func memoryStore(t testing.TB, outbox ...bool) *store.ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	options := store.StoreOptions{}
	if len(outbox) > 0 {
		options.Outbox = &outbox[0]
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), background, options)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	chordStore := opened.(*store.ChordStore)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = chordStore.Close(ctx)
	})
	return chordStore
}

// rejection is the wire form of err's ChordError (its code, reason and message), or the error's text: Pi's `rejection`.
func rejection(t testing.TB, err error) any {
	t.Helper()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if chordErr, ok := data.IsChordError(err); ok {
		return chordErr.ToJSON()
	}
	return err.Error()
}

// obj is a draft or value helper: the document `{key: value, ...}` as the ordered object Chord stores.
func obj(pairs ...any) *delta.JsonObject { return delta.JsonObjectOf(pairs...) }
