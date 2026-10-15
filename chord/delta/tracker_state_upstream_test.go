package delta

import (
	"errors"
	"fmt"
	"testing"
)

// Ports packages/chord/test/delta-clone.test.ts, state-value.test.ts, state-draft.test.ts and state-fuzz.test.ts onto the overlay Tracker, whose Object and Array handles are upstream's Proxy draft. Go mapping as in tracker_upstream_test.go. Upstream's Object.isFrozen checks assert that nothing is frozen, which holds in Go because nothing can be; a null prototype does not exist in Go, so that case keeps its alias-free half.

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectSameContainer(t *testing.T, got, want any, what string) {
	t.Helper()
	if !sameContainer(got, want) {
		t.Fatalf("%s: not the same container", what)
	}
}

func expectDistinctContainer(t *testing.T, got, want any, what string) {
	t.Helper()
	if sameContainer(got, want) {
		t.Fatalf("%s: the same container", what)
	}
}

func field(t *testing.T, value any, keys ...any) any {
	t.Helper()
	for _, key := range keys {
		switch typed := key.(type) {
		case string:
			object, ok := value.(*JsonObject)
			if !ok {
				t.Fatalf("%T has no key %q", value, typed)
			}
			value = object.Value(typed)
		case int:
			array, ok := value.([]any)
			if !ok || typed >= len(array) {
				t.Fatalf("%T has no index %d", value, typed)
			}
			value = array[typed]
		}
	}
	return value
}

func TestTrackerOwnership(t *testing.T) {
	t.Run("takes immutable ownership of the imported revision in O(1)", func(t *testing.T) {
		// delta-clone.test.ts:5-15
		input := jsonObject(t, `{"point":{"x":3,"y":7,"pressure":0.1},"rows":[{"values":[0,false,null,"text",{"n":1}]}]}`)
		tracker := Track(input)
		expectSameContainer(t, tracker.Value(), input, "tracker.value")
		expectSameContainer(t, field(t, tracker.Value(), "point"), field(t, input, "point"), "point")
		expectSameContainer(t, field(t, tracker.Value(), "rows", 0, "values", 4), field(t, input, "rows", 0, "values", 4), "rows[0].values[4]")
	})

	t.Run("preserves null prototypes in an alias-free owned root", func(t *testing.T) {
		// delta-clone.test.ts:17-25
		dictionary := jsonObject(t, `{"enabled":true,"child":{"n":1}}`)
		input := JsonObjectOf("dictionary", dictionary, "left", jsonOf(t, `{"nested":[{"n":1}]}`), "right", jsonOf(t, `{"nested":[{"n":1}]}`))
		tracker := Track(input)
		expectSameContainer(t, field(t, tracker.Value(), "dictionary"), dictionary, "dictionary")
		expectDistinctContainer(t, field(t, tracker.Value(), "left"), field(t, tracker.Value(), "right"), "left/right")
		expectDistinctContainer(t, field(t, tracker.Value(), "left", "nested", 0), field(t, tracker.Value(), "right", "nested", 0), "nested rows")
	})

	t.Run("does not traverse a trusted root while taking ownership", func(t *testing.T) {
		// delta-clone.test.ts:27-39. The throwing accessor is a value no walk accepts: Track must not look at it.
		input := JsonObjectOf("untrustedAccessor", make(chan int))
		tracker := Track(input)
		expectSameContainer(t, tracker.Value(), input, "tracker.value")
	})

	t.Run("copies assigned and inserted values immediately", func(t *testing.T) {
		// delta-clone.test.ts:41-52
		tracker := Track(jsonObject(t, `{"rows":[]}`))
		assigned := jsonObject(t, `{"nested":{"value":1}}`)
		change := tracker.BeginChange()
		_, err := change.State().Array("rows").Push(assigned)
		mustNoErr(t, err)
		assigned.Value("nested").(*JsonObject).Set("value", 9.0)
		if got := change.State().Array("rows").Object(0).Object("nested").Get("value"); got != 1.0 {
			t.Fatalf("the draft sees the caller's later write: %v", got)
		}
		prepared := mustPrepare(t, change)
		if got := field(t, prepared.Value(), "rows", 0, "nested", "value"); got != 1.0 {
			t.Fatalf("prepared value %v, want 1", got)
		}
		replica, err := Apply(clone(t, prepared.Base()), prepared.Ops())
		mustNoErr(t, err)
		expectJSONText(t, replica, textOf(t, prepared.Value()))
	})
}

