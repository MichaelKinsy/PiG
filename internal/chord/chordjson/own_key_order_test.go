package chordjson

import (
	"math"
	"testing"
)

// upstream: packages/chord/src/json.ts:20-60. copy walks Reflect.ownKeys, so array-index keys come first in numeric order, and a
// value that is not an object fails with `non-JSON ${typeof value}`. Measured on the installed chord 1.1.0 dist:
//
//	copyJson({"10": NaN, "2": () => 1})  throws "Value contains a non-JSON function; expected strict JSON"
//	copyJson({"10": () => 1, "2": NaN})  throws "Value contains a non-finite number and is not strict JSON"
//	copyJson({d: new Date()})            throws "Value must contain strict JSON plain objects or arrays"
//
// A Go func is a JavaScript function. A Go struct, pointer, typed map or slice, array, or channel stands where Pi sees an object
// that is not a plain object or array.
func TestCopyFollowsPisKeyOrderAndMessages(t *testing.T) {
	type namedValue struct{ A int }
	for _, tc := range []struct {
		name  string
		value map[string]any
		want  string
	}{
		{"index 2 before 10", map[string]any{"10": math.NaN(), "2": func() {}}, "Value contains a non-JSON function; expected strict JSON"},
		{"index 2 before 10 reversed", map[string]any{"10": func() {}, "2": math.NaN()}, "Value contains a non-finite number and is not strict JSON"},
		{"index key before a name", map[string]any{"a": math.NaN(), "7": namedValue{1}}, "Value must contain strict JSON plain objects or arrays"},
		{"struct", map[string]any{"d": namedValue{1}}, "Value must contain strict JSON plain objects or arrays"},
		{"pointer", map[string]any{"d": &namedValue{1}}, "Value must contain strict JSON plain objects or arrays"},
		{"typed slice", map[string]any{"d": []string{"x"}}, "Value must contain strict JSON plain objects or arrays"},
		{"typed map", map[string]any{"d": map[string]int{"x": 1}}, "Value must contain strict JSON plain objects or arrays"},
		{"function", map[string]any{"f": func() {}}, "Value contains a non-JSON function; expected strict JSON"},
	} {
		for range 50 {
			if _, err := Copy(tc.value); err == nil || err.Error() != tc.want {
				t.Fatalf("%s: Copy = %v, want %q", tc.name, err, tc.want)
			}
		}
	}
}

// upstream: packages/chord/src/json.ts:52 copies in Reflect.ownKeys order, which keeps insertion order for keys that are not array
// indexes. Measured on the installed chord 1.1.0 dist: copyJson({b: NaN, a: () => 1}) throws the non-finite error of b, and
// copyJson({a: () => 1, b: NaN}) the function error of a.
func TestCopyReportsTheFirstInvalidPropertyInInsertionOrder(t *testing.T) {
	for _, tc := range []struct {
		object *Object
		want   string
	}{
		{ObjectOf("b", math.NaN(), "a", func() {}), "Value contains a non-finite number and is not strict JSON"},
		{ObjectOf("a", func() {}, "b", math.NaN()), "Value contains a non-JSON function; expected strict JSON"},
		{ObjectOf("b", math.NaN(), "a", func() {}, "3", struct{}{}), "Value must contain strict JSON plain objects or arrays"},
	} {
		if _, err := Copy(tc.object); err == nil || err.Error() != tc.want {
			t.Fatalf("Copy(%v) = %v, want %q", tc.object.Keys(), err, tc.want)
		}
	}
}
