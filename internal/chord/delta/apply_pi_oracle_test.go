package delta

import (
	"errors"
	"testing"
)

// Expected results were taken from Pi 1.0.0 packages/chord/src/delta/index.ts (apply, applyImmutable) run under Node 24 on the same targets and operations.

// index.ts resolveValue rejects a string segment on an array with UnsafePathError before its Object.hasOwn check, so the mutable applier reports UnsafePathError even for a missing name. applyImmutableBatches walks the same path with copyContainers first, which checks Object.hasOwn first and reports PathError.
func TestApplyResolvesArraySegmentsInUpstreamOrder(t *testing.T) {
	for _, tc := range []struct {
		name, target, ops     string
		mutable, unsafe, path string
	}{
		{name: "set below an array", target: `[{"x":1}]`, ops: `[["s",["x","y"],1]]`, mutable: "x", path: `["x"]`},
		{name: "splice of an array name", target: `[{"x":1}]`, ops: `[["p",["x"],0,0,[]]]`, mutable: "x", path: `["x"]`},
		{name: "own length name", target: `{"a":[1]}`, ops: `[["t",["a","length","q"],1]]`, mutable: "length", unsafe: "length"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Apply(parse(t, tc.target), ops(t, tc.ops))
			var unsafe *UnsafePathError
			if !errors.As(err, &unsafe) || unsafe.Segment != tc.mutable {
				t.Fatalf("Apply error = %v, want unsafe path segment: %s", err, tc.mutable)
			}
			_, err = ApplyImmutable(parse(t, tc.target), ops(t, tc.ops))
			if tc.unsafe != "" {
				if !errors.As(err, &unsafe) || unsafe.Segment != tc.unsafe {
					t.Fatalf("ApplyImmutable error = %v, want unsafe path segment: %s", err, tc.unsafe)
				}
				return
			}
			var pathErr *PathError
			if !errors.As(err, &pathErr) || jsonText(pathErr.Path) != tc.path {
				t.Fatalf("ApplyImmutable error = %v, want unresolvable path: %s", err, tc.path)
			}
		})
	}
}

// "t" counts UTF-16 code units. Ending inside a surrogate pair leaves Pi a lone low surrogate ("\udc00a" for ["t",["a"],1] on "😀a"), which JSON carries as an escape and encoding/json decodes as U+FFFD; the Go result is that decoded value.
func TestTruncateInsideASurrogatePairKeepsTheLowHalf(t *testing.T) {
	for _, apply := range []func(JsonValue, []Op) (JsonValue, error){Apply, ApplyImmutable} {
		got, err := apply(parse(t, `{"a":"😀a","b":"x😀"}`), ops(t, `[["t",["a"],1],["t",["b"],2]]`))
		if err != nil {
			t.Fatal(err)
		}
		expectJSON(t, got, `{"a":"\ufffda","b":"\ufffd"}`)
	}
}
