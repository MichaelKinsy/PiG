package documents

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/documents/handles.test.ts
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
		if _, err := notes.Change(key, handlesSetText(key), background); err != nil {
			t.Fatal(err)
		}
	}
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"a", "b", "\uFF5E", "\U0001F600"})
	got, err := notes.Get("\uFF5E", background)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "\uFF5E"))
}

func TestSealedHandlesStillReadSynchronouslyAndFromMemoryChangesAndRetirementsAreRefusedReleased(t *testing.T) {
	bodyDefinition, notesDefinition := handlesDefinitions(t)
	var body *Singleton
	var notes *Keyed
	o, first := activated(t, objectOptions{label: "app.restartable(1)"}, func(a *attempt) {
		body = a.Singleton(bodyDefinition)
		notes = a.Keyed(notesDefinition)
	})
	if _, err := body.Change(handlesSetText("x"), background); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Change("k", handlesSetText("y"), background); err != nil {
		t.Fatal(err)
	}
	first.Fail()
	value, err := slotValue(o, body)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("text", "x"))
	got, err := notes.Get("k", background)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "y"))
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"k"})
	released := obj("code", "released", "reason", "ended", "message", "app.restartable(1): ended")
	noop := func(*delta.Object) (any, error) { return nil, nil }
	_, err = body.Change(noop, background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, body.Retire(background)), released)
	_, err = notes.Change("k", noop, background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, notes.Retire("k", background)), released)
}
