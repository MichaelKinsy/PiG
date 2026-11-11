package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// pi: packages/chord/src/host/reference-keys.ts
// Ports packages/chord/test/host/reference-keys.test.ts: the keys a reference holds (slice 6a §12; decision
// "References are the only indirection" (4)): its key watches and factory pins of an object's keyed members, holding
// their last values while it is failed (one `failed` call), pinned again on its next instance once that is live (one
// frame), released once. Every case serves objects through a whole host.

var kmSchema = contracts.Obj(contracts.Prop("n", contracts.Int()))

func kmDefine(name string, options documents.StateOptions) *documents.StateDefinition {
	options.Schema, options.Version = kmSchema, 1
	options.Initial = func() *delta.JsonObject { return jsonObject("n", float64(0)) }
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	kmNotes = kmDefine("notes", documents.StateOptions{Keyed: true})
	kmOwn   = kmDefine("own", documents.StateOptions{})
	// kmStored is a persisted keyed state whose keys a new host loads from the store.
	kmStored = kmDefine("notes", documents.StateOptions{
		Keyed: true, Persist: true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	kmProvider = mustContract(contracts.Local("app.provider", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	kmExposer = mustContract(contracts.Local("app.exposer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	// kmViewer is a viewer with params: it ends with its last reference (an object with empty params lives as long as its
	// host).
	kmViewer = mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("value", kmSchema), contracts.Mem("double", contracts.Int())},
	}))
	kmColdProvider = mustContract(contracts.Local("app.cold-provider", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
)

// kmRegistry is what a case's factories write and its test goroutine reads.
type kmRegistry struct {
	mu        sync.Mutex
	providers map[string]*documents.Keyed
	exposers  []Self
	which     string
}

func newKmRegistry(which string) *kmRegistry {
	return &kmRegistry{providers: map[string]*documents.Keyed{}, which: which}
}

func (r *kmRegistry) provider(id string) *documents.Keyed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.providers[id]
}

func (r *kmRegistry) setProvider(id string, notes *documents.Keyed) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[id] = notes
}

func (r *kmRegistry) addExposer(self Self) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exposers = append(r.exposers, self)
}

func (r *kmRegistry) exposer(index int) Self {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 {
		index += len(r.exposers)
	}
	return r.exposers[index]
}

func (r *kmRegistry) setWhich(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.which = id
}

func (r *kmRegistry) current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.which
}

// kmKeyed is a keyed member of an implementation: the handle, or a reference's keyed member re-exposed.
func kmKeyed(handle any) map[string]contracts.KeyedWatchable[delta.JsonValue] {
	return map[string]contracts.KeyedWatchable[delta.JsonValue]{"notes": ExposeKeyed(handle)}
}

// kmViewerFactory is a viewer of key `k` of the exposer's notes.
func kmViewerFactory(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
	value, err := self.Get(kmExposer, jsonObject()).Keyed("notes").At("k")
	if err != nil {
		return contracts.Implementation{}, err
	}
	double := self.Derive([]any{value}, func(values []any) (delta.JsonValue, error) {
		return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
	})
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"value": Expose(value), "double": double},
	}}, nil
}

// kmServeProviders serves the providers `a` and `b` (in memory) and the exposer re-exposing the registry's `which`.
func kmServeProviders(h FacetHost, registry *kmRegistry) error {
	if err := h.Serve(kmProvider, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		notes := self.State(kmNotes).(*documents.Keyed)
		registry.setProvider(params.Value("id").(string), notes)
		return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		registry.addExposer(self)
		self.State(kmOwn)
		notes := self.Get(kmProvider, jsonObject("id", registry.current())).Keyed("notes")
		return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
	}, ServeOptions{})
}

// kmSet sets the n of key `key` of notes.
func kmSet(t testing.TB, notes *documents.Keyed, key string, n float64) {
	t.Helper()
	_, err := notes.Change(background, key, func(draft *delta.Object) (any, error) { return nil, draft.Set("n", n) })
	must(t, err)
}

// kmPins is the pins on key `k` of notes (-1: none: Pi's undefined).
func kmPins(host Host, notes *documents.Keyed) int {
	pins := -1
	host.(*chordHost).loop().Run(func() {
		if source := notes.Slot.KeySourceOf("k"); source != nil {
			pins = source.Pins()
		}
	})
	return pins
}

// kmServed is a host with providers `a` and `b` (key `k`: 1 and 2), an exposer re-exposing `registry.which`'s notes and a
// viewer of its key `k` (Pi's `served`).
type kmServed struct {
	Host     Host
	Registry *kmRegistry
	A, B     *Reference
	Exposer  *Reference
	Viewer   *Reference
}

func (s *kmServed) fail() {
	failLive(s.Registry.exposer(-1), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
}

func kmServe(t *testing.T, which string) *kmServed {
	t.Helper()
	registry := newKmRegistry(which)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := kmServeProviders(h, registry); err != nil {
			return err
		}
		return h.Serve(kmViewer, kmViewerFactory, ServeOptions{})
	}, loadedOptions{})
	served := &kmServed{Host: host, Registry: registry}
	var err error
	served.A, err = host.Get(background, kmProvider, jsonObject("id", "a"), GetOptions{})
	must(t, err)
	served.B, err = host.Get(background, kmProvider, jsonObject("id", "b"), GetOptions{})
	must(t, err)
	kmSet(t, registry.provider("a"), "k", 1)
	kmSet(t, registry.provider("b"), "k", 2)
	served.Exposer, err = host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	served.Viewer, err = host.Get(background, kmViewer, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	return served
}

// kmMember is the current value of a state member.
func kmMember(t testing.TB, ref *Reference, member string) any {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return value
}

func kmWatch(t testing.TB, member *ReferenceMember) *nexttest.Collector[chord.WatchCall[delta.JsonValue]] {
	t.Helper()
	watch, err := member.Watch(background)
	must(t, err)
	return nexttest.ChangesOf(watch)
}

func kmKeyWatch(t testing.TB, keyed *ReferenceKeyed) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	watch, err := keyed.Watch(background, "k")
	must(t, err)
	if watch == nil {
		t.Fatalf("the key is absent")
	}
	return watch
}

func kmN(n float64) *delta.JsonObject { return jsonObject("n", n) }

// kmRetry asks the exposer for its next attempt and waits for it: `exposer.retry(); (await host.get(Exposer)).release()`.
func kmRetry(t testing.TB, host Host, exposer *Reference) {
	t.Helper()
	exposer.Retry()
	again, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	again.Release()
}

