package documents

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/next/documents/slot.test.ts
//
// Pi's `restartable(objects, factory)` host is the harness's object + attempt: `restart()` is the failed attempt
// failing (attempt.Fail) and the next attempt running its factory and activating (object.restart); `retry()` after a
// factory that throws is an attempt whose factory threw: the documents are never told of it. The host's reports
// (`errors`, the "app.restartable(1): " label on a failed activation) are not ported: see the PENDING(host) notes.

// slotValue reads a singleton's value from a section, as the state package's reads are.
func slotValue(o *object, handle SingletonHandle) (value *JsonObject, err error) {
	o.Do(func() { value, err = handle.Value() })
	return value, err
}

// slotKeysOf is the keys a keyed handle lists.
func slotKeysOf(t *testing.T, handle KeyedHandle) []any {
	t.Helper()
	keys := []any{}
	iterator := handle.Keys(background)
	for {
		key, ok, err := iterator.Next(background)
		if err != nil {
			t.Fatalf("Keys: %v", err)
		}
		if !ok {
			return keys
		}
		keys = append(keys, key)
	}
}

// slotPush is a change that pushes text onto items.
func slotPush(text string) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) {
		_, err := draft.Array("items").Push(text)
		return nil, err
	}
}

func slotOps(ops []delta.Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = []any(op)
	}
	return out
}

// slotFramesJSON is each frame as [value, ops].
func slotFramesJSON(frames []nexttest.Frame[delta.JsonValue]) []any {
	out := make([]any, len(frames))
	for i, frame := range frames {
		out[i] = []any{frame.Value, slotOps(frame.Ops)}
	}
	return out
}

// slotCallsJSON is each call as {kind, value, ops, retired}.
func slotCallsJSON(calls []chord.WatchCall[delta.JsonValue]) []any {
	out := make([]any, len(calls))
	for i, call := range calls {
		out[i] = obj("kind", string(call.Kind), "value", call.Value, "ops", slotOps(call.Ops), "retired", call.Retired)
	}
	return out
}

// slotClosed waits until watch ended and returns how.
func slotClosed(t *testing.T, watch chord.WatchHandle[delta.JsonValue]) chord.WatchEnd {
	t.Helper()
	select {
	case <-watch.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never ended")
	}
	return watch.End()
}

// slotRestart is `restart()`: the live attempt fails, the next one runs its factory and activates.
func slotRestart(t *testing.T, o *object, failed *attempt, factory func(a *attempt)) *attempt {
	t.Helper()
	failed.Fail()
	next, err := o.restart(factory)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	return next
}

func slotDefine(t *testing.T, name string, options StateOptions) *StateDefinition {
	t.Helper()
	definition, err := DefineState(name, options)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func slotListSchema() contracts.TSchema {
	return contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str())))
}

func slotEmptyList() *JsonObject { return obj("items", []any{}) }

func slotListV1(t *testing.T) *StateDefinition {
	return slotDefine(t, "list", StateOptions{Schema: slotListSchema(), Version: 1, Initial: slotEmptyList})
}

func slotErrorCode(err error) data.ChordErrorCode {
	if chordErr, ok := data.IsChordError(err); ok {
		return chordErr.Code
	}
	return ""
}

func TestRetirementReachesWatchesAsInitialWithRetiredTrue(t *testing.T) {
	ListV1 := slotListV1(t)
	var list *Singleton
	o, _ := activated(t, objectOptions{}, func(a *attempt) { list = a.Singleton(ListV1) })
	initial, err := slotValue(o, list)
	if err != nil {
		t.Fatal(err)
	}
	watch, err := list.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	changes := nexttest.ChangesOf(watch)
	if _, err := list.Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	if err := list.Retire(background); err != nil {
		t.Fatal(err)
	}
	o.delivered()
	want := []any{
		obj("kind", "value", "value", obj("items", []any{"a"}), "ops", []any{[]any{"p", []any{"items"}, 0, 0, []any{"a"}}}, "retired", false),
		obj("kind", "value", "value", initial, "ops", []any{[]any{"r", initial}}, "retired", true),
	}
	nexttest.RequireJSONEqual(t, slotCallsJSON(changes.Items()), want)
}

