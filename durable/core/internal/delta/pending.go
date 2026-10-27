package delta

import (
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Ports packages/chord/src/delta/pending.ts: the pending queue, the ops of one state that a slow consumer has not taken yet,
// folded while they wait. The Session keeps one per document with deferred commits it has not stored (session/held.go).

// MaxPendingOps is the number of pending ops past which only "replace with the current value" stays pending.
const MaxPendingOps = 10_000

// PendingTake is what PendingOps.Take hands over: the pending ops, folded, or Replace after an overflow, which the owner of
// the queue turns into a root replacement with the current value.
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

// pendingLeaf is the last pending leaf op on one path: its position in ops, and, once an append merged into it, the text it
// has grown to. The text lives in a byte buffer until the ops are handed over, so a long run of appends costs the new text
// each, not the length so far (the upstream strings are ropes).
type pendingLeaf struct {
	at      int
	buf     []byte
	growing bool
}

// PendingOps holds the pending ops of one state for one consumer, in order. "r" drops everything before it. A leaf op (an
// append, or a set of a primitive) merges into the last pending leaf op on the same path ("a" and "a" join, "s" replaces "s"
// or "a", "a" after a string "s" appends into it); any other op clears the leaf index. Leaf ops on different paths commute,
// except after an ancestor primitive "s", after which the descendant cannot be written until a structural op clears the
// index. Past MaxPendingOps only "replace with the current value" stays pending.
//
// Ops are only those of the tracker, DiffRevisions and AppendText; received op slices are never mutated (they feed other
// consumers too), a merge makes a new op. The zero value is an empty queue.
type PendingOps struct {
	kind pendingKind
	// ops is the one batch, or the folded ops, which the queue owns until Take hands them over and which are never one of the
	// received slices.
	ops []Op
	// leaves is the last pending leaf op of each path, by path key.
	leaves map[string]pendingLeaf
}

// Push queues the ops of one commit.
func (pending *PendingOps) Push(ops []Op) {
	if len(ops) == 0 {
		return
	}
	// A batch that starts with "r" (the tracker's and DiffRevisions's only place for it) drops everything pending, also an
	// overflow, and is kept as received.
	if ops[0].Verb == Replace {
		pending.kind, pending.ops, pending.leaves = pendingBatch, ops, nil
		return
	}
	switch pending.kind {
	case pendingEmpty:
		pending.kind, pending.ops = pendingBatch, ops
	case pendingBatch:
		received := pending.ops
		pending.kind, pending.ops, pending.leaves = pendingFolding, nil, map[string]pendingLeaf{}
		if pending.fold(received) {
			pending.fold(ops)
		}
	case pendingFolding:
		pending.fold(ops)
	case pendingReplace:
	}
}

// Size is the number of pending ops after folding; 1 once overflowed (the replacement the owner sends). One batch is counted
// as received, so a single commit larger than MaxPendingOps counts in full until a second one arrives and overflows.
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
	take := pending.Peek(nil)
	pending.kind, pending.ops, pending.leaves = pendingEmpty, nil, nil
	return take
}

// Peek is what Take would hand over after Push(extra), leaving the queue as it is: the Session offers a store the held ops
// and a commit's own before it knows that Storage takes them.
func (pending *PendingOps) Peek(extra []Op) PendingTake {
	if len(extra) == 0 {
		if pending.kind == pendingReplace {
			return PendingTake{Replace: true}
		}
		pending.flush()
		return PendingTake{Ops: pending.ops}
	}
	// Push mutates what it owns: work on a copy.
	pending.flush()
	copied := PendingOps{kind: pending.kind, ops: slices.Clone(pending.ops)}
	if pending.leaves != nil {
		copied.leaves = make(map[string]pendingLeaf, len(pending.leaves))
		for key, leaf := range pending.leaves {
			copied.leaves[key] = pendingLeaf{at: leaf.at}
		}
	}
	copied.Push(extra)
	copied.flush()
	if copied.kind == pendingReplace {
		return PendingTake{Replace: true}
	}
	return PendingTake{Ops: copied.ops}
}