// reference-keys.test.ts › a failed object's held keys › keep their last values and get the failure once (at, watch, a
// derive over at); the provider goes on.
func TestReferenceKeysAFailedObjectsHeldKeysKeepTheirLastValuesAndGetTheFailureOnceAtWatchADeriveOverAtTheProviderGoesOn(t *testing.T) {
	s := kmServe(t, "a")
	values := kmWatch(t, s.Viewer.Member("value"))
	keyWatch := kmKeyWatch(t, s.Exposer.Keyed("notes"))
	keyed := nexttest.ChangesOf(keyWatch)
	direct, err := s.A.Keyed("notes").Watch(background, "k")
	must(t, err)
	directCalls := nexttest.ChangesOf(direct)
	s.fail()
	awaitWatchCalls(t, "the failure reaches the viewer's key", values, 1)
	awaitWatchCalls(t, "the failure reaches the key watch", keyed, 1)
	kmSet(t, s.Registry.provider("a"), "k", 3)
	deliveredOn(s.Host)
	if kind := s.Exposer.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("exposer state = %s, want failed", kind)
	}
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Viewer, "value"), kmMember(t, s.Viewer, "double"), keyWatch.Value()},
		[]any{kmN(1), float64(2), kmN(1)})
	nexttest.RequireJSONEqual(t, []any{msKinds(values.Items()), msKinds(keyed.Items())}, []any{[]any{"failed"}, []any{"failed"}})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(directCalls.Items()), []any{kmN(3)})
	must(t, s.Host.Close(background))
}

// reference-keys.test.ts › re-binding when a new attempt goes live › pins again on the new instance's member of the same name
// (one frame); the old provider's pin is released.
func TestReferenceKeysRebindingPinsAgainOnTheNewInstancesMemberOfTheSameNameOneFrameTheOldProvidersPinIsReleased(t *testing.T) {
	s := kmServe(t, "a")
	values := kmWatch(t, s.Viewer.Member("value"))
	doubles := kmWatch(t, s.Viewer.Member("double"))
	keyWatch := kmKeyWatch(t, s.Exposer.Keyed("notes"))
	keyed := nexttest.ChangesOf(keyWatch)
	pins := func(id string) int { return kmPins(s.Host, s.Registry.provider(id)) }
	if got := pins("a"); got != 1 {
		t.Fatalf("pins(a) = %d, want 1", got)
	}
	s.fail()
	// Its keys follow the failed instance until its admitted commits published (audit A10), then show the failure.
	awaitWatchCalls(t, "the failure reaches the viewer's key", values, 1)
	deliveredOn(s.Host)
	s.Registry.setWhich("b")
	kmRetry(t, s.Host, s.Exposer)
	// The keys pin again on the new instance: its pins land on tasks of the loop (Pi's promise reactions).
	scopeEventually(t, "the keys pinned again on provider b", func() bool {
		return nexttest.JSONEqual(kmMember(t, s.Viewer, "value"), kmN(2)) && nexttest.JSONEqual(keyWatch.Value(), kmN(2)) &&
			pins("a") == -1 && pins("b") == 1
	})
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Viewer, "value"), kmMember(t, s.Viewer, "double"), keyWatch.Value()},
		[]any{kmN(2), float64(4), kmN(2)})
	kmSet(t, s.Registry.provider("b"), "k", 4)
	kmSet(t, s.Registry.provider("a"), "k", 5)
	deliveredOn(s.Host)
	// The failure once, then whole values (the first after a failure is `[["r", value]]`).
	boom := nexttest.FailedValue{Failed: "boom"}
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(values.Items()), []any{boom, kmN(2), kmN(4)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(keyed.Items()), []any{boom, kmN(2), kmN(4)})
	// A derive over the key reads its last value meanwhile: it does not fail.
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(doubles.Items()), []any{float64(4), float64(8)})
	must(t, s.Host.Close(background))
}

// reference-keys.test.ts › re-binding when a new attempt goes live › loads a cold persisted key of the new member once the new
// instance is live (the last value meanwhile).
func TestReferenceKeysRebindingLoadsAColdPersistedKeyOfTheNewMemberOnceTheNewInstanceIsLiveTheLastValueMeanwhile(t *testing.T) {
	durable := mustContract(contracts.Local("app.durable", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	st := memoryStore(t)
	var memos atomic.Pointer[documents.Keyed]
	serveDurable := func(h FacetHost) error {
		return h.Serve(durable, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(kmStored).(*documents.Keyed)
			memos.Store(notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{})
	}
	seed, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveDurable(h) }, loadedOptions{Store: st})
	_, err := seed.Get(background, durable, jsonObject(), GetOptions{})
	must(t, err)
	kmSet(t, memos.Load(), "k", 10)
	must(t, seed.Close(background))
	// A new host: the key is in the store only.
	registry := newKmRegistry("a")
	var runs atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(kmProvider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(self.State(kmNotes))}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := serveDurable(h); err != nil {
			return err
		}
		if err := h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			registry.addExposer(self)
			self.State(kmOwn)
			var notes *ReferenceKeyed
			if runs.Add(1) == 1 {
				notes = self.Get(kmProvider, jsonObject("id", "a")).Keyed("notes")
			} else {
				notes = self.Get(durable, jsonObject()).Keyed("notes")
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(kmViewer, kmViewerFactory, ServeOptions{})
	}, loadedOptions{Store: st})
	exposer, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	viewer, err := host.Get(background, kmViewer, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	failLiveThen(host, registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil), exposer.Retry)
	again, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	again.Release()
	// The key loads from the store after the new instance went live: one frame then.
	scopeEventually(t, "the viewer shows the stored key", func() bool {
		value, err := viewer.Member("value").Value()
		double, derr := viewer.Member("double").Value()
		return err == nil && derr == nil && nexttest.JSONEqual(value, kmN(10)) && nexttest.JSONEqual(double, float64(20))
	})
	must(t, host.Close(background))
}

// readGate holds the first read of a store once armed, after it ran (Pi's `persistence.read` wrapper that resolves
// `entered`, then awaits `gate`).
type readGate struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

func newReadGate() *readGate {
	return &readGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *readGate) hit() {
	if !g.armed.Load() {
		return
	}
	held := false
	g.once.Do(func() { held = true })
	if !held {
		return
	}
	close(g.entered)
	<-g.release
}