func TestARetireAndARecreateOfAPresentDocumentInOneTransactionOneFrameRetiredTheNewValue(t *testing.T) {
	ListV1 := slotListV1(t)
	var list *Singleton
	o, first := activated(t, objectOptions{}, func(a *attempt) { list = a.Singleton(ListV1) })
	if _, err := list.Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	watch, err := list.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	changes := nexttest.ChangesOf(watch)
	_, err = first.Tx(func(tx *Transaction) (any, error) {
		if err := tx.Retire(list); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(list)
		if err != nil {
			return nil, err
		}
		_, err = draft.Array("items").Push("b")
		return nil, err
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	o.delivered()
	want := []any{
		obj("kind", "value", "value", obj("items", []any{"b"}), "ops", []any{[]any{"r", obj("items", []any{"b"})}}, "retired", true),
	}
	nexttest.RequireJSONEqual(t, slotCallsJSON(changes.Items()), want)
}

func TestAcrossAttemptsKeepInMemoryDocumentsAcrossAFailureAndRetryInitialIsTakenOnce(t *testing.T) {
	initials := 0
	List := slotDefine(t, "list", StateOptions{Schema: slotListSchema(), Version: 1, Initial: func() *JsonObject {
		initials++
		return slotEmptyList()
	}})
	var handles []*Singleton
	o, first := activated(t, objectOptions{}, func(a *attempt) { handles = append(handles, a.Singleton(List)) })
	watch, err := handles[0].Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	changes := nexttest.ChangesOf(watch)
	if _, err := handles[0].Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	slotRestart(t, o, first, func(a *attempt) { handles = append(handles, a.Singleton(List)) })
	value, err := slotValue(o, handles[1])
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("items", []any{"a"}))
	if _, err := handles[1].Change(slotPush("b"), background); err != nil {
		t.Fatal(err)
	}
	o.delivered()
	// The watch taken through the first attempt's handle follows the slot across attempts.
	nexttest.RequireJSONEqual(t, nexttest.ValuesOf(changes.Items()), []any{obj("items", []any{"a"}), obj("items", []any{"a", "b"})})
	if initials != 1 {
		t.Fatalf("initial() taken %d times, want 1", initials)
	}
}

func TestAcrossAttemptsDropTheInMemoryDocumentsALaterFactoryNoLongerDeclaresTheirWatchesEnd(t *testing.T) {
	ListV1 := slotListV1(t)
	var list *Singleton
	o, first := activated(t, objectOptions{}, func(a *attempt) { list = a.Singleton(ListV1) })
	if _, err := list.Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	watch, err := list.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	slotRestart(t, o, first, func(a *attempt) {})
	if end := slotClosed(t, watch); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %+v, want ended", end)
	}
}

// PENDING(host): the factory's thrown error message "app.restartable(1): factory threw" (`errors`, activation_failed)
// is the host's report; what is ported is what the documents do when an attempt's factory throws: they never hear of it.
func TestAcrossAttemptsKeepThemAcrossAThrowingFactoryOnlyAFactoryThatReturnsDropsWhatItDoesNotDeclare(t *testing.T) {
	ListV1 := slotListV1(t)
	var handles []*Singleton
	o, first := activated(t, objectOptions{}, func(a *attempt) { handles = append(handles, a.Singleton(ListV1)) })
	if _, err := handles[0].Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	first.Fail()
	// Attempt 2's factory threw: the host never activates it, so the documents are untouched.
	o.newAttempt()
	value, err := slotValue(o, handles[0])
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("items", []any{"a"}))
	if _, err := o.restart(func(a *attempt) { handles = append(handles, a.Singleton(ListV1)) }); err != nil {
		t.Fatal(err)
	}
	value, err = slotValue(o, handles[1])
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("items", []any{"a"}))
}

