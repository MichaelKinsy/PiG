package delta

import (
	"fmt"
	"slices"
	"testing"
)

// Ports packages/chord/test/delta-tracker/tracker.test.ts, delta-clone.test.ts, state-value.test.ts, state-draft.test.ts and state-fuzz.test.ts.
//
// Go mapping. Upstream edits a Proxy overlay; Go edits the private working copy that Change.State returns, with ordinary map and slice operations, and Prepare validates, de-aliases and diffs it (tracker.go). Cases that observe a Proxy, a prototype chain, JavaScript key ordering, native Array coercion, or engine-specific sort comparator calls have no Go counterpart; the cases here keep every contract that survives: lifecycle, staleness, no-op normalization, strict-JSON validation, independent placements, replay equality, and ownership. Placement is by reference in Go, so a value mutated after it was placed shows the mutation: callers must not edit a value after placing it. Cases whose assertion is the exact operation count of tracker.ts's region-folding policy assert replay equality instead, because operation shape is not canonical (delta/README.md, "Operations").

// replay is apply(clone(base), clone(operations)).
func replay(t *testing.T, base any, operations []Op) any {
	t.Helper()
	return mustApply(t, clone(t, base), ops(t, mustJSON(t, operations)))
}

// expectAliasFree fails when one container appears at two paths of a revision.
func expectAliasFree(t *testing.T, value any) {
	t.Helper()
	seen := map[identity]string{}
	var visit func(current any, path string)
	visit = func(current any, path string) {
		if key, tracked := identityOf(current); tracked {
			if previous, dup := seen[key]; dup {
				t.Fatalf("container at %s aliases %s", path, previous)
			}
			seen[key] = path
		}
		switch typed := current.(type) {
		case []any:
			for at, item := range typed {
				visit(item, fmt.Sprintf("%s[%d]", path, at))
			}
		case map[string]any:
			for key, item := range typed {
				visit(item, path+"."+key)
			}
		}
	}
	visit(value, "$root")
}

// settle runs one change, checks its replay and ownership guarantees, adopts it, and returns the candidate. mutate returns a replacement root when the root itself changes, otherwise nil.
func settle(t *testing.T, tracker *Tracker, mutate func(state any) any) any {
	t.Helper()
	baseRoot := tracker.Value()
	base := clone(t, baseRoot)
	change := tracker.BeginChange()
	if replacement := mutate(mustState(t, change)); replacement != nil {
		expectNoErr(t, change.Replace(replacement))
	}
	prepared := mustPrepare(t, change)
	candidate := clone(t, prepared.Value)
	expectEqual(t, replay(t, base, prepared.Ops), candidate)
	expectEqual(t, baseRoot, base)
	expectNoErr(t, tracker.Adopt(prepared))
	expectEqual(t, baseRoot, base)
	expectSame(t, tracker.Value(), prepared.Value, "tracker.value")
	expectEqual(t, tracker.Value(), candidate)
	return candidate
}

