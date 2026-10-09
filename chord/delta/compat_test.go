package delta

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// The 0.4.1 API stays usable: Op is a plain []any, ErrInvalidOp matches every malformed operation, and the slice and Op forms of ApplyImmutableBatches and AssertValidOp behave as the Pi-shaped forms do.
//
// mutation-checked: dropping TypeError.Is fails it.
func TestTheReleasedAPIKeepsWorking(t *testing.T) {
	var boxed any = Op{"s", []any{"a"}, 1.0}
	if _, ok := boxed.([]any); !ok {
		t.Fatal("an Op boxed in an interface is not a []any")
	}
	plain := func(batch [][]any) int { return len(batch) }
	if plain([]Op{{"r", 1.0}}) != 1 {
		t.Fatal("a []Op is not a [][]any")
	}

	for _, invalid := range []Op{{"x", []any{"a"}}, {"s", []any{}, 1.0}, {"m", []any{}, []any{0.0, 0.0}}} {
		err := AssertValidOp(invalid)
		if !errors.Is(err, ErrInvalidOp) {
			t.Errorf("AssertValidOp(%v) = %v, not ErrInvalidOp", invalid, err)
		}
		if other := AssertValidOpValue(invalid); other == nil || other.Error() != err.Error() {
			t.Errorf("AssertValidOpValue(%v) = %v, AssertValidOp = %v", invalid, other, err)
		}
		var typed *TypeError
		if !errors.As(err, &typed) {
			t.Errorf("AssertValidOp(%v) = %T, want *TypeError", invalid, err)
		}
		if _, err := ApplyImmutable(JsonObjectOf("a", 1.0), []Op{invalid}); !errors.Is(err, ErrInvalidOp) {
			t.Errorf("ApplyImmutable(%v) = %v, not ErrInvalidOp", invalid, err)
		}
	}
	if err := AssertValidOp(Op{"s", []any{"a"}, 1.0}); err != nil {
		t.Fatal(err)
	}
	// Pi throws UnsafePathError and PathError for these, as 0.4.1 did; they are not malformed operations.
	var unsafe *UnsafePathError
	if err := AssertValidOp(Op{"s", []any{"__proto__"}, 1.0}); !errors.As(err, &unsafe) || errors.Is(err, ErrInvalidOp) {
		t.Errorf("AssertValidOp(__proto__) = %v, want an UnsafePathError", err)
	}
	if _, err := ApplyImmutable(JsonObjectOf("a", 1.0), []Op{{"s", []any{"b", "c"}, 1.0}}); errors.Is(err, ErrInvalidOp) {
		t.Errorf("an unresolvable path matched ErrInvalidOp: %v", err)
	}

	batches := [][]Op{{{"s", []any{"a"}, 1.0}}, {{"s", []any{"b"}, 2.0}}}
	viaSlice, err := ApplyImmutableBatches(NewJsonObject(0), batches)
	if err != nil {
		t.Fatal(err)
	}
	viaSeq, err := ApplyImmutableBatchesSeq(NewJsonObject(0), slices.Values(batches))
	if err != nil {
		t.Fatal(err)
	}
	expectJSONText(t, viaSlice, `{"a":1,"b":2}`)
	expectJSONText(t, viaSeq, `{"a":1,"b":2}`)
}

// Ops decodes the objects its operations carry in their JSON key order, as Pi's JSON.parse does; a []Op decoded by encoding/json would hold Go maps.
//
// mutation-checked: decoding Ops with encoding/json fails it.
func TestOpsDecodeKeepsObjectKeyOrder(t *testing.T) {
	var ops Ops
	if err := json.Unmarshal([]byte(`[["s",["rows",0],{"z":1,"a":{"y":2,"b":3}}],["r",{"b":1,"a":2}]]`), &ops); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `[["s",["rows",0],{"z":1,"a":{"y":2,"b":3}}],["r",{"b":1,"a":2}]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	var plain []Op = ops
	if Verb(plain[0]) != "s" || len(PathOf(plain[0])) != 2 || PathOf(plain[1]) != nil {
		t.Fatalf("Verb/PathOf: %v", plain)
	}
	if err := json.Unmarshal([]byte(`[1]`), &ops); err == nil {
		t.Fatal("a non-tuple decoded")
	}
}

// An "m" permutation a Go caller builds as []int, Pi's number[], validates and applies as the decoded []any form does; 0.4.1 accepted it.
//
// mutation-checked: reading the permutation only as []any fails it.
func TestAnIntPermutationStillApplies(t *testing.T) {
	op := Op{"m", []any{}, []int{2, 0, 1}}
	if err := AssertValidOp(op); err != nil {
		t.Fatalf("AssertValidOp([]int permutation) = %v", err)
	}
	if err := AssertValidOp(Op{"m", []any{}, []int{0, 0}}); err == nil || err.Error() != "m permutation is not a bijection" {
		t.Fatalf("AssertValidOp([]int{0, 0}) = %v, want the bijection TypeError", err)
	}
	for name, apply := range map[string]func() (JsonValue, error){
		"ApplyImmutable": func() (JsonValue, error) { return ApplyImmutable([]any{"a", "b", "c"}, []Op{op}) },
		"Apply":          func() (JsonValue, error) { return Apply([]any{"a", "b", "c"}, []Op{op}) },
	} {
		got, err := apply()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		expectJSONText(t, got, `["c","a","b"]`)
	}
}

// PathOf of a replacement is nil even when the replacement value is an array, which would otherwise read as a path.
func TestPathOfAReplacementIsNil(t *testing.T) {
	if path := PathOf(Op{"r", []any{"a"}}); path != nil {
		t.Fatalf("PathOf([\"r\", [\"a\"]]) = %v, want nil", path)
	}
	if path := PathOf(Op{"d", []any{"a"}}); len(path) != 1 || path[0] != "a" {
		t.Fatalf("PathOf([\"d\", [\"a\"]]) = %v", path)
	}
}