func (g *readGate) Release() { g.freed.Do(func() { close(g.release) }) }

type gatedReads struct {
	sqlite.SqliteExecutor
	gate *readGate
}

func (e *gatedReads) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	row, err := e.SqliteExecutor.Get(sql, params...)
	e.gate.hit()
	return row, err
}

func (e *gatedReads) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	rows, err := e.SqliteExecutor.All(sql, params...)
	e.gate.hit()
	return rows, err
}

// readGateDatabase is a database whose reads, once armed, are held after they ran.
type readGateDatabase struct {
	sqlite.SqliteDatabase
	gate *readGate
}

func (db *readGateDatabase) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	return (&gatedReads{SqliteExecutor: db.SqliteDatabase, gate: db.gate}).Get(sql, params...)
}

func (db *readGateDatabase) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	return (&gatedReads{SqliteExecutor: db.SqliteDatabase, gate: db.gate}).All(sql, params...)
}

func (db *readGateDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		return callback(&gatedReads{SqliteExecutor: transaction, gate: db.gate})
	})
}

// kmCold is Pi's `cold`: provider `a` (its key `k` in the store only, its loads held at the gate once Hold is called) and
// `b` (in memory), an exposer re-exposing `which`'s notes, and a viewer of key `k`.
type kmCold struct {
	Host     Host
	Errors   *hostErrors
	Registry *kmRegistry
	Exposer  *Reference
	Gate     *readGate
	Store    *store.ChordStore
}

func (c *kmCold) hold() { c.Gate.armed.Store(true) }

func (c *kmCold) fail() {
	failLive(c.Registry.exposer(-1), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
}

func kmColdServe(t *testing.T, which string) *kmCold {
	t.Helper()
	gate := newReadGate()
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &readGateDatabase{SqliteDatabase: db, gate: gate}
	}, nil)
	t.Cleanup(gate.Release)
	var seeded atomic.Pointer[documents.Keyed]
	seed, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(kmColdProvider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(kmStored).(*documents.Keyed)
			seeded.Store(notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: st})
	_, err := seed.Get(background, kmColdProvider, jsonObject(), GetOptions{})
	must(t, err)
	kmSet(t, seeded.Load(), "k", 1)
	must(t, seed.Close(background))
	// A new host: the key is in the store only.
	registry := newKmRegistry(which)
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(kmColdProvider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(kmStored).(*documents.Keyed)
			registry.setProvider("a", notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(kmProvider, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(kmNotes).(*documents.Keyed)
			registry.setProvider(params.Value("id").(string), notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			registry.addExposer(self)
			self.State(kmOwn)
			var notes *ReferenceKeyed
			if registry.current() == "a" {
				notes = self.Get(kmColdProvider, jsonObject()).Keyed("notes")
			} else {
				notes = self.Get(kmProvider, jsonObject("id", "b")).Keyed("notes")
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(kmViewer, kmViewerFactory, ServeOptions{})
	}, loadedOptions{Store: st})
	_, err = host.Get(background, kmColdProvider, jsonObject(), GetOptions{})
	must(t, err)
	_, err = host.Get(background, kmProvider, jsonObject("id", "b"), GetOptions{})
	must(t, err)
	kmSet(t, registry.provider("b"), "k", 2)
	exposer, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	return &kmCold{Host: host, Errors: errs, Registry: registry, Exposer: exposer, Gate: gate, Store: st}
}

// getAsync is `host.get` started on a task: the result arrives on the channel.
type kmGot struct {
	ref *Reference
	err error
}

func kmGetViewer(host Host, ctx context.Context) <-chan kmGot {
	done := make(chan kmGot, 1)
	go func() {
		ref, err := host.Get(ctx, kmViewer, jsonObject("id", float64(1)), GetOptions{})
		done <- kmGot{ref: ref, err: err}
	}()
	return done
}

// reference-keys.test.ts › a key whose first pin still loads › the object fails and re-binds meanwhile: the consumer retries
// on the new member, the late pin is released.
func TestReferenceKeysAKeyWhoseFirstPinStillLoadsTheObjectFailsAndRebindsMeanwhileTheConsumerRetriesOnTheNewMemberTheLatePinIsReleased(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	loading := kmGetViewer(c.Host, background)
	<-c.Gate.entered
	c.fail()
	c.Registry.setWhich("b")
	kmRetry(t, c.Host, c.Exposer)
	c.Gate.Release()
	got := <-loading
	must(t, got.err)
	viewer := got.ref
	deliveredOn(c.Host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, viewer, "value"), kmMember(t, viewer, "double")}, []any{kmN(2), float64(4)})
	scopeEventually(t, "the late pin on a is released and b is pinned once", func() bool {
		return kmPins(c.Host, c.Registry.provider("a")) == -1 && kmPins(c.Host, c.Registry.provider("b")) == 1
	})
	// The pin's failed load parked the viewer (a dependency failure is not its own): nothing failed it.
	if got := failures(c.Errors.All()); len(got) != 0 {
		t.Fatalf("activation failures = %v, want none", got)
	}
	values := kmWatch(t, viewer.Member("value"))
	kmSet(t, c.Registry.provider("a"), "k", 3)
	kmSet(t, c.Registry.provider("b"), "k", 4)
	deliveredOn(c.Host)
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(values.Items()), []any{kmN(4)})
	must(t, c.Host.Close(background))
}

// reference-keys.test.ts › a key whose first pin still loads › the object fails and stays failed: the consumer parks failed {
// waiting } on it, never live on it.
func TestReferenceKeysAKeyWhoseFirstPinStillLoadsTheObjectFailsAndStaysFailedTheConsumerParksFailedWaitingOnItNeverLiveOnIt(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	abort, cancel := chordctx.WithCancel(background)
	viewer, err := c.Host.Hold(background, kmViewer, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	loading := kmGetViewer(c.Host, abort)
	<-c.Gate.entered
	c.fail()
	c.Gate.Release()
	deliveredOn(c.Host)
	scopeEventually(t, "the viewer parks failed { waiting }", func() bool {
		state := viewer.State().Value()
		return state.Kind == RefStateFailed && state.Cause.Kind == data.FailureWaiting
	})
	if kind := c.Exposer.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("exposer state = %s, want failed", kind)
	}
	viewer.Release()
	cancel(errors.New("aborted"))
	if got := <-loading; got.err == nil {
		t.Fatalf("the get of the viewer = %v, want a rejection", got.ref)
	}
	scopeEventually(t, "the late pin on a is released", func() bool { return kmPins(c.Host, c.Registry.provider("a")) == -1 })
	must(t, c.Host.Close(background))
}