func TestTrackerOwnership(t *testing.T) {
	t.Run("takes immutable ownership of the imported revision in O(1)", func(t *testing.T) {
		input := parse(t, `{"point":{"x":3,"y":7,"pressure":0.1},"rows":[{"values":[0,false,null,"text",{"n":1}]}]}`)
		tracker := Track(input)
		expectSame(t, tracker.Value(), input, "tracker.value")
		expectSame(t, obj(t, tracker.Value())["point"], obj(t, input)["point"], "point")
		rowsOf := func(v any) any { return arr(t, obj(t, v)["rows"])[0] }
		expectSame(t, arr(t, obj(t, rowsOf(tracker.Value()))["values"])[4], arr(t, obj(t, rowsOf(input))["values"])[4], "values[4]")
	})

	t.Run("keeps an alias-free owned root distinct", func(t *testing.T) {
		// delta-clone.test.ts:19-26 also asserts null prototypes, which Go maps do not have.
		tracker := Track(parse(t, `{"dictionary":{"enabled":true,"child":{"n":1}},"left":{"nested":[{"n":1}]},"right":{"nested":[{"n":1}]}}`))
		root := obj(t, tracker.Value())
		expectNotSame(t, root["left"], root["right"], "left/right")
		expectNotSame(t, arr(t, obj(t, root["left"])["nested"])[0], arr(t, obj(t, root["right"])["nested"])[0], "nested rows")
	})

	t.Run("does not traverse a trusted root while taking ownership", func(t *testing.T) {
		// delta-clone.test.ts:28-39. A value that is not strict JSON would fail any walk; Track must not look at it.
		input := map[string]any{"untrustedAccessor": make(chan int)}
		tracker := Track(input)
		expectSame(t, tracker.Value(), input, "tracker.value")
	})

	t.Run("copies assigned and inserted values on prepare", func(t *testing.T) {
		// delta-clone.test.ts:41-52 mutates the placed value after placement; Go placement is by reference, so the mutation is not in this port. Prepare still takes a validated copy of aliased placements and replay converges.
		tracker := Track(parse(t, `{"rows":[]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		assigned := parse(t, `{"nested":{"value":1}}`)
		state["rows"] = append(arr(t, state["rows"]), assigned)
		prepared := mustPrepare(t, change)
		expectJSON(t, prepared.Value, `{"rows":[{"nested":{"value":1}}]}`)
		expectEqual(t, mustApply(t, clone(t, prepared.Base), ops(t, mustJSON(t, prepared.Ops))), prepared.Value)
	})
}

func TestStateValueOwnership(t *testing.T) {
	t.Run("takes ownership of an alias-free mutable JSON root without freezing", func(t *testing.T) {
		input := parse(t, `{"left":{"value":1},"right":{"value":1}}`)
		tracker := Track(input)
		expectSame(t, tracker.Value(), input, "tracker.value")
		expectNotSame(t, obj(t, tracker.Value())["left"], obj(t, tracker.Value())["right"], "left/right")
	})

	t.Run("commits transaction copies in place while sharing unchanged branches", func(t *testing.T) {
		tracker := Track(parse(t, `{"changed":{"value":1},"retained":{"value":2}}`))
		base := tracker.Value()
		baseSnapshot := clone(t, base)
		change := tracker.BeginChange()
		obj(t, obj(t, mustState(t, change))["changed"])["value"] = 3.0
		prepared := mustPrepare(t, change)
		expectNotSame(t, obj(t, prepared.Value)["changed"], obj(t, base)["changed"], "changed")
		expectSame(t, obj(t, prepared.Value)["retained"], obj(t, base)["retained"], "retained")
		expectNoErr(t, tracker.Adopt(prepared))
		expectEqual(t, base, baseSnapshot)
		expectSame(t, tracker.Value(), prepared.Value, "tracker.value")
	})

	t.Run("makes repeated placements independent", func(t *testing.T) {
		tracker := Track(parse(t, `{"values":[]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		shared := parse(t, `{"value":1}`)
		state["values"] = append(arr(t, state["values"]), shared, shared)
		prepared := mustPrepare(t, change)
		values := arr(t, obj(t, prepared.Value)["values"])
		expectNotSame(t, values[0], values[1], "placements")
	})
}

func TestTransactionalOverlayDrafts(t *testing.T) {
	t.Run("copies only changed branches", func(t *testing.T) {
		tracker := Track(parse(t, `{"changed":{"count":1,"sibling":{"value":"kept"}},"untouched":{"value":2}}`))
		base := tracker.Value()
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		first := state["changed"]
		expectSame(t, state["changed"], first, "state.changed")
		obj(t, first)["count"] = 3.0
		prepared := mustPrepare(t, change)
		expectNotSame(t, prepared.Value, base, "value")
		expectNotSame(t, obj(t, prepared.Value)["changed"], obj(t, base)["changed"], "changed")
		expectSame(t, obj(t, obj(t, prepared.Value)["changed"])["sibling"], obj(t, obj(t, base)["changed"])["sibling"], "sibling")
		expectSame(t, obj(t, prepared.Value)["untouched"], obj(t, base)["untouched"], "untouched")
	})

	t.Run("supports deletion and the array mutators", func(t *testing.T) {
		// state-draft.test.ts:26-43: push, pop, unshift, shift, splice, sort, reverse, fill and copyWithin, each spelled with its Go slice equivalent.
		tracker := Track(parse(t, `{"optional":"remove","values":[3,1,2]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		delete(state, "optional")
		values := arr(t, state["values"])
		values = append(values, 4.0)
		values = values[:len(values)-1]
		values = slices.Insert(values, 0, 0.0)
		values = values[1:]
		values = slices.Replace(values, 1, 2, 5.0, 4.0)
		slices.SortFunc(values, func(a, b any) int { return int(a.(float64) - b.(float64)) })
		slices.Reverse(values)
		values[1], values[2] = 9.0, 9.0
		copy(values[1:3], slices.Clone(values[0:2]))
		state["values"] = values
		prepared := mustPrepare(t, change)
		expectJSON(t, prepared.Value, `{"values":[5,5,9,2]}`)
	})

	t.Run("copies inserted draft values without aliasing their original handle", func(t *testing.T) {
		// state-draft.test.ts:45-55. Go places by reference, so the case keeps the contract that two placements of one draft container become independent containers.
		tracker := Track(parse(t, `{"values":[{"value":1},{"value":2}]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := arr(t, state["values"])
		state["values"] = slices.Insert(values, 0, values[0])
		prepared := mustPrepare(t, change)
		inserted := arr(t, obj(t, prepared.Value)["values"])
		expectJSON(t, inserted, `[{"value":1},{"value":1},{"value":2}]`)
		expectNotSame(t, inserted[0], inserted[1], "placements")
	})

	t.Run("drops writes through detached child handles", func(t *testing.T) {
		tracker := Track(parse(t, `{"child":{"value":"removed"},"items":[{"value":"removed"},{"value":"kept"}]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		child := obj(t, state["child"])
		items := arr(t, state["items"])
		shifted := obj(t, items[0])
		state["items"] = items[1:]
		delete(state, "child")
		child["value"] = "detached child"
		shifted["value"] = "detached item"
		expectJSON(t, mustPrepare(t, change).Value, `{"items":[{"value":"kept"}]}`)
	})

	for _, method := range []string{"unshift", "splice"} {
		t.Run(fmt.Sprintf("inserts 100,000 items with %s without argument overflow", method), func(t *testing.T) {
			tracker := Track(parse(t, `{"values":[-1]}`))
			change := tracker.BeginChange()
			state := obj(t, mustState(t, change))
			items := make([]any, 100_000)
			for at := range items {
				items[at] = float64(at)
			}
			if method == "unshift" {
				state["values"] = append(items, arr(t, state["values"])...)
			} else {
				state["values"] = slices.Insert(arr(t, state["values"]), 1, items...)
			}
			prepared := mustPrepare(t, change)
			values := arr(t, obj(t, prepared.Value)["values"])
			offset := 0
			if method == "splice" {
				offset = 1
			}
			if len(values) != 100_001 || values[offset] != 0.0 || values[offset+99_999] != 99_999.0 {
				t.Fatalf("len=%d", len(values))
			}
			expectEqual(t, mustApplyImmutable(t, prepared.Base, prepared.Ops), prepared.Value)
		})
	}

	t.Run("rejects non-strict JSON placements without mutating the base", func(t *testing.T) {
		// state-draft.test.ts:95-115. Go has no undefined and no property descriptors; the checks that apply are NaN, a function, a non-plain value (the Date case) and a cycle. Validation happens in Prepare, which settles the change as aborted.
		initial := parse(t, `{"number":0,"payload":null,"values":[1,2]}`)
		tracker := Track(initial)
		type namedValue struct{ A int }
		for _, tc := range []struct {
			name    string
			place   func(state map[string]any)
			pattern string
		}{
			{"NaN", func(s map[string]any) { s["number"] = nan() }, "strict JSON"},
			{"infinity", func(s map[string]any) { s["number"] = inf() }, "strict JSON"},
			{"function", func(s map[string]any) { s["payload"] = map[string]any{"nested": func() {}} }, "strict JSON"},
			{"channel", func(s map[string]any) { s["payload"] = map[string]any{"nested": make(chan int)} }, "strict JSON"},
			{"struct", func(s map[string]any) { s["payload"] = namedValue{1} }, "plain objects"},
			{"cycle", func(s map[string]any) {
				cyclic := map[string]any{}
				cyclic["self"] = cyclic
				s["payload"] = cyclic
			}, "cycles"},
		} {
			change := tracker.BeginChange()
			tc.place(obj(t, mustState(t, change)))
			_, err := change.Prepare()
			expectErr(t, err, tc.pattern)
			expectTypeError(t, err)
			_, stateErr := change.State()
			expectTypeError(t, stateErr)
		}
		expectJSON(t, tracker.Value(), `{"number":0,"payload":null,"values":[1,2]}`)
		expectSame(t, tracker.Value(), initial, "tracker.value")

		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state["payload"] = map[string]any{"valid": true}
		state["values"] = append(arr(t, state["values"]), map[string]any{"valid": true})
		state["number"] = 7 // a Go int is a number and becomes float64
		prepared := mustPrepare(t, change)
		expectJSON(t, prepared.Value, `{"number":7,"payload":{"valid":true},"values":[1,2,{"valid":true}]}`)
		if _, isFloat := obj(t, prepared.Value)["number"].(float64); !isFloat {
			t.Fatalf("number kept its Go type: %T", obj(t, prepared.Value)["number"])
		}
		expectEqual(t, replay(t, initial, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})
}

// state-fuzz.test.ts

type fuzzDocument = map[string]any

func mulberry32(seed int32) func() float64 {
	return func() float64 {
		seed += 0x6d2b79f5
		s := uint32(seed)
		value := int32(imul(s^(s>>15), 1|s))
		value = (value + int32(imul(uint32(value)^(uint32(value)>>7), 61|uint32(value)))) ^ value
		return float64(uint32(value)^(uint32(value)>>14)) / 4_294_967_296
	}
}

// imul is Math.imul on the unsigned bit patterns.
func imul(a, b uint32) uint32 { return a * b }

func fuzzMutate(t *testing.T, document fuzzDocument, choice, value int) {
	item := func() any {
		return map[string]any{"id": float64(value), "text": fmt.Sprintf("item-%d", value), "score": float64(value % 7)}
	}
	items := arr(t, document["items"])
	text := document["text"].(string)
	meta := obj(t, document["meta"])
	switch choice {
	case 0:
		document["text"] = text + fmt.Sprintf("-%d", value)
	case 1:
		document["text"] = text[min(2, len(text)):] + fmt.Sprint(value)
	case 2:
		document["items"] = append(items, item())
	case 3:
		document["items"] = slices.Insert(items, 0, item())
	case 4:
		if len(items) > 0 {
			document["items"] = items[1:]
		}
	case 5:
		if len(items) > 0 {
			document["items"] = items[:len(items)-1]
		}
	case 6:
		index, remove := 0, 0
		if len(items) > 0 {
			index, remove = value%(len(items)+1), value%2
		}
		document["items"] = slices.Replace(items, index, min(index+remove, len(items)), item())
	case 7:
		slices.Reverse(items)
	case 8:
		slices.SortStableFunc(items, func(a, b any) int { return int(obj(t, a)["id"].(float64) - obj(t, b)["id"].(float64)) })
	case 9:
		if len(items) > 0 {
			obj(t, items[value%len(items)])["score"] = float64(value)
		}
	case 10:
		meta["revision"] = meta["revision"].(float64) + 1
		meta["label"] = fmt.Sprintf("revision-%d", value)
	case 11:
		delete(meta, "label")
	case 12:
		if len(items) > 1 {
			items[1] = clone(t, items[0])
		}
	default:
		for at := range min(2, len(items)) {
			items[at] = item()
		}
	}
}

func TestStateFuzzConvergesAcrossRandomizedPreparedRevisions(t *testing.T) {
	for seed := 1; seed <= 100; seed++ {
		rng := mulberry32(int32(seed))
		initial := fuzzDocument{
			"items": rows(4, func(id int) any {
				return map[string]any{"id": float64(id), "text": fmt.Sprintf("item-%d", id), "score": 0.0}
			}),
			"text": "start",
			"meta": map[string]any{"revision": 0.0},
		}
		tracker := Track(initial)
		expected := clone(t, initial).(map[string]any)
		replica := clone(t, tracker.Value())
		for step := range 100 {
			choice := int(rng() * 14)
			value := seed*1_000 + step
			baseRoot := tracker.Value()
			base := clone(t, baseRoot)
			fuzzMutate(t, expected, choice, value)
			change := tracker.BeginChange()
			fuzzMutate(t, obj(t, mustState(t, change)), choice, value)
			prepared := mustPrepare(t, change)
			operations := ops(t, mustJSON(t, prepared.Ops))
			where := fmt.Sprintf("seed %d step %d choice %d", seed, step, choice)
			if !equalJson(baseRoot, base) {
				t.Fatalf("prepare changed base: %s", where)
			}
			expectSame(t, prepared.Base, baseRoot, "prepared.base "+where)
			replica = mustApplyImmutable(t, replica, operations)
			if !equalJson(replica, prepared.Value) {
				t.Fatalf("replay diverged: %s", where)
			}
			expectNoErr(t, tracker.Adopt(prepared))
			if !equalJson(baseRoot, base) {
				t.Fatalf("adopt changed base: %s", where)
			}
			expectSame(t, tracker.Value(), prepared.Value, "tracker.value "+where)
			if !equalJson(tracker.Value(), expected) || !equalJson(replica, expected) {
				t.Fatalf("state diverged: %s", where)
			}
			expectAliasFree(t, tracker.Value())
		}
	}
}
