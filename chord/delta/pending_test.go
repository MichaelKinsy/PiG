package delta

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// upstream: packages/chord/test/delta-pending.test.ts: folding is invisible to a replica, the cap, ownership of received ops, cost.

// pendingRandom is a seeded generator, so a failing sequence reproduces from its seed. The texts hold surrogate halves and separators: an append may split a code point, and paths hold any string.
type pendingRandom struct{ *rand.Rand }

var (
	pendingTexts = []string{"a", "bc", " tok", "é", "😀", "\u2028", "\xed\xa0\xbd", "\xed\xb8\x80", "日本語", `"q"`, `\`, "", strings.Repeat("x", 30)}
	pendingKeys  = []string{"a", "b", "0", "1", "01", "a.b", "x y", "text", "list"}
)

func newPendingRandom(seed uint64) *pendingRandom {
	return &pendingRandom{rand.New(rand.NewPCG(seed, 1))}
}

func (r *pendingRandom) pick(items []string) string { return items[r.IntN(len(items))] }
func (r *pendingRandom) text() string               { return r.pick(pendingTexts) }

func (r *pendingRandom) primitive() any {
	switch r.IntN(4) {
	case 0:
		return float64(r.IntN(100))
	case 1:
		return r.text()
	case 2:
		return r.Float64() < 0.5
	}
	return nil
}

func (r *pendingRandom) value(depth int) any {
	roll := r.Float64()
	if depth > 2 || roll < 0.4 {
		return r.primitive()
	}
	if roll < 0.7 {
		items := make([]any, r.IntN(4))
		for index := range items {
			items[index] = r.value(depth + 1)
		}
		return items
	}
	object := chordjson.NewObject(0)
	for count := r.IntN(4); count >= 0; count-- {
		object.Set(r.pick(pendingKeys), r.value(depth+1))
	}
	return object
}

func (r *pendingRandom) root() *JsonObject {
	object := chordjson.NewObject(0)
	for count := r.IntN(4) + 1; count >= 0; count-- {
		object.Set(r.pick(pendingKeys), r.value(0))
	}
	return object
}

// pendingSlot is one (parent, key) position of a draft: parent is an *Object (key a string) or an *Array (key an int).
type pendingSlot struct {
	parent any
	key    any
}

func (slot pendingSlot) read() any {
	switch parent := slot.parent.(type) {
	case *Object:
		return parent.Get(slot.key.(string))
	case *Array:
		return parent.Get(slot.key.(int))
	}
	return nil
}

func (slot pendingSlot) write(t *testing.T, value any) {
	t.Helper()
	switch parent := slot.parent.(type) {
	case *Object:
		must(t, parent.Set(slot.key.(string), value))
	case *Array:
		must(t, parent.Set(slot.key.(int), value))
	}
}

// pendingSlots lists every slot under a draft container, parents first.
func pendingSlots(container any, out []pendingSlot) []pendingSlot {
	switch node := container.(type) {
	case *Object:
		for _, key := range node.Keys() {
			out = append(out, pendingSlot{node, key})
			out = pendingSlots(node.Get(key), out)
		}
	case *Array:
		for index := range node.Len() {
			out = append(out, pendingSlot{node, index})
			out = pendingSlots(node.Get(index), out)
		}
	}
	return out
}

// mutateAnywhere is one random edit anywhere in draft: string appends and trims, array splices and reorders, object edits.
func mutateAnywhere(t *testing.T, draft *Object, r *pendingRandom, viaAppendText bool) {
	t.Helper()
	all := pendingSlots(draft, nil)
	if len(all) == 0 {
		must(t, draft.Set(r.pick([]string{"a", "b"}), r.value(0)))
		return
	}
	slot := all[r.IntN(len(all))]
	current := slot.read()
	roll := r.Float64()
	switch current := current.(type) {
	case string:
		parent, isObject := slot.parent.(*Object)
		switch {
		case roll < 0.3 && isObject:
			if viaAppendText {
				must(t, AppendText(parent, slot.key.(string), r.text()))
			} else {
				slot.write(t, jsConcat(current, r.text()))
			}
		case roll < 0.6:
			slot.write(t, jsConcat(current, r.text()))
		case roll < 0.8:
			slot.write(t, utf16Suffix(current, min(utf16Len(current), r.IntN(3))))
		default:
			slot.write(t, r.primitive())
		}
	case *Array:
		switch {
		case roll < 0.3:
			_, err := current.Push(r.value(0))
			must(t, err)
		case roll < 0.5:
			var items []any
			if r.Float64() < 0.5 {
				items = []any{r.value(0)}
			}
			_, err := current.Splice(r.IntN(current.Len()+1), r.IntN(2), items...)
			must(t, err)
		case roll < 0.65 && current.Len() > 0:
			must(t, current.Set(r.IntN(current.Len()), r.value(0)))
		case roll < 0.75 && current.Len() > 1:
			current.Reverse()
		case roll < 0.85 && current.Len() > 1:
			current.Sort(func(a, b any) bool { return mustJSON(t, a) < mustJSON(t, b) })
		case roll < 0.95:
			slot.write(t, r.value(0))
		default:
			current.SetLen(0)
		}
	case *Object:
		keys := current.Keys()
		switch {
		case roll < 0.4:
			must(t, current.Set(r.pick([]string{"a", "b", "1", "x y"}), r.value(0)))
		case roll < 0.6 && len(keys) > 0:
			current.Delete(keys[r.IntN(len(keys))])
		default:
			slot.write(t, r.value(0))
		}
	default:
		if r.Float64() < 0.7 {
			slot.write(t, r.primitive())
		} else {
			slot.write(t, r.value(0))
		}
	}
}

func jsConcat(a, b string) string { return jsstring.Concat(a, b) }

// mutateList is a leaf edit of a list of elements interleaved with splices, pushes and reorders of the list.
func mutateList(t *testing.T, draft *Object, r *pendingRandom) {
	t.Helper()
	element := func() any { return chordjson.ObjectOf("x", "", "n", float64(0)) }
	list := draft.Array("list")
	if list == nil {
		must(t, draft.Set("list", []any{element(), element(), element()}))
		return
	}
	roll := r.Float64()
	switch {
	case roll < 0.45 && list.Len() > 0:
		index := r.IntN(list.Len())
		if item := list.Object(index); item != nil {
			switch {
			case r.Float64() < 0.3:
				must(t, AppendText(item, "x", r.text()))
			case r.Float64() < 0.5:
				must(t, item.Set("x", jsConcat(item.Get("x").(string), r.text())))
			default:
				must(t, item.Set("n", float64(r.IntN(9))))
			}
		} else {
			must(t, list.Set(index, element()))
		}
	case roll < 0.6:
		var items []any
		if r.Float64() < 0.6 {
			items = []any{chordjson.ObjectOf("x", r.text(), "n", float64(0))}
		}
		_, err := list.Splice(r.IntN(list.Len()+1), r.IntN(2), items...)
		must(t, err)
	case roll < 0.7:
		_, err := list.Push(element())
		must(t, err)
	case roll < 0.8 && list.Len() > 1:
		list.Reverse()
	case roll < 0.9 && list.Len() > 0:
		must(t, list.Set(r.IntN(list.Len()), r.primitive()))
	case list.Len() > 0:
		_, err := list.Splice(r.IntN(list.Len()), 1)
		must(t, err)
	}
}

// mutateRootArray is an edit of a root array: pushes, splices, reorders, element sets and shifts.
func mutateRootArray(t *testing.T, draft *Array, r *pendingRandom) {
	t.Helper()
	switch r.IntN(7) {
	case 0:
		_, err := draft.Push(r.value(0))
		must(t, err)
	case 1:
		_, err := draft.Splice(r.IntN(draft.Len()+1), r.IntN(4), r.value(0))
		must(t, err)
	case 2:
		draft.Reverse()
	case 3:
		draft.Sort(func(a, b any) bool { return mustJSON(t, a) < mustJSON(t, b) })
	case 4:
		if draft.Len() > 0 {
			must(t, draft.Set(r.IntN(draft.Len()), r.value(0)))
		}
	case 5:
		draft.Shift()
	default:
		_, err := draft.Splice(r.IntN(draft.Len()+1), r.IntN(4))
		must(t, err)
	}
}

// pendingSequence is one sequence of commits from a value: the commits' op batches and the value after each.
type pendingSequence struct {
	initial any
	commits []pendingCommit
}

type pendingCommit struct {
	ops   []Op
	value any
}

func trackedPendingSequence(t *testing.T, initial any, commits int, r *pendingRandom, mutate func(root *Object, array *Array), replace func() any) pendingSequence {
	t.Helper()
	var tracker *Tracker
	switch root := initial.(type) {
	case *JsonObject:
		tracker = Track(copyTrusted(root).(*JsonObject))
	case []any:
		tracker = TrackArray(copyTrusted(root).([]any))
	}
	var out []pendingCommit
	for range commits {
		var prepared *Prepared
		var err error
		if r.Float64() < 0.03 {
			prepared, err = tracker.PrepareReplaceRoot(replace())
			must(t, err)
		} else {
			change := tracker.BeginChange()
			for edits := 1 + r.IntN(4); edits > 0; edits-- {
				mutate(change.State(), change.Elements())
			}
			prepared, err = change.Prepare()
			must(t, err)
		}
		must(t, tracker.Adopt(prepared))
		out = append(out, pendingCommit{ops: prepared.Ops(), value: copyTrusted(tracker.Root())})
	}
	return pendingSequence{initial: initial, commits: out}
}

// diffPendingSequence is diffs between values, each an edit of the previous one or a new value.
func diffPendingSequence(t *testing.T, r *pendingRandom, commits int) pendingSequence {
	t.Helper()
	initial := r.value(0)
	var out []pendingCommit
	value := initial
	for range commits {
		tracker := Track(chordjson.ObjectOf("value", copyTrusted(value)))
		change := tracker.BeginChange()
		for edits := 1 + r.IntN(3); edits > 0; edits-- {
			mutateAnywhere(t, change.State(), r, false)
		}
		prepared, err := change.Prepare()
		must(t, err)
		next := copyTrusted(prepared.Value().Value("value"))
		if r.Float64() < 0.2 {
			next = r.value(0)
		}
		out = append(out, pendingCommit{ops: DiffRevisions(value, next), value: next})
		value = next
	}
	return pendingSequence{initial: initial, commits: out}
}

// checkPendingSequence feeds the sequence through one queue, taking at random points as a slow consumer would, and replays what was taken on a replica, once as ops and once through Delta's encoder and decoder. Both must reach the final value; the received ops must be untouched.
func checkPendingSequence(t *testing.T, sequence pendingSequence, r *pendingRandom) (ops, folded int) {
	t.Helper()
	received := mustJSON(t, commitOps(sequence))
	var queue PendingOps
	type taken struct {
		batch PendingTake
		value any
	}
	var takes []taken
	for index, commit := range sequence.commits {
		queue.Push(commit.ops)
		if r.Float64() < 0.1 || index == len(sequence.commits)-1 {
			takes = append(takes, taken{queue.Take(), commit.value})
		}
	}
	final := sequence.initial
	if len(sequence.commits) > 0 {
		final = sequence.commits[len(sequence.commits)-1].value
	}
	replica := copyTrusted(sequence.initial)
	encoder, decoder := NewEncoder(), NewDecoder()
	decoded := roundTripPending(t, encoder, decoder, nil, []Op{{"r", copyTrusted(sequence.initial)}})
	for _, take := range takes {
		batch := take.batch.Ops
		if take.batch.Replace {
			batch = []Op{{"r", take.value}}
		}
		folded += len(batch)
		replica = mustApplyImmutable(t, replica, batch)
		decoded = roundTripPending(t, encoder, decoder, decoded, batch)
	}
	if !equalTrustedJSON(replica, final) {
		t.Fatalf("replica %s, want %s", mustJSON(t, replica), mustJSON(t, final))
	}
	if !equalTrustedJSON(decoded, final) {
		t.Fatalf("decoded replica %s, want %s", mustJSON(t, decoded), mustJSON(t, final))
	}
	if after := mustJSON(t, commitOps(sequence)); after != received {
		t.Fatalf("the queue changed the ops it received:\n got %s\nwant %s", after, received)
	}
	for _, commit := range sequence.commits {
		ops += len(commit.ops)
	}
	return ops, folded
}

func commitOps(sequence pendingSequence) [][]Op {
	var all [][]Op
	for _, commit := range sequence.commits {
		all = append(all, commit.ops)
	}
	return all
}

// roundTripPending applies batch to replica after it went through the wire form: encoded, detached and decoded.
func roundTripPending(t *testing.T, encoder *Encoder, decoder *Decoder, replica any, batch []Op) any {
	t.Helper()
	wire, err := encoder.Encode(batch)
	must(t, err)
	// The wire form is copied, as a boundary would detach it; going through JSON text would turn a lone surrogate into U+FFFD, which a JavaScript JSON.stringify writes as an escape.
	parsed := make([]WireOp, len(wire))
	for index, op := range wire {
		parsed[index] = WireOp(copyTrusted([]any(op)).([]any))
	}
	decoded, err := decoder.Decode(parsed)
	must(t, err)
	return mustApplyImmutable(t, replica, decoded)
}

func TestPendingOpsFoldingIsInvisibleToAReplicaTrackerEditsAnywhereAppendTextSplicesAndReorders(t *testing.T) {
	r := newPendingRandom(1)
	ops, folded := 0, 0
	for range 800 {
		sequence := trackedPendingSequence(t, r.root(), 1+r.IntN(30), r, func(root *Object, _ *Array) { mutateAnywhere(t, root, r, true) }, func() any { return r.root() })
		o, f := checkPendingSequence(t, sequence, r)
		ops, folded = ops+o, folded+f
	}
	// The sequences fold: a fuzzer whose ops never merge proves nothing.
	if float64(folded) >= float64(ops)*0.9 {
		t.Fatalf("%d of %d ops survived folding", folded, ops)
	}
}

func TestPendingOpsFoldingIsInvisibleToAReplicaLeafEditsOfListElementsBetweenSplicesAndReordersOfTheList(t *testing.T) {
	r := newPendingRandom(2)
	ops, folded := 0, 0
	for range 800 {
		initial := chordjson.ObjectOf("list", []any{chordjson.ObjectOf("x", "a", "n", float64(0)), chordjson.ObjectOf("x", "b", "n", float64(1))})
		sequence := trackedPendingSequence(t, initial, 5+r.IntN(40), r, func(root *Object, _ *Array) { mutateList(t, root, r) }, func() any { return chordjson.ObjectOf("list", []any{}) })
		o, f := checkPendingSequence(t, sequence, r)
		ops, folded = ops+o, folded+f
	}
	if float64(folded) >= float64(ops)*0.9 {
		t.Fatalf("%d of %d ops survived folding", folded, ops)
	}
}

func TestPendingOpsFoldingIsInvisibleToAReplicaARootArray(t *testing.T) {
	r := newPendingRandom(3)
	for range 300 {
		initial := make([]any, r.IntN(6))
		for index := range initial {
			initial[index] = r.value(0)
		}
		replace := func() any {
			items := make([]any, r.IntN(6))
			for index := range items {
				items[index] = r.value(0)
			}
			return items
		}
		sequence := trackedPendingSequence(t, initial, 40, r, func(_ *Object, array *Array) { mutateRootArray(t, array, r) }, replace)
		checkPendingSequence(t, sequence, r)
	}
}

func TestPendingOpsFoldingIsInvisibleToAReplicaDiffRevisionsBetweenArbitraryValues(t *testing.T) {
	r := newPendingRandom(4)
	for range 500 {
		checkPendingSequence(t, diffPendingSequence(t, r, 30), r)
	}
}

// foldOneByOne pushes each op as its own batch, takes, and checks the replay; it returns the folded ops.
func foldOneByOne(t *testing.T, initial any, ops []Op) []Op {
	t.Helper()
	var queue PendingOps
	for _, op := range ops {
		queue.Push([]Op{op})
	}
	batch := queue.Take()
	if batch.Replace {
		t.Fatal("unexpected overflow")
	}
	expected := mustApplyImmutable(t, clone(t, initial), ops)
	if got := mustApplyImmutable(t, clone(t, initial), batch.Ops); !equalTrustedJSON(got, expected) {
		t.Fatalf("folded ops give %s, want %s", mustJSON(t, got), mustJSON(t, expected))
	}
	encoder, decoder := NewEncoder(), NewDecoder()
	base := roundTripPending(t, encoder, decoder, nil, []Op{{"r", clone(t, initial)}})
	if got := roundTripPending(t, encoder, decoder, base, batch.Ops); !equalTrustedJSON(got, expected) {
		t.Fatalf("decoded folded ops give %s, want %s", mustJSON(t, got), mustJSON(t, expected))
	}
	return batch.Ops
}

func pendingDoc(t *testing.T, text string) any {
	t.Helper()
	return parse(t, text)
}

func expectOpsJSON(t *testing.T, got []Op, want string) {
	t.Helper()
	if mustJSON(t, got) != mustJSON(t, parse(t, want)) {
		t.Fatalf("ops %s, want %s", mustJSON(t, got), want)
	}
}

func TestPendingOpsAdversarialALeafSetInAnArrayElementASpliceBeforeItTheSamePathSetAgain(t *testing.T) {
	ops := parseOps(t, `[["s",["arr",2,"x"],1],["p",["arr"],0,0,[{"x":9}]],["s",["arr",2,"x"],2]]`)
	expectOpsJSON(t, foldOneByOne(t, pendingDoc(t, `{"arr":[{"x":0},{"x":0},{"x":0}]}`), ops), mustJSON(t, ops))
	appends := parseOps(t, `[["a",["arr",0,"x"],"1"],["p",["arr"],0,0,[{"x":"new"}]],["a",["arr",0,"x"],"2"]]`)
	expectOpsJSON(t, foldOneByOne(t, pendingDoc(t, `{"arr":[{"x":"a"},{"x":"b"}]}`), appends), mustJSON(t, appends))
}

func TestPendingOpsAdversarialTThenAAndAppendsAroundATrim(t *testing.T) {
	if got := foldOneByOne(t, pendingDoc(t, `{"s":"hello"}`), parseOps(t, `[["t",["s"],2],["a",["s"],"!"]]`)); len(got) != 2 {
		t.Fatalf("ops %v", got)
	}
	if got := foldOneByOne(t, pendingDoc(t, `{"s":"hello"}`), parseOps(t, `[["a",["s"],"AB"],["t",["s"],3],["a",["s"],"C"]]`)); len(got) != 3 {
		t.Fatalf("ops %v", got)
	}
}

func TestPendingOpsAdversarialAnObjectSAtAParentThenLeafSetsBelowIt(t *testing.T) {
	if got := foldOneByOne(t, pendingDoc(t, `{"o":{"a":1}}`), parseOps(t, `[["s",["o","a"],2],["s",["o"],{"a":5,"b":6}],["s",["o","a"],7]]`)); len(got) != 3 {
		t.Fatalf("ops %v", got)
	}
	got := foldOneByOne(t, pendingDoc(t, `{"x":{"s":"a"}}`), parseOps(t, `[["s",["x"],{"s":"b"}],["a",["x","s"],"c"],["s",["x","s"],"d"]]`))
	expectOpsJSON(t, got, `[["s",["x"],{"s":"b"}],["s",["x","s"],"d"]]`)
}

func TestPendingOpsAdversarialAnAncestorPrimitiveSBetweenLeafOpsBelowIt(t *testing.T) {
	got := foldOneByOne(t, pendingDoc(t, `{"o":{"a":"x","b":"y"}}`), parseOps(t, `[["a",["o","a"],"1"],["s",["o","b"],"q"],["s",["o"],"flat"],["a",["o"],"!"]]`))
	expectOpsJSON(t, got, `[["a",["o","a"],"1"],["s",["o","b"],"q"],["s",["o"],"flat!"]]`)
	got = foldOneByOne(t, pendingDoc(t, `{"a":{"b":0}}`), parseOps(t, `[["s",["a","b"],1],["s",["a"],null],["s",["a"],"x"]]`))
	expectOpsJSON(t, got, `[["s",["a","b"],1],["s",["a"],"x"]]`)
}

func TestPendingOpsAdversarialDeletesMovesAndSplicesShiftWhatAPathNames(t *testing.T) {
	if got := foldOneByOne(t, pendingDoc(t, `{"arr":["a","b","c"]}`), parseOps(t, `[["s",["arr",2],"C"],["d",["arr",0]],["s",["arr",1],"Z"]]`)); len(got) != 3 {
		t.Fatalf("ops %v", got)
	}
	if got := foldOneByOne(t, pendingDoc(t, `{"arr":[1,2,3]}`), parseOps(t, `[["s",["arr",0],10],["m",["arr"],[2,1,0]],["s",["arr",0],20]]`)); len(got) != 3 {
		t.Fatalf("ops %v", got)
	}
	if got := foldOneByOne(t, pendingDoc(t, `{"arr":[{"x":0}]}`), parseOps(t, `[["s",["arr",0,"x"],1],["p",["arr"],1,0,[{"x":0}]],["s",["arr",0,"x"],2],["s",["arr",1,"x"],3]]`)); len(got) != 4 {
		t.Fatalf("ops %v", got)
	}
}

func TestPendingOpsAdversarialKeysOfOneParentNeverCollide(t *testing.T) {
	foldOneByOne(t, pendingDoc(t, `{"a":{"b":"x"},"a.b":"z"}`), parseOps(t, `[["a",["a","b"],"1"],["s",["a.b"],"y"],["a",["a","b"],"2"]]`))
	foldOneByOne(t, pendingDoc(t, `{"o":{"1":"a"},"arr":["x","y"]}`), parseOps(t, `[["s",["o","1"],"b"],["s",["arr",1],"z"],["s",["o","1"],"c"],["s",["arr",1],"w"]]`))
}

func TestPendingOpsAdversarialAnArraySetAtTheEndAppendsAndALaterSetOfThatIndexFoldsIntoIt(t *testing.T) {
	got := foldOneByOne(t, pendingDoc(t, `{"arr":[1]}`), parseOps(t, `[["s",["arr",1],2],["s",["arr",0],9],["s",["arr",1],3]]`))
	expectOpsJSON(t, got, `[["s",["arr",1],3],["s",["arr",0],9]]`)
}

func TestPendingOpsAdversarialRDropsEverythingBeforeIt(t *testing.T) {
	got := foldOneByOne(t, pendingDoc(t, `{"a":1}`), parseOps(t, `[["s",["a"],2],["r",{"b":"x"}],["a",["b"],"y"]]`))
	expectOpsJSON(t, got, `[["r",{"b":"x"}],["a",["b"],"y"]]`)
}

func TestPendingOpsAdversarialJoinsAppendsFoldsAppendsIntoAStringSetAndKeepsSplitSurrogates(t *testing.T) {
	got := foldOneByOne(t, pendingDoc(t, `{"s":""}`), parseOps(t, `[["s",["s"],"ab"],["a",["s"],"c"],["s",["t"],1],["a",["s"],"d"]]`))
	expectOpsJSON(t, got, `[["s",["s"],"abcd"],["s",["t"],1]]`)
	split := []Op{{"a", []any{"s"}, "\xed\xa0\xbd"}, {"s", []any{"n"}, float64(1)}, {"a", []any{"s"}, "\xed\xb8\x80"}, {"a", []any{"s"}, "é"}}
	got = foldOneByOne(t, pendingDoc(t, `{"s":""}`), split)
	if want := []Op{{"a", []any{"s"}, "😀é"}, {"s", []any{"n"}, float64(1)}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops %v, want %v", got, want)
	}
}

func TestPendingOpsAdversarialInterleavedLeavesAStructuralOpThenTheSameLeavesAgain(t *testing.T) {
	got := foldOneByOne(t, pendingDoc(t, `{"a":"","b":"","list":[0]}`), parseOps(t, `[["a",["a"],"1"],["a",["b"],"1"],["a",["a"],"2"],["p",["list"],1,0,[1]],["a",["b"],"2"],["a",["a"],"3"]]`))
	expectOpsJSON(t, got, `[["a",["a"],"12"],["a",["b"],"1"],["p",["list"],1,0,[1]],["a",["b"],"2"],["a",["a"],"3"]]`)
}

// distinctPendingSets is count set ops on distinct paths.
func distinctPendingSets(count, from int) []Op {
	ops := make([]Op, count)
	for index := range ops {
		ops[index] = Op{"s", []any{"k", from + index}, float64(index)}
	}
	return ops
}

func TestPendingOpsCapKeepsMaxPendingOpsPendingOpsAndReplacesPastThem(t *testing.T) {
	var queue PendingOps
	queue.Push(nil)
	queue.Push(distinctPendingSets(MaxPendingOps, 0))
	if exact := queue.Take(); exact.Replace || len(exact.Ops) != MaxPendingOps {
		t.Fatalf("exact take %v ops, replace %v", len(exact.Ops), exact.Replace)
	}
	queue.Push(distinctPendingSets(MaxPendingOps/2, 0))
	queue.Push(distinctPendingSets(MaxPendingOps/2, MaxPendingOps/2))
	if full := queue.Take(); full.Replace || len(full.Ops) != MaxPendingOps {
		t.Fatalf("full take %v ops, replace %v", len(full.Ops), full.Replace)
	}
	queue.Push(distinctPendingSets(MaxPendingOps/2, 0))
	queue.Push(distinctPendingSets(MaxPendingOps/2+1, MaxPendingOps/2))
	if over := queue.Take(); !over.Replace {
		t.Fatalf("over take = %v, want replace", over)
	}
}

func TestPendingOpsCapCountsOpsAfterFolding(t *testing.T) {
	var queue PendingOps
	for range 3 * MaxPendingOps {
		queue.Push([]Op{{"a", []any{"text"}, "x"}})
	}
	taken := queue.Take()
	if want := []Op{{"a", []any{"text"}, strings.Repeat("x", 3*MaxPendingOps)}}; taken.Replace || !reflect.DeepEqual(taken.Ops, want) {
		t.Fatalf("take = %d ops, replace %v", len(taken.Ops), taken.Replace)
	}
}

func TestPendingOpsCapAccumulatesNothingOnceReplacedUntilTakenOrABatchStartingWithR(t *testing.T) {
	var queue PendingOps
	queue.Push(distinctPendingSets(1, 0))
	queue.Push(distinctPendingSets(MaxPendingOps+1, 0))
	queue.Push(distinctPendingSets(3, 0))
	if taken := queue.Take(); !taken.Replace {
		t.Fatalf("take = %v, want replace", taken)
	}
	if taken := queue.Take(); taken.Replace || len(taken.Ops) != 0 {
		t.Fatalf("second take = %v, want nothing", taken)
	}

	queue.Push(distinctPendingSets(1, 0))
	queue.Push(distinctPendingSets(MaxPendingOps+1, 0))
	fresh := []Op{{"r", chordjson.ObjectOf("k", []any{float64(0)})}}
	queue.Push(fresh)
	if taken := queue.Take(); taken.Replace || &taken.Ops[0] != &fresh[0] {
		t.Fatalf("take = %v, want the fresh batch by identity", taken)
	}
	queue.Push(distinctPendingSets(1, 0))
	queue.Push(fresh)
	queue.Push([]Op{{"s", []any{"n"}, float64(1)}})
	if taken := queue.Take(); taken.Replace || !reflect.DeepEqual(taken.Ops, append(slices.Clone(fresh), Op{"s", []any{"n"}, float64(1)})) {
		t.Fatalf("take = %v", taken)
	}
}

func TestPendingOpsCapHandsOneBatchOverAsReceivedWhateverItsSize(t *testing.T) {
	var queue PendingOps
	batch := distinctPendingSets(MaxPendingOps+1, 0)
	queue.Push(batch)
	if taken := queue.Take(); taken.Replace || &taken.Ops[0] != &batch[0] {
		t.Fatal("the batch was not handed over as received")
	}
	queue.Push(batch)
	queue.Push([]Op{{"s", []any{"n"}, float64(1)}})
	if taken := queue.Take(); !taken.Replace {
		t.Fatalf("take = %v, want replace", taken)
	}
}

func TestPendingOpsOwnershipHandsOverAReceivedBatchByIdentityAndNeverMutatesBatchesOrOpTuples(t *testing.T) {
	var queue PendingOps
	first := []Op{{"a", []any{"s"}, "1"}}
	queue.Push(first)
	if alone := queue.Take(); &alone.Ops[0] != &first[0] {
		t.Fatal("a batch pushed alone is not handed over by identity")
	}
	second := []Op{{"a", []any{"s"}, "2"}}
	queue.Push(first)
	queue.Push(second)
	if taken := queue.Take(); !reflect.DeepEqual(taken.Ops, []Op{{"a", []any{"s"}, "12"}}) {
		t.Fatalf("take = %v", taken.Ops)
	}
	if !reflect.DeepEqual(first, []Op{{"a", []any{"s"}, "1"}}) || !reflect.DeepEqual(second, []Op{{"a", []any{"s"}, "2"}}) {
		t.Fatalf("a received batch changed: %v %v", first, second)
	}
}

func TestPendingOpsSizeCountsPendingOpsAfterFoldingTheReplacementOnceOverflowedAndNothingOnceTaken(t *testing.T) {
	var queue PendingOps
	expectSize := func(want int) {
		t.Helper()
		if got := queue.Size(); got != want {
			t.Fatalf("size = %d, want %d", got, want)
		}
	}
	expectSize(0)
	queue.Push(nil)
	expectSize(0)
	queue.Push([]Op{{"a", []any{"text"}, "a"}, {"s", []any{"n"}, float64(1)}})
	expectSize(2)
	queue.Push([]Op{{"a", []any{"text"}, "b"}, {"s", []any{"n"}, float64(2)}, {"p", []any{"list"}, 0, 0, []any{float64(1)}}})
	expectSize(3)
	queue.Take()
	expectSize(0)
	queue.Push(distinctPendingSets(MaxPendingOps, 0))
	expectSize(MaxPendingOps)
	queue.Push([]Op{{"s", []any{"n"}, float64(1)}})
	expectSize(1)
	if taken := queue.Take(); !taken.Replace {
		t.Fatalf("take = %v", taken)
	}
	expectSize(0)
}

func TestPendingOpsKeepsOneOpPerLeafPathHoweverManyCommitsWriteIt(t *testing.T) {
	var queue PendingOps
	for index := range 50_000 {
		queue.Push([]Op{{"a", []any{"entries", 0, "text"}, "tok "}, {"s", []any{"meta", "tokens"}, float64(index)}})
	}
	taken := queue.Take()
	want := []Op{{"a", []any{"entries", 0, "text"}, strings.Repeat("tok ", 50_000)}, {"s", []any{"meta", "tokens"}, float64(49_999)}}
	if taken.Replace || !reflect.DeepEqual(taken.Ops, want) {
		t.Fatalf("take = %d ops, replace %v", len(taken.Ops), taken.Replace)
	}
}

// Once taken, the queue holds no op: what it handed over is the caller's, and a replaced or folded-over op is not kept.
func TestPendingOpsReleasesWhatItHandedOverAndWhatItFoldedOver(t *testing.T) {
	var queue PendingOps
	queue.Push([]Op{{"s", []any{"n"}, float64(1)}})
	queue.Push([]Op{{"s", []any{"n"}, float64(2)}, {"s", []any{"o"}, "payload"}})
	if got := queue.Take(); !reflect.DeepEqual(got.Ops, []Op{{"s", []any{"n"}, float64(2)}, {"s", []any{"o"}, "payload"}}) {
		t.Fatalf("take = %v", got.Ops)
	}
	if queue.Size() != 0 || queue.ops != nil || queue.leaves != nil {
		t.Fatalf("a taken queue holds %+v", queue)
	}
	queue.Push([]Op{{"s", []any{"o"}, "payload"}})
	queue.Push([]Op{{"p", []any{"list"}, 0, 0, []any{float64(1)}}})
	queue.Take()
	if queue.ops != nil || queue.leaves != nil {
		t.Fatalf("a taken folding queue holds %+v", queue)
	}
}

func parseOps(t *testing.T, text string) []Op { return ops(t, text) }
