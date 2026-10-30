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
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/host/keyed-members.test.ts: keyed members across attempts (slice 6a §1.2, §1.4, §12):
// the keys references hold on an object's keyed members, frozen while it is failed, re-bound by name (loaded first)
// when its next attempt goes live, released once. Every case serves objects through a whole host.

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
	_, err := notes.Change(key, func(draft *delta.Object) (any, error) { return nil, draft.Set("n", n) }, background)
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
	served.A, err = host.Get(kmProvider, jsonObject("id", "a"), background, GetOptions{})
	must(t, err)
	served.B, err = host.Get(kmProvider, jsonObject("id", "b"), background, GetOptions{})
	must(t, err)
	kmSet(t, registry.provider("a"), "k", 1)
	kmSet(t, registry.provider("b"), "k", 2)
	served.Exposer, err = host.Get(kmExposer, jsonObject(), background, GetOptions{})
	must(t, err)
	served.Viewer, err = host.Get(kmViewer, jsonObject("id", float64(1)), background, GetOptions{})
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
	again, err := host.Get(kmExposer, jsonObject(), background, GetOptions{})
	must(t, err)
	again.Release()
}

// keyed-members.test.ts › a failed object's held keys › keep their last values and get no frames (at, watch, a derive over
// at); the provider goes on.
func TestKeyedMembersAFailedObjectsHeldKeysKeepTheirLastValuesAndGetNoFramesAtWatchADeriveOverAtTheProviderGoesOn(t *testing.T) {
	s := kmServe(t, "a")
	values := kmWatch(t, s.Viewer.Member("value"))
	keyWatch := kmKeyWatch(t, s.Exposer.Keyed("notes"))
	keyed := nexttest.ChangesOf(keyWatch)
	direct, err := s.A.Keyed("notes").Watch(background, "k")
	must(t, err)
	directCalls := nexttest.ChangesOf(direct)
	s.fail()
	kmSet(t, s.Registry.provider("a"), "k", 3)
	deliveredOn(s.Host)
	if kind := s.Exposer.State().Value().Kind; kind != RefStateFailed {
		t.Fatalf("exposer state = %s, want failed", kind)
	}
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Viewer, "value"), kmMember(t, s.Viewer, "double"), keyWatch.Value()},
		[]any{kmN(1), float64(2), kmN(1)})
	nexttest.RequireJSONEqual(t, []any{msCalls(values.Items()), msCalls(keyed.Items())}, []any{[]any{}, []any{}})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(directCalls.Items()), []any{kmN(3)})
	must(t, s.Host.Close(background))
}

// keyed-members.test.ts › re-binding when a new attempt goes live › follows the new attempt's member of the same name (one
// frame); the old provider's pin is released.
func TestKeyedMembersRebindingFollowsTheNewAttemptsMemberOfTheSameNameOneFrameTheOldProvidersPinIsReleased(t *testing.T) {
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
	s.Registry.setWhich("b")
	kmRetry(t, s.Host, s.Exposer)
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, s.Viewer, "value"), kmMember(t, s.Viewer, "double"), keyWatch.Value()},
		[]any{kmN(2), float64(4), kmN(2)})
	if a, b := pins("a"), pins("b"); a != -1 || b != 1 {
		t.Fatalf("pins = %d, %d, want none and 1", a, b)
	}
	kmSet(t, s.Registry.provider("b"), "k", 4)
	kmSet(t, s.Registry.provider("a"), "k", 5)
	deliveredOn(s.Host)
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(values.Items()), []any{kmN(2), kmN(4)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(keyed.Items()), []any{kmN(2), kmN(4)})
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(doubles.Items()), []any{float64(4), float64(8)})
	must(t, s.Host.Close(background))
}

// keyed-members.test.ts › re-binding when a new attempt goes live › loads a cold persisted key of the new member before the
// new attempt goes live.
func TestKeyedMembersRebindingLoadsAColdPersistedKeyOfTheNewMemberBeforeTheNewAttemptGoesLive(t *testing.T) {
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
	_, err := seed.Get(durable, jsonObject(), background, GetOptions{})
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
	exposer, err := host.Get(kmExposer, jsonObject(), background, GetOptions{})
	must(t, err)
	viewer, err := host.Get(kmViewer, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	failLive(registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	kmRetry(t, host, exposer)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, viewer, "value"), kmMember(t, viewer, "double")}, []any{kmN(10), float64(20)})
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
	_, err := seed.Get(kmColdProvider, jsonObject(), background, GetOptions{})
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
	_, err = host.Get(kmColdProvider, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = host.Get(kmProvider, jsonObject("id", "b"), background, GetOptions{})
	must(t, err)
	kmSet(t, registry.provider("b"), "k", 2)
	exposer, err := host.Get(kmExposer, jsonObject(), background, GetOptions{})
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
		ref, err := host.Get(kmViewer, jsonObject("id", float64(1)), ctx, GetOptions{})
		done <- kmGot{ref: ref, err: err}
	}()
	return done
}

// keyed-members.test.ts › a key whose first pin still loads › the object fails and re-binds meanwhile: the consumer retries
// on the new member, the late pin is released.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectFailsAndRebindsMeanwhileTheConsumerRetriesOnTheNewMemberTheLatePinIsReleased(t *testing.T) {
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

// keyed-members.test.ts › a key whose first pin still loads › the object fails and stays failed: the consumer parks failed {
// waiting } on it, never live on it.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectFailsAndStaysFailedTheConsumerParksFailedWaitingOnItNeverLiveOnIt(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	abort, cancel := chordctx.WithCancel(background)
	viewer, err := c.Host.Hold(kmViewer, jsonObject("id", float64(1)), background, GetOptions{})
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
	if got := kmPins(c.Host, c.Registry.provider("a")); got != -1 {
		t.Fatalf("pins(a) = %d, want none", got)
	}
	must(t, c.Host.Close(background))
}

