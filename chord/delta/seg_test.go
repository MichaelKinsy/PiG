package delta

import (
	"errors"
	"math"
	"testing"
)

// delta/index.ts:14 `type Seg = string | number` and :317-325 assertSafePath: a string segment is an object key unless it is one of RESERVED_SEGMENTS, and a number segment must be a non-negative integer (`!Number.isInteger(seg) || seg < 0` throws UnsafePathError(seg)). Any other value is not a Seg: Number.isInteger(true) and Number.isInteger(null) are false, so it throws too. The Go Seg is any, so the gate is the only place its domain is enforced.
//
// mutation-checked: accepting a negative integer, accepting a non-integer number, or accepting a reserved key fails it.
func TestSegDomainIsAStringKeyOrANonNegativeInteger(t *testing.T) {
	for _, seg := range []Seg{"a", "", "0", "__proto", 0, 7, int64(3), uint8(2), float64(3), 1e15} {
		if err := AssertSafePath(Path{"root", seg}); err != nil {
			t.Errorf("segment %#v: %v, want accepted", seg, err)
		}
	}
	for _, seg := range []Seg{"__proto__", "constructor", "prototype", -1, int64(-1), 1.5, math.NaN(), math.Inf(1), math.Inf(-1), true, nil, []any{"a"}} {
		err := AssertSafePath(Path{"root", seg})
		unsafe := (*UnsafePathError)(nil)
		if !errors.As(err, &unsafe) {
			t.Errorf("segment %#v: error %v, want *UnsafePathError", seg, err)
			continue
		}
		if f, isNumber := seg.(float64); isNumber && math.IsNaN(f) {
			continue
		}
		if _, isList := seg.([]any); !isList && unsafe.Segment != seg {
			t.Errorf("segment %#v: error carries %#v", seg, unsafe.Segment)
		}
	}
}

// apply (index.ts:243-309) resolves a string Seg as an object key and a number Seg as an array index (a number on an object is its decimal key); a segment outside the Seg domain is refused before the value is touched.
func TestApplyResolvesEachSegKindAndRefusesTheRest(t *testing.T) {
	for _, tc := range []struct {
		base string
		path []any
		want string
	}{
		{`{"xs":[0,1]}`, []any{"xs", 1}, `{"xs":[0,"y"]}`},
		{`{"xs":[0,1]}`, []any{"xs", 2}, `{"xs":[0,1,"y"]}`},
		{`{"a":{"b":1}}`, []any{"a", "b"}, `{"a":{"b":"y"}}`},
		{`{"a":1}`, []any{""}, `{"a":1,"":"y"}`},
	} {
		got, err := Apply(jsonOf(t, tc.base), []Op{{"s", tc.path, "y"}})
		if err != nil {
			t.Fatalf("%s %v: %v", tc.base, tc.path, err)
		}
		expectJSONText(t, got, tc.want)
	}
	for _, path := range [][]any{{"xs", -1}, {"xs", 1.5}, {"xs", true}, {"__proto__", "x"}} {
		base := jsonOf(t, `{"xs":[0,1]}`)
		_, err := Apply(base, []Op{{"s", path, "y"}})
		unsafe := (*UnsafePathError)(nil)
		if !errors.As(err, &unsafe) {
			t.Fatalf("path %v: error %v, want *UnsafePathError", path, err)
		}
		expectJSONText(t, base, `{"xs":[0,1]}`)
	}
}