// reference-keys.test.ts › a key whose first pin still loads › the object ends meanwhile: the consumer's wait rejects, the
// late pin is released.
func TestReferenceKeysAKeyWhoseFirstPinStillLoadsTheObjectEndsMeanwhileTheConsumersWaitRejectsTheLatePinIsReleased(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	loading := kmGetViewer(c.Host, background)
	<-c.Gate.entered
	closing := make(chan error, 1)
	go func() { closing <- c.Host.Close(background) }()
	// Pi's `host.close()` ends the object in its own synchronous part, before `gate.release()` runs. Here Close runs on a
	// goroutine of its own: wait for that section (the host closed, its objects ended) before the pin is released.
	scopeEventually(t, "the host to close its objects", func() bool {
		host := c.Host.(*chordHost)
		var closed bool
		host.enter(func() { closed = host.closing() })
		return closed
	})
	c.Gate.Release()
	if got := <-loading; got.err == nil {
		t.Fatalf("the get of the viewer = %v, want a rejection", got.ref)
	}
	must(t, <-closing)
	if got := kmPins(c.Host, c.Registry.provider("a")); got != -1 {
		t.Fatalf("pins(a) = %d, want none", got)
	}
}

// reference-keys.test.ts › a keyed watch whose object closes while it is set up › rejects with the object's end error,
// whichever microtask the close lands in. Pi's gap counts the microtasks before the close; Go's close races the watch's
// task, started first, over the same number of turns (`runtime.Gosched` each).
func TestReferenceKeysAKeyedWatchWhoseObjectClosesWhileItIsSetUpRejectsWithTheObjectsEndErrorWhicheverMicrotaskTheCloseLandsIn(t *testing.T) {
	for gap := range 40 {
		var notes atomic.Pointer[documents.Keyed]
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				handle := self.State(kmNotes).(*documents.Keyed)
				notes.Store(handle)
				return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(handle)}}, nil
			}, ServeOptions{})
		}, loadedOptions{})
		exposer, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
		must(t, err)
		kmSet(t, notes.Load(), "a", 1)
		outcome := make(chan string, 1)
		go func() {
			watch, err := exposer.Keyed("notes").Watch(background, "a")
			if err == nil {
				_ = watch
				outcome <- "watch"
				return
			}
			chordErr, _ := data.IsChordError(err)
			outcome <- string(chordErr.Code) + "/" + string(chordErr.Reason)
		}()
		for range gap {
			host.(*chordHost).loop().Run(func() {})
		}
		must(t, host.Close(background))
		if got := <-outcome; got != "watch" && got != "released/ended" {
			t.Fatalf("gap %d: the watch = %s, want watch or released/ended", gap, got)
		}
	}
}

// reference-keys.test.ts › chains and releases › a key re-exposed through two objects holds with the middle one (the failure
// once) and follows its next instance.
func TestReferenceKeysChainsAndReleasesAKeyReexposedThroughTwoObjectsHoldsWithTheMiddleOneTheFailureOnceAndFollowsItsNextInstance(t *testing.T) {
	outer := mustContract(contracts.Local("app.outer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	outerViewer := mustContract(contracts.Local("app.outer-viewer", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("value", kmSchema)},
	}))
	registry := newKmRegistry("a")
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := kmServeProviders(h, registry); err != nil {
			return err
		}
		if err := h.Serve(outer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: kmKeyed(self.Get(kmExposer, jsonObject()).Keyed("notes")),
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(outerViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value, err := self.Get(outer, jsonObject()).Keyed("notes").At("k")
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"value": Expose(value)},
			}}, err
		}, ServeOptions{})
	}, loadedOptions{})
	for _, id := range []string{"a", "b"} {
		_, err := host.Get(background, kmProvider, jsonObject("id", id), GetOptions{})
		must(t, err)
		n := float64(2)
		if id == "a" {
			n = 1
		}
		kmSet(t, registry.provider(id), "k", n)
	}
	middle, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	viewer, err := host.Get(background, outerViewer, jsonObject(), GetOptions{})
	must(t, err)
	values := kmWatch(t, viewer.Member("value"))
	failLive(registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	awaitWatchCalls(t, "the failure reaches the viewer", values, 1)
	kmSet(t, registry.provider("a"), "k", 3)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, viewer, "value"), msKinds(values.Items())}, []any{kmN(1), []any{"failed"}})
	registry.setWhich("b")
	kmRetry(t, host, middle)
	scopeEventually(t, "the viewer follows the next instance", func() bool { return nexttest.JSONEqual(kmMember(t, viewer, "value"), kmN(2)) })
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, kmMember(t, viewer, "value"), kmN(2))
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(values.Items()), []any{nexttest.FailedValue{Failed: "boom"}, kmN(2)})
	must(t, host.Close(background))
}

// reference-keys.test.ts › chains and releases › a consumer released while the object re-activates leaves no hold behind;
// nothing is kept for it.
func TestReferenceKeysAConsumerReleasedWhileTheObjectReactivatesLeavesNoHoldBehindNothingIsKeptForIt(t *testing.T) {
	var refs []weak.Pointer[Reference]
	var providerA []weak.Pointer[documents.Keyed]
	func() {
		s := kmServe(t, "a")
		pins := func(id string) int { return kmPins(s.Host, s.Registry.provider(id)) }
		s.fail()
		s.Registry.setWhich("b")
		s.Exposer.Retry()
		s.Viewer.Release()
		again, err := s.Host.Get(background, kmExposer, jsonObject(), GetOptions{})
		must(t, err)
		again.Release()
		// The viewer's teardown releases its references.
		scopeEventually(t, "no pins left", func() bool { return pins("a") == -1 && pins("b") == -1 })
		refs = append(refs, weak.Make(s.Viewer))
		providerA = append(providerA, weak.Make(s.Registry.provider("a")))
		must(t, s.Host.Close(background))
	}()
	expectCollectedEventually(t, refs...)
	expectCollectedEventually(t, providerA...)
}

