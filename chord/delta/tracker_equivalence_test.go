package delta

import (
	"cmp"
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

// Equivalence ports of the tracker.test.ts cases whose upstream form relies on a JavaScript mechanism Go lacks: a
// borrowed method applied to a foreign receiver, implicit valueOf coercion, inherited properties, and prototype setters.
// Each test cites its upstream case and asserts the invariant that case protects, through the Go API that can reach
// it. Expected values were produced by Pi 1.0.0 packages/chord/src/delta (track, prepare, encoder) under Node 24 on the
// same inputs; object keys are chosen in sorted order so the JSON text of a Go map, whose keys encode sorted, is
// byte-identical to Pi's insertion-ordered JSON.

// sortedFloats is Array.prototype.sort with a numeric comparator over float64 elements.
func sortedFloats(values []any) {
	slices.SortFunc(values, func(left, right any) int { return cmp.Compare(left.(float64), right.(float64)) })
}

// mutateWithSliceOps applies, with Go's slice operations, the operation sequence that tracker.test.ts:503 applies with
// borrowed mutators: push(4), splice(1, 1, 5), a numeric sort, fill(7, 1, 2), and copyWithin(2, 0, 1). It returns the
// resulting slice, the length after the push, and the elements the splice removed.
func mutateWithSliceOps(receiver []any) (result []any, pushed int, removed []any) {
	receiver = append(receiver, 4.0)
	pushed = len(receiver)
	removed = slices.Clone(receiver[1:2])
	receiver = slices.Replace(receiver, 1, 2, any(5.0))
	sortedFloats(receiver)
	for index := 1; index < 2; index++ {
		receiver[index] = 7.0
	}
	copy(receiver[2:3], receiver[0:1])
	return receiver, pushed, removed
}

// mutateReadOuts applies Go's map and slice operations to every value read out of the draft: the array Snapshot, the
// root Snapshot, and an element of the root Snapshot. None of them may reach the draft.
func mutateReadOuts(t *testing.T, state *Object) {
	t.Helper()
	values := state.Array("values")
	if snapshot, _, _ := mutateWithSliceOps(values.Snapshot()); textOf(t, snapshot) != `[1,7,1,5]` {
		t.Fatalf("slice operations on the array snapshot gave %s", textOf(t, snapshot))
	}
	inPlace := values.Snapshot()
	sortedFloats(inPlace)
	slices.Reverse(inPlace)
	inPlace[0] = 99.0
	root := state.Snapshot()
	root["values"].([]any)[1] = 42.0
	root["placed"].(map[string]any)["inner"] = "changed"
	root["extra"] = true
	delete(root, "placed")
	if got := textOf(t, root); got != `{"extra":true,"values":[1,42,3]}` {
		t.Fatalf("map operations on the root snapshot gave %s", got)
	}
}

func TestTrackerDraftMutatorsActOnlyOnTheDraftAndReadOutsAreDetached(t *testing.T) {
	// Equivalence port of tracker.test.ts:503 "forwards borrowed mutators to ordinary generic array receivers". Go binds
	// a method value to its receiver, so a draft mutator cannot be borrowed onto another slice; Go's slice operations are
	// the ordinary-receiver form of the same mutators. The invariant is that a draft's mutators act only on the draft,
	// and that values read out of a draft are detached: Go's slice and map operations applied to them never change the
	// draft, never record a delta, and leave the draft equal to its pre-change value after Abort. Same data as upstream:
	// the draft holds [1,2,3] and the ordinary receiver [3,1,2] becomes [2,7,2,5].
	initial := `{"placed":{"inner":"base"},"values":[1,2,3]}`

	t.Run("an ordinary receiver and read-outs never reach the draft", func(t *testing.T) {
		tracker := Track(jsonObject(t, initial))
		base := tracker.Value()
		change := tracker.BeginChange()
		state := change.State()
		values := state.Array("values")

		receiver, pushed, removed := mutateWithSliceOps([]any{3.0, 1.0, 2.0})
		if pushed != 4 || textOf(t, removed) != `[1]` {
			t.Fatalf("push length %d, splice removed %s", pushed, textOf(t, removed))
		}
		expectJSONText(t, receiver, `[2,7,2,5]`)
		mutateReadOuts(t, state)
		expectJSONText(t, values.Snapshot(), `[1,2,3]`)
		expectJSONText(t, state.Snapshot(), initial)
		expectKeys(t, state, "placed", "values")

		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 0 || !sameContainer(prepared.Value(), base) {
			t.Fatalf("mutating read-outs recorded ops %s", opsText(t, prepared.Ops()))
		}
		expectJSONText(t, tracker.Value(), initial)
	})

	t.Run("read-outs of written placements never reach the draft", func(t *testing.T) {
		tracker := Track(jsonObject(t, initial))
		change := tracker.BeginChange()
		state := change.State()
		if err := state.Set("placed", map[string]any{"inner": "draft"}); err != nil {
			t.Fatal(err)
		}
		if _, err := state.Array("values").Push(); err != nil {
			t.Fatal(err)
		}
		if err := state.Array("values").Set(0, 1.0); err != nil {
			t.Fatal(err)
		}
		mutateReadOuts(t, state)
		expectJSONText(t, state.Snapshot(), `{"placed":{"inner":"draft"},"values":[1,2,3]}`)
		prepared := mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[["s",["placed"],{"inner":"draft"}]]` {
			t.Fatalf("ops %s", got)
		}
	})

	t.Run("bound draft mutators act only on the draft and Abort restores it", func(t *testing.T) {
		tracker := Track(jsonObject(t, initial))
		base := tracker.Value()
		change := tracker.BeginChange()
		state := change.State()
		values := state.Array("values")
		push, splice, sortValues, fill, copyWithin := values.Push, values.Splice, values.Sort, values.Fill, values.CopyWithin
		before := values.Snapshot()
		receiver := []any{3.0, 1.0, 2.0}

		if length, err := push(4.0); err != nil || length != 4 {
			t.Fatalf("push: %d %v", length, err)
		}
		if removed, err := splice(1, 1, 5.0); err != nil || textOf(t, removed) != `[2]` {
			t.Fatalf("splice: %v %v", removed, err)
		}
		sortValues(func(left, right any) bool { return left.(float64) < right.(float64) })
		if err := fill(7.0, 1, 2); err != nil {
			t.Fatal(err)
		}
		if err := copyWithin(2, 0, 1); err != nil {
			t.Fatal(err)
		}
		expectJSONText(t, values.Snapshot(), `[1,7,1,5]`)
		expectJSONText(t, receiver, `[3,1,2]`)
		expectJSONText(t, before, `[1,2,3]`)
		expectJSONText(t, base, initial)

		change.Abort()
		if !sameContainer(tracker.Value(), base) {
			t.Fatal("Abort must keep the committed revision")
		}
		expectJSONText(t, tracker.Value(), initial)
		assertRevoked(t, func() { values.Len() })
	})
}

// piStructuralMutators is testdata/pi_structural_mutators.json, written by testdata/pi_structural_mutators.mjs.
type piStructuralMutators struct {
	Upstream string `json:"upstream"`
	Rows     []struct {
		Method        string `json:"method"`
		Args          []any  `json:"args"`
		Native        []any  `json:"native"`
		NativeRemoved []any  `json:"nativeRemoved"`
		Draft         []any  `json:"draft"`
		DraftRemoved  []any  `json:"draftRemoved"`
		DraftError    string `json:"draftError"`
	} `json:"rows"`
}

// recordedNumber decodes a golden argument; the generator records NaN, which JSON cannot carry, as the string "NaN".
func recordedNumber(t *testing.T, value any) float64 {
	t.Helper()
	switch typed := value.(type) {
	case float64:
		return typed
	case string:
		if typed == "NaN" {
			return math.NaN()
		}
	}
	t.Fatalf("golden argument %v is not a number", value)
	return 0
}

func TestTrackerStructuralMutatorsMatchNativeArgumentHandling(t *testing.T) {
	// Equivalence port of tracker.test.ts:522 "matches native structural coercion ordering for splice, fill, and
	// copyWithin". Go's typed API takes int indices and performs no implicit coercion, so there is no valueOf call to
	// order. The invariant is that for every argument shape the Go API can express (negative, out-of-range, start after
	// end, zero-length, and a NaN fill value) Splice, Fill and CopyWithin produce exactly the array that JavaScript's
	// native method produces. The golden rows record, for each call, the native result and Pi's draft result after
	// prepare, replay and adopt (testdata/pi_structural_mutators.mjs). Pi's draft rejects a non-finite placement, so the
	// one row whose native result holds NaN records Pi's error and the unchanged array instead.
	encoded, err := os.ReadFile("testdata/pi_structural_mutators.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden piStructuralMutators
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Rows) == 0 {
		t.Fatal("no golden rows")
	}
	shapes := map[string]int{}
	for _, row := range golden.Rows {
		shapes[row.Method]++
		name := row.Method + textOf(t, row.Args)
		t.Run(name, func(t *testing.T) {
			if row.DraftError == "" && textOf(t, row.Native) != textOf(t, row.Draft) {
				t.Fatalf("golden row: Pi's draft %s differs from native %s", textOf(t, row.Draft), textOf(t, row.Native))
			}
			if row.Method == "splice" && row.DraftError == "" && textOf(t, row.NativeRemoved) != textOf(t, row.DraftRemoved) {
				t.Fatalf("golden row: Pi's draft removed %s, native %s", textOf(t, row.DraftRemoved), textOf(t, row.NativeRemoved))
			}
			tracker := Track(jsonObject(t, `{"values":[1,2,3]}`))
			var callErr error
			var removed []any
			value := settle(t, tracker, func(state *Object) {
				values := state.Array("values")
				index := func(position int) int { return int(recordedNumber(t, row.Args[position])) }
				switch row.Method {
				case "splice":
					removed, callErr = values.Splice(index(0), index(1), row.Args[2:]...)
				case "fill":
					callErr = values.Fill(recordedNumber(t, row.Args[0]), index(1), index(2))
				case "copyWithin":
					callErr = values.CopyWithin(index(0), index(1), index(2))
				default:
					t.Fatalf("unknown method %q", row.Method)
				}
			})
			expectJSONText(t, value["values"], textOf(t, row.Draft))
			if row.DraftError != "" {
				message, _ := strings.CutPrefix(row.DraftError, "TypeError: ")
				if !errors.Is(callErr, ErrNotStrictJSON) || callErr.Error() != message {
					t.Fatalf("error %v, want Pi's %q", callErr, row.DraftError)
				}
				return
			}
			if callErr != nil {
				t.Fatal(callErr)
			}
			if row.Method == "splice" {
				expectJSONText(t, removed, textOf(t, row.DraftRemoved))
			}
		})
	}
	for _, method := range []string{"splice", "fill", "copyWithin"} {
		if shapes[method] == 0 {
			t.Fatalf("golden data has no %s rows", method)
		}
	}
}

func TestTrackerKeysAndSnapshotsListOnlyData(t *testing.T) {
	// Equivalence port of tracker.test.ts:602 "does not report inherited methods as own array properties". A Go handle
	// has methods, not properties, and a revision is a map or slice with no prototype. The invariant is that Keys, Has,
	// Snapshot and the JSON of an Array or Object never list a method name unless the document stores it as data, and
	// that a stored method name is ordinary data. Pi lists the same keys and JSON for the same input.
	names := []string{"constructor", "hasOwnProperty", "length", "map", "push", "splice", "toString", "valueOf"}
	tracker := Track(jsonObject(t, `{"object":{"a":1},"values":[1]}`))
	change := tracker.BeginChange()
	state := change.State()
	object, values := state.Object("object"), state.Array("values")
	expectKeys(t, state, "object", "values")
	expectKeys(t, object, "a")
	for _, name := range append(slices.Clone(names), "__proto__", "Push", "Splice", "Len", "Get") {
		if state.Has(name) || object.Has(name) {
			t.Fatalf("%q reads as an own key", name)
		}
		if _, present := object.Lookup(name); present {
			t.Fatalf("%q looks up as present", name)
		}
	}
	if values.Len() != 1 {
		t.Fatalf("length %d", values.Len())
	}
	expectJSONText(t, values.Snapshot(), `[1]`)
	expectJSONText(t, state.Snapshot(), `{"object":{"a":1},"values":[1]}`)

	for index, name := range names {
		if err := object.Set(name, float64(index+1)); err != nil {
			t.Fatal(err)
		}
	}
	expectKeys(t, object, append([]string{"a"}, names...)...)
	for _, name := range names {
		if !object.Has(name) {
			t.Fatalf("stored %q does not read back", name)
		}
	}
	prepared := mustPrepare(t, change)
	const stored = `{"a":1,"constructor":1,"hasOwnProperty":2,"length":3,"map":4,"push":5,"splice":6,"toString":7,"valueOf":8}`
	if got := opsText(t, prepared.Ops()); got != `[["s",["object"],`+stored+`]]` {
		t.Fatalf("ops %s", got)
	}
	expectJSONText(t, prepared.Value(), `{"object":`+stored+`,"values":[1]}`)
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerStoresReservedAndInheritedNamesAsOwnData(t *testing.T) {
	// Equivalence port of tracker.test.ts:1400 "defines own properties without invoking inherited setters". A Go map has
	// no prototype chain or setters. The invariant is that setting keys named __proto__, constructor, prototype,
	// toString and trap stores them as plain own data, that the operations and the replayed revision are byte-identical
	// to Pi's for the same input, and that no other object changes. A reserved key folds into a whole-value write at the
	// nearest safe ancestor, a root replacement at the root (tracker.ts:1637-1645).
	keys := []string{"__proto__", "constructor", "prototype", "toString", "trap"}
	const stored = `{"Keep":1,"__proto__":1,"constructor":2,"prototype":3,"toString":4,"trap":5}`
	cases := []struct {
		name, initial string
		target        func(*Object) *Object
		ops, value    string
	}{
		{"root", `{"Keep":1}`, func(state *Object) *Object { return state },
			`[["r",` + stored + `]]`, stored},
		{"nested", `{"nested":{"Keep":1},"other":{"x":1}}`, func(state *Object) *Object { return state.Object("nested") },
			`[["s",["nested"],` + stored + `]]`, `{"nested":` + stored + `,"other":{"x":1}}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bystander := Track(jsonObject(t, test.initial))
			tracker := Track(jsonObject(t, test.initial))
			base := tracker.Value()
			change := tracker.BeginChange()
			target := test.target(change.State())
			for index, key := range keys {
				if err := target.Set(key, float64(index+1)); err != nil {
					t.Fatal(err)
				}
			}
			expectKeys(t, target, append([]string{"Keep"}, keys...)...)
			prepared := mustPrepare(t, change)
			if got := opsText(t, prepared.Ops()); got != test.ops {
				t.Fatalf("ops %s, want Pi's %s", got, test.ops)
			}
			expectJSONText(t, prepared.Value(), test.value)
			expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), test.value)
			expectJSONText(t, base, test.initial)
			if other, ok := base["other"]; ok && !sameContainer(other, prepared.Value()["other"]) {
				t.Fatal("an untouched sibling must stay the same container")
			}
			if err := tracker.Adopt(prepared); err != nil {
				t.Fatal(err)
			}
			expectJSONText(t, bystander.Value(), test.initial)
		})
	}

	t.Run("imported reserved and inherited names stay own data", func(t *testing.T) {
		// delta.test.ts:396 "imports own properties without invoking inherited setters", on the same input. Pi 1.0.0
		// track keeps the imported root, a safe edit is [["s",["trap"],6]], and an edit below __proto__ folds into a
		// root replacement.
		const imported = `{"__proto__":{"z":1},"constructor":2,"prototype":3,"toString":4,"trap":5}`
		root := jsonObject(t, imported)
		tracker := Track(root)
		if !sameContainer(tracker.Value(), root) || textOf(t, tracker.Value()) != imported {
			t.Fatalf("import %s", textOf(t, tracker.Value()))
		}
		change := tracker.BeginChange()
		expectKeys(t, change.State(), "__proto__", "constructor", "prototype", "toString", "trap")
		if err := change.State().Set("trap", 6.0); err != nil {
			t.Fatal(err)
		}
		prepared := mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[["s",["trap"],6]]` {
			t.Fatalf("ops %s, want Pi's", got)
		}
		expectJSONText(t, replayThroughWire(t, root, prepared.Ops()), `{"__proto__":{"z":1},"constructor":2,"prototype":3,"toString":4,"trap":6}`)

		change = Track(root).BeginChange()
		if err := change.State().Object("__proto__").Set("z", 2.0); err != nil {
			t.Fatal(err)
		}
		if got := opsText(t, mustPrepare(t, change).Ops()); got != `[["r",{"__proto__":{"z":2},"constructor":2,"prototype":3,"toString":4,"trap":5}]]` {
			t.Fatalf("ops %s, want Pi's", got)
		}
		expectJSONText(t, root, imported)
	})

	t.Run("safe names below a safe path stay ordinary sets", func(t *testing.T) {
		tracker := Track(jsonObject(t, `{"a":{"b":{"Keep":1}}}`))
		value := settle(t, tracker, func(state *Object) {
			b := state.Object("a").Object("b")
			if err := b.Set("trap", 1.0); err != nil {
				t.Fatal(err)
			}
			if err := b.Set("toString", 2.0); err != nil {
				t.Fatal(err)
			}
		})
		expectJSONText(t, value, `{"a":{"b":{"Keep":1,"toString":2,"trap":1}}}`)
		tracker = Track(jsonObject(t, `{"a":{"b":{"Keep":1}}}`))
		change := tracker.BeginChange()
		b := change.State().Object("a").Object("b")
		if err := b.Set("trap", 1.0); err != nil {
			t.Fatal(err)
		}
		if err := b.Set("toString", 2.0); err != nil {
			t.Fatal(err)
		}
		if got := opsText(t, mustPrepare(t, change).Ops()); got != `[["s",["a","b","trap"],1],["s",["a","b","toString"],2]]` {
			t.Fatalf("ops %s", got)
		}
	})
}

func TestTrackerRemovedValuesAreDetached(t *testing.T) {
	// A value that Pop, Shift or Splice removes from a draft is a plain value, as in upstream, where the removed element
	// returned by the Proxy draft's pop, shift or splice is detached. Pi 1.0.0 under Node 24, for {"values":[{"a":1},{"b":2}]}:
	// writing x = 9 into the removed object leaves the committed revision {"values":[{"a":1},{"b":2}]}, and the batch is
	// the removal alone.
	const initial = `{"values":[{"a":1},{"b":2}]}`
	cases := []struct {
		name, ops, value string
		remove           func(*Array) any
	}{
		{"pop", `[["p",["values"],1,1,[]]]`, `{"values":[{"a":1}]}`, func(values *Array) any { return values.Pop() }},
		{"shift", `[["p",["values"],0,1,[]]]`, `{"values":[{"b":2}]}`, func(values *Array) any { return values.Shift() }},
		{"splice", `[["p",["values"],0,1,[]]]`, `{"values":[{"b":2}]}`, func(values *Array) any {
			removed, err := values.Splice(0, 1)
			if err != nil {
				t.Fatal(err)
			}
			return removed[0]
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tracker := Track(jsonObject(t, initial))
			base := tracker.Value()
			change := tracker.BeginChange()
			removed, ok := test.remove(change.State().Array("values")).(map[string]any)
			if !ok {
				t.Fatal("the removed element is not a plain object")
			}
			removed["x"] = 9.0
			expectJSONText(t, base, initial)
			prepared := mustPrepare(t, change)
			if got := opsText(t, prepared.Ops()); got != test.ops {
				t.Fatalf("ops %s, want Pi's %s", got, test.ops)
			}
			expectJSONText(t, prepared.Value(), test.value)
			if err := tracker.Adopt(prepared); err != nil {
				t.Fatal(err)
			}
			expectJSONText(t, base, initial)
		})
	}
}

func TestTrackerSortKeepsComparatorEditsOnObjectDrafts(t *testing.T) {
	// Port of state-draft.test.ts:50 "keeps comparator edits when sorting object drafts", on the overlay tracker whose
	// comparator receives draft handles. With two elements both Pi and Go's stable sort compare once, so each row
	// records one comparison; Pi 1.0.0 gives ranks [1,2] and comparisons [1,1].
	tracker := Track(jsonObject(t, `{"rows":[{"comparisons":0,"rank":2},{"comparisons":0,"rank":1}]}`))
	change := tracker.BeginChange()
	count := func(row *Object) {
		if err := row.Set("comparisons", row.Get("comparisons").(float64)+1); err != nil {
			t.Fatal(err)
		}
	}
	change.State().Array("rows").Sort(func(left, right any) bool {
		count(left.(*Object))
		count(right.(*Object))
		return left.(*Object).Get("rank").(float64) < right.(*Object).Get("rank").(float64)
	})
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value(), `{"rows":[{"comparisons":1,"rank":1},{"comparisons":1,"rank":2}]}`)
	expectJSONText(t, replayThroughWire(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
}

func TestTrackerRejectsArrayHolesWithoutMutatingTheBase(t *testing.T) {
	// Equivalence port of state-draft.test.ts:110 "rejects array holes without mutating the committed base". Go has no
	// delete of an array index; the draft writes that Go can express and that upstream rejects are a set past the end and
	// a set at a negative index. Pi 1.0.0 under Node 24 throws TypeError "Overlay arrays cannot contain holes" for
	// delete values[0] and values[3] = 9 (tracker.ts:573, :596), and "Only array indices and length can be written" for
	// values[-1] = 9 (tracker.ts:572). Each write fails with Pi's text, the draft is unchanged, and Abort keeps the base.
	tracker := Track(jsonObject(t, `{"values":[1,2]}`))
	base := tracker.Value()
	change := tracker.BeginChange()
	values := change.State().Array("values")
	if err := values.Set(3, 9.0); !errors.Is(err, ErrSparseArray) || err.Error() != "Overlay arrays cannot contain holes" {
		t.Fatalf("Set(3): %v, want Pi's hole error", err)
	}
	if err := values.Set(-1, 9.0); err == nil || err.Error() != "Only array indices and length can be written" {
		t.Fatalf("Set(-1): %v, want Pi's index error", err)
	}
	expectJSONText(t, values.Snapshot(), `[1,2]`)
	change.Abort()
	if !sameContainer(tracker.Value(), base) {
		t.Fatal("Abort must keep the committed revision")
	}
	expectJSONText(t, tracker.Value(), `{"values":[1,2]}`)
}
