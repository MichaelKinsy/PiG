package host

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Host halves of the documents tests (D4 rows): the cases of slot.test.ts, keyed-slot.test.ts and transaction.test.ts whose
// assertions are the host's (the attempt's declaration checks and failure kinds, the reports, `host.get`'s rejection), and
// the held-commit cases of object.test.ts's `a live object failing`. They run on a real host through the restartable and
// storeRestartable helpers or `loaded`.

func listDefinition(t testing.TB, version int, keyed bool, schema contracts.TSchema, migrate func(*delta.JsonObject, int) (*delta.JsonObject, error)) *documents.StateDefinition {
	t.Helper()
	definition, err := documents.DefineState("list", documents.StateOptions{
		Schema: schema, Version: version, Keyed: keyed, Migrate: migrate,
		Initial: func() *delta.JsonObject { return jsonObject("items", []any{}) },
	})
	must(t, err)
	return definition
}

// lastActivationFailure is the message of the last reported activation failure's error (the file's `failure(errors)`).
func lastActivationFailure(errs []HostError) string {
	message := ""
	for _, report := range errs {
		if report.Kind == ErrActivationFailed {
			message = report.Failure.Error.Message
		}
	}
	return message
}

func reportKinds(errs []HostError) []HostErrorKind {
	var kinds []HostErrorKind
	for _, report := range errs {
		kinds = append(kinds, report.Kind)
	}
	return kinds
}

// restartableGet is `retry()` of Pi's restartable when the next attempt is expected to fail: it asks for the attempt and gets.
func restartableGet(t testing.TB, run *restartableRunner) error {
	t.Helper()
	run.Reference.Retry()
	_, err := run.Host.Get(Restartable, jsonObject("id", 1.0), background, GetOptions{})
	return err
}

// slot.test.ts "across attempts › keep them across a throwing factory: only a factory that returns drops what it does not declare".
func TestSlotAcrossAttemptsKeepThemAcrossAThrowingFactoryOnlyAFactoryThatReturnsDropsWhatItDoesNotDeclare(t *testing.T) {
	var mu sync.Mutex
	var handles []*singletonDoc
	run := restartable(t, func(attempt int, self Self) {
		if attempt == 2 {
			panic(errors.New("factory threw"))
		}
		mu.Lock()
		handles = append(handles, stateOf(self, attemptListDoc))
		mu.Unlock()
	}, loadedOptions{})
	mu.Lock()
	first := handles[0]
	mu.Unlock()
	must(t, first.Change(func(draft *delta.Object) { _, _ = draft.Array("items").Push("a") }, background))
	run.Fail()
	if err := restartableGet(t, run); err == nil {
		t.Fatal("the attempt with the throwing factory did not fail")
	}
	if got := lastActivationFailure(run.Errors.All()); got != "app.restartable(1): factory threw" {
		t.Fatalf("failure = %q, want the factory's message", got)
	}
	run.Retry()
	mu.Lock()
	second := handles[1]
	mu.Unlock()
	var value *delta.JsonObject
	run.Host.(*chordHost).enter(func() { value = second.Value() })
	if !reflect.DeepEqual(value, jsonObject("items", []any{"a"})) {
		t.Fatalf("list = %v, want {items: [a]}", value)
	}
}