func TestStateValueOwnership(t *testing.T) {
	t.Run("takes ownership of an alias-free mutable JSON root without freezing", func(t *testing.T) {
		// state-value.test.ts:5-12
		input := jsonObject(t, `{"left":{"value":1},"right":{"value":1}}`)
		tracker := Track(input)
		expectSameContainer(t, tracker.Value(), input, "tracker.value")
		expectDistinctContainer(t, field(t, tracker.Value(), "left"), field(t, tracker.Value(), "right"), "left/right")
	})

	t.Run("commits transaction copies in place while sharing unchanged branches", func(t *testing.T) {
		// state-value.test.ts:14-27
		tracker := Track(jsonObject(t, `{"changed":{"value":1},"retained":{"value":2}}`))
		base := tracker.Value()
		baseSnapshot := textOf(t, base)
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Object("changed").Set("value", 3.0))
		prepared := mustPrepare(t, change)
		expectDistinctContainer(t, field(t, prepared.Value(), "changed"), field(t, base, "changed"), "changed")
		expectSameContainer(t, field(t, prepared.Value(), "retained"), field(t, base, "retained"), "retained")
		mustNoErr(t, tracker.Adopt(prepared))
		expectJSONText(t, base, baseSnapshot)
		expectSameContainer(t, tracker.Value(), prepared.Value(), "tracker.value")
	})

	t.Run("makes repeated placements independent", func(t *testing.T) {
		// state-value.test.ts:29-36
		tracker := Track(jsonObject(t, `{"values":[]}`))
		change := tracker.BeginChange()
		shared := jsonObject(t, `{"value":1}`)
		_, err := change.State().Array("values").Push(shared, shared)
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectDistinctContainer(t, field(t, prepared.Value(), "values", 0), field(t, prepared.Value(), "values", 1), "placements")
	})
}

