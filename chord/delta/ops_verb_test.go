package delta

import (
	"errors"
	"testing"
)

// assertValidOp's default branch throws TypeError(`unknown op verb: ${String(op[0])}`)
// (packages/chord/src/delta/index.ts:189).
func TestAssertValidOpNamesAnUnknownVerbAsStringDoes(t *testing.T) {
	for _, test := range []struct {
		verb any
		want string
	}{
		{"unknown", "unknown op verb: unknown"},
		{"", "unknown op verb: "},
		{nil, "unknown op verb: null"},
		{float64(7), "unknown op verb: 7"},
		{1.5, "unknown op verb: 1.5"},
		{true, "unknown op verb: true"},
		{[]any{"s", nil, float64(1)}, "unknown op verb: s,,1"},
		{map[string]any{"verb": "s"}, "unknown op verb: [object Object]"},
	} {
		err := AssertValidOp(Op{test.verb, []any{"path"}})
		if !errors.Is(err, ErrInvalidOp) {
			t.Fatalf("AssertValidOp(%#v) = %v, want ErrInvalidOp", test.verb, err)
		}
		if got, want := err.Error(), ErrInvalidOp.Error()+": "+test.want; got != want {
			t.Errorf("AssertValidOp(%#v) = %q, want %q", test.verb, got, want)
		}
	}
}