func TestAcrossAttemptsMigrateInMemoryWhenALaterAttemptDeclaresANewerVersionTheOldHandlesAreRefused(t *testing.T) {
	initials := 0
	ListV1 := slotListV1(t)
	entriesSchema := contracts.Obj(contracts.Prop("entries", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str())))))
	ListV2 := slotDefine(t, "list", StateOptions{
		Schema: entriesSchema, Version: 2,
		Initial: func() *JsonObject {
			initials++
			return obj("entries", []any{})
		},
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) {
			entries := []any{}
			for _, text := range old.Value("items").([]any) {
				entries = append(entries, obj("text", text))
			}
			return obj("entries", entries), nil
		},
	})
	Notes1 := slotDefine(t, "notes", StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Keyed: true,
		Initial: func() *JsonObject { return obj("n", 0.0) },
	})
	Notes2 := slotDefine(t, "notes", StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int()), contracts.Prop("m", contracts.Int())), Version: 2, Keyed: true,
		Initial: func() *JsonObject { return obj("n", 0.0, "m", 0.0) },
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) { return obj("n", old.Value("n"), "m", 1.0), nil },
	})
	var lists []*Singleton
	var notes []*Keyed
	var inFactory []error
	var o *object
	factory := func(attemptNumber int) func(a *attempt) {
		return func(a *attempt) {
			if attemptNumber == 1 {
				lists = append(lists, a.Singleton(ListV1))
				notes = append(notes, a.Keyed(Notes1))
				return
			}
			lists = append(lists, a.Singleton(ListV2))
			notes = append(notes, a.Keyed(Notes2))
			// The new code's handle in its own factory: the values are still at version 1 until the activation.
			_, err := slotValue(o, lists[1])
			inFactory = append(inFactory, err)
		}
	}
	o = newObject(t, objectOptions{})
	first := o.newAttempt()
	factory(1)(first)
	if err := first.Activate(); err != nil {
		t.Fatal(err)
	}
	push := func(draft *delta.Object) (any, error) {
		_, err := draft.Array("items").Push("a")
		return nil, err
	}
	if _, err := lists[0].Change(push, background); err != nil {
		t.Fatal(err)
	}
	if _, err := notes[0].Change("k", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", 5.0) }, background); err != nil {
		t.Fatal(err)
	}
	watch, err := lists[0].Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	changes := nexttest.ChangesOf(watch)
	slotRestart(t, o, first, factory(2))
	value, err := slotValue(o, lists[1])
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("entries", []any{obj("text", "a")}))
	got, err := notes[1].Get("k", background)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("n", 5.0, "m", 1.0))
	_, err = slotValue(o, lists[0])
	if err == nil || err.Error() != "list: a handle of version 1; the documents are at version 2" {
		t.Fatalf("stale list.Value() = %v", err)
	}
	_, err = notes[0].Get("k", background)
	if slotErrorCode(err) != data.CodeReleased {
		t.Fatalf("stale notes.Get = %v, want released", err)
	}
	o.delivered()
	nexttest.RequireJSONEqual(t, slotCallsJSON(changes.Items()), []any{
		obj("kind", "value", "value", obj("entries", []any{obj("text", "a")}), "ops", []any{[]any{"r", obj("entries", []any{obj("text", "a")})}}, "retired", false),
	})
	if initials != 1 {
		t.Fatalf("initial() of version 2 taken %d times, want 1", initials)
	}
	if len(inFactory) != 1 || slotErrorCode(inFactory[0]) != data.CodeNotReady {
		t.Fatalf("read in the new factory = %v, want one not_ready", inFactory)
	}
}

// PENDING(host): the `errors` kinds ["object_failed", "activation_failed"] and the "app.restartable(1): " label of the
// failure are the host's reports; what is ported is the attempt's declaration failing with the documents' message.
func TestAcrossAttemptsFailTheAttemptBrokenForAFlippedKeyedAnOlderVersionOrASchemaChangedWithoutABump(t *testing.T) {
	ListV1 := slotListV1(t)
	KeyedList := slotDefine(t, "list", StateOptions{Schema: slotListSchema(), Version: 1, Initial: slotEmptyList, Keyed: true})
	Changed := slotDefine(t, "list", StateOptions{
		Schema: contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Int()))), Version: 1, Initial: slotEmptyList,
	})
	Newer := slotDefine(t, "list", StateOptions{
		Schema: slotListSchema(), Version: 2, Initial: slotEmptyList,
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) { return old, nil },
	})
	cases := []struct {
		name         string
		first, later *StateDefinition
		message      string
	}{
		{"flipped keyed", ListV1, KeyedList, "list: declared keyed, its documents are not"},
		{"schema changed without a bump", ListV1, Changed, "list: the schema changed without a version bump (version 1)"},
		{"older version", Newer, ListV1, "list: version 1 is older than its documents' (2)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, first := activated(t, objectOptions{}, func(a *attempt) { a.State(c.first) })
			first.Fail()
			second := o.newAttempt()
			_, err := second.TryState(c.later)
			if err == nil || err.Error() != c.message {
				t.Fatalf("declaring error = %v, want %q", err, c.message)
			}
		})
	}
}

