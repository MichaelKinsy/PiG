package delta

import (
	"errors"
	"testing"
)

// upstream: packages/chord/src/delta/revision-validator.ts:26-33 validates object properties in Reflect.ownKeys order and throws the first failing property's TypeError, so a state with several invalid properties always reports the same one. A Go map has no insertion order; Prepare reports the first failing property in ascending key order, in chordjson.OwnKeys order. Before this order was fixed, the reported error followed Go's random map iteration.
func TestPrepareReportsTheFirstInvalidPropertyDeterministically(t *testing.T) {
	type namedValue struct{ A int }
	initial := parse(t, `{"a":0,"b":null}`)
	tracker := trackCopy(initial)
	for range 200 {
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("a", nan())
		state.Set("b", namedValue{1})
		state.Set("c", []any{map[string]any{"y": make(chan int), "x": inf()}})
		_, err := change.Prepare()
		if typeErr := (*TypeError)(nil); err == nil || !errors.As(err, &typeErr) || err.Error() != "Replicated state values must be strict JSON" {
			t.Fatalf("Prepare = %v, want the strict JSON error of key a", err)
		}
	}
	nested := parse(t, `{"outer":[{}]}`)
	tracker = trackCopy(nested)
	for range 200 {
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("outer", []any{map[string]any{"y": namedValue{2}, "x": inf()}})
		_, err := change.Prepare()
		if err == nil || err.Error() != "Replicated state values must be strict JSON" {
			t.Fatalf("nested Prepare = %v, want the strict JSON error of key x", err)
		}
	}
}