func TestTransactionalOverlayDrafts(t *testing.T) {
	t.Run("copies only changed branches", func(t *testing.T) {
		// state-draft.test.ts:5-20. A Go handle is a new value per read, so the identity of change.state.changed is the identity of the node it wraps: a write through the first handle is seen through the second.
		tracker := Track(jsonObject(t, `{"changed":{"count":1,"sibling":{"value":"kept"}},"untouched":{"value":2}}`))
		base := tracker.Value()
		change := tracker.BeginChange()
		first := change.State().Object("changed")
		mustNoErr(t, change.State().Object("changed").Set("count", 3.0))
		if first.Get("count") != 3.0 {
			t.Fatal("two reads of change.state.changed are different drafts")
		}
		prepared := mustPrepare(t, change)
		expectDistinctContainer(t, prepared.Value(), base, "value")
		expectDistinctContainer(t, field(t, prepared.Value(), "changed"), field(t, base, "changed"), "changed")
		expectSameContainer(t, field(t, prepared.Value(), "changed", "sibling"), field(t, base, "changed", "sibling"), "changed.sibling")
		expectSameContainer(t, field(t, prepared.Value(), "untouched"), field(t, base, "untouched"), "untouched")
	})

	t.Run("supports frozen bases, deletion, and native array mutators", func(t *testing.T) {
		// state-draft.test.ts:22-37
		tracker := Track(jsonObject(t, `{"optional":"remove","values":[3,1,2]}`))
		change := tracker.BeginChange()
		change.State().Delete("optional")
		values := change.State().Array("values")
		_, err := values.Push(4.0)
		mustNoErr(t, err)
		if got := values.Pop(); got != 4.0 {
			t.Fatalf("pop %v", got)
		}
		_, err = values.Unshift(0.0)
		mustNoErr(t, err)
		if got := values.Shift(); got != 0.0 {
			t.Fatalf("shift %v", got)
		}
		removed, err := values.Splice(1, 1, 5.0, 4.0)
		mustNoErr(t, err)
		expectJSONText(t, removed, `[1]`)
		values.Sort(func(left, right any) bool { return left.(float64) < right.(float64) })
		values.Reverse()
		mustNoErr(t, values.Fill(9.0, 1, 3))
		mustNoErr(t, values.CopyWithin(1, 0, 2))
		expectJSONText(t, mustPrepare(t, change).Value(), `{"values":[5,5,9,2]}`)
	})

	t.Run("copies inserted draft values without aliasing their original handle", func(t *testing.T) {
		// state-draft.test.ts:39-48
		tracker := Track(jsonObject(t, `{"values":[{"value":1},{"value":2}]}`))
		change := tracker.BeginChange()
		values := change.State().Array("values")
		held := values.Object(0)
		_, err := values.Unshift(held)
		mustNoErr(t, err)
		mustNoErr(t, held.Set("value", 9.0))
		prepared := mustPrepare(t, change)
		expectJSONText(t, field(t, prepared.Value(), "values"), `[{"value":1},{"value":9},{"value":2}]`)
		expectDistinctContainer(t, field(t, prepared.Value(), "values", 0), field(t, prepared.Value(), "values", 1), "placements")
	})

	t.Run("drops writes through detached child handles", func(t *testing.T) {
		// state-draft.test.ts:68-77
		tracker := Track(jsonObject(t, `{"child":{"value":"removed"},"items":[{"value":"removed"},{"value":"kept"}]}`))
		change := tracker.BeginChange()
		child := change.State().Object("child")
		shifted := change.State().Array("items").Shift()
		change.State().Delete("child")
		mustNoErr(t, child.Set("value", "detached child"))
		if shifted, ok := shifted.(*JsonObject); ok {
			shifted.Set("value", "detached item")
		}
		expectJSONText(t, mustPrepare(t, change).Value(), `{"items":[{"value":"kept"}]}`)
	})

	for _, method := range []string{"unshift", "splice"} {
		t.Run(fmt.Sprintf("inserts 100,000 items with %s without argument overflow", method), func(t *testing.T) {
			// state-draft.test.ts:79-90
			tracker := Track(jsonObject(t, `{"values":[-1]}`))
			change := tracker.BeginChange()
			items := make([]any, 100_000)
			for value := range items {
				items[value] = float64(value)
			}
			values := change.State().Array("values")
			var err error
			if method == "unshift" {
				_, err = values.Unshift(items...)
			} else {
				_, err = values.Splice(1, 0, items...)
			}
			mustNoErr(t, err)
			prepared := mustPrepare(t, change)
			inserted := field(t, prepared.Value(), "values").([]any)
			first, last := 0, 99_999
			if method == "splice" {
				first, last = 1, 100_000
			}
			if len(inserted) != 100_001 || inserted[first] != 0.0 || inserted[last] != 99_999.0 {
				t.Fatalf("len %d, [%d]=%v, [%d]=%v", len(inserted), first, inserted[first], last, inserted[last])
			}
			applied, err := ApplyImmutable(prepared.Base(), prepared.Ops())
			mustNoErr(t, err)
			expectJSONText(t, applied, textOf(t, prepared.Value()))
		})
	}

	t.Run("rejects non-strict JSON placements without mutating the draft or base", func(t *testing.T) {
		// state-draft.test.ts:92-108. Go has no undefined, so the undefined element and the {nested: undefined} push are a channel and a function, the values Go has that are not JSON; the Date is a struct.
		tracker := Track(jsonObject(t, `{"number":0,"payload":null,"values":[1,2]}`))
		change := tracker.BeginChange()
		state := change.State()
		expectStrictJSONError(t, state.Array("values").Set(0, make(chan int)), "strict JSON")
		_, err := state.Array("values").Push(JsonObjectOf("nested", func() {}))
		expectStrictJSONError(t, err, "strict JSON")
		expectStrictJSONError(t, state.Set("number", nan()), "strict JSON")
		type date struct{ Time int64 }
		expectStrictJSONError(t, state.Set("payload", date{}), "plain objects")
		expectJSONText(t, state.Snapshot(), textOf(t, tracker.Value()))
		change.Abort()
		expectJSONText(t, tracker.Value(), `{"number":0,"payload":null,"values":[1,2]}`)
	})
}

func expectStrictJSONError(t *testing.T, err error, pattern string) {
	t.Helper()
	if !errors.Is(err, ErrNotStrictJSON) {
		t.Fatalf("got %v, want a strict-JSON TypeError", err)
	}
	expectErr(t, err, pattern)
}

