package delta

import (
	"fmt"
	"slices"
	"testing"
)

// Ports the "delta tracker piece arrays" cases of packages/chord/test/delta-tracker/tracker.test.ts that tracker_upstream_test.go and tracker_state_upstream_test.go do not, onto the overlay Tracker. Go mapping: Array.Sort takes a less function for the comparator, Splice takes its start and delete count explicitly (Pi's omitted delete count is the rest of the array), and a mutator returns its native result where it has one.
func TestTrackerPieceArrayCases(t *testing.T) {
	ascending := func(left, right any) bool { return left.(float64) < right.(float64) }

	t.Run("emits one insert for an append to a long array", func(t *testing.T) {
		// tracker.test.ts:927-936 (Pi 1.2.0)
		values := make([]any, 10_000)
		for index := range values {
			values[index] = float64(index)
		}
		tracker := Track(JsonObjectOf("values", values))
		change := tracker.BeginChange()
		if _, err := change.State().Array("values").Push(-1.0); err != nil {
			t.Fatal(err)
		}
		prepared := mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[["p",["values"],10000,0,[-1]]]` {
			t.Fatalf("ops = %s", got)
		}
		mustNoErr(t, tracker.Adopt(prepared))
		adopted := field(t, tracker.Value(), "values").([]any)
		if len(adopted) != 10_001 || adopted[len(adopted)-1] != -1.0 {
			t.Fatalf("adopted length %d, last %v", len(adopted), adopted[len(adopted)-1])
		}
	})

	t.Run("emits removals from the end backwards, then inserts, when kept elements keep their order", func(t *testing.T) {
		// tracker.test.ts:938-962 (Pi 1.2.0)
		mutate := func(values *Array) {
			values.Shift()                      // gap before the first kept element
			_, err := values.Splice(1, 2, 20.0) // gap between kept elements, with an insert
			mustNoErr(t, err)
			_, err = values.Splice(5, 1) // another gap
			mustNoErr(t, err)
			values.Pop() // gap after the last kept element
			_, err = values.Push(30.0)
			mustNoErr(t, err)
		}
		tracker := Track(jsonObject(t, `{"values":[0,1,2,3,4,5,6,7,8,9]}`))
		change := tracker.BeginChange()
		mutate(change.State().Array("values"))
		prepared := mustPrepare(t, change)
		expectJSONText(t, field(t, prepared.Value(), "values"), `[1,20,4,5,6,8,30]`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		want := `[["p",["values"],9,1,[]],["p",["values"],7,1,[]],["p",["values"],2,2,[]],["p",["values"],0,1,[]],["p",["values"],1,0,[20]],["p",["values"],6,0,[30]]]`
		if got := opsText(t, prepared.Ops()); got != want {
			t.Fatalf("ops = %s\nwant %s", got, want)
		}
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("emits a permutation when kept elements change order", func(t *testing.T) {
		// tracker.test.ts:964-978 (Pi 1.2.0)
		parity := func(left, right any) bool {
			l, r := int(left.(float64)), int(right.(float64))
			if l%2 != r%2 {
				return l%2 < r%2
			}
			return l < r
		}
		for name, test := range map[string]struct {
			mutate   func(values *Array)
			expected string
		}{
			"reverse": {func(values *Array) { values.Reverse() }, `[4,3,2,1,0]`},
			"sort":    {func(values *Array) { values.Sort(parity) }, `[0,2,4,1,3]`},
		} {
			t.Run(name, func(t *testing.T) {
				tracker := Track(jsonObject(t, `{"values":[0,1,2,3,4]}`))
				change := tracker.BeginChange()
				test.mutate(change.State().Array("values"))
				prepared := mustPrepare(t, change)
				if !slices.ContainsFunc(prepared.Ops(), func(op Op) bool { return Verb(op) == "m" }) {
					t.Fatalf("no permutation in %s", opsText(t, prepared.Ops()))
				}
				expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), `{"values":`+test.expected+`}`)
			})
		}
	})

	t.Run("keeps adoption independent from mutable detached permutation metadata", func(t *testing.T) {
		// tracker.test.ts:927-937
		tracker := Track(jsonObject(t, `{"values":[3,1,2]}`))
		change := tracker.BeginChange()
		change.State().Array("values").Sort(ascending)
		prepared := mustPrepare(t, change)
		at := slices.IndexFunc(prepared.Ops(), func(op Op) bool { return Verb(op) == "m" })
		if at < 0 {
			t.Fatalf("expected a permutation, got %s", opsText(t, prepared.Ops()))
		}
		slices.Reverse(prepared.Ops()[at][2].([]any))
		mustNoErr(t, tracker.Adopt(prepared))
		expectJSONText(t, field(t, tracker.Value(), "values"), `[1,2,3]`)
	})

	t.Run("matches native splice with zero or one argument", func(t *testing.T) {
		// tracker.test.ts:939-962. splice() is Splice(0, 0); splice(start) removes the rest.
		noArguments := Track(jsonObject(t, `{"values":[1,2,3]}`))
		noArgumentsChange := noArguments.BeginChange()
		removed, err := noArgumentsChange.State().Array("values").Splice(0, 0)
		mustNoErr(t, err)
		expectJSONText(t, removed, `[]`)
		noArgumentsPrepared := mustPrepare(t, noArgumentsChange)
		expectJSONText(t, field(t, noArgumentsPrepared.Value(), "values"), `[1,2,3]`)
		if len(noArgumentsPrepared.Ops()) != 0 {
			t.Fatalf("splice() emitted %s", opsText(t, noArgumentsPrepared.Ops()))
		}
		mustNoErr(t, noArguments.Adopt(noArgumentsPrepared))

		oneArgument := Track(jsonObject(t, `{"values":[1,2,3,4]}`))
		oneArgumentChange := oneArgument.BeginChange()
		values := oneArgumentChange.State().Array("values")
		removed, err = values.Splice(1, values.Len()-1)
		mustNoErr(t, err)
		expectJSONText(t, removed, `[2,3,4]`)
		oneArgumentPrepared := mustPrepare(t, oneArgumentChange)
		expectJSONText(t, field(t, oneArgumentPrepared.Value(), "values"), `[1]`)
		expectJSONText(t, replay(t, oneArgument.Value(), oneArgumentPrepared.Ops()), textOf(t, oneArgumentPrepared.Value()))
		mustNoErr(t, oneArgument.Adopt(oneArgumentPrepared))

		pastEnd := Track(jsonObject(t, `{"values":[1,2,3]}`))
		pastEndChange := pastEnd.BeginChange()
		removed, err = pastEndChange.State().Array("values").Splice(3, 0)
		mustNoErr(t, err)
		expectJSONText(t, removed, `[]`)
		pastEndPrepared := mustPrepare(t, pastEndChange)
		expectJSONText(t, field(t, pastEndPrepared.Value(), "values"), `[1,2,3]`)
		mustNoErr(t, pastEnd.Adopt(pastEndPrepared))
	})

	t.Run("supports all structural mutators and native return values", func(t *testing.T) {
		// tracker.test.ts:964-981. sort and reverse return the receiver in JavaScript and nothing in Go.
		tracker := Track(jsonObject(t, `{"values":[3,1,2]}`))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := change.State().Array("values")
		if length, err := values.Push(4.0); err != nil || length != 4 {
			t.Fatalf("push = %d, %v", length, err)
		}
		if popped := values.Pop(); popped != 4.0 {
			t.Fatalf("pop = %v", popped)
		}
		if length, err := values.Unshift(0.0); err != nil || length != 4 {
			t.Fatalf("unshift = %d, %v", length, err)
		}
		if shifted := values.Shift(); shifted != 0.0 {
			t.Fatalf("shift = %v", shifted)
		}
		removed, err := values.Splice(1, 1, 5.0, 4.0)
		mustNoErr(t, err)
		expectJSONText(t, removed, `[1]`)
		values.Sort(ascending)
		values.Reverse()
		mustNoErr(t, values.Fill(9.0, 1, 3))
		mustNoErr(t, values.CopyWithin(1, 0, 2))
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"values":[5,5,9,2]}`)
		expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("tracks held handles after reindex and suppresses detached writes", func(t *testing.T) {
		// tracker.test.ts:983-996
		tracker := Track(jsonObject(t, `{"values":[{"value":"a"},{"value":"b"},{"value":"c"}]}`))
		change := tracker.BeginChange()
		values := change.State().Array("values")
		held := values.Object(1)
		_, err := values.Unshift(jsonOf(t, `{"value":"front"}`))
		mustNoErr(t, err)
		mustNoErr(t, held.Set("value", "moved"))
		if got := values.Object(2).Get("value"); got != "moved" {
			t.Fatalf("values[2].value = %v, want moved", got)
		}
		_, err = values.Splice(2, 1)
		mustNoErr(t, err)
		mustNoErr(t, held.Set("value", "detached"))
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"values":[{"value":"front"},{"value":"a"},{"value":"c"}]}`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("replays introduced, moved, then edited array descendants", func(t *testing.T) {
		// tracker.test.ts:998-1017
		tracker := Track(jsonObject(t, `{"values":[]}`))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := change.State().Array("values")
		_, err := values.Push(jsonOf(t, `{"id":1,"nested":[1]}`))
		mustNoErr(t, err)
		held := values.Object(0)
		_, err = values.Unshift(jsonOf(t, `{"id":0,"nested":[]}`))
		mustNoErr(t, err)
		_, err = held.Array("nested").Push(2.0)
		mustNoErr(t, err)
		values.Reverse()
		_, err = held.Array("nested").Push(3.0)
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"values":[{"id":1,"nested":[1,2,3]},{"id":0,"nested":[]}]}`)
		expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("combines movement and edits with permutation plus final paths", func(t *testing.T) {
		// tracker.test.ts:1019-1036
		tracker := Track(jsonObject(t, `{"values":[{"rank":3,"edited":0},{"rank":1,"edited":0},{"rank":2,"edited":0}]}`))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := change.State().Array("values")
		held := values.Object(0)
		values.Sort(func(left, right any) bool {
			return left.(*Object).Get("rank").(float64) < right.(*Object).Get("rank").(float64)
		})
		mustNoErr(t, held.Set("edited", 1.0))
		prepared := mustPrepare(t, change)
		expectJSONText(t, opsAsJSON(prepared.Ops()[:1]), `[["m",["values"],[1,2,0]]]`)
		expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("normalizes a 20,000-operation queue transaction to two operations", func(t *testing.T) {
		// tracker.test.ts:1198-1210
		tracker := Track(JsonObjectOf("values", valueObjects(20_000)))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := change.State().Array("values")
		for index := range 10_000 {
			values.Shift()
			_, err := values.Push(JsonObjectOf("value", float64(20_000+index)))
			mustNoErr(t, err)
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 2 {
			t.Fatalf("%d operations, want 2", len(prepared.Ops()))
		}
		expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("handles adversarial fragmentation and resolves held handles without piece scans", func(t *testing.T) {
		// tracker.test.ts:1212-1229
		const size = 20_000
		heldAt := []int{1, 1_001, 5_001, 10_001, 15_001, 19_999}
		tracker := Track(JsonObjectOf("values", valueObjects(size)))
		expected := valueObjects(size)
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := change.State().Array("values")
		held := make([]*Object, len(heldAt))
		for position, index := range heldAt {
			held[position] = values.Object(index)
		}
		for index := 0; index < size; index += 2 {
			_, err := values.Splice(index, 1, JsonObjectOf("value", float64(-index-1)))
			mustNoErr(t, err)
			expected[index] = JsonObjectOf("value", float64(-index-1))
		}
		for _, value := range held {
			mustNoErr(t, value.Set("value", value.Get("value").(float64)+100_000))
		}
		for _, index := range heldAt {
			row := expected[index].(*JsonObject)
			current, _ := row.Get("value")
			row.Set("value", current.(float64)+100_000)
		}
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), textOf(t, JsonObjectOf("values", expected)))
		expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("normalizes restored overrides and cancelled structural edits to no operations", func(t *testing.T) {
		// tracker.test.ts:1231-1244
		tracker := Track(jsonObject(t, `{"values":[1,2,3]}`))
		change := tracker.BeginChange()
		values := change.State().Array("values")
		mustNoErr(t, values.Set(1, 9.0))
		mustNoErr(t, values.Set(1, 2.0))
		values.Reverse()
		values.Reverse()
		_, err := values.Push(4.0)
		mustNoErr(t, err)
		if popped := values.Pop(); popped != 4.0 {
			t.Fatalf("pop = %v", popped)
		}
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"values":[1,2,3]}`)
		if len(prepared.Ops()) != 0 {
			t.Fatalf("prepared.ops = %s", opsText(t, prepared.Ops()))
		}
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("handles null sparse overrides without confusing absence", func(t *testing.T) {
		// tracker.test.ts:1246-1254
		tracker := Track(JsonObjectOf("values", rows(10_000, func(at int) any { return float64(at) })))
		settle(t, tracker, func(draft *Object) {
			mustNoErr(t, draft.Array("values").Set(17, nil))
			mustNoErr(t, draft.Array("values").Set(9_000, nil))
		})
		for _, index := range []int{17, 9_000} {
			if got := field(t, tracker.Value(), "values", index); got != nil {
				t.Fatalf("values[%d] = %v, want null", index, got)
			}
		}
		if got := field(t, tracker.Value(), "values", 18); got != 18.0 {
			t.Fatalf("values[18] = %v, want 18", got)
		}
	})

	for _, method := range []string{"unshift", "splice"} {
		t.Run(fmt.Sprintf("inserts 100,000 items with %s", method), func(t *testing.T) {
			// tracker.test.ts:1267-1284
			tracker := Track(jsonObject(t, `{"values":[-1]}`))
			base := clone(t, tracker.Value())
			change := tracker.BeginChange()
			items := rows(100_000, func(at int) any { return float64(at) })
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
			last := 100_000
			if method == "unshift" {
				last = 99_999
			}
			if len(inserted) != 100_001 || inserted[last] != 99_999.0 {
				t.Fatalf("%d values, [%d] = %v", len(inserted), last, inserted[last])
			}
			expectJSONText(t, replay(t, base, prepared.Ops()), textOf(t, prepared.Value()))
			mustNoErr(t, tracker.Adopt(prepared))
		})
	}
}

// valueObjects is Array.from({length}, (_, value) => ({value})).
func valueObjects(length int) []any {
	return rows(length, func(at int) any { return JsonObjectOf("value", float64(at)) })
}