// keyed-members.test.ts › a key whose first pin still loads › the object ends meanwhile: the consumer's wait rejects, the
// late pin is released.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectEndsMeanwhileTheConsumersWaitRejectsTheLatePinIsReleased(t *testing.T) {
	c := kmColdServe(t, "a")
	c.hold()
	loading := kmGetViewer(c.Host, background)
	<-c.Gate.entered
	closing := make(chan error, 1)
	go func() { closing <- c.Host.Close(background) }()
	c.Gate.Release()
	if got := <-loading; got.err == nil {
		t.Fatalf("the get of the viewer = %v, want a rejection", got.ref)
	}
	must(t, <-closing)
	if got := kmPins(c.Host, c.Registry.provider("a")); got != -1 {
		t.Fatalf("pins(a) = %d, want none", got)
	}
}

// keyed-members.test.ts › a keyed watch whose object closes while it is set up › rejects with the object's end error,
// whichever microtask the close lands in. Pi's gap counts the microtasks before the close; Go's close races the watch's
// task, started first, over the same number of turns (`runtime.Gosched` each).
func TestKeyedMembersAKeyedWatchWhoseObjectClosesWhileItIsSetUpRejectsWithTheObjectsEndErrorWhicheverMicrotaskTheCloseLandsIn(t *testing.T) {
	for gap := range 40 {
		var notes atomic.Pointer[documents.Keyed]
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				handle := self.State(kmNotes).(*documents.Keyed)
				notes.Store(handle)
				return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(handle)}}, nil
			}, ServeOptions{})
		}, loadedOptions{})
		exposer, err := host.Get(kmExposer, jsonObject(), background, GetOptions{})
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

// keyed-members.test.ts › chains and releases › a key re-exposed through two objects freezes with the middle one and
// follows its next attempt.
func TestKeyedMembersAKeyReexposedThroughTwoObjectsFreezesWithTheMiddleOneAndFollowsItsNextAttempt(t *testing.T) {
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
		_, err := host.Get(kmProvider, jsonObject("id", id), background, GetOptions{})
		must(t, err)
		n := float64(2)
		if id == "a" {
			n = 1
		}
		kmSet(t, registry.provider(id), "k", n)
	}
	middle, err := host.Get(kmExposer, jsonObject(), background, GetOptions{})
	must(t, err)
	viewer, err := host.Get(outerViewer, jsonObject(), background, GetOptions{})
	must(t, err)
	values := kmWatch(t, viewer.Member("value"))
	failLive(registry.exposer(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	kmSet(t, registry.provider("a"), "k", 3)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, []any{kmMember(t, viewer, "value"), msCalls(values.Items())}, []any{kmN(1), []any{}})
	registry.setWhich("b")
	kmRetry(t, host, middle)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, kmMember(t, viewer, "value"), kmN(2))
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(values.Items()), []any{kmN(2)})
	must(t, host.Close(background))
}

// keyed-members.test.ts › chains and releases › a consumer released while the object re-activates leaves no hold behind;
// nothing is kept for it.
func TestKeyedMembersAConsumerReleasedWhileTheObjectReactivatesLeavesNoHoldBehindNothingIsKeptForIt(t *testing.T) {
	var refs []weak.Pointer[Reference]
	var providerA []weak.Pointer[documents.Keyed]
	func() {
		s := kmServe(t, "a")
		pins := func(id string) int { return kmPins(s.Host, s.Registry.provider(id)) }
		s.fail()
		s.Registry.setWhich("b")
		s.Exposer.Retry()
		s.Viewer.Release()
		again, err := s.Host.Get(kmExposer, jsonObject(), background, GetOptions{})
		must(t, err)
		again.Release()
		// The viewer's teardown releases its references.
		scopeEventually(t, "no pins left", func() bool { return pins("a") == -1 && pins("b") == -1 })
		refs = append(refs, weak.Make(s.Viewer))
		providerA = append(providerA, weak.Make(s.Registry.provider("a")))
		must(t, s.Host.Close(background))
	}()
	nexttest.ExpectCollected(t, refs...)
	nexttest.ExpectCollected(t, providerA...)
}

// keyed-members.test.ts › a key whose first pin still loads at startup (scenario 20, Astra) › keeps the first pass pending
// during an otherwise-ready dependency's delayed keyed pin. Pi delays `ChordObject.prototype.pinKey` with a spy; Go has
// no such hook, so the pin is delayed where it waits for real: the persisted key's cold load, held at the store.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsAtStartupScenario20AstraKeepsTheFirstPassPendingDuringAnOtherwiseReadyDependencysDelayedKeyedPin(t *testing.T) {
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
	_, err := seed.Get(pinStore, jsonObject(), background, GetOptions{})
	must(t, err)
	kmSet(t, memos.Load(), "k", 4)
	must(t, seed.Close(background))
	gate.armed.Store(true)
	host := newHost(t, HostOptions{OnError: func(HostError) {}, Store: st})
	loading := make(chan error, 1)
	go func() {
		loading <- host.Load([]HostFacet{{Name: "@test/pin", Facet: DefineFacet(facet)}}, background)
	}()
	<-gate.entered
	select {
	case err := <-loading:
		t.Fatalf("load finished (%v) while the keyed pin still loads", err)
	default:
	}
	gate.Release()
	must(t, <-loading)
	ref, err := host.Get(reader, jsonObject(), background, GetOptions{})
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
	value, err := promise.await()
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