// slot.test.ts "across attempts › fail the attempt broken for a flipped keyed, an older version or a schema changed without a bump".
func TestSlotAcrossAttemptsFailTheAttemptBrokenForAFlippedKeyedAnOlderVersionOrASchemaChangedWithoutABump(t *testing.T) {
	strItems := contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str())))
	listV1 := listDefinition(t, 1, false, strItems, nil)
	keyedList := listDefinition(t, 1, true, strItems, nil)
	changed := listDefinition(t, 1, false, contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Int()))), nil)
	newer := listDefinition(t, 2, false, strItems, func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) { return old, nil })
	for _, each := range []struct {
		name         string
		first, later *documents.StateDefinition
		message      string
	}{
		{"a flipped keyed", listV1, keyedList, "list: declared keyed, its documents are not"},
		{"a schema changed without a bump", listV1, changed, "list: the schema changed without a version bump (version 1)"},
		{"an older version", newer, listV1, "list: version 1 is older than its documents' (2)"},
	} {
		t.Run(each.name, func(t *testing.T) {
			run := restartable(t, func(attempt int, self Self) {
				if attempt == 1 {
					self.State(each.first)
					return
				}
				self.State(each.later)
			}, loadedOptions{})
			run.Fail()
			if err := restartableGet(t, run); err == nil {
				t.Fatal("the attempt did not fail")
			}
			if got := reportKinds(run.Errors.All()); !reflect.DeepEqual(got, []HostErrorKind{ErrObjectFailed, ErrActivationFailed}) {
				t.Fatalf("report kinds = %v, want [object_failed activation_failed]", got)
			}
			if got := lastActivationFailure(run.Errors.All()); got != "app.restartable(1): "+each.message {
				t.Fatalf("failure = %q, want %q", got, each.message)
			}
		})
	}
}

func prefsDefinition(t testing.TB, version int, schema contracts.TSchema, persist bool, initial func() *delta.JsonObject) *documents.StateDefinition {
	t.Helper()
	definition, err := documents.DefineState("prefs", documents.StateOptions{
		Schema: schema, Version: version, Persist: persist, Initial: initial,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	must(t, err)
	return definition
}

// slot.test.ts "persisted singletons › fail the activation broken for a newer stored version or another fingerprint at the same version (D9)".
func TestSlotPersistedSingletonsFailTheActivationBrokenForANewerStoredVersionOrAnotherFingerprintAtTheSameVersionD9(t *testing.T) {
	chordStore := memoryStore(t)
	themeN := contracts.Obj(contracts.Prop("theme", contracts.Str()), contracts.Prop("n", contracts.Int()))
	light := func() *delta.JsonObject { return jsonObject("theme", "light", "n", 0.0) }
	prefs := prefsDefinition(t, 1, themeN, true, light)
	v2 := prefsDefinition(t, 2, themeN, true, light)
	edited := prefsDefinition(t, 2, contracts.Obj(contracts.Prop("theme", contracts.Str())), true,
		func() *delta.JsonObject { return jsonObject("theme", "light") })
	serve := func(definition *documents.StateDefinition, doc **singletonDoc) func(h FacetHost, _ FacetSelf) error {
		return func(h FacetHost, _ FacetSelf) error {
			return h.Serve(StoreRestartable, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				handle := self.(StoreSelf).State(definition).(*documents.Singleton)
				if doc != nil {
					*doc = &singletonDoc{s: handle}
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"touch": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				}}}, nil
			}, ServeOptions{})
		}
	}
	var writerDoc *singletonDoc
	writer, _ := loaded(t, serve(v2, &writerDoc), loadedOptions{Store: chordStore})
	mustGet(t, writer, StoreRestartable, jsonObject())
	must(t, writerDoc.Change(func(draft *delta.Object) { _ = draft.Set("n", 1.0) }, background))
	closeHosts(t, writer)
	for _, each := range []struct {
		definition *documents.StateDefinition
		message    string
	}{
		{prefs, "prefs: stored at version 2, newer than the code's 1"},
		{edited, "prefs: stored with another schema at version 2 (a schema changed without a bump)"},
	} {
		host, errs := loaded(t, serve(each.definition, nil), loadedOptions{Store: chordStore})
		if _, err := host.Get(StoreRestartable, jsonObject(), background, GetOptions{}); err == nil {
			t.Fatalf("get with %q did not fail", each.message)
		}
		if got := lastActivationFailure(errs.All()); got != "app.store: "+each.message {
			t.Fatalf("failure = %q, want %q", got, "app.store: "+each.message)
		}
		closeHosts(t, host)
	}
	must(t, chordStore.Close(background))
}

