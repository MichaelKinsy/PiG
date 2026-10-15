package delta

import (
	"errors"
	"testing"
)

// upstream: packages/chord/src/delta/revision-validator.ts:26-33 validates object properties in Reflect.ownKeys order and throws the first failing property's TypeError, so a state with several invalid properties always reports the same one. A Go map has no insertion order; DiffCandidate reports the first failing property in ascending key order, in chordjson.OwnKeys order. Before this order was fixed, the reported error followed Go's random map iteration.
func TestDiffCandidateReportsTheFirstInvalidPropertyDeterministically(t *testing.T) {
	type namedValue struct{ A int }
	initial := parse(t, `{"a":0,"b":null}`)
	for range 200 {
		state := obj(t, clone(t, initial))
		state.Set("a", nan())
		state.Set("b", namedValue{1})
		state.Set("c", []any{map[string]any{"y": make(chan int), "x": inf()}})
		_, _, err := DiffCandidate(initial, state)
		if typeErr := (*TypeError)(nil); err == nil || !errors.As(err, &typeErr) || err.Error() != "Replicated state values must be strict JSON" {
			t.Fatalf("DiffCandidate = %v, want the strict JSON error of key a", err)
		}
	}
	nested := parse(t, `{"outer":[{}]}`)
	for range 200 {
		state := obj(t, clone(t, nested))
		state.Set("outer", []any{map[string]any{"y": namedValue{2}, "x": inf()}})
		_, _, err := DiffCandidate(nested, state)
		if err == nil || err.Error() != "Replicated state values must be strict JSON" {
			t.Fatalf("nested DiffCandidate = %v, want the strict JSON error of key x", err)
		}
	}
}
