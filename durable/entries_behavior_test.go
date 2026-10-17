package durable_test

// pi: packages/durable/src/entries.ts

// Pins packages/durable/src/entries.ts (defineEntry guard, typed decode, draft and the empty-kind refusal), which the
// storage and session tests reach only through whole transactions. errors.ts is pinned by errors_test.go.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// mutation-checked: zeroing the results of Entry.Is fails it
// mutation-checked: dropping the reads and writes of Entry.Kind fails it
func TestDefineEntryGuardsByKindAndDecodesTypedData(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}
	token := durable.DefineEntry[payload]("test.counter")
	other := durable.DefineEntry[payload]("test.other")
	if token.Kind != "test.counter" {
		t.Fatalf("Kind = %q", token.Kind)
	}
	record := &durable.EntryRecord{Kind: "test.counter", Data: map[string]any{"count": float64(3)}}
	if !token.Is(record) || token.Is(nil) || other.Is(record) {
		t.Fatalf("Is: own %v, nil %v, other kind %v; want true, false, false", token.Is(record), token.Is(nil), other.Is(record))
	}
	typed, err := token.As(record)
	if err != nil || typed == nil || typed.TypedData.Count != 3 {
		t.Fatalf("As = %+v, %v", typed, err)
	}
	if wrong, err := token.As(&durable.EntryRecord{Kind: "test.other"}); wrong != nil || err != nil {
		t.Fatalf("As on another kind = %+v, %v; want nil, nil", wrong, err)
	}
	if absent, err := token.As(nil); absent != nil || err != nil {
		t.Fatalf("As(nil) = %+v, %v", absent, err)
	}
	if _, err := token.As(&durable.EntryRecord{Kind: "test.counter", Data: map[string]any{"count": "three"}}); err == nil {
		t.Fatal("As accepted data that does not decode as the payload type")
	}
	draft, err := token.Draft(durable.TypedEntryDraft[payload]{Data: payload{Count: 5}})
	if err != nil || draft.Kind != "test.counter" || draft.Data == nil {
		t.Fatalf("Draft = %+v, %v", draft, err)
	}
	defer func() {
		// entries.ts:7 throws TypeError("Entry kind must be a non-empty string").
		if recovered := recover(); recovered != "Entry kind must be a non-empty string" {
			t.Errorf("DefineEntry(\"\") panicked with %v, want the entries.ts message", recovered)
		}
	}()
	durable.DefineEntry[payload]("")
}
