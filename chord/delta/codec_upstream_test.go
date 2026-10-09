package delta

import (
	"testing"
)

// AssertValidWireOp accepts the wire grammar's ids and short forms and rejects everything else (delta/index.ts:211-276).
func TestAssertValidWireOpAcceptsTheWireGrammarOnly(t *testing.T) {
	for _, op := range []WireOp{{"s", 1.0}, {"s", []any{"a"}, 1}, {"#", 0.0, []any{"value"}}, {"r", 1}, {"d"}, {"d", 0.0}, {"t", []any{"a"}, 2.0}, {"p", 0.0, 1.0, 0.0, []any{}}} {
		if err := AssertValidWireOp(op); err != nil {
			t.Errorf("AssertValidWireOp(%v) = %v, want nil", op, err)
		}
	}
	for name, op := range map[string]WireOp{
		"empty": {}, "unknown verb": {"z", 1}, "r arity": {"r"}, "string path": {"s", "x", 1}, "negative id": {"s", -1.0, 1},
		"fractional id": {"s", 1.5, 1}, "append non-string": {"a", []any{"a"}, 3}, "truncate negative": {"t", []any{"a"}, -1.0},
		"splice items": {"p", 0.0, 1.0, 0.0, "x"}, "define non-array": {"#", 0.0, "x"}, "define unsafe": {"#", 0.0, []any{"constructor"}},
	} {
		if err := AssertValidWireOp(op); err == nil {
			t.Errorf("%s: AssertValidWireOp(%v) = nil, want an error", name, op)
		}
	}
}