// flush writes the text of every growing leaf into its op.
func (pending *PendingOps) flush() {
	for key, leaf := range pending.leaves {
		if !leaf.growing {
			continue
		}
		leaf.growing = false
		op := pending.ops[leaf.at]
		if op.Verb == Append {
			op.Text = string(leaf.buf)
		} else {
			op.Value = string(leaf.buf)
		}
		pending.ops[leaf.at] = op
		leaf.buf = nil
		pending.leaves[key] = leaf
	}
}

// fold folds ops into the queue; false once it overflowed into replace.
func (pending *PendingOps) fold(ops []Op) bool {
	for _, op := range ops {
		switch op.Verb {
		case Replace:
			pending.ops = pending.ops[:0]
			clear(pending.leaves)
		case Append:
			if pending.mergeLeaf(op) {
				continue
			}
		case Set:
			if isPrimitive(op.Value) {
				if pending.mergeLeaf(op) {
					continue
				}
			} else {
				pending.clearLeaves()
			}
		default:
			pending.clearLeaves()
		}
		pending.ops = append(pending.ops, op)
		if len(pending.ops) > MaxPendingOps {
			pending.kind, pending.ops, pending.leaves = pendingReplace, nil, nil
			return false
		}
	}
	return true
}

// clearLeaves forgets the leaf index, after the text it was growing went into its ops.
func (pending *PendingOps) clearLeaves() {
	if len(pending.leaves) == 0 {
		return
	}
	pending.flush()
	clear(pending.leaves)
}

func isPrimitive(value any) bool {
	switch value.(type) {
	case *jv.Object, []any:
		return false
	}
	return true
}

// mergeLeaf merges the leaf op op into the last pending leaf op on its path, or indexes it as that op at the position it is
// about to be pushed to. It reports whether it merged.
func (pending *PendingOps) mergeLeaf(op Op) bool {
	key := pathKey(op.Path)
	leaf, indexed := pending.leaves[key]
	if !indexed {
		pending.leaves[key] = pendingLeaf{at: len(pending.ops)}
		return false
	}
	// The index points at leaf ops only.
	if op.Verb == Set {
		pending.ops[leaf.at] = op
		pending.leaves[key] = pendingLeaf{at: leaf.at}
		return true
	}
	if !leaf.growing {
		previous := pending.ops[leaf.at]
		var text string
		if previous.Verb == Append {
			text = previous.Text
		} else if s, isString := previous.Value.(string); isString {
			// Only a string set takes an append; the tracker never appends to another primitive.
			text = s
		} else {
			pending.leaves[key] = pendingLeaf{at: len(pending.ops)}
			return false
		}
		leaf.buf, leaf.growing = append(leaf.buf[:0], text...), true
	}
	leaf.buf = appendText(leaf.buf, op.Text)
	pending.leaves[key] = leaf
	return true
}

// appendText appends text to buf as UTF-16 concatenation does: a trailing high surrogate of buf (a lone one, in its
// three-byte generalized form) and a leading low surrogate of text join into one code point.
func appendText(buf []byte, text string) []byte {
	if len(buf) >= 3 && len(text) >= 3 {
		tail := string(buf[len(buf)-3:])
		if joined := jv.Concat(tail, text[:3]); len(joined) != len(tail)+3 {
			return append(append(buf[:len(buf)-3], joined...), text[3:]...)
		}
	}
	return append(buf, text...)
}

// pathKey is the key of a path in the leaf index. It is injective, as merging two distinct paths would lose a write:
// ["a.b"] and ["a", "b"] are different keys, as are a key "1" and the index 1.
func pathKey(path Path) string {
	var key []byte
	for _, segment := range path {
		if segment.IsIndex {
			key = append(key, 'i')
			key = strconv.AppendInt(key, int64(segment.Index), 10)
			key = append(key, ';')
			continue
		}
		key = append(key, 's')
		key = strconv.AppendInt(key, int64(len(segment.Key)), 10)
		key = append(key, ':')
		key = append(key, segment.Key...)
	}
	return string(key)
}