var slotPrefsSchema = contracts.Obj(contracts.Prop("theme", contracts.Str()), contracts.Prop("n", contracts.Int()))

func slotPrefs(t *testing.T) *StateDefinition {
	return slotDefine(t, "prefs", StateOptions{
		Schema: slotPrefsSchema, Version: 1, Persist: true,
		Initial:        func() *JsonObject { return obj("theme", "light", "n", 0.0) },
		CheckpointWhen: func(*JsonObject, []delta.Op, CheckpointInfo) bool { return false },
	})
}

// slotPrefsHost is an object of app.store declaring definition per attempt over chordStore; prefs is the latest handle.
type slotPrefsHost struct {
	object *object
	last   *attempt
	prefs  *Singleton
}

func slotPrefsIn(t *testing.T, chordStore *store.ChordStore, definition *StateDefinition, ctx context.Context) *slotPrefsHost {
	t.Helper()
	host := &slotPrefsHost{}
	host.object, host.last = activated(t, objectOptions{label: "app.store", owner: "app.store", store: chordStore, ctx: ctx}, func(a *attempt) {
		host.prefs = a.Singleton(definition)
	})
	return host
}

func (host *slotPrefsHost) restart(t *testing.T, definition *StateDefinition) {
	t.Helper()
	host.last = slotRestart(t, host.object, host.last, func(a *attempt) { host.prefs = a.Singleton(definition) })
}

func (host *slotPrefsHost) value(t *testing.T) *JsonObject {
	t.Helper()
	value, err := slotValue(host.object, host.prefs)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// slotOutboxKinds is the kinds of the outbox's writes, per commit.
func slotOutboxKinds(t *testing.T, chordStore *store.ChordStore) []any {
	t.Helper()
	commits, err := chordStore.Outbox().Read(0, 100, background)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]any, len(commits))
	for i, commit := range commits {
		kinds := []any{}
		for _, write := range commit.Writes {
			if change, ok := write.(*store.DocumentChange); ok {
				kinds = append(kinds, "change:"+change.Content.Kind)
			} else {
				kinds = append(kinds, write.StoreWriteKind())
			}
		}
		out[i] = kinds
	}
	return out
}

func slotChange(key string, value any) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set(key, value) }
}

func TestPersistedSingletonsLoadOncePerObjectAtActivationNotAgainOnARetryAndSurviveTheHost(t *testing.T) {
	chordStore := memoryStore(t, true)
	Prefs := slotPrefs(t)
	first := slotPrefsIn(t, chordStore, Prefs, background)
	nexttest.RequireJSONEqual(t, first.value(t), obj("theme", "light", "n", 0.0))
	if _, err := first.prefs.Change(slotChange("theme", "dark"), background); err != nil {
		t.Fatal(err)
	}
	if _, err := first.prefs.Change(slotChange("n", 1.0), background); err != nil {
		t.Fatal(err)
	}
	first.object.End()
	meter := nexttest.Metered(t)
	second := slotPrefsIn(t, chordStore, Prefs, meter.Context)
	nexttest.RequireJSONEqual(t, second.value(t), obj("theme", "dark", "n", 1.0))
	if loads := meter.Counts()["chord.store.loads"]; loads != 1 {
		t.Fatalf("store loads after the activation = %v, want 1", loads)
	}
	second.restart(t, Prefs)
	nexttest.RequireJSONEqual(t, second.value(t), obj("theme", "dark", "n", 1.0))
	if loads := meter.Counts()["chord.store.loads"]; loads != 1 {
		t.Fatalf("store loads after the restart = %v, want 1", loads)
	}
	if err := second.prefs.Retire(background); err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, second.value(t), obj("theme", "light", "n", 0.0))
	nexttest.RequireJSONEqual(t, slotOutboxKinds(t, chordStore), []any{[]any{"document.create"}, []any{"change:delta"}, []any{"document.retire"}})
	second.object.End()
}