// slot.test.ts "persisted singletons › refuse persisting without a stable identity or a store, and a flipped persist".
func TestSlotPersistedSingletonsRefusePersistingWithoutAStableIdentityOrAStoreAndAFlippedPersist(t *testing.T) {
	themeN := contracts.Obj(contracts.Prop("theme", contracts.Str()), contracts.Prop("n", contracts.Int()))
	light := func() *delta.JsonObject { return jsonObject("theme", "light", "n", 0.0) }
	prefs := prefsDefinition(t, 1, themeN, true, light)
	inMemory := prefsDefinition(t, 1, themeN, false, light)
	// The types refuse it; a plugin may still try (P3).
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(Restartable, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.State(prefs)
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	if _, err := host.Get(Restartable, jsonObject("id", 1.0), background, GetOptions{}); err == nil || !hasText(err.Error(), "persist needs a stable identity") {
		t.Fatalf("keyed get = %v, want persist needs a stable identity", err)
	}
	storeless, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(StoreRestartable, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.State(prefs)
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	if _, err := storeless.Get(StoreRestartable, jsonObject(), background, GetOptions{}); err == nil || !hasText(err.Error(), "persist needs a store") {
		t.Fatalf("storeless get = %v, want persist needs a store", err)
	}
	chordStore := memoryStore(t)
	run := storeRestartable(t, func(attempt int, self StoreSelf) {
		if attempt == 1 {
			self.State(prefs)
			return
		}
		self.State(inMemory)
	}, loadedOptions{Store: chordStore})
	run.Fail()
	run.Reference.Retry()
	if _, err := run.Host.Get(StoreRestartable, jsonObject(), background, GetOptions{}); err == nil {
		t.Fatal("the flipped persist did not fail")
	}
	if got := lastActivationFailure(run.Errors.All()); got != "app.store: prefs: declared in memory, its documents are not" {
		t.Fatalf("failure = %q", got)
	}
	must(t, chordStore.Close(background))
}

// --- object.test.ts "a live object failing": the held-commit cases ------------------------------------------------------

var objCounterPersisted = func() *documents.StateDefinition {
	definition, err := documents.DefineState("counter", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Persist: true,
		Initial:        func() *delta.JsonObject { return jsonObject("n", 0.0) },
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func TestObjectALiveObjectFailingLetsACommitAdmittedBeforeTheFailureAdoptAndPublishOnceDerivesAndDependentsIncluded(t *testing.T) {
	doubled := mustContract(contracts.Local("app.doubled", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("double", contracts.Int())}}))
	viewerType := mustContract(contracts.Local("app.double-viewer", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("tens", contracts.Int())}}))
	chordStore, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	var self Self
	var counter *singletonDoc
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(doubled, func(_ *delta.JsonObject, s Self) (contracts.Implementation, error) {
			mu.Lock()
			self, counter = s, &singletonDoc{s: s.(StoreSelf).State(objCounterPersisted).(*documents.Singleton)}
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"double": s.Derive([]any{counter.s}, func(values []any) (delta.JsonValue, error) { return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil }),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(viewerType, func(_ *delta.JsonObject, s Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"tens": s.Derive([]any{s.Get(doubled, jsonObject()).Member("double")}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) * 10, nil }),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	ref := mustGet(t, host, doubled, jsonObject())
	viewer := mustGet(t, host, viewerType, jsonObject())
	doubles, err := ref.Member("double").Watch(background)
	must(t, err)
	doubleFrames := nexttest.ChangesOf(doubles)
	tens, err := viewer.Member("tens").Watch(background)
	must(t, err)
	tenFrames := nexttest.ChangesOf(tens)
	held := holdNextCommit()
	changed := make(chan error, 1)
	go func() {
		changed <- counter.Change(func(draft *delta.Object) { _ = draft.Set("n", 1.0) }, background)
	}()
	receive(t, held.Entered())
	mu.Lock()
	failing := self
	mu.Unlock()
	failLive(failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	held.Release()
	must(t, receive(t, changed))
	turn(host)
	if got := stateOfRef(ref).Kind; got != RefStateFailed {
		t.Fatalf("state = %s, want failed", got)
	}
	var stored *delta.JsonObject
	host.(*chordHost).enter(func() { stored = counter.Value() })
	double, _ := ref.Member("double").Value()
	ten, _ := viewer.Member("tens").Value()
	if !reflect.DeepEqual(stored, jsonObject("n", 1.0)) || double != 2.0 || ten != 20.0 {
		t.Fatalf("counter %v, double %v, tens %v, want {n:1}, 2, 20", stored, double, ten)
	}
	if got := nexttest.ValuesOf(doubleFrames.Items()); !reflect.DeepEqual(got, []any{2.0}) {
		t.Fatalf("double frames = %v, want [2]", got)
	}
	if got := nexttest.ValuesOf(tenFrames.Items()); !reflect.DeepEqual(got, []any{20.0}) {
		t.Fatalf("tens frames = %v, want [20]", got)
	}
	closeHosts(t, host)
	must(t, chordStore.Close(background))
}

func TestObjectALiveObjectFailingStartsARetrysFactoryOnlyOnceACommitAdmittedBeforeTheFailureAdopted(t *testing.T) {
	chordStore, _, holdNextCommit := faultyStore(t)
	counterV1 := objCounterPersisted
	counterV2, err := documents.DefineState("counter", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("m", contracts.Int())), Version: 2, Persist: true,
		Initial: func() *delta.JsonObject { return jsonObject("m", 0.0) },
		Migrate: func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
			return jsonObject("m", old.Value("n")), nil
		},
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	must(t, err)
	var mu sync.Mutex
	var counters []*singletonDoc
	closeTimeout := 5 * time.Millisecond
	run := storeRestartable(t, func(attempt int, self StoreSelf) {
		definition := counterV1
		if attempt != 1 {
			definition = counterV2
		}
		mu.Lock()
		counters = append(counters, &singletonDoc{s: self.State(definition).(*documents.Singleton)})
		mu.Unlock()
	}, loadedOptions{Store: chordStore, CloseTimeout: &closeTimeout})
	mu.Lock()
	v1 := counters[0]
	mu.Unlock()
	must(t, v1.Change(func(draft *delta.Object) { _ = draft.Set("n", 1.0) }, background))
	held := holdNextCommit()
	changed := make(chan error, 1)
	go func() { changed <- v1.Change(func(draft *delta.Object) { _ = draft.Set("n", 2.0) }, background) }()
	receive(t, held.Entered())
	failLive(run.Attempts.at(0), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	// Past the drain: only the admitted step holds the retry.
	run.Reference.Retry()
	turn(run.Host)
	mu.Lock()
	started := len(counters)
	mu.Unlock()
	if started != 1 {
		t.Fatalf("factories started = %d, want 1 while the commit is held", started)
	}
	held.Release()
	must(t, receive(t, changed))
	mustGet(t, run.Host, StoreRestartable, jsonObject()).Release()
	mu.Lock()
	second := counters[1]
	mu.Unlock()
	var value *delta.JsonObject
	run.Host.(*chordHost).enter(func() { value = second.Value() })
	if !reflect.DeepEqual(value, jsonObject("m", 2.0)) {
		t.Fatalf("counter = %v, want {m: 2}", value)
	}
	closeHosts(t, run.Host)
	must(t, chordStore.Close(background))
}

func TestObjectALiveObjectFailingKeepsItsStateMembersLastValuesForSameHostReadsALiveDependentsDeriveOverThemStaysLive(t *testing.T) {
	viewerType := mustContract(contracts.Local("app.count-viewer", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("tens", contracts.Int())}}))
	var mu sync.Mutex
	var storeSelf Self
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			storeSelf = self
			mu.Unlock()
			return serveStore(self), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(viewerType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"tens": self.Derive([]any{self.Get(objStore, jsonObject()).Member("count")}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) * 10, nil }),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, objStore, jsonObject())
	_, err := refCall(ref, "add", jsonObject("text", "a"))
	must(t, err)
	viewer := mustGet(t, host, viewerType, jsonObject())
	watch, err := viewer.Member("tens").Watch(background)
	must(t, err)
	tens := nexttest.ChangesOf(watch)
	// A change committed in the turn of the failure: the frozen member computes it.
	_, err = refCall(ref, "add", jsonObject("text", "b"))
	must(t, err)
	mu.Lock()
	failing := storeSelf
	mu.Unlock()
	failLive(failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	if v, _ := ref.Member("count").Value(); v != 2.0 {
		t.Fatalf("count = %v, want 2", v)
	}
	_, err = refCall(ref, "add", jsonObject("text", "c"))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("add after the failure = %v, want boom", err)
	}
	if got := stateOfRef(viewer).Kind; got != RefStateLive {
		t.Fatalf("viewer state = %s, want live", got)
	}
	if v, _ := viewer.Member("tens").Value(); v != 20.0 {
		t.Fatalf("tens = %v, want 20", v)
	}
	turn(host)
	if got := nexttest.ValuesOf(tens.Items()); !reflect.DeepEqual(got, []any{20.0}) {
		t.Fatalf("tens frames = %v, want [20]", got)
	}
	if got := reportKinds(errs.All()); !reflect.DeepEqual(got, []HostErrorKind{ErrObjectFailed}) {
		t.Fatalf("report kinds = %v, want [object_failed]", got)
	}
	// A retry takes the new attempt's members.
	ref.Retry()
	mustGet(t, host, objStore, jsonObject()).Release()
	if v, _ := ref.Member("count").Value(); v != 2.0 {
		t.Fatalf("count after the retry = %v, want 2", v)
	}
	closeHosts(t, host)
}

