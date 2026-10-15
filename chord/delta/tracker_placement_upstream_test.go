package delta

import (
	"fmt"
	"testing"
)

// Ports the "delta tracker by-value placements" cases of packages/chord/test/delta-tracker/tracker.test.ts that TestTrackerByValuePlacementsOfEveryArrayMutator does not, onto the overlay Tracker. A placement is copied when it is made, so a caller's later edit to the source never reaches the draft.
func TestTrackerByValuePlacementCases(t *testing.T) {
	t.Run("rejects non-strict JSON placements before changing the draft", func(t *testing.T) {
		// tracker.test.ts:713-786. Go has no undefined, symbol, bigint, sparse array or accessor property: undefined is a channel, a symbol a complex number and a bigint a function, each a value Go has that JSON does not; the Date is a struct, and a cycle is an object that holds itself. `optional = undefined` is Delete, and a JsonObject has no prototype.
		initial := jsonObject(t, `{"optional":"remove","payload":null,"number":0,"values":[1,2]}`)
		tracker := Track(initial)
		change := tracker.BeginChange()
		state := change.State()
		values := state.Array("values")
		undefinedValue := make(chan int)

		expectStrictJSONError(t, values.Set(0, undefinedValue), "strict JSON")
		_, err := values.Push(JsonObjectOf("staged", true), undefinedValue)
		expectStrictJSONError(t, err, "strict JSON")
		_, err = values.Unshift(undefinedValue)
		expectStrictJSONError(t, err, "strict JSON")
		_, err = values.Splice(1, 0, undefinedValue)
		expectStrictJSONError(t, err, "strict JSON")
		expectStrictJSONError(t, values.Fill(undefinedValue, 0, values.Len()), "strict JSON")
		for _, nested := range []any{undefinedValue, func() int { return 1 }, complex(1, 1)} {
			expectStrictJSONError(t, state.Set("payload", JsonObjectOf("nested", nested)), "strict JSON")
		}
		expectStrictJSONError(t, state.Set("number", nan()), "strict JSON")
		expectStrictJSONError(t, state.Set("number", inf()), "strict JSON")
		type date struct{ Time int64 }
		expectStrictJSONError(t, state.Set("payload", date{}), "plain objects")
		cyclic := NewJsonObject(1)
		cyclic.Set("self", cyclic)
		expectStrictJSONError(t, state.Set("payload", cyclic), "cycles")

		expectJSONText(t, state.Snapshot(), textOf(t, initial))
		state.Delete("optional")
		mustNoErr(t, state.Set("payload", JsonObjectOf("valid", true)))
		_, err = values.Push(JsonObjectOf("valid", true))
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"payload":{"valid":true},"number":0,"values":[1,2,{"valid":true}]}`)
		expectJSONText(t, replay(t, initial, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("expands repeated source aliases into independent placements", func(t *testing.T) {
		// tracker.test.ts:788-811
		tracker := Track(jsonObject(t, `{"left":null,"right":null,"rows":[]}`))
		shared := jsonObject(t, `{"nested":{"value":1}}`)
		change := tracker.BeginChange()
		state := change.State()
		mustNoErr(t, state.Set("left", shared))
		mustNoErr(t, state.Set("right", shared))
		_, err := state.Array("rows").Push(shared, shared)
		mustNoErr(t, err)
		mustNoErr(t, state.Object("left").Object("nested").Set("value", 9.0))
		mustNoErr(t, state.Array("rows").Object(0).Object("nested").Set("value", 8.0))
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"left":{"nested":{"value":9}},"right":{"nested":{"value":1}},"rows":[{"nested":{"value":8}},{"nested":{"value":1}}]}`)
		expectDistinctContainer(t, field(t, prepared.Value(), "left"), field(t, prepared.Value(), "right"), "left and right")
		expectDistinctContainer(t, field(t, prepared.Value(), "rows", 0), field(t, prepared.Value(), "rows", 1), "rows[0] and rows[1]")
		expectJSONText(t, shared, `{"nested":{"value":1}}`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("deep-clones draft-sourced placements within and across revisions", func(t *testing.T) {
		// tracker.test.ts:813-841
		tracker := Track(jsonObject(t, `{"a":{"child":{"value":1}},"b":null,"rows":[{"child":{"value":1}},{"child":{"value":2}}]}`))
		first := tracker.BeginChange()
		state := first.State()
		mustNoErr(t, state.Set("b", state.Object("a")))
		mustNoErr(t, state.Object("b").Object("child").Set("value", 3.0))
		rows := state.Array("rows")
		mustNoErr(t, rows.Set(1, rows.Object(0)))
		firstPrepared := mustPrepare(t, first)
		value := firstPrepared.Value()
		if field(t, value, "a", "child", "value") != 1.0 || field(t, value, "b", "child", "value") != 3.0 {
			t.Fatalf("first revision %s", textOf(t, value))
		}
		expectDistinctContainer(t, field(t, value, "a"), field(t, value, "b"), "a and b")
		expectDistinctContainer(t, field(t, value, "a", "child"), field(t, value, "b", "child"), "a.child and b.child")
		expectDistinctContainer(t, field(t, value, "rows", 0), field(t, value, "rows", 1), "rows[0] and rows[1]")
		expectDistinctContainer(t, field(t, value, "rows", 0, "child"), field(t, value, "rows", 1, "child"), "the row children")
		expectAliasFree(t, value)
		mustNoErr(t, tracker.Adopt(firstPrepared))

		base := clone(t, tracker.Value())
		second := tracker.BeginChange()
		mustNoErr(t, second.State().Array("rows").Object(1).Object("child").Set("value", 4.0))
		secondPrepared := mustPrepare(t, second)
		expectJSONText(t, field(t, secondPrepared.Value(), "rows"), `[{"child":{"value":1}},{"child":{"value":4}}]`)
		expectJSONText(t, replay(t, base, secondPrepared.Ops()), textOf(t, secondPrepared.Value()))
		expectAliasFree(t, secondPrepared.Value())
		mustNoErr(t, tracker.Adopt(secondPrepared))
	})

	t.Run("distinguishes raw committed references from draft references", func(t *testing.T) {
		// tracker.test.ts:843-857
		tracker := Track(jsonObject(t, `{"source":{"value":1},"rawCopy":null,"draftCopy":null}`))
		raw := field(t, tracker.Value(), "source")
		change := tracker.BeginChange()
		state := change.State()
		mustNoErr(t, state.Object("source").Set("value", 2.0))
		mustNoErr(t, state.Set("rawCopy", raw))
		mustNoErr(t, state.Set("draftCopy", state.Object("source")))
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"source":{"value":2},"rawCopy":{"value":1},"draftCopy":{"value":2}}`)
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("folds edits to introduced object and array subtrees into placement payloads", func(t *testing.T) {
		// tracker.test.ts:859-877
		tracker := Track(jsonObject(t, `{"nested":null,"rows":[]}`))
		change := tracker.BeginChange()
		state := change.State()
		mustNoErr(t, state.Set("nested", jsonOf(t, `{"rows":[{"value":1}]}`)))
		mustNoErr(t, state.Object("nested").Array("rows").Object(0).Set("value", 2.0))
		_, err := state.Object("nested").Array("rows").Push(jsonOf(t, `{"value":3}`))
		mustNoErr(t, err)
		_, err = state.Array("rows").Push(jsonOf(t, `{"values":[1]}`))
		mustNoErr(t, err)
		_, err = state.Array("rows").Object(0).Array("values").Push(2.0)
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectJSONText(t, opsAsJSON(prepared.Ops()), `[["s",["nested"],{"rows":[{"value":2},{"value":3}]}],["p",["rows"],0,0,[{"values":[1,2]}]]]`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("folds pathological operation counts without payload-cost estimation", func(t *testing.T) {
		// tracker.test.ts:879-903
		wide := NewJsonObject(5_000)
		for index := range 5_000 {
			wide.Set(fmt.Sprintf("field%d", index), 0.0)
		}
		wideTracker := Track(wide)
		wideBase := clone(t, wideTracker.Value())
		wideChange := wideTracker.BeginChange()
		for index := range 5_000 {
			mustNoErr(t, wideChange.State().Set(fmt.Sprintf("field%d", index), 1.0))
		}
		widePrepared := mustPrepare(t, wideChange)
		if len(widePrepared.Ops()) != 1 || Verb(widePrepared.Ops()[0]) != "r" {
			t.Fatalf("%d wide operations, want one replacement", len(widePrepared.Ops()))
		}
		expectJSONText(t, replay(t, wideBase, widePrepared.Ops()), textOf(t, widePrepared.Value()))
		mustNoErr(t, wideTracker.Adopt(widePrepared))

		sparseTracker := Track(JsonObjectOf("rows", rows(15_000, func(at int) any { return JsonObjectOf("value", float64(at)) })))
		sparseBase := clone(t, sparseTracker.Value())
		sparseChange := sparseTracker.BeginChange()
		sparseRows := sparseChange.State().Array("rows")
		for index := 0; index < 15_000; index += 3 {
			mustNoErr(t, sparseRows.Object(index).Set("value", float64(-index-1)))
		}
		sparsePrepared := mustPrepare(t, sparseChange)
		if len(sparsePrepared.Ops()) != 1 || Verb(sparsePrepared.Ops()[0]) != "r" {
			t.Fatalf("%d sparse operations, want one replacement", len(sparsePrepared.Ops()))
		}
		expectJSONText(t, replay(t, sparseBase, sparsePrepared.Ops()), textOf(t, sparsePrepared.Value()))
		mustNoErr(t, sparseTracker.Adopt(sparsePrepared))
	})

	t.Run("shares immutable operation placements with the materialized candidate", func(t *testing.T) {
		// tracker.test.ts:905-923. Go operations hold no draft handle (isProxy), and the splice payload is the candidate's row.
		tracker := Track(jsonObject(t, `{"rows":[]}`))
		change := tracker.BeginChange()
		_, err := change.State().Array("rows").Push(jsonOf(t, `{"value":1}`))
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		pending := []any{prepared.Ops()}
		for len(pending) > 0 {
			value := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			switch typed := value.(type) {
			case *Object, *Array:
				t.Fatalf("an operation holds a draft handle %T", typed)
			case []Op:
				for _, op := range typed {
					pending = append(pending, op)
				}
			case []any:
				pending = append(pending, typed...)
			case *JsonObject:
				for _, item := range typed.All() {
					pending = append(pending, item)
				}
			}
		}
		splice := prepared.Ops()[0]
		if Verb(splice) != "p" {
			t.Fatalf("expected splice, got %s", opsText(t, prepared.Ops()))
		}
		expectSameContainer(t, splice[4].([]any)[0], field(t, prepared.Value(), "rows", 0), "the splice payload")
		mustNoErr(t, tracker.Adopt(prepared))
		if tracker.Value() != prepared.Value() {
			t.Fatal("adoption must install prepared.value")
		}
	})
}
