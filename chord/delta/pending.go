package delta

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The pending queue: the ops a slow consumer of one state has not taken yet, folded while they wait. Ports packages/chord/src/delta/pending.ts.

// MaxPendingOps is the number of pending ops past which only "replace with the current value" stays pending.
const MaxPendingOps = 10_000

// PendingTake is what PendingOps.Take hands over: the pending ops, folded, or Replace after an overflow, which the owner of the queue turns into a root replacement with the current value.
type PendingTake struct {
	Replace bool
	Ops     []Op
}

type pendingKind uint8

const (
	pendingEmpty pendingKind = iota
	// pendingBatch is one batch, kept by reference: a consumer that keeps up never pays for folding.
	pendingBatch
	pendingFolding
	// pendingReplace is the overflow: nothing more accumulates until Take or a batch that starts with "r".
	pendingReplace
)

// PendingOps holds the pending ops of one state for one consumer, in order and raw (never encoded while they wait). "r" drops everything before it. A leaf op (an append, or a set of a primitive) merges into the last pending leaf op on the same path ("a" and "a" join, "s" replaces "s" or "a", "a" after a string "s" appends into it); any other op clears the leaf index. Leaf ops on different paths commute, except after an ancestor primitive "s", after which the descendant cannot be written until a structural op clears the index. Past MaxPendingOps only "replace with the current value" stays pending.
//
// Ops are only those of Delta's tracker, DiffRevisions and AppendText; received op slices and tuples are never mutated (they feed other consumers too), a merge makes a new tuple. The zero value is an empty queue; a queue is not safe for concurrent use.
type PendingOps struct {
	kind pendingKind
	// ops is the one batch, or the folded ops, which the queue owns until Take hands them over and which are never one of the received slices.
	ops []Op
	// leaves is the path key of the last pending leaf op on each path, to its position in ops.
	leaves map[string]int
}

// Push queues the ops of one commit.
func (pending *PendingOps) Push(ops []Op) {
	if len(ops) == 0 {
		return
	}
	// A batch that starts with "r" (the tracker's and DiffRevisions's only place for it) drops everything pending, also an overflow, and is kept as received.
	if ops[0][0] == "r" {
		pending.kind, pending.ops, pending.leaves = pendingBatch, ops, nil
		return
	}
	switch pending.kind {
	case pendingEmpty:
		pending.kind, pending.ops = pendingBatch, ops
	case pendingBatch:
		received := pending.ops
		pending.kind, pending.ops, pending.leaves = pendingFolding, nil, map[string]int{}
		if pending.fold(received) {
			pending.fold(ops)
		}
	case pendingFolding:
		pending.fold(ops)
	case pendingReplace:
	}
}

// Size is the number of pending ops after folding; 1 once overflowed (the replacement the owner sends). One batch is counted as received, so a single commit larger than MaxPendingOps counts in full until a second one arrives and overflows.
func (pending *PendingOps) Size() int {
	switch pending.kind {
	case pendingEmpty:
		return 0
	case pendingReplace:
		return 1
	}
	return len(pending.ops)
}

// Take hands over everything pending and starts empty.
func (pending *PendingOps) Take() PendingTake {
	kind, ops := pending.kind, pending.ops
	pending.kind, pending.ops, pending.leaves = pendingEmpty, nil, nil
	if kind == pendingReplace {
		return PendingTake{Replace: true}
	}
	return PendingTake{Ops: ops}
}

// fold folds ops into the queue; false once it overflowed into replace.
func (pending *PendingOps) fold(ops []Op) bool {
	for _, op := range ops {
		switch Verb(op) {
		case "r":
			pending.ops = pending.ops[:0]
			clear(pending.leaves)
		case "a":
			if pending.mergeLeaf(op) {
				continue
			}
		case "s":
			if isPrimitiveValue(op[2]) {
				if pending.mergeLeaf(op) {
					continue
				}
			} else if len(pending.leaves) > 0 {
				clear(pending.leaves)
			}
		default:
			if len(pending.leaves) > 0 {
				clear(pending.leaves)
			}
		}
		pending.ops = append(pending.ops, op)
		if len(pending.ops) > MaxPendingOps {
			pending.kind, pending.ops, pending.leaves = pendingReplace, nil, nil
			return false
		}
	}
	return true
}

func isPrimitiveValue(value any) bool {
	switch value.(type) {
	case *JsonObject, []any, map[string]any:
		return false
	}
	return true
}

// mergeLeaf merges the leaf op op into the last pending leaf op on its path, or indexes it as that op at the position it is about to be pushed to. It reports whether it merged.
func (pending *PendingOps) mergeLeaf(op Op) bool {
	key := pendingPathKey(op[1])
	if at, indexed := pending.leaves[key]; indexed {
		// The index points at leaf ops only.
		if merged, ok := mergeLeafOps(pending.ops[at], op); ok {
			pending.ops[at] = merged
			return true
		}
	}
	pending.leaves[key] = len(pending.ops)
	return false
}

// pendingPathKey is the key of a path in the leaf index. It is injective, as merging two distinct paths would lose a write: ["a.b"] and ["a", "b"] are different keys.
func pendingPathKey(path any) string {
	var key strings.Builder
	segments, _ := path.([]any)
	for _, segment := range segments {
		switch segment := segment.(type) {
		case string:
			key.WriteString("s")
			key.WriteString(strconv.Itoa(len(segment)))
			key.WriteByte(':')
			key.WriteString(segment)
		case int:
			key.WriteString("i")
			key.WriteString(strconv.Itoa(segment))
			key.WriteByte(';')
		case float64:
			key.WriteString("i")
			key.WriteString(strconv.FormatFloat(segment, 'g', -1, 64))
			key.WriteByte(';')
		}
	}
	return key.String()
}

// mergeLeafOps is next folded into previous (same path), or false when they do not fold.
func mergeLeafOps(previous, next Op) (Op, bool) {
	if Verb(next) == "s" {
		return next, true
	}
	appended, _ := next[2].(string)
	if Verb(previous) == "a" {
		return Op{"a", previous[1], jsstring.Concat(previous[2].(string), appended)}, true
	}
	// Only a string set takes an append; the tracker never appends to another primitive.
	if before, isString := previous[2].(string); isString {
		return Op{"s", previous[1], jsstring.Concat(before, appended)}, true
	}
	return nil, false
}
