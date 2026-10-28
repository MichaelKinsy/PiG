package delta

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// upstream: packages/chord/test/delta-pending.test.ts: folding is invisible to a replica, the cap, ownership of received ops.

func pendingBase() *jv.Object {
	o := jv.NewObject()
	o.Set("text", "")
	o.Set("count", float64(0))
	o.Set("list", []any{})
	return o
}

func apply(t *testing.T, value any, ops []Op) any {
	t.Helper()
	out, err := Apply(value, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out
}

func sameValue(a, b any) bool { return bytes.Equal(jv.Append(nil, a), jv.Append(nil, b)) }

func TestPendingFoldIsInvisibleToAReplica(t *testing.T) {
	batches := [][]Op{
		{{Verb: Append, Path: Path{Key("text")}, Text: "a"}},
		{{Verb: Append, Path: Path{Key("text")}, Text: "b"}, {Verb: Set, Path: Path{Key("count")}, Value: float64(1)}},
		{{Verb: Set, Path: Path{Key("text")}, Value: "xy"}},
		{{Verb: Append, Path: Path{Key("text")}, Text: "z"}, {Verb: Splice, Path: Path{Key("list")}, Items: []any{"i"}}},
		{{Verb: Append, Path: Path{Key("text")}, Text: "!"}, {Verb: Set, Path: Path{Key("count")}, Value: float64(2)}},
	}
	var pending PendingOps
	want := any(pendingBase())
	for _, batch := range batches {
		pending.Push(batch)
		want = apply(t, want, batch)
	}
	peeked := pending.Peek(nil)
	taken := pending.Take()
	if peeked.Replace || taken.Replace || len(taken.Ops) >= 8 {
		t.Fatalf("folded to %d ops: %#v", len(taken.Ops), taken)
	}
	if got := apply(t, pendingBase(), taken.Ops); !sameValue(got, want) {
		t.Fatalf("replica %s, want %s", jv.Append(nil, got), jv.Append(nil, want))
	}
	if pending.Size() != 0 {
		t.Fatalf("Take left %d ops", pending.Size())
	}
}

func TestPendingOneBatchIsKeptByReference(t *testing.T) {
	batch := []Op{{Verb: Append, Path: Path{Key("text")}, Text: "a"}}
	var pending PendingOps
	pending.Push(batch)
	pending.Push([]Op{{Verb: Append, Path: Path{Key("text")}, Text: "b"}})
	if batch[0].Text != "a" {
		t.Fatalf("a received op was mutated: %q", batch[0].Text)
	}
}

func TestPendingPeekLeavesTheQueueAsItWas(t *testing.T) {
	var pending PendingOps
	pending.Push([]Op{{Verb: Append, Path: Path{Key("text")}, Text: "a"}})
	pending.Push([]Op{{Verb: Append, Path: Path{Key("text")}, Text: "b"}})
	own := []Op{{Verb: Append, Path: Path{Key("text")}, Text: "c"}}
	for range 2 {
		peeked := pending.Peek(own)
		if len(peeked.Ops) != 1 || peeked.Ops[0].Text != "abc" {
			t.Fatalf("peek: %#v", peeked)
		}
	}
	taken := pending.Take()
	if len(taken.Ops) != 1 || taken.Ops[0].Text != "ab" {
		t.Fatalf("take after peeks: %#v", taken)
	}
}

func TestPendingKeepsDistinctPathsApart(t *testing.T) {
	var pending PendingOps
	pending.Push([]Op{{Verb: Set, Path: Path{Key("a.b")}, Value: float64(1)}})
	pending.Push([]Op{{Verb: Set, Path: Path{Key("a"), Key("b")}, Value: float64(2)}, {Verb: Set, Path: Path{Key("1")}, Value: float64(3)}, {Verb: Set, Path: Path{Idx(1)}, Value: float64(4)}})
	if got := len(pending.Take().Ops); got != 4 {
		t.Fatalf("merged paths that differ: %d ops", got)
	}
}

func TestPendingAppendsJoinASurrogatePairSplitAcrossThem(t *testing.T) {
	var pending PendingOps
	path := Path{Key("text")}
	pending.Push([]Op{{Verb: Append, Path: path, Text: "a\xed\xa0\xbd"}})
	pending.Push([]Op{{Verb: Append, Path: path, Text: "\xed\xb8\x80b"}})
	if got := pending.Take().Ops[0].Text; got != "a\U0001F600b" {
		t.Fatalf("joined text %q", got)
	}
}

func TestPendingOverflowsToReplaceAndAReplacingBatchRecovers(t *testing.T) {
	var pending PendingOps
	set := func(i int) []Op {
		return []Op{{Verb: Set, Path: Path{Key("k" + strings.Repeat("x", 1) + itoa(i))}, Value: float64(i)}}
	}
	for i := 0; i <= MaxPendingOps; i++ {
		pending.Push(set(i))
	}
	if pending.Size() != 1 {
		t.Fatalf("size %d after overflow", pending.Size())
	}
	pending.Push(set(0))
	if take := pending.Peek(nil); !take.Replace {
		t.Fatalf("an overflowed queue took more: %#v", take)
	}
	pending.Push([]Op{{Verb: Replace, Value: pendingBase()}})
	if take := pending.Take(); take.Replace || len(take.Ops) != 1 {
		t.Fatalf("a root replacement did not recover: %#v", take)
	}
}

func itoa(i int) string { return string(jv.AppendNumber(nil, float64(i))) }

func TestPendingAppendCostFollowsTheNewText(t *testing.T) {
	var pending PendingOps
	path := Path{Key("text")}
	chunk := strings.Repeat("x", 100)
	for range 5000 {
		pending.Push([]Op{{Verb: Append, Path: path, Text: chunk}})
	}
	take := pending.Take()
	if len(take.Ops) != 1 || len(take.Ops[0].Text) != 5000*len(chunk) {
		t.Fatalf("folded to %d ops", len(take.Ops))
	}
}
