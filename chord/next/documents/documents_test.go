package documents

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/documents/documents.test.ts
//
// Pi serves a `local("app.owner")` through a createHost host and reads the handles from `self.state`; the Go harness
// object is that object (label "app.owner(1)"), `reference.release()` is object.End().

func documentsDefinitions(t *testing.T) (list, drafts *StateDefinition) {
	t.Helper()
	list, err := DefineState("list", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str()))),
		Version: 1,
		Initial: func() *JsonObject { return obj("items", []any{}) },
	})
	if err != nil {
		t.Fatal(err)
	}
	drafts, err = DefineState("drafts", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("body", contracts.Str())),
		Version: 1,
		Initial: func() *JsonObject { return obj("body", "") },
		Keyed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return list, drafts
}

func documentsSetBody(body string) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set("body", body) }
}

func TestSingletonDocumentsReadInitialUntilWrittenAndAgainAfterARetireWhoseFrameReplacesTheRoot(t *testing.T) {
	listDefinition, draftsDefinition := documentsDefinitions(t)
	var list *Singleton
	o, _ := activated(t, objectOptions{}, func(a *attempt) {
		list = a.Singleton(listDefinition)
		a.Keyed(draftsDefinition)
	})
	initial, err := slotValue(o, list)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, initial, obj("items", []any{}))
	watch, err := list.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	frames := nexttest.FramesOf(watch)
	if _, err := list.Change(slotPush("a"), background); err != nil {
		t.Fatal(err)
	}
	value, err := slotValue(o, list)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("items", []any{"a"}))
	if err := list.Retire(background); err != nil {
		t.Fatal(err)
	}
	again, err := slotValue(o, list)
	if err != nil {
		t.Fatal(err)
	}
	if again != initial {
		t.Fatal("list.value after the retire is not the initial() value itself (toBe)")
	}
	if err := list.Retire(background); err != nil {
		t.Fatal(err)
	}
	o.delivered()
	want := []any{
		[]any{obj("items", []any{"a"}), []any{[]any{"p", []any{"items"}, 0, 0, []any{"a"}}}},
		[]any{initial, []any{[]any{"r", initial}}},
	}
	nexttest.RequireJSONEqual(t, slotFramesJSON(frames.Items()), want)
}

func TestSingletonDocumentsThrowNotReadyWhenReadInTheFactory(t *testing.T) {
	listDefinition, _ := documentsDefinitions(t)
	o := newObject(t, objectOptions{})
	a := o.newAttempt()
	list := a.Singleton(listDefinition)
	_, err := slotValue(o, list)
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeNotReady || chordErr.Message != "list: read before activation" {
		t.Fatalf("list.Value() in the factory = %v, want not_ready \"list: read before activation\"", err)
	}
}

func TestKeyedDocumentsAreAbsentUntilWrittenGetKeysAndRetireByKey(t *testing.T) {
	_, draftsDefinition := documentsDefinitions(t)
	var drafts *Keyed
	activated(t, objectOptions{}, func(a *attempt) { drafts = a.Keyed(draftsDefinition) })
	got, err := drafts.Get("a", background)
	if err != nil || got != nil {
		t.Fatalf("Get(a) before a write = %v, %v, want absent", got, err)
	}
	for _, write := range []struct{ key, body string }{{"a", "x"}, {"b", "y"}} {
		if _, err := drafts.Change(write.key, documentsSetBody(write.body), background); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := drafts.Change("c", func(*delta.Object) (any, error) { return nil, nil }, background); err != nil {
		t.Fatal(err)
	}
	got, err = drafts.Get("a", background)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("body", "x"))
	nexttest.RequireJSONEqual(t, slotKeysOf(t, drafts), []any{"a", "b"})
	if err := drafts.Retire("a", background); err != nil {
		t.Fatal(err)
	}
	if err := drafts.Retire("missing", background); err != nil {
		t.Fatal(err)
	}
	got, err = drafts.Get("a", background)
	if err != nil || got != nil {
		t.Fatalf("Get(a) after the retire = %v, %v, want absent", got, err)
	}
	nexttest.RequireJSONEqual(t, slotKeysOf(t, drafts), []any{"b"})
}

func TestWhenTheOwnerEndsDocumentsAreDroppedEveryUseThrowsReleasedAndWatchesEnd(t *testing.T) {
	listDefinition, draftsDefinition := documentsDefinitions(t)
	var list *Singleton
	var drafts *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) {
		list = a.Singleton(listDefinition)
		drafts = a.Keyed(draftsDefinition)
	})
	watch, err := list.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	o.End()
	if end := slotClosed(t, watch); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %+v, want ended", end)
	}
	released := obj("code", "released", "reason", "ended", "message", "app.owner(1): ended")
	_, err = slotValue(o, list)
	if err == nil || err.Error() != "app.owner(1): ended" {
		t.Fatalf("list.Value() = %v, want app.owner(1): ended", err)
	}
	_, err = drafts.Get("a", background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	iterator := drafts.Keys(background)
	_, _, err = iterator.Next(background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	_, err = list.Change(func(*delta.Object) (any, error) { return nil, nil }, background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
}
