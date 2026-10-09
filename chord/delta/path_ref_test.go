package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

// NonEmptyPath is upstream packages/chord/src/delta/index.ts:16 (`readonly [Seg, ...Seg[]]`), the path of the "s", "d", "a" and
// "t" operations (index.ts:34-37). PathRef is index.ts:19 (`P | number`), the wire form of a path: inline, or the id the encoder
// assigned (index.ts:578 `let ref: PathRef = path`). The installed Pi rejects an empty NonEmptyPath at run time: assertValidOp throws
// TypeError "path is empty" for those four verbs, the decoder throws PathError "unresolvable path: []", and both accept an empty
// root path for "p" and "m". The encoder turns a path's second use into a ["#", id, path] definition plus a numeric PathRef.
func TestNonEmptyPathAndPathRefMatchPi(t *testing.T) {
	root := NonEmptyPath{}
	for _, op := range []Op{{"s", root, 1.0}, {"d", root}, {"a", root, "x"}, {"t", root, 1.0}} {
		var typed *TypeError
		if err := AssertValidOpValue(op); !errors.As(err, &typed) || typed.Message != "path is empty" {
			t.Errorf("AssertValidOpValue(%v) = %v, want TypeError path is empty", op, err)
		}
		var pathError *PathError
		if _, err := NewDecoder().Decode([]WireOp{{"r", 1.0}, WireOp(op)}); !errors.As(err, &pathError) || err.Error() != "unresolvable path: []" {
			t.Errorf("Decode(%v) = %v, want PathError unresolvable path: []", op, err)
		}
	}
	for _, op := range []Op{{"p", Path{}, 0.0, 0.0, []any{}}, {"m", Path{}, []any{}}} {
		if err := AssertValidOpValue(op); err != nil {
			t.Errorf("AssertValidOpValue(%v) = %v, want a root path accepted", op, err)
		}
		if _, err := NewDecoder().Decode([]WireOp{{"r", 1.0}, WireOp(op)}); err != nil {
			t.Errorf("Decode(%v) = %v, want a root path accepted", op, err)
		}
	}

	a, b := NonEmptyPath{"a", 0}, NonEmptyPath{"b"}
	wire, err := NewEncoder().Encode([]Op{{"s", a, 1}, {"a", b, "x"}, {"s", a, 2}, {"d", b}, {"s", a, 3}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[["s",["a",0],1],["a",["b"],"x"],["#",0,["a",0]],["s",0,2],["#",1,["b"]],["d",1],["s",0,3]]`; string(encoded) != want {
		t.Fatalf("Encode = %s, want Pi's %s", encoded, want)
	}
	if id, ok := wire[3][1].(int); !ok || id != 0 {
		t.Fatalf("second use PathRef = %#v, want the numeric id 0", wire[3][1])
	}
}
