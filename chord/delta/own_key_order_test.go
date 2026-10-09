package delta

import (
	"encoding/json"
	"testing"
)

// upstream: packages/chord/src/delta/diff.ts:426-440 walks Object.keys, so array-index keys come first in numeric order, then the
// other keys in insertion order. Measured on the installed chord 1.1.0 dist:
//
//	diffRevisions({x:0}, {x:0, a:1, "10":2, "2":3, b:4}) = [["s",["2"],3],["s",["10"],2],["s",["a"],1],["s",["b"],4]]
//	diffRevisions({a:1, "10":2, "2":3, b:4}, {})          = [["d",["2"]],["d",["10"]],["d",["a"]],["d",["b"]]]
//	diffRevisions({x:0}, {x:0, z:1, b:2, "7":3, a:4})     = [["s",["7"],3],["s",["z"],1],["s",["b"],2],["s",["a"],4]]
//	diffRevisions({z:1, b:2, a:3}, {})                    = [["d",["z"]],["d",["b"]],["d",["a"]]]
//
// PiG emitted "10" before "2" (byte order) and, once objects were Go maps, the other keys in ascending order instead of
// insertion order.
func TestDiffRevisionsVisitsArrayIndexKeysFirstInNumericOrder(t *testing.T) {
	for _, tc := range []struct{ before, after, want string }{
		{`{"x":0}`, `{"x":0,"a":1,"10":2,"2":3,"b":4}`, `[["s",["2"],3],["s",["10"],2],["s",["a"],1],["s",["b"],4]]`},
		{`{"a":1,"10":2,"2":3,"b":4}`, `{}`, `[["d",["2"]],["d",["10"]],["d",["a"]],["d",["b"]]]`},
		{`{"x":0}`, `{"x":0,"z":1,"b":2,"7":3,"a":4}`, `[["s",["7"],3],["s",["z"],1],["s",["b"],2],["s",["a"],4]]`},
		{`{"z":1,"b":2,"a":3}`, `{}`, `[["d",["z"]],["d",["b"]],["d",["a"]]]`},
	} {
		ops, err := json.Marshal(DiffRevisions(parse(t, tc.before), parse(t, tc.after)))
		if err != nil {
			t.Fatal(err)
		}
		if string(ops) != tc.want {
			t.Fatalf("DiffRevisions(%s, %s) = %s, want %s", tc.before, tc.after, ops, tc.want)
		}
	}
}

// upstream: packages/chord/src/delta/revision-validator.ts:26-33 (Reflect.ownKeys order) and :44-55 (the messages). Measured on
// the installed chord 1.1.0 dist: a function and NaN both fail with "Replicated state values must be strict JSON", and a Date or
// Map with "Replicated state containers must be plain objects or arrays", with no suffix naming the value.
func TestPrepareReportsPisValidatorErrorForTheFirstArrayIndexKey(t *testing.T) {
	type namedValue struct{ A int }
	for _, tc := range []struct {
		name  string
		place map[string]any
		want  string
	}{
		{"index 2 before 10", map[string]any{"10": namedValue{1}, "2": nan()}, "Replicated state values must be strict JSON"},
		{"index 2 before 10 reversed", map[string]any{"10": nan(), "2": namedValue{1}}, "Replicated state containers must be plain objects or arrays"},
		{"index key before a name", map[string]any{"a": namedValue{1}, "7": func() {}}, "Replicated state values must be strict JSON"},
		{"infinity", map[string]any{"n": inf()}, "Replicated state values must be strict JSON"},
	} {
		for range 50 {
			tracker := trackCopy(parse(t, `{}`))
			change := tracker.BeginChange()
			state := obj(t, mustState(t, change))
			for key, value := range tc.place {
				state.Set(key, value)
			}
			_, err := change.Prepare()
			if err == nil || err.Error() != tc.want {
				t.Fatalf("%s: Prepare = %v, want %q", tc.name, err, tc.want)
			}
		}
	}
}
