package delta

import "testing"

// Pi delta/pending.ts:9 `PendingTake = { kind: "ops", ops } | { kind: "replace" }`: the take is discriminated by `kind`.
func TestPendingTakeIsDiscriminatedByKind(t *testing.T) {
	var queue PendingOps
	queue.Push([]Op{{"s", []any{"n"}, float64(1)}})
	ops := queue.Take()
	if ops.Kind != PendingTakeOps || len(ops.Ops) != 1 {
		t.Errorf("take of one op = kind %q with %d ops, want %q with 1", ops.Kind, len(ops.Ops), PendingTakeOps)
	}
	if empty := queue.Take(); empty.Kind != PendingTakeOps || len(empty.Ops) != 0 {
		t.Errorf("take of nothing = kind %q with %d ops", empty.Kind, len(empty.Ops))
	}
	queue.Push(distinctPendingSets(MaxPendingOps/2, 0))
	queue.Push(distinctPendingSets(MaxPendingOps/2+1, MaxPendingOps/2))
	if replace := queue.Take(); replace.Kind != PendingTakeReplace || replace.Ops != nil {
		t.Errorf("overflow take = kind %q with %d ops, want %q with none", replace.Kind, len(replace.Ops), PendingTakeReplace)
	}
}