// reference-keys.test.ts › a key whose first pin still loads at startup (scenario 20, Astra) › keeps the first pass pending
// during an otherwise-ready dependency's delayed keyed pin. Pi delays `ChordObject.prototype.pinKey` with a spy; Go has
// no such hook, so the pin is delayed where it waits for real: the persisted key's cold load, held at the store.
func TestReferenceKeysAKeyWhoseFirstPinStillLoadsAtStartupScenario20AstraKeepsTheFirstPassPendingDuringAnOtherwiseReadyDependencysDelayedKeyedPin(t *testing.T) {
	pinStore := mustContract(contracts.Local("app.pin-store", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	reader := mustContract(contracts.Local("app.pin-reader", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("value", kmSchema)},
	}))
	data4 := kmDefine("app.pin-data", documents.StateOptions{
		Keyed: true, Persist: true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	gate := newReadGate()
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &readGateDatabase{SqliteDatabase: db, gate: gate}
	}, nil)
	t.Cleanup(gate.Release)
	var memos atomic.Pointer[documents.Keyed]
	facet := func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(pinStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(data4).(*documents.Keyed)
			memos.Store(notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(reader, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value, err := self.Get(pinStore, jsonObject()).Keyed("notes").At("k")
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"value": Expose(value)},
			}}, err
		}, ServeOptions{})
	}
	seed, _ := loaded(t, facet, loadedOptions{Store: st})
	_, err := seed.Get(background, pinStore, jsonObject(), GetOptions{})
	must(t, err)
	kmSet(t, memos.Load(), "k", 4)
	must(t, seed.Close(background))
	gate.armed.Store(true)
	host := newHost(t, HostOptions{OnError: func(HostError) {}, Store: st})
	loading := make(chan error, 1)
	go func() {
		_, err := host.Load([]HostFacet{{Name: "@test/pin", Facet: DefineFacet(facet)}}, background)
		loading <- err
	}()
	<-gate.entered
	select {
	case err := <-loading:
		t.Fatalf("load finished (%v) while the keyed pin still loads", err)
	default:
	}
	gate.Release()
	must(t, <-loading)
	ref, err := host.Get(background, reader, jsonObject(), GetOptions{})
	must(t, err)
	nexttest.RequireJSONEqual(t, kmMember(t, ref, "value"), kmN(4))
	must(t, host.Close(background))
}

// Own test of keyedPromise, the stored Promise of Pi's `ready` (several awaiters): its reactions run as microtasks of the
// loop, after the section that settled it, in the order they were registered.
func TestKeyedPromiseCallbacksRunAsMicrotasksInTheOrderTheyWereRegistered(t *testing.T) {
	h := newMemberHarness(t)
	promise := newKeyedPromise[int](h.loop)
	var order []int
	h.loop.Run(func() {
		for i := range 3 {
			promise.then(func(value int, err error) { order = append(order, value*10+i) })
		}
		promise.resolve(7)
		// Reactions run after the section that settled the promise, not inside it.
		expectEqual(t, "reactions inside the settling section", len(order), 0)
		promise.resolve(8)
		promise.then(func(value int, _ error) { order = append(order, value*10+3) })
	})
	expectEqual(t, "order", order, []int{70, 71, 72, 73})
	value, err := promise.await(background)
	expectEqual(t, "await", []any{value, err}, []any{7, nil})
}

// Own test, mirrors keyed-members.ts `awaitWithContext(entry.ready, context)`: the abort reason wins when the signal is
// aborted first, a result wins when the promise settled first, and either way the callback runs once.
func TestKeyedPromiseAwaitWithContextCallsBackOnceWhicheverComesFirst(t *testing.T) {
	h := newMemberHarness(t)
	reason := errors.New("aborted")
	t.Run("the abort first", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		var got []error
		done := make(chan struct{})
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(_ int, err error) { got = append(got, err); close(done) })
		})
		cancel(reason)
		<-done
		h.loop.Run(func() { promise.resolve(1) })
		expectEqual(t, "calls", got, []error{reason})
	})
	t.Run("the result first", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		var got []any
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(value int, err error) { got = append(got, value, err) })
			promise.resolve(1)
		})
		cancel(reason)
		h.loop.Wait()
		h.loop.Run(func() {})
		expectEqual(t, "calls", got, []any{1, nil})
	})
	t.Run("an aborted context rejects at once, a context with no abort signal waits on the promise alone", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		cancel(reason)
		var got []any
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(value int, err error) { got = append(got, value, err) })
			promise.thenWithContext(memberBackground, func(value int, err error) { got = append(got, value, err) })
			promise.resolve(2)
		})
		expectEqual(t, "calls", got, []any{0, reason, 2, nil})
	})
}

// Own test of keyedMap, the Map of Pi's `#entries`: insertion order, and iterators that are live as a JavaScript Map's.
func TestKeyedMapIteratesLiveInInsertionOrder(t *testing.T) {
	var m keyedMap[int]
	for i, key := range []string{"a", "b", "c"} {
		m.set(key, i)
	}
	m.set("a", 9)
	expectEqual(t, "order", m.values(), []int{9, 1, 2})
	it := m.iterate()
	first, _ := it.next()
	// An entry added meanwhile is visited, one deleted before its turn is not; a deleted one re-added goes last.
	m.set("d", 3)
	m.delete("b")
	m.delete("a")
	m.set("a", 4)
	var visited []int
	visited = append(visited, first)
	for value, ok := it.next(); ok; value, ok = it.next() {
		visited = append(visited, value)
	}
	expectEqual(t, "visited", visited, []int{9, 2, 3, 4})
	expectEqual(t, "after", m.values(), []int{2, 3, 4})
	if m.iterators != 0 {
		t.Fatalf("a finished iterator is still counted")
	}
	// A cleared map goes on with what is added after it.
	it = m.iterate()
	m.clear()
	m.set("e", 5)
	value, ok := it.next()
	expectEqual(t, "after clear", []any{value, ok, m.len()}, []any{5, true, 1})
	it.close()
	it.close()
	expectEqual(t, "iterators", m.iterators, 0)
	// Compaction drops the removed entries only when nothing iterates.
	for i := range 100 {
		m.set(string(rune('A'+i%26))+string(rune('a'+i/26)), i)
	}
	for i := range 100 {
		m.delete(string(rune('A'+i%26)) + string(rune('a'+i/26)))
	}
	if len(m.nodes) > 40 {
		t.Fatalf("removed entries are kept: %d nodes for %d entries", len(m.nodes), m.len())
	}
	expectEqual(t, "survivor", m.values(), []int{5})
}

