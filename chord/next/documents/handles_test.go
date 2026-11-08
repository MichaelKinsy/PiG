package documents

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/documents/handles.test.ts
//
// Pi's `restartable(objects, factory)` host is the harness's object + attempt; `fail()` is attempt.Fail().

func handlesDefinitions(t *testing.T) (body, notes *StateDefinition) {
	t.Helper()
	textSchema := contracts.Obj(contracts.Prop("text", contracts.Str()))
	initial := func() *JsonObject { return obj("text", "") }
	body, err := DefineState("body", StateOptions{Schema: textSchema, Version: 1, Initial: initial})
	if err != nil {
		t.Fatal(err)
	}
	notes, err = DefineState("notes", StateOptions{Schema: textSchema, Version: 1, Initial: initial, Keyed: true})
	if err != nil {
		t.Fatal(err)
	}
	return body, notes
}

func handlesSetText(text string) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set("text", text) }
}

func TestKeyedHandlesListKeysInCodePointOrderNotUTF16Order(t *testing.T) {
	_, notesDefinition := handlesDefinitions(t)
	var notes *Keyed
	activated(t, objectOptions{}, func(a *attempt) { notes = a.Keyed(notesDefinition) })
	// U+FF5E sorts before U+1F600 by code point; as UTF-16 its unit 0xFF5E sorts after the surrogate 0xD83D.
	for _, key := range []string{"\U0001F600", "\uFF5E", "b", "a"} {
		if _, err := notes.Change(background, key, handlesSetText(key)); err != nil {
			t.Fatal(err)
		}
	}
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"a", "b", "\uFF5E", "\U0001F600"})
	got, err := notes.Get(background, "\uFF5E")
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "\uFF5E"))
}

// TestSealedHandlesStillReadSynchronouslyAndFromMemoryChangesAndRetirementsAreRefusedReleased runs `it("still read
// synchronously and from memory; changes and retirements are refused released")` of
// packages/chord/test/documents/handles.test.ts:48. The handles are held as what `self.state` hands out, the
// SingletonHandle and KeyedHandle interfaces: `body.change` and `body.retire` (handles.test.ts:70-71) are
// SingletonHandle.Change and Retire, `notes.change` and `notes.retire` (handles.test.ts:72-73) KeyedHandle.Change and Retire.
func TestSealedHandlesStillReadSynchronouslyAndFromMemoryChangesAndRetirementsAreRefusedReleased(t *testing.T) {
	bodyDefinition, notesDefinition := handlesDefinitions(t)
	var body SingletonHandle
	var notes KeyedHandle
	o, first := activated(t, objectOptions{label: "app.restartable(1)"}, func(a *attempt) {
		body = a.Singleton(bodyDefinition)
		notes = a.Keyed(notesDefinition)
	})
	if _, err := body.Change(background, handlesSetText("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Change(background, "k", handlesSetText("y")); err != nil {
		t.Fatal(err)
	}
	first.Fail()
	value, err := slotValue(o, body)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("text", "x"))
	got, err := notes.Get(background, "k")
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "y"))
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"k"})
	released := obj("code", "released", "reason", "ended", "message", "app.restartable(1): ended")
	noop := func(*delta.Object) (any, error) { return nil, nil }
	_, err = body.Change(background, noop)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, body.Retire(background)), released)
	_, err = notes.Change(background, "k", noop)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, notes.Retire(background, "k")), released)
}
