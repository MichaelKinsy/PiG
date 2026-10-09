package chord

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Measured with the installed chord 1.1.0 dist (delta/tracker.js): over {nested: {a: 4, b: 1, c: 2}, x: 1}, a change that deletes
// nested.a and sets it again records [["d",["nested","a"]],["s",["nested","a"],4]] and leaves {"nested":{"b":1,"c":2,"a":4},"x":1}.
// A typed Change edits a decoded copy, so only the copy's key order shows the move; an assigned, deeply equal object keeps the base
// order instead (tracker.test.ts:452-465).
func TestChangeRecordsAnInPlaceDeleteAndReAddAsUpstreamDoes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(root *chordjson.Object)
		ops    string
		value  string
	}{
		{"in place", func(root *chordjson.Object) {
			nested := root.Value("nested").(*chordjson.Object)
			value := nested.Value("a")
			nested.Delete("a")
			nested.Set("a", value)
		}, `[["d",["nested","a"]],["s",["nested","a"],4]]`, `{"nested":{"b":1,"c":2,"a":4},"x":1}`},
		{"assigned", func(root *chordjson.Object) {
			root.Set("nested", chordjson.ObjectOf("b", 1.0, "c", 2.0, "a", 4.0))
		}, `null`, `{"nested":{"a":4,"b":1,"c":2},"x":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, typed := range []string{"object", "any"} {
				initial := chordjson.ObjectOf("nested", chordjson.ObjectOf("a", 4.0, "b", 1.0, "c", 2.0), "x", 1.0)
				var batches [][]Op
				var value any
				if typed == "object" {
					state, err := NewReplicatedState(initial)
					if err != nil {
						t.Fatal(err)
					}
					state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, ops) })
					if err := state.Change(t.Context(), func(root *chordjson.Object) error { tc.mutate(root); return nil }); err != nil {
						t.Fatal(err)
					}
					value = state.Value()
				} else {
					state, err := NewReplicatedState[any](initial)
					if err != nil {
						t.Fatal(err)
					}
					state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, ops) })
					if err := state.Change(t.Context(), func(root any) error { tc.mutate(root.(*chordjson.Object)); return nil }); err != nil {
						t.Fatal(err)
					}
					value = state.Value()
				}
				var ops any
				if len(batches) == 1 {
					ops = batches[0]
				} else if len(batches) > 1 {
					t.Fatalf("%s: %d batches", typed, len(batches))
				}
				gotOps, _ := json.Marshal(ops)
				gotValue, _ := json.Marshal(value)
				if string(gotOps) != tc.ops || string(gotValue) != tc.value {
					t.Fatalf("%s: ops %s value %s, want %s %s", typed, gotOps, gotValue, tc.ops, tc.value)
				}
			}
		})
	}
}
