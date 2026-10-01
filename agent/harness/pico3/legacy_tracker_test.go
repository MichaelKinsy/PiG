package pico3

import (
	"reflect"
	"testing"
)

// Source: packages/agent/test/harness/pico3/legacy-tracker.test.ts (v0.99.1),
// "preserves draft-sourced descendant edits without exposing tracker metadata".
//
// Upstream assigns one draft into another location and Chord copies it, so the
// later edit of `rows[1]` cannot reach `rows[0]`. Go maps alias on assignment
// and Track owns a plain tree, so this port copies at the two assignments
// (cloneJSON, as every Pico3 document write does). The Tracker contract under
// test is the same: the published operations reproduce the tracked value, and
// the published tree shares no container with itself.
func TestTrackerPreservesDraftSourcedDescendantEditsWithoutExposingTrackerMetadata(t *testing.T) {
	tracker := Track(JsonObject{
		"a": JsonObject{"child": JsonObject{"value": float64(1)}},
		"b": nil,
		"rows": []any{
			JsonObject{"child": JsonObject{"value": float64(1)}},
			JsonObject{"child": JsonObject{"value": float64(2)}},
		},
	})
	replica, err := ApplyImmutable(nil, tracker.Flush())
	check(t, err)

	state := tracker.State()
	state["a"].(JsonObject)["child"].(JsonObject)["value"] = float64(2)
	state["b"] = cloneJSON(state["a"])
	rows := state["rows"].([]any)
	rows[1] = cloneJSON(rows[0])
	rows[1].(JsonObject)["child"].(JsonObject)["value"] = float64(3)
	operations := tracker.Flush()
	replica, err = ApplyImmutable(replica, operations)
	check(t, err)

	want := map[string]any{
		"a": map[string]any{"child": map[string]any{"value": float64(2)}},
		"b": map[string]any{"child": map[string]any{"value": float64(2)}},
		"rows": []any{
			map[string]any{"child": map[string]any{"value": float64(1)}},
			map[string]any{"child": map[string]any{"value": float64(3)}},
		},
	}
	if !jsonEqual(replica, want) || !jsonEqual(replica, JsonValue(map[string]any(tracker.State()))) {
		t.Fatalf("replica = %v, want %v and the tracked state %v", replica, want, tracker.State())
	}
	published := replica.(map[string]any)
	a, b := published["a"].(map[string]any), published["b"].(map[string]any)
	publishedRows := published["rows"].([]any)
	first, second := publishedRows[0].(map[string]any), publishedRows[1].(map[string]any)
	pairs := [][2]any{{a, b}, {a["child"], b["child"]}, {first, second}, {first["child"], second["child"]}}
	for index, pair := range pairs {
		if reflect.ValueOf(pair[0]).Pointer() == reflect.ValueOf(pair[1]).Pointer() {
			t.Fatalf("published containers %d alias each other", index)
		}
	}
}

// Go regression guard for the same upstream test: a mutation of the tracked
// state after Flush must not reach the value the published operations built.
func TestTrackerPublishedValueIsIndependentOfLaterStateEdits(t *testing.T) {
	tracker := Track(JsonObject{"list": []any{JsonObject{"n": float64(1)}}})
	replica, err := ApplyImmutable(nil, tracker.Flush())
	check(t, err)
	tracker.State()["list"].([]any)[0].(JsonObject)["n"] = float64(9)
	if !jsonEqual(replica, map[string]any{"list": []any{map[string]any{"n": float64(1)}}}) {
		t.Fatalf("replica changed with the tracked state: %v", replica)
	}
}
