package delta

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

// Ports cases of packages/chord/test/delta-tracker/tracker.test.ts onto the overlay Tracker, whose Object and Array
// handles are the Go form of upstream's Proxy draft. Go mapping, shared by every case below: objects are
// *JsonObject, which keeps JavaScript's property order; numbers are float64; a comparator or callback receives draft handles; and Go has no valueOf coercion,
// prototype chain, or receiver rebinding.

func jsonOf(t *testing.T, text string) any {
	t.Helper()
	value, err := DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func jsonObject(t *testing.T, text string) *JsonObject {
	t.Helper()
	return jsonOf(t, text).(*JsonObject)
}

func textOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func expectJSONText(t *testing.T, got any, want string) {
	t.Helper()
	if text := textOf(t, got); text != want {
		t.Fatalf("got %s, want %s", text, want)
	}
}

func opsText(t *testing.T, ops []Op) string {
	t.Helper()
	return textOf(t, opsAsJSON(ops))
}

// replayThroughWire applies ops to a deep copy of base after a JSON round trip, as a remote replica would.
func replayThroughWire(t *testing.T, base any, ops []Op) any {
	t.Helper()
	var wire Ops
	if err := json.Unmarshal([]byte(textOf(t, opsAsJSON(ops))), &wire); err != nil {
		t.Fatal(err)
	}
	copied, err := CloneJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ApplyImmutable(copied, wire)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return replayed
}

// settle is tracker.test.ts settle: prepare, replay the wire form of ops over the base, adopt, and return the candidate.
func settle(t *testing.T, tracker *Tracker, mutate func(*Object)) *JsonObject {
	t.Helper()
	baseRoot := tracker.Value()
	base := textOf(t, baseRoot)
	change := tracker.BeginChange()
	mutate(change.State())
	prepared := mustPrepare(t, change)
	candidate := textOf(t, prepared.Value())
	if got := textOf(t, replayThroughWire(t, baseRoot, prepared.Ops())); got != candidate {
		t.Fatalf("replay %s, candidate %s", got, candidate)
	}
	if textOf(t, baseRoot) != base {
		t.Fatal("preparing mutated the base revision")
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	if !sameContainer(tracker.Value(), prepared.Value()) || textOf(t, tracker.Value()) != candidate {
		t.Fatal("adoption must install the prepared revision")
	}
	return prepared.Value()
}

func expectAliasFreeValue(t *testing.T, value any) {
	t.Helper()
	seen := map[uintptr]string{}
	var visit func(current any, path string)
	visit = func(current any, path string) {
		var id uintptr
		switch typed := current.(type) {
		case *JsonObject:
			id = reflect.ValueOf(typed).Pointer()
			for key, item := range typed.All() {
				defer visit(item, path+"."+key)
			}
		case []any:
			if len(typed) == 0 {
				return
			}
			id = reflect.ValueOf(typed).Pointer()
			for index, item := range typed {
				defer visit(item, fmt.Sprintf("%s[%d]", path, index))
			}
		default:
			return
		}
		if previous, ok := seen[id]; ok {
			t.Fatalf("container at %s aliases %s", path, previous)
		}
		seen[id] = path
	}
	visit(value, "$root")
}

func expectKeys(t *testing.T, object *Object, want ...string) {
	t.Helper()
	if got := object.Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
}

func TestTrackerPolicyViewReadsAfterEdits(t *testing.T) {
	// tracker.test.ts:351 "supports native reads, descriptors, keys, iteration, map, and JSON". Array.isArray and the
	// property descriptor are the *Array type and Get; iteration and map are Len with Get or Snapshot.
	tracker := Track(jsonObject(t, `{"values":[1,2,3],"object":{"a":1}}`))
	change := tracker.BeginChange()
	state := change.State()
	values := state.Array("values")
	if values == nil {
		t.Fatal("an array reads as an *Array handle")
	}
	if _, err := values.Splice(1, 1, 4.0, 5.0); err != nil {
		t.Fatal(err)
	}
	object := state.Object("object")
	if err := object.Set("b", 2.0); err != nil {
		t.Fatal(err)
	}
	if values.Len() != 4 {
		t.Fatalf("length %d", values.Len())
	}
	expectJSONText(t, values.Snapshot(), `[1,4,5,3]`)
	doubled := make([]any, values.Len())
	for index := range doubled {
		doubled[index] = values.Get(index).(float64) * 2
	}
	expectJSONText(t, doubled, `[2,8,10,6]`)
	if values.Get(2) != 5.0 {
		t.Fatalf("element 2 is %v", values.Get(2))
	}
	expectKeys(t, object, "a", "b")
	expectJSONText(t, state.Snapshot(), `{"values":[1,4,5,3],"object":{"a":1,"b":2}}`)
	change.Abort()
}

func TestTrackerDocumentKeysAreDistinctFromHandleFields(t *testing.T) {
	// tracker.test.ts:367. Keys that name the proxy's own fields are ordinary document keys.
	tracker := Track(jsonObject(t, `{"object":{"context":1,"base":2,"parent":3,"dirty":4,"target":5,"proxy":6}}`))
	change := tracker.BeginChange()
	object := change.State().Object("object")
	for key, value := range map[string]float64{"context": 7, "proxy": 8} {
		if err := object.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	expectKeys(t, object, "context", "base", "parent", "dirty", "target", "proxy")
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value().Value("object"), `{"context":7,"base":2,"parent":3,"dirty":4,"target":5,"proxy":8}`)
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerStringOperationFormsAndArgumentSideEffects(t *testing.T) {
	// tracker.test.ts:387. Go has no valueOf coercion, so the side effect that upstream runs while splice coerces its
	// start argument runs while the argument is evaluated; the draft write it makes must survive the splice.
	tracker := Track(jsonObject(t, `{"text":"abcdefgh","values":[1,2,3],"marker":0}`))
	change := tracker.BeginChange()
	state := change.State()
	if err := state.Set("text", state.Get("text").(string)[3:]+"xyz"); err != nil {
		t.Fatal(err)
	}
	start := func() int {
		if err := state.Set("marker", 1.0); err != nil {
			t.Fatal(err)
		}
		return 1
	}
	if _, err := state.Array("values").Splice(start(), 1, 4.0); err != nil {
		t.Fatal(err)
	}
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value(), `{"text":"defghxyz","values":[1,4,3],"marker":1}`)
	var sawTrim, sawAppend bool
	for _, op := range prepared.Ops() {
		sawTrim = sawTrim || opsText(t, []Op{op}) == `[["t",["text"],3]]`
		sawAppend = sawAppend || opsText(t, []Op{op}) == `[["a",["text"],"xyz"]]`
	}
	if !sawTrim || !sawAppend {
		t.Fatalf("expected a trim and an append of the text, got %s", opsText(t, prepared.Ops()))
	}
	expectJSONText(t, replayThroughWire(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerKeyOrderAfterDeleteAndReAdd(t *testing.T) {
	// tracker.test.ts:424. A deleted key that is added again moves behind the keys that stayed, in the draft and in the
	// adopted revision.
	tracker := Track(jsonObject(t, `{"first":1,"second":2}`))
	change := tracker.BeginChange()
	state := change.State()
	state.Delete("first")
	if err := state.Set("first", 1.0); err != nil {
		t.Fatal(err)
	}
	expectKeys(t, state, "second", "first")
	// JSON.stringify(change.state) reads the draft in the same order.
	expectJSONText(t, state.Snapshot(), `{"second":2,"first":1}`)
	prepared := mustPrepare(t, change)
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	expectJSONText(t, tracker.Value(), `{"second":2,"first":1}`)
}

func TestTrackerKeysMoveAReAddedNewKeyToTheEnd(t *testing.T) {
	// tracker.ts ownKeys (:617) lists written keys in the order of the writes Map (setObjectWrite :689), and
	// deleteProperty (:593) removes a key from that Map, so a key added by the change, deleted and added again moves
	// behind later keys and is listed once, while rewriting a present key keeps its position. Probed on Pi 1.0.0:
	// {b} then x, y, delete x, x, z lists ["b","y","x","z"], and writing y again keeps that order.
	tracker := Track(jsonObject(t, `{"object":{"b":1}}`))
	change := tracker.BeginChange()
	object := change.State().Object("object")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(object.Set("x", 1.0))
	must(object.Set("y", 2.0))
	object.Delete("x")
	must(object.Set("x", 3.0))
	must(object.Set("z", 4.0))
	expectKeys(t, object, "b", "y", "x", "z")
	must(object.Set("y", 5.0))
	expectKeys(t, object, "b", "y", "x", "z")
	if object.Len() != 4 {
		t.Fatalf("length %d", object.Len())
	}
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value().Value("object"), `{"b":1,"y":5,"x":3,"z":4}`)
	if got := len(prepared.Ops()); got != 3 {
		t.Fatalf("each written key is emitted once, got %s", opsText(t, prepared.Ops()))
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerIntegerLikeKeysOrderBeforeStrings(t *testing.T) {
	// tracker.test.ts:435, :467 and :485. Integer-like keys come first in ascending order, then the other keys in the
	// order the object holds them. Go has no null-prototype object (:485); every object is prototype-free.
	t.Run("new integer-like keys", func(t *testing.T) {
		tracker := Track(jsonObject(t, `{"object":{"label":"x"},"first":""}`))
		base := tracker.Value()
		change := tracker.BeginChange()
		state := change.State()
		object := state.Object("object")
		for _, key := range []string{"2", "1"} {
			if err := object.Set(key, map[string]string{"1": "one", "2": "two"}[key]); err != nil {
				t.Fatal(err)
			}
		}
		expectKeys(t, object, "1", "2", "label")
		if err := state.Set("first", object.Keys()[0]); err != nil {
			t.Fatal(err)
		}
		prepared := mustPrepare(t, change)
		if prepared.Value().Value("first") != "1" {
			t.Fatalf("first is %v", prepared.Value().Value("first"))
		}
		expectJSONText(t, prepared.Value().Value("object"), `{"1":"one","2":"two","label":"x"}`)
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("across deletion and re-addition", func(t *testing.T) {
		tracker := Track(jsonObject(t, `{"object":{"1":"one","2":"two","first":"a","second":"b"}}`))
		change := tracker.BeginChange()
		object := change.State().Object("object")
		object.Delete("2")
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(object.Set("2", "two"))
		object.Delete("first")
		must(object.Set("first", "a"))
		must(object.Set("3", "three"))
		must(object.Set("0", "zero"))
		expectKeys(t, object, "0", "1", "2", "3", "second", "first")
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value().Value("object"), `{"0":"zero","1":"one","2":"two","3":"three","second":"b","first":"a"}`)
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
	})
}

// Pi source: packages/chord/src/delta/tracker.ts
// mutation-checked: zeroing the results of Prepared.Value, Tracker.BeginChange fails it
func TestTrackerByValuePlacementsOfEveryArrayMutator(t *testing.T) {
	// tracker.test.ts:685. Each placement is a clone taken when it is made, so editing the source afterwards, or
	// filling and copying from a draft element, never aliases the slots.
	tracker := Track(jsonObject(t, `{"property":null,"values":[{"value":0},{"value":1},{"value":2}]}`))
	external := JsonObjectOf("value", 5.0)
	change := tracker.BeginChange()
	state := change.State()
	values := state.Array("values")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(state.Set("property", external))
	must(values.Set(0, external))
	_, err := values.Push(external)
	must(err)
	_, err = values.Unshift(external)
	must(err)
	_, err = values.Splice(2, 0, external)
	must(err)
	external.Set("value", 99.0)
	must(values.Fill(values.Get(0), 1, 3))
	must(values.CopyWithin(3, 0, 2))
	prepared := mustPrepare(t, change)
	candidate := prepared.Value()
	expectJSONText(t, candidate.Value("property"), `{"value":5}`)
	expectJSONText(t, candidate.Value("values").([]any)[:5], `[{"value":5},{"value":5},{"value":5},{"value":5},{"value":5}]`)
	expectAliasFreeValue(t, candidate)
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerSupportsSelfOverlappingFillAndCopyWithinByValue(t *testing.T) {
	// tracker.test.ts:1256.
	tracker := Track(jsonObject(t, `{"values":[{"n":0},{"n":1},{"n":2},{"n":3}]}`))
	value := settle(t, tracker, func(state *Object) {
		values := state.Array("values")
		if err := values.CopyWithin(1, 0, 3); err != nil {
			t.Fatal(err)
		}
		if err := values.Fill(values.Get(1), 0, 2); err != nil {
			t.Fatal(err)
		}
		if err := values.Object(0).Set("n", 9.0); err != nil {
			t.Fatal(err)
		}
	})
	expectJSONText(t, value.Value("values"), `[{"n":9},{"n":0},{"n":1},{"n":2}]`)
	expectAliasFreeValue(t, value)
}

func TestTrackerFillReplacesTheClampedRange(t *testing.T) {
	// tracker.ts fill (:1314): the range is clamped first, an empty range returns without cloning the value, and a
	// non-empty range clones the value once per slot, so a value that is not strict JSON is rejected only when it would
	// be placed. Probed on Pi 1.0.0: fill(NaN, 2, 1) leaves [1,2,3] and fill(NaN, 0, 1) throws "Value contains a
	// non-finite number and is not strict JSON".
	tracker := Track(jsonObject(t, `{"values":[1,2,3,4]}`))
	value := settle(t, tracker, func(state *Object) {
		values := state.Array("values")
		if err := values.Fill(math.NaN(), 2, 1); err != nil {
			t.Fatalf("an empty range places nothing, got %v", err)
		}
		if err := values.Fill(math.NaN(), 4, 9); err != nil {
			t.Fatalf("a range clamped to empty places nothing, got %v", err)
		}
		if err := values.Fill(math.NaN(), 0, 1); !errors.Is(err, ErrNotStrictJSON) {
			t.Fatalf("a non-empty range rejects NaN, got %v", err)
		}
		expectJSONText(t, values.Snapshot(), `[1,2,3,4]`)
		if err := values.Fill(JsonObjectOf("n", 7.0), -3, -1); err != nil {
			t.Fatal(err)
		}
	})
	expectJSONText(t, value.Value("values"), `[1,{"n":7},{"n":7},4]`)
	expectAliasFreeValue(t, value)
}

func TestTrackerSortComparatorWritesAndReentrantAppends(t *testing.T) {
	// tracker.test.ts:1038. The comparator receives draft elements. Element writes survive; a write to an element slot
	// is replaced by the sorted order, and an append made by the comparator stays behind the sorted prefix.
	tracker := Track(jsonObject(t, `{"values":[{"rank":2,"comparisons":0},{"rank":1,"comparisons":0}]}`))
	base := tracker.Value()
	change := tracker.BeginChange()
	values := change.State().Array("values")
	appended := false
	bump := func(item any) {
		element := item.(*Object)
		if err := element.Set("comparisons", element.Get("comparisons").(float64)+1); err != nil {
			t.Fatal(err)
		}
	}
	values.Sort(func(left, right any) bool {
		bump(left)
		bump(right)
		if !appended {
			appended = true
			if err := values.Set(0, JsonObjectOf("rank", 9.0, "comparisons", 0.0)); err != nil {
				t.Fatal(err)
			}
			if _, err := values.Push(JsonObjectOf("rank", 3.0, "comparisons", 0.0)); err != nil {
				t.Fatal(err)
			}
		}
		return left.(*Object).Get("rank").(float64) < right.(*Object).Get("rank").(float64)
	})
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value().Value("values"), `[{"rank":1,"comparisons":1},{"rank":2,"comparisons":1},{"rank":3,"comparisons":0}]`)
	expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerSortNormalizesDuplicatesFromReentrantStructuralEdits(t *testing.T) {
	// tracker.test.ts:1065. A comparator that inserts at the front leaves one element in two slots; the copy is an
	// independent clone carrying the element's edits.
	tracker := Track(jsonObject(t, `{"values":[{"rank":2,"edited":0,"nested":{"value":0}},{"rank":1,"edited":0,"nested":{"value":0}}]}`))
	base := tracker.Value()
	change := tracker.BeginChange()
	values := change.State().Array("values")
	inserted := false
	edit := func(item any) {
		element := item.(*Object)
		rank := element.Get("rank").(float64)
		if err := element.Set("edited", rank*10); err != nil {
			t.Fatal(err)
		}
		if err := element.Object("nested").Set("value", rank*100); err != nil {
			t.Fatal(err)
		}
	}
	values.Sort(func(left, right any) bool {
		edit(left)
		edit(right)
		if !inserted {
			inserted = true
			if _, err := values.Unshift(JsonObjectOf("rank", 4.0, "edited", 0.0, "nested", JsonObjectOf("value", 0.0))); err != nil {
				t.Fatal(err)
			}
		}
		return left.(*Object).Get("rank").(float64) < right.(*Object).Get("rank").(float64)
	})
	prepared := mustPrepare(t, change)
	expectJSONText(t, prepared.Value().Value("values"), `[{"rank":1,"edited":10,"nested":{"value":100}},{"rank":2,"edited":20,"nested":{"value":200}},{"rank":1,"edited":10,"nested":{"value":100}}]`)
	expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
	expectAliasFreeValue(t, prepared.Value())
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
}

// mutation-checked: zeroing the results of Prepared.Ops, Tracker.Value fails it
func TestTrackerFoldsDenseArrayRegions(t *testing.T) {
	rowsOf := func(count int, build func(index int) any) []any {
		rows := make([]any, count)
		for index := range rows {
			rows[index] = build(index)
		}
		return rows
	}
	row := func(index int) any { return JsonObjectOf("value", float64(index)) }
	opHead := func(t *testing.T, op Op) string {
		t.Helper()
		return textOf(t, []any{op[0], op[1], op[2], op[3]})
	}

	t.Run("dense child and direct-index edits fold into one operation", func(t *testing.T) {
		// tracker.test.ts:1097.
		tracker := Track(JsonObjectOf("rows", rowsOf(1_000, row)))
		base := tracker.Value()
		change := tracker.BeginChange()
		rows := change.State().Array("rows")
		for index := range rows.Len() {
			element := rows.Object(index)
			if err := element.Set("value", element.Get("value").(float64)+1); err != nil {
				t.Fatal(err)
			}
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 1 || prepared.Ops()[0][0] != "p" {
			t.Fatalf("ops %d, first %v", len(prepared.Ops()), prepared.Ops()[0][0])
		}
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))

		numbers := make([]any, 1_000)
		for index := range numbers {
			numbers[index] = float64(index)
		}
		valuesTracker := Track(JsonObjectOf("values", numbers))
		valuesBase := valuesTracker.Value()
		valuesChange := valuesTracker.BeginChange()
		values := valuesChange.State().Array("values")
		for index := range 600 {
			if err := values.Set(index, float64(-index-1)); err != nil {
				t.Fatal(err)
			}
		}
		valuesPrepared := mustPrepare(t, valuesChange)
		if len(valuesPrepared.Ops()) != 1 || valuesPrepared.Ops()[0][0] != "p" {
			t.Fatalf("ops %v", opsText(t, valuesPrepared.Ops()))
		}
		expectJSONText(t, replayThroughWire(t, valuesBase, valuesPrepared.Ops()), textOf(t, valuesPrepared.Value()))
	})

	t.Run("only a deeply nested dense region folds", func(t *testing.T) {
		// tracker.test.ts:1119.
		tracker := Track(JsonObjectOf(
			"rows", rowsOf(2_000, func(index int) any {
				return JsonObjectOf("nested", JsonObjectOf("value", float64(index)))
			}),
		))
		base := tracker.Value()
		change := tracker.BeginChange()
		rows := change.State().Array("rows")
		if err := rows.Object(0).Object("nested").Set("value", -1.0); err != nil {
			t.Fatal(err)
		}
		for index := 500; index < 1_000; index++ {
			if err := rows.Object(index).Object("nested").Set("value", float64(-index)); err != nil {
				t.Fatal(err)
			}
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 2 {
			t.Fatalf("ops %s", opsText(t, prepared.Ops()))
		}
		splice := prepared.Ops()[slices.IndexFunc(prepared.Ops(), func(op Op) bool { return op[0] == "p" })]
		if opHead(t, splice) != `["p",["rows"],500,500]` || len(splice[4].([]any)) != 500 {
			t.Fatalf("splice %s", opHead(t, splice))
		}
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
	})

	t.Run("disjoint regions keep the operations outside their boundaries", func(t *testing.T) {
		// tracker.test.ts:1177.
		tracker := Track(JsonObjectOf("values", rowsOf(1_400, row)))
		base := tracker.Value()
		change := tracker.BeginChange()
		values := change.State().Array("values")
		edit := func(index int) {
			if err := values.Object(index).Set("value", float64(-index-1)); err != nil {
				t.Fatal(err)
			}
		}
		for _, index := range []int{97, 358, 897, 1_158} {
			edit(index)
		}
		for index := 100; index < 356; index++ {
			edit(index)
		}
		for index := 900; index < 1_156; index++ {
			edit(index)
		}
		prepared := mustPrepare(t, change)
		var splices []string
		for _, op := range prepared.Ops() {
			if op[0] == "p" {
				splices = append(splices, opHead(t, op))
			}
		}
		if !slices.Equal(splices, []string{`["p",["values"],100,256]`, `["p",["values"],900,256]`}) {
			t.Fatalf("splices %v", splices)
		}
		for _, index := range []int{97, 358, 897, 1_158} {
			want := fmt.Sprintf(`["s",["values",%d,"value"],%d]`, index, -index-1)
			if !slices.ContainsFunc(prepared.Ops(), func(op Op) bool { return textOf(t, []any(op)) == want }) {
				t.Fatalf("missing %s in %d ops", want, len(prepared.Ops()))
			}
		}
		if len(prepared.Ops()) != 6 {
			t.Fatalf("ops %d", len(prepared.Ops()))
		}
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
	})

	t.Run("nested reserved-key folds covered by an outer region emit one operation", func(t *testing.T) {
		// tracker.test.ts:1137. The reserved __proto__ key is an ordinary key of a decoded document.
		numbers := make([]any, 400)
		for index := range numbers {
			numbers[index] = float64(index)
		}
		rows := rowsOf(400, func(int) any { return JsonObjectOf("flag", 0.0) })
		rows[100].(*JsonObject).Set("special", JsonObjectOf("__proto__", JsonObjectOf("values", numbers)))
		tracker := Track(JsonObjectOf("rows", rows))
		base := tracker.Value()
		change := tracker.BeginChange()
		state := change.State().Array("rows")
		for index := range 256 {
			if err := state.Object(index).Set("flag", 1.0); err != nil {
				t.Fatal(err)
			}
		}
		inner := state.Object(100).Object("special").Object("__proto__").Array("values")
		for index := range 256 {
			if err := inner.Set(index, float64(-index-1)); err != nil {
				t.Fatal(err)
			}
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 1 || opHead(t, prepared.Ops()[0]) != `["p",["rows"],0,256]` {
			t.Fatalf("ops %d, first %s", len(prepared.Ops()), opHead(t, prepared.Ops()[0]))
		}
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		if got := prepared.Value().Value("rows").([]any)[100].(*JsonObject).Value("special").(*JsonObject).Value("__proto__").(*JsonObject).Value("values").([]any)[255]; got != -256.0 {
			t.Fatalf("inner value %v", got)
		}
	})

	t.Run("nested structural and leaf operations covered by an outer region are suppressed", func(t *testing.T) {
		// tracker.test.ts:1157.
		inner := make([]any, 400)
		for index := range inner {
			inner[index] = JsonObjectOf("value", float64(index))
		}
		tracker := Track(JsonObjectOf(
			"rows", rowsOf(400, func(index int) any {
				values := []any{}
				if index == 100 {
					values = inner
				}
				return JsonObjectOf("flag", 0.0, "values", values)
			}),
		))
		base := tracker.Value()
		change := tracker.BeginChange()
		rows := change.State().Array("rows")
		for index := range 256 {
			if err := rows.Object(index).Set("flag", 1.0); err != nil {
				t.Fatal(err)
			}
		}
		values := rows.Object(100).Array("values")
		for index := range 256 {
			if err := values.Object(index).Set("value", float64(-index-1)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := values.Push(JsonObjectOf("value", 999.0)); err != nil {
			t.Fatal(err)
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 1 || opHead(t, prepared.Ops()[0]) != `["p",["rows"],0,256]` {
			t.Fatalf("ops %d, first %s", len(prepared.Ops()), opHead(t, prepared.Ops()[0]))
		}
		if got := len(prepared.Value().Value("rows").([]any)[100].(*JsonObject).Value("values").([]any)); got != 401 {
			t.Fatalf("values length %d", got)
		}
		expectJSONText(t, replayThroughWire(t, base, prepared.Ops()), textOf(t, prepared.Value()))
	})
}

// Ports the queue scenario of packages/chord/test/delta-benchmark/benchmark.worker.ts: a draft used as a queue (shift one row, push one row) edits its entry list in place, as the JavaScript splice does, so the allocation of N queue steps does not grow with the array length. Copying the list per step allocated 64 MB here.
// mutation-checked: zeroing the results of Change.Prepare fails it
func TestTrackerQueueStepsDoNotCopyTheEntryList(t *testing.T) {
	const rows, steps = 20_000, 200
	document := make([]any, rows)
	for index := range document {
		document[index] = JsonObjectOf("id", float64(index))
	}
	tracker := Track(JsonObjectOf("rows", document))
	change := tracker.BeginChange()
	queue := change.State().Array("rows")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for index := range steps {
		queue.Shift()
		if _, err := queue.Push(JsonObjectOf("id", float64(rows+index))); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("%d queue steps over %d rows allocated %d bytes; the entry list must be edited in place", steps, rows, allocated)
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(prepared.Value().Value("rows").([]any)); got != rows {
		t.Fatalf("rows = %d, want %d", got, rows)
	}
}