func TestPersistedSingletonsMigrateAStoredOlderVersionAtLoadTheNextChangeWritesTheMigratedBaseBack(t *testing.T) {
	chordStore := memoryStore(t, true)
	old := slotPrefsIn(t, chordStore, slotPrefs(t), background)
	if _, err := old.prefs.Change(slotChange("n", 1.0), background); err != nil {
		t.Fatal(err)
	}
	old.object.End()
	V2 := slotDefine(t, "prefs", StateOptions{
		Schema: slotPrefsSchema, Version: 2, Persist: true,
		Initial: func() *JsonObject { return obj("theme", "light", "n", 0.0) },
		Migrate: func(value *JsonObject, _ int) (*JsonObject, error) {
			return obj("theme", "migrated", "n", value.Value("n")), nil
		},
		CheckpointWhen: func(*JsonObject, []delta.Op, CheckpointInfo) bool { return false },
	})
	host := slotPrefsIn(t, chordStore, V2, background)
	nexttest.RequireJSONEqual(t, host.value(t), obj("theme", "migrated", "n", 1.0))
	if _, err := host.prefs.Change(slotChange("n", 2.0), background); err != nil {
		t.Fatal(err)
	}
	kinds := slotOutboxKinds(t, chordStore)
	nexttest.RequireJSONEqual(t, kinds[len(kinds)-1], []any{"change:base"})
	host.object.End()
}

// PENDING(host): the "app.store: " label of the reported failure (`activation_failed` through host.get); what is
// ported is the activation's error, the documents' message.
func TestPersistedSingletonsFailTheActivationBrokenForANewerStoredVersionOrAnotherFingerprintAtTheSameVersion(t *testing.T) {
	chordStore := memoryStore(t)
	V2 := slotDefine(t, "prefs", StateOptions{
		Schema: slotPrefsSchema, Version: 2, Persist: true,
		Initial:        func() *JsonObject { return obj("theme", "light", "n", 0.0) },
		CheckpointWhen: func(*JsonObject, []delta.Op, CheckpointInfo) bool { return false },
	})
	Edited := slotDefine(t, "prefs", StateOptions{
		Schema: contracts.Obj(contracts.Prop("theme", contracts.Str())), Version: 2, Persist: true,
		Initial:        func() *JsonObject { return obj("theme", "light") },
		CheckpointWhen: func(*JsonObject, []delta.Op, CheckpointInfo) bool { return false },
	})
	writer := slotPrefsIn(t, chordStore, V2, background)
	if _, err := writer.prefs.Change(slotChange("n", 1.0), background); err != nil {
		t.Fatal(err)
	}
	writer.object.End()
	cases := []struct {
		name       string
		definition *StateDefinition
		message    string
	}{
		{"newer stored version", slotPrefs(t), "prefs: stored at version 2, newer than the code's 1"},
		{"another fingerprint", Edited, "prefs: stored with another schema at version 2 (a schema changed without a bump)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := newObject(t, objectOptions{label: "app.store", owner: "app.store", store: chordStore})
			a := o.newAttempt()
			a.State(c.definition)
			err := a.Activate()
			if err == nil || err.Error() != c.message {
				t.Fatalf("activation error = %v, want %q", err, c.message)
			}
			o.End()
		})
	}
}

// PENDING(host): "refuse persisting without a stable identity" (`self.state` of a keyed object: "persist needs a stable
// identity") and "without a store" ("persist needs a store") are Pi's host/attempt.ts:918-920 checks, which documents
// do not make; the flipped persist is ported (the factory's thrown "app.store: " label is the host's).
func TestPersistedSingletonsRefusePersistingWithoutAStableIdentityOrAStoreAndAFlippedPersist(t *testing.T) {
	InMemory := slotDefine(t, "prefs", StateOptions{
		Schema: slotPrefsSchema, Version: 1,
		Initial: func() *JsonObject { return obj("theme", "light", "n", 0.0) },
	})
	chordStore := memoryStore(t)
	host := slotPrefsIn(t, chordStore, slotPrefs(t), background)
	host.last.Fail()
	_, err := host.object.newAttempt().TryState(InMemory)
	if err == nil || err.Error() != "prefs: declared in memory, its documents are not" {
		t.Fatalf("declaring error = %v, want %q", err, "prefs: declared in memory, its documents are not")
	}
}