// reference-keys.test.ts › a released key leaves its document (final review) › collects a reference whose key watch stopped
// while another reference pins the same key (Astra).
func TestReferenceKeysAReleasedKeyLeavesItsDocumentFinalReviewCollectsAReferenceWhoseKeyWatchStoppedWhileAnotherReferencePinsTheSameKeyAstra(t *testing.T) {
	rows := func() *documents.StateDefinition {
		definition, err := documents.DefineState("rows", documents.StateOptions{
			Schema: kmSchema, Version: 1, Keyed: true, Initial: func() *delta.JsonObject { return jsonObject("n", float64(1)) },
		})
		must(t, err)
		return definition
	}()
	typ := mustContract(contracts.Local("app.key-gc", contracts.LocalDeclaration{
		Params: contracts.Params{}, Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("rows", contracts.Keyed(kmSchema)), contracts.Mem("set", contracts.NewMethod())},
	}))
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(rows).(*documents.Keyed)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"rows": ExposeKeyed(handle)},
				Methods: map[string]contracts.MethodFunction{"set": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := handle.Change(ctx, "a", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(2)) })
					return nil, err
				}},
			}}, nil
		}, ServeOptions{})
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/key-gc", Facet: facet}}, background))
	keeper := refGet(t, host, typ, jsonObject())
	_, err := keeper.Call("set", nil, background)
	must(t, err)
	kept, err := keeper.Keyed("rows").Watch(background, "a")
	must(t, err)
	if kept == nil {
		t.Fatalf("the key is absent")
	}
	nexttest.ChangesOf(kept)
	released := func() weak.Pointer[Reference] {
		ref := refGet(t, host, typ, jsonObject())
		watch, err := ref.Keyed("rows").Watch(background, "a")
		must(t, err)
		nexttest.ChangesOf(watch)
		_, err = watch.Stop()
		must(t, err)
		ref.Release()
		return weak.Make(ref)
	}()
	nexttest.Flushed()
	expectCollectedEventually(t, released)
	_, err = kept.Stop()
	must(t, err)
	keeper.Release()
	must(t, host.Close(background))
}

var kmKeys = mustContract(contracts.Local("app.keys", contracts.LocalDeclaration{
	Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
}))

// kmKeysHandles are the notes handles of every attempt of `Keys`, as a case's factories write them.
type kmKeysHandles struct {
	mu      sync.Mutex
	handles []*documents.Keyed
}

func (k *kmKeysHandles) add(notes *documents.Keyed) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.handles = append(k.handles, notes)
}

func (k *kmKeysHandles) at(index int) *documents.Keyed {
	k.mu.Lock()
	defer k.mu.Unlock()
	if index < 0 {
		index += len(k.handles)
	}
	return k.handles[index]
}

// kmServeKeys serves `Keys` over the persisted kmStored (its handle kept in handles), with a native opened by open.
func kmServeKeys(handles *kmKeysHandles, open func(ctx context.Context) error) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(kmKeys, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) { return nil, open(ctx) },
				func(any, NativeCloseReason, context.Context) error { return nil })
			notes := self.State(kmStored).(*documents.Keyed)
			handles.add(notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{})
	})
}

// reference-keys.test.ts › keys across a restart and a failed re-pin (Astra's coverage note) › a commit admitted to a watched
// key, landing after a restart's drain, is in the key's last value.
func TestReferenceKeysKeysAcrossARestartAndAFailedRePinACommitAdmittedToAWatchedKeyLandingAfterARestartsDrainIsInTheKeysLastValue(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	handles := &kmKeysHandles{}
	host := newHost(t, HostOptions{Store: st, OnError: func(HostError) {}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: kmServeKeys(handles, func(context.Context) error { return nil })}}, background))
	keys := refGet(t, host, kmKeys, jsonObject())
	change := func(n float64) error {
		_, err := handles.at(-1).Change(background, "k", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", n) })
		return err
	}
	must(t, change(1))
	watch, err := keys.Keyed("notes").Watch(background, "k")
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	held := holdNextCommit()
	admitted := make(chan error, 1)
	go func() { admitted <- change(5) }()
	<-held.Entered()
	// The new code's native stays opening: the new instance is not live while the key holds its last value.
	opening := make(chan struct{})
	reloaded := make(chan error, 1)
	go func() {
		_, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: kmServeKeys(handles, func(ctx context.Context) error {
			select {
			case <-opening:
			case <-ctx.Done():
			}
			return nil
		})}}, background, ReloadOptions{})
		reloaded <- err
	}()
	deliveredOn(host)
	held.Release()
	must(t, <-admitted)
	must(t, <-reloaded)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, []any{watch.Value(), nexttest.ValuesOf(calls.Items())}, []any{kmN(5), []any{kmN(5)}})
	close(opening)
	scopeEventually(t, "the keys object is live", func() bool { return keys.State().Value().Kind == RefStateLive })
	nexttest.RequireJSONEqual(t, watch.Value(), kmN(5))
	must(t, host.Close(background))
}

// fakeInstance is an instance of a fake object: its notes key's source, and the attachable the references attach to.
type fakeInstance struct {
	source state.DeriveSource
}

func (*fakeInstance) attachedVariant() string { return "" }

func (*fakeInstance) eventNode(string, int) *eventNode { return nil }

// fakeKeysTarget is the object a reference's keys pin on: one live instance at a time, whose `pinKey` fails once when
// asked (Pi replaces the key slot's `persistence.read` once).
type fakeKeysTarget struct {
	target
	mu       sync.Mutex
	live     *fakeInstance
	failNext bool
	released atomic.Int32
}

func (f *fakeKeysTarget) instance() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		return nil
	}
	return f.live
}

func (f *fakeKeysTarget) attachable() attachable {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		return nil
	}
	return f.live
}

func (f *fakeKeysTarget) state() *RefState { return refLive }
func (f *fakeKeysTarget) label() string    { return "app.keys" }

func (f *fakeKeysTarget) pinKey(_, _ string, _ int, _ context.Context) (pinnedKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return pinnedKey{}, errors.New("disk")
	}
	return pinnedKey{source: f.live.source, release: func() { f.released.Add(1) }}, nil
}

func (f *fakeKeysTarget) become(instance *fakeInstance, failNext bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live, f.failNext = instance, failNext
}

// fakeKeysHolder is the reference whose keys these are.
type fakeKeysHolder struct {
	h      *memberHarness
	object target
	// cold is whether a pin of the key reads the store (the keys are in memory when nil).
	cold *atomic.Bool
}