// state-fuzz.test.ts:40-88 mutate, on a draft.
func fuzzMutateDraft(t *testing.T, document *Object, choice, value int) {
	t.Helper()
	item := func() any {
		return JsonObjectOf("id", float64(value), "text", fmt.Sprintf("item-%d", value), "score", float64(value%7))
	}
	items := document.Array("items")
	text := document.Get("text").(string)
	switch choice {
	case 0:
		mustNoErr(t, document.Set("text", text+fmt.Sprintf("-%d", value)))
	case 1:
		mustNoErr(t, document.Set("text", text[min(2, len(text)):]+fmt.Sprint(value)))
	case 2:
		_, err := items.Push(item())
		mustNoErr(t, err)
	case 3:
		_, err := items.Unshift(item())
		mustNoErr(t, err)
	case 4:
		if items.Len() > 0 {
			items.Shift()
		}
	case 5:
		if items.Len() > 0 {
			items.Pop()
		}
	case 6:
		index, remove := 0, 0
		if items.Len() > 0 {
			index, remove = value%(items.Len()+1), value%2
		}
		_, err := items.Splice(index, remove, item())
		mustNoErr(t, err)
	case 7:
		items.Reverse()
	case 8:
		items.Sort(func(left, right any) bool {
			return left.(*Object).Get("id").(float64) < right.(*Object).Get("id").(float64)
		})
	case 9:
		if items.Len() > 0 {
			mustNoErr(t, items.Object(value%items.Len()).Set("score", float64(value)))
		}
	case 10:
		meta := document.Object("meta")
		mustNoErr(t, meta.Set("revision", meta.Get("revision").(float64)+1))
		mustNoErr(t, meta.Set("label", fmt.Sprintf("revision-%d", value)))
	case 11:
		document.Object("meta").Delete("label")
	case 12:
		if items.Len() > 1 {
			mustNoErr(t, items.Set(1, items.Object(0).Snapshot()))
		}
	default:
		for index := range min(2, items.Len()) {
			mustNoErr(t, items.Set(index, item()))
		}
	}
}

func TestStateFuzzConvergesAcrossRandomizedPreparedRevisions(t *testing.T) {
	// state-fuzz.test.ts:90-125
	for seed := 1; seed <= 100; seed++ {
		rng := mulberry32(int32(seed))
		initial := JsonObjectOf(
			"items", rows(4, func(id int) any {
				return JsonObjectOf("id", float64(id), "text", fmt.Sprintf("item-%d", id), "score", 0.0)
			}),
			"text", "start",
			"meta", JsonObjectOf("revision", 0.0),
		)
		expected := clone(t, initial).(*JsonObject)
		tracker := Track(initial)
		replica := clone(t, tracker.Value())
		for step := range 100 {
			choice := int(rng() * 14)
			value := seed*1_000 + step
			where := fmt.Sprintf("seed %d step %d choice %d", seed, step, choice)
			baseRoot := tracker.Value()
			base := textOf(t, baseRoot)
			fuzzMutate(t, expected, choice, value)
			change := tracker.BeginChange()
			fuzzMutateDraft(t, change.State(), choice, value)
			prepared := mustPrepare(t, change)
			operations := ops(t, mustJSON(t, prepared.Ops()))
			if textOf(t, baseRoot) != base {
				t.Fatalf("prepare changed the base: %s", where)
			}
			expectSameContainer(t, prepared.Base(), baseRoot, "prepared.base "+where)
			replica = mustApplyImmutable(t, replica, operations)
			if textOf(t, replica) != textOf(t, prepared.Value()) {
				t.Fatalf("replay diverged: %s", where)
			}
			mustNoErr(t, tracker.Adopt(prepared))
			if textOf(t, baseRoot) != base {
				t.Fatalf("adopt changed the base: %s", where)
			}
			expectSameContainer(t, tracker.Value(), prepared.Value(), "tracker.value "+where)
			if textOf(t, tracker.Value()) != textOf(t, expected) || textOf(t, replica) != textOf(t, expected) {
				t.Fatalf("state diverged: %s\n got %s\nwant %s", where, textOf(t, tracker.Value()), textOf(t, expected))
			}
			expectAliasFreeValue(t, tracker.Value())
		}
	}
}