// transaction.test.ts "persisted documents in a transaction › a handler's commit held in the SQL past closeTimeout still adopts
// before the object ends; the store stays usable".
func TestTransactionPersistedDocumentsAHandlersCommitHeldInTheSQLPastCloseTimeoutStillAdoptsBeforeTheObjectEndsTheStoreStaysUsable(t *testing.T) {
	loop := newScopeLoop()
	writer := mustContract(contracts.Local("app.writer", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("write", contracts.NewMethod())}}))
	chordStore, _, holdNextCommit := faultyStore(t, true)
	var counter *singletonDoc
	closeTimeout := 20 * time.Millisecond
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(writer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			counter = &singletonDoc{s: self.(StoreSelf).State(objCounterPersisted).(*documents.Singleton)}
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"write": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, bumpDoc(counter, ctx) },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore, CloseTimeout: &closeTimeout, Loop: loop})
	ref := mustGet(t, host, writer, jsonObject())
	held := holdNextCommit()
	called := make(chan error, 1)
	go func() { _, err := refCall(ref, "write", nil); called <- err }()
	receive(t, held.Entered())
	closed := make(chan error, 1)
	go func() { closed <- host.Close(background) }()
	until(t, "the close's drain timer", func() bool { _, found := (&routinesEnv{loop: loop}).nextTimerAt(); return found })
	loop.AdvanceAsync(60)
	until(t, "the drain_timeout report", func() bool { return errs.Len() == 1 })
	if got := reportKinds(errs.All()); !reflect.DeepEqual(got, []HostErrorKind{ErrDrainTimeout}) {
		t.Fatalf("report kinds = %v, want [drain_timeout]", got)
	}
	select {
	case <-closed:
		t.Fatal("the host closed before the admitted commit adopted")
	default:
	}
	held.Release()
	must(t, receive(t, closed))
	_ = receive(t, called)
	// The store stays usable: a new host over it serves the committed value.
	again, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(writer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			counter = &singletonDoc{s: self.(StoreSelf).State(objCounterPersisted).(*documents.Singleton)}
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"write": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	mustGet(t, again, writer, jsonObject())
	var value *delta.JsonObject
	again.(*chordHost).enter(func() { value = counter.Value() })
	if !reflect.DeepEqual(value, jsonObject("n", 1.0)) {
		t.Fatalf("stored counter = %v, want {n: 1}", value)
	}
	closeHosts(t, again)
	must(t, chordStore.Close(background))
}
