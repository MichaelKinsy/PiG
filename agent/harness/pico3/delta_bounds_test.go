package pico3

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

// Upstream packages/chord/src/delta/index.ts accepts any non-negative Number.isInteger splice index and count, and Array.prototype.splice clamps both to the array, so an index or count beyond int range must clamp instead of wrapping negative.
func TestSpliceClampsIntegersBeyondIntRange(t *testing.T) {
	for _, test := range []struct {
		name string
		op   Op
		want []any
	}{
		{"index", Op{"p", []any{}, 1e300, 0.0, []any{"x"}}, []any{"a", "b", "x"}},
		{"remove", Op{"p", []any{}, 0.0, 1e300, []any{"x"}}, []any{"x"}},
		{"index plus remove", Op{"p", []any{}, 1.0, float64(math.MaxInt), []any{}}, []any{"a"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Apply([]any{"a", "b"}, []Op{test.op})
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Apply(%v) = %v, %v; want %v", test.op, got, err, test.want)
			}
		})
	}
}

// An array path index beyond int range reads undefined upstream, so it is a PathError, not a wrapped negative index.
func TestPathIndexBeyondIntRangeIsPathError(t *testing.T) {
	root := map[string]any{"items": []any{map[string]any{"v": 1.0}}}
	_, err := Apply(root, []Op{{"s", []any{"items", 1e300, "v"}, 2.0}})
	if _, ok := errors.AsType[*PathError](err); !ok {
		t.Fatalf("err = %v, want PathError", err)
	}
}