func (f *fakeKeysHolder) none() error                             { return notReady("test: not live yet") }
func (f *fakeKeysHolder) keysBatch() *state.Batch                 { return f.h.batch }
func (f *fakeKeysHolder) keysScheduler() *chord.DeliveryScheduler { return f.h.scheduler }
func (f *fakeKeysHolder) keysLoop() sectionLoop                   { return f.h.loop }
func (f *fakeKeysHolder) keysVersion() int                        { return 1 }
func (f *fakeKeysHolder) keysTarget() target                      { return f.object }
func (f *fakeKeysHolder) keysInMemory(_, _ string) bool           { return f.cold == nil || !f.cold.Load() }

// reference-keys.test.ts › keys across a restart and a failed re-pin (Astra's coverage note) › a key's re-pin on a new
// instance that fails to load keeps its last value, one failed call; the next pin heals it. Forced difference: Pi
// replaces the key slot's `persistence.read` to throw "disk" once while a reload swaps the exposer; a Go `Persistence` is a
// concrete type over the store, whose guarded reads stop the whole store (closing the host) on a backend error, and a
// stored version a read returns newer than the code's breaks the owner object. The re-pin's failure is injected where the
// reference's keys take it, in the object's `pinKey`, and the instances are the object's successive live instances.
func TestReferenceKeysKeysAcrossARestartAndAFailedRePinAKeysRePinOnANewInstanceThatFailsToLoadKeepsItsLastValueOneFailedCallTheNextPinHealsIt(t *testing.T) {
	h := newMemberHarness(t)
	first := &fakeInstance{source: newMemberDoc(h.batch, kmN(2))}
	object := &fakeKeysTarget{live: first}
	keys := newReferenceKeys(&fakeKeysHolder{h: h, object: object})
	held, err := keys.hold("notes", "k", background)
	must(t, err)
	watch := h.watch(t, held.source.(state.SourceHandle))
	calls := nexttest.ChangesOf(watch)
	// A swap in place: the new instance shows the key; its pin fails to load.
	next := &fakeInstance{source: newMemberDoc(h.batch, kmN(1))}
	object.become(next, true)
	h.within(t, func() { keys.follow(background) })
	scopeEventually(t, "the failed re-pin shows on the key", func() bool { return len(calls.Items()) == 1 })
	nexttest.RequireJSONEqual(t, []any{watch.Value(), nexttest.ValuesOf(calls.Items())}, []any{kmN(2), []any{nexttest.FailedValue{Failed: "app.keys: disk"}}})
	if released := object.released.Load(); released != 0 {
		t.Fatalf("pins released = %d, want none yet (the first pin is released with the key or when a pin replaces it)", released)
	}
	// The next instance pins it again, loaded this time: one frame heals it.
	healed := &fakeInstance{source: newMemberDoc(h.batch, kmN(1))}
	object.become(healed, false)
	h.within(t, func() { keys.follow(background) })
	scopeEventually(t, "the key heals", func() bool { return nexttest.JSONEqual(watch.Value(), kmN(1)) })
	h.delivered()
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(calls.Items()), []any{nexttest.FailedValue{Failed: "app.keys: disk"}, kmN(1)})
	h.loop.Run(held.release)
	if released := object.released.Load(); released != 2 {
		t.Fatalf("pins released = %d, want the replaced pin and the last", released)
	}
}

// gatedInstance is an instance of a gated object: its notes key's source, and its pin of the key, which loads once the test
// lets it (fails with err when set).
type gatedInstance struct {
	fakeInstance
	entered  chan struct{}
	loaded   chan struct{}
	letOnce  sync.Once
	err      error
	released atomic.Int32
}

func newGatedInstance(source state.DeriveSource) *gatedInstance {
	return &gatedInstance{source: source, entered: make(chan struct{}), loaded: make(chan struct{})}
}

// let loads the instance's pin.
func (g *gatedInstance) let() { g.letOnce.Do(func() { close(g.loaded) }) }

// gatedKeysTarget is the object a reference's keys pin on: one live instance at a time, whose `pinKey` is entered when it
// starts and returns when the test lets that instance's pin load (a cold load held open, as Pi's `persistence.read` is).
type gatedKeysTarget struct {
	target
	mu   sync.Mutex
	live *gatedInstance
}

func (g *gatedKeysTarget) current() *gatedInstance {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.live
}

func (g *gatedKeysTarget) become(instance *gatedInstance) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.live = instance
}

func (g *gatedKeysTarget) instance() any {
	if live := g.current(); live != nil {
		return live
	}
	return nil
}

func (g *gatedKeysTarget) attachable() attachable {
	if live := g.current(); live != nil {
		return live
	}
	return nil
}

func (g *gatedKeysTarget) state() *RefState { return refLive }
func (g *gatedKeysTarget) label() string    { return "app.keys" }

func (g *gatedKeysTarget) pinKey(_, _ string, _ int, _ context.Context) (pinnedKey, error) {
	instance := g.current()
	close(instance.entered)
	<-instance.loaded
	if instance.err != nil {
		return pinnedKey{}, instance.err
	}
	return pinnedKey{source: instance.source, release: func() { instance.released.Add(1) }}, nil
}

// listenedContext is a context that tells when a waiter listens for its abort (a `context.AfterFunc`, as an
// `addEventListener("abort")` is seen in Pi): the wait of a hold on a pin is such a listener, so a test sees when a hold went
// on waiting without a clock.
type listenedContext struct {
	*scopeCounting
	listened chan struct{}
}

func (c *listenedContext) AfterFunc(f func()) func() bool {
	stop := c.scopeCounting.AfterFunc(f)
	c.listened <- struct{}{}
	return stop
}

// gatedHold is a `hold` of key `k` started on a goroutine, on an object whose live instance is `first`: its pin has started
// loading and the hold waits for it. Pi's `await hold` is the same wait; the test's turns are the channels.
type gatedHold struct {
	first    *gatedInstance
	abort    func(cause error)
	cold     atomic.Bool
	h        *memberHarness
	keys     *referenceKeys
	object   *gatedKeysTarget
	listened chan struct{}
	holding  chan gatedHeld
}

type gatedHeld struct {
	pin pinnedKey
	err error
}

