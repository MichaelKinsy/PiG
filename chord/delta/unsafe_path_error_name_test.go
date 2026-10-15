package delta

import (
	"errors"
	"testing"
)

// namedError is what a caller that records error.name reads (telemetry memory.ts:81 reads the thrown error's name).
type namedError interface{ Name() string }

// delta/index.ts:133-140: UnsafePathError sets this.name = "UnsafePathError", so the error ApplyImmutable raises for a path that reaches the prototype chain (delta.test.ts:211,
// ["s", ["constructor", "prototype", "x"], true]) and for a string segment on an array (delta-apply-immutable.test.ts:230) reports that name, as Pi's thrown error does.
func TestUnsafePathErrorNameIsUnsafePathError(t *testing.T) {
	for _, c := range []struct {
		label string
		base  any
		op    Op
	}{
		{"reserved segment", JsonObjectOf(), Op{"s", []any{"constructor", "prototype", "x"}, true}},
		{"string index on an array", JsonObjectOf("values", []any{JsonObjectOf()}), Op{"s", []any{"values", "0", "value"}, float64(1)}},
	} {
		t.Run(c.label, func(t *testing.T) {
			_, err := ApplyImmutable(c.base, []Op{c.op})
			unsafe, ok := errors.AsType[*UnsafePathError](err)
			if !ok {
				t.Fatalf("err = %v, want *UnsafePathError", err)
			}
			named, ok := error(unsafe).(namedError)
			if !ok || named.Name() != "UnsafePathError" {
				t.Fatalf("name = %v, want UnsafePathError", named)
			}
		})
	}
	if got := (&UnsafePathError{Segment: "__proto__"}).Name(); got != "UnsafePathError" {
		t.Fatalf("Name() = %q", got)
	}
}

// delta-apply-immutable.test.ts:229-230: on an array, a string segment that is not an own key ("missing") is a PathError, while one that is ("0", and "length") is an UnsafePathError
// (index.ts:464 checks Object.hasOwn first, then typeof segment !== "number").
func TestStringSegmentOnAnArrayIsUnsafeOnlyWhenOwn(t *testing.T) {
	for _, c := range []struct {
		segment string
		unsafe  bool
	}{{"missing", false}, {"0", true}, {"length", true}, {"1", false}, {"00", false}, {"", false}, {"-1", false}} {
		_, err := ApplyImmutable(JsonObjectOf("values", []any{JsonObjectOf()}), []Op{{"s", []any{"values", c.segment, "value"}, float64(1)}})
		_, isUnsafe := errors.AsType[*UnsafePathError](err)
		_, isPath := errors.AsType[*PathError](err)
		if isUnsafe != c.unsafe || isPath == c.unsafe {
			t.Errorf("segment %q: err = %v, want unsafe=%v", c.segment, err, c.unsafe)
		}
	}
}
