package delta

import "testing"

// A numeric path segment on an object is that object's key, because JavaScript converts a property key to a string: Pi 1.1.0 applyImmutable({a:1}, [["s",[1],2]]) returns {"1":2,"a":1}, and [["d",[2]]] on {} returns {} (delta/index.ts applyImmutable, run under Node 24 against .upstream/current/packages/chord/src/delta/index.ts).
//
// mutation-checked: resolving a numeric segment on an object as unresolvable fails it.
func TestApplyImmutableSetsANumericSegmentOnAnObjectAsAKey(t *testing.T) {
	for _, tc := range []struct{ base, ops, want string }{
		{`{"a":1}`, `[["s",[1],2]]`, `{"1":2,"a":1}`},
		{`{}`, `[["d",[2]]]`, `{}`},
		{`[1,{"a":[1,2]}]`, `[["s",[1,1],"x"]]`, `[1,{"1":"x","a":[1,2]}]`},
	} {
		var ops Ops
		if err := ops.UnmarshalJSON([]byte(tc.ops)); err != nil {
			t.Fatal(err)
		}
		got, err := ApplyImmutable(jsonOf(t, tc.base), ops)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.base, tc.ops, err)
		}
		expectJSONText(t, got, tc.want)
	}
}