func startGatedHold(t *testing.T, first *gatedInstance) *gatedHold {
	t.Helper()
	h := newMemberHarness(t)
	g := &gatedHold{first: first, h: h, object: &gatedKeysTarget{live: first}, listened: make(chan struct{}, 8), holding: make(chan gatedHeld, 1)}
	g.keys = newReferenceKeys(&fakeKeysHolder{h: h, object: g.object, cold: &g.cold})
	signal, abort := newScopeCounting()
	g.abort = abort
	// Registered after the harness's cleanup, so it runs first: a pin still held when a case ends would keep its task, and
	// the harness waits for the tasks.
	t.Cleanup(func() {
		if live := g.object.current(); live != nil {
			live.let()
		}
		first.let()
	})
	go func() {
		pin, err := g.keys.hold("notes", "k", &listenedContext{scopeCounting: signal, listened: g.listened})
		g.holding <- gatedHeld{pin: pin, err: err}
	}()
	<-first.entered
	<-g.listened
	return g
}

// move is the object going live on instance, the key following it (`keys.follow`): its pin there starts loading.
func (g *gatedHold) move(t *testing.T, instance *gatedInstance) {
	t.Helper()
	g.object.become(instance)
	g.h.within(t, func() { g.keys.follow(background) })
	<-instance.entered
}

// letLoad lets the first instance's pin load and returns once the hold settled it: the hold it returned, or nil when it went
// on waiting, on another pin.
func (g *gatedHold) letLoad() (held *gatedHeld) {
	g.first.let()
	select {
	case got := <-g.holding:
		return &got
	case <-g.listened:
		return nil
	}
}

// reference-keys.test.ts › a key whose first pin still loads › the object fails and re-binds meanwhile: the consumer retries
// on the new member, the late pin is released. Pi's test lets the late pin load once the new member's pin has landed, which
// its turn order guarantees; here the late pin loads first, and the hold goes on waiting for the pin on the new instance.
func TestReferenceKeysAHoldWhosePinTheKeyLeftForANewInstanceWaitsForThePinOnTheNewInstance(t *testing.T) {
	first := newGatedInstance(nil)
	g := startGatedHold(t, first)
	second := newGatedInstance(newMemberDoc(g.h.batch, kmN(2)))
	g.move(t, second)
	if held := g.letLoad(); held != nil {
		t.Fatalf("the hold returned (%v) with its source not loaded: the pin on the new instance had not landed", held.err)
	}
	second.let()
	got := <-g.holding
	must(t, got.err)
	nexttest.RequireJSONEqual(t, g.h.read(t, got.pin.source), kmN(2))
	if released := first.released.Load(); released != 1 {
		t.Fatalf("late pins released = %d, want the one", released)
	}
	g.h.loop.Run(got.pin.release)
	if released := second.released.Load(); released != 1 {
		t.Fatalf("pins on the new instance released = %d, want the one", released)
	}
}

// The pin on the new instance fails to load: the hold still returns its source, as Pi's does once that pin settled (a
// failure of it shows on the key, which never had a value: reads of it are `not_ready`), and keeps the key until released.
func TestReferenceKeysAHoldWhosePinTheKeyLeftForANewInstanceReturnsItsSourceOnceThePinOnTheNewInstanceFailedToLoad(t *testing.T) {
	first := newGatedInstance(nil)
	g := startGatedHold(t, first)
	second := newGatedInstance(nil)
	second.err = errors.New("disk")
	g.move(t, second)
	if held := g.letLoad(); held != nil {
		t.Fatalf("the hold returned (%v) before the pin on the new instance settled", held.err)
	}
	second.let()
	got := <-g.holding
	must(t, got.err)
	var readErr error
	var kept int
	g.h.loop.Run(func() {
		_, readErr = got.pin.source.Value()
		kept = g.keys.entries.len()
	})
	if readErr == nil || readErr.Error() != "test: not live yet" {
		t.Fatalf("read of the key = %v, want not_ready (it never had a value)", readErr)
	}
	if kept != 1 {
		t.Fatalf("keys held = %d, want the one", kept)
	}
	g.h.loop.Run(got.pin.release)
	g.h.loop.Run(func() { kept = g.keys.entries.len() })
	if kept != 0 {
		t.Fatalf("keys held after release = %d, want none", kept)
	}
}

// A new pin whose load has not finished when the old pin settles has not landed in Pi either, so Pi's hold has returned
// by then, with the key's source as it is: the hold does not wait for it.
func TestReferenceKeysAHoldWhosePinTheKeyLeftForANewInstanceDoesNotWaitForAPinOnTheNewInstanceThatReadsTheStore(t *testing.T) {
	first := newGatedInstance(nil)
	g := startGatedHold(t, first)
	second := newGatedInstance(newMemberDoc(g.h.batch, kmN(2)))
	g.cold.Store(true)
	g.move(t, second)
	held := g.letLoad()
	if held == nil {
		t.Fatalf("the hold went on waiting for a pin on the new instance that reads the store: Pi's has returned by then")
	}
	must(t, held.err)
	var readErr error
	g.h.loop.Run(func() { _, readErr = held.pin.source.Value() })
	if readErr == nil || readErr.Error() != "test: not live yet" {
		t.Fatalf("read of the key = %v, want not_ready (the new pin has not landed)", readErr)
	}
	second.let()
	g.h.loop.Wait()
	g.h.loop.Run(held.pin.release)
	if released := second.released.Load(); released != 1 {
		t.Fatalf("pins on the new instance released = %d, want the one", released)
	}
}

// The context of a hold ends its wait for the pin on the new instance too: it fails with the context's cause, lets the key
// go, and the pin that lands later is released.
func TestReferenceKeysAHoldWaitingForThePinOnTheNewInstanceEndsWithItsContext(t *testing.T) {
	first := newGatedInstance(nil)
	g := startGatedHold(t, first)
	second := newGatedInstance(newMemberDoc(g.h.batch, kmN(2)))
	g.move(t, second)
	if held := g.letLoad(); held != nil {
		t.Fatalf("the hold returned (%v) before the pin on the new instance settled", held.err)
	}
	cause := errors.New("aborted")
	g.abort(cause)
	got := <-g.holding
	if !errors.Is(got.err, cause) {
		t.Fatalf("the hold = %v, want the context's cause", got.err)
	}
	var kept int
	g.h.loop.Run(func() { kept = g.keys.entries.len() })
	if kept != 0 {
		t.Fatalf("keys held = %d, want none", kept)
	}
	second.let()
	g.h.loop.Wait()
	if released := second.released.Load(); released != 1 {
		t.Fatalf("pins on the new instance released = %d, want the one that landed after the hold ended", released)
	}
}
