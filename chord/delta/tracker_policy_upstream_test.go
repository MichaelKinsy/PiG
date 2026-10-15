package delta

import (
	"fmt"
	"slices"
	"testing"
)

// Ports the "delta tracker policy view" cases of packages/chord/test/delta-tracker/tracker.test.ts that tracker_upstream_test.go and tracker_equivalence_test.go do not, onto the overlay Tracker. Go mapping: a Proxy property read is Get or Object/Array on a handle, `undefined` assignment is Delete (JSON has no undefined), and `length = n` is SetLen.
func TestTrackerPolicyViewCases(t *testing.T) {
	t.Run("emits deletion and supports root arrays", func(t *testing.T) {
		// tracker.test.ts:409-422
		objectTracker := Track(jsonObject(t, `{"keep":1,"remove":2}`))
		settle(t, objectTracker, func(draft *Object) { draft.Delete("remove") })
		expectJSONText(t, objectTracker.Value(), `{"keep":1}`)

		arrayTracker := TrackArray(jsonOf(t, `[1,2,3]`).([]any))
		change := arrayTracker.BeginChange()
		draft := change.Elements()
		draft.Reverse()
		_, err := draft.Push(4.0)
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectJSONText(t, replay(t, arrayTracker.Root(), prepared.Ops()), `[3,2,1,4]`)
		mustNoErr(t, arrayTracker.Adopt(prepared))
		expectJSONText(t, arrayTracker.Root(), `[3,2,1,4]`)
	})

	t.Run("normalizes deeply equal container assignments before direct candidate materialization", func(t *testing.T) {
		// tracker.test.ts:452-465. The equal child keeps the base key order, as a replica replaying the operations has it.
		initial := jsonObject(t, `{"child":{"a":1,"b":2},"count":0}`)
		tracker := Track(initial)
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("child", jsonOf(t, `{"b":2,"a":1}`)))
		mustNoErr(t, change.State().Set("count", 1.0))
		prepared := mustPrepare(t, change)
		replayed := replay(t, initial, prepared.Ops())
		expectJSONText(t, opsAsJSON(prepared.Ops()), `[["s",["count"],1]]`)
		for what, root := range map[string]any{"prepared": prepared.Value(), "replayed": replayed} {
			if got := field(t, root, "child").(*JsonObject).Keys(); !slices.Equal(got, []string{"a", "b"}) {
				t.Fatalf("%s child keys = %v, want [a b]", what, got)
			}
		}
		expectJSONText(t, prepared.Value(), textOf(t, replayed))
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("keeps repeated identical writes instead of reverting their pending override", func(t *testing.T) {
		// tracker.test.ts:609-621
		tracker := Track(jsonObject(t, `{"value":0,"values":[0]}`))
		settle(t, tracker, func(draft *Object) {
			mustNoErr(t, draft.Set("value", 1.0))
			mustNoErr(t, draft.Set("value", 1.0))
			values := draft.Array("values")
			mustNoErr(t, values.Set(0, nil))
			mustNoErr(t, values.Set(0, nil))
			_, err := values.Push(2.0)
			mustNoErr(t, err)
			mustNoErr(t, values.Set(1, nil))
			mustNoErr(t, values.Set(1, nil))
		})
		expectJSONText(t, tracker.Value(), `{"value":1,"values":[null,null]}`)
	})

	t.Run("rejects array index gaps without mutation while allowing replacement and append", func(t *testing.T) {
		// tracker.test.ts:623-639
		tracker := Track(jsonObject(t, `{"values":[1,2]}`))
		change := tracker.BeginChange()
		values := change.State().Array("values")
		expectErr(t, values.Set(3, 4.0), "holes")
		expectJSONText(t, values.Snapshot(), `[1,2]`)
		expectJSONText(t, tracker.Value(), `{"values":[1,2]}`)

		mustNoErr(t, values.Set(1, 9.0))
		mustNoErr(t, values.Set(2, 3.0))
		prepared := mustPrepare(t, change)
		expectJSONText(t, field(t, prepared.Value(), "values"), `[1,9,3]`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
		expectJSONText(t, field(t, tracker.Value(), "values"), `[1,9,3]`)
	})

	t.Run("supports optional deletion, explicit null length growth, shrinking, and default sort", func(t *testing.T) {
		// tracker.test.ts:641-652. Array.prototype.sort without a comparator orders by String(value); a Go slice element cannot be deleted, so the hole check of `delete values[0]` is TestTrackerRejectsArrayHolesWithoutMutatingTheBase's.
		tracker := Track(jsonObject(t, `{"optional":"remove","values":[3,1,2]}`))
		settle(t, tracker, func(draft *Object) {
			draft.Delete("optional")
			values := draft.Array("values")
			values.SetLen(5)
			expectJSONText(t, values.Snapshot(), `[3,1,2,null,null]`)
			values.SetLen(4)
			values.Sort(func(a, b any) bool { return jsText(a) < jsText(b) })
		})
		expectJSONText(t, tracker.Value(), `{"values":[1,2,3,null]}`)
	})

	t.Run("enumerates wide objects without duplicate keys", func(t *testing.T) {
		// tracker.test.ts:654-663
		tracker := Track(jsonObject(t, `{"values":{}}`))
		change := tracker.BeginChange()
		values := change.State().Object("values")
		for index := range 20_000 {
			mustNoErr(t, values.Set(fmt.Sprintf("field%d", index), float64(index)))
		}
		keys := values.Keys()
		if len(keys) != 20_000 || keys[0] != "field0" || keys[len(keys)-1] != "field19999" {
			t.Fatalf("%d keys from %q to %q", len(keys), keys[0], keys[len(keys)-1])
		}
		change.Abort()
	})

	t.Run("does not dirty read-only traversals", func(t *testing.T) {
		// tracker.test.ts:665-672
		tracker := Track(jsonObject(t, `{"nested":{"rows":[{"value":1}]}}`))
		change := tracker.BeginChange()
		if got := change.State().Object("nested").Array("rows").Object(0).Get("value"); got != 1.0 {
			t.Fatalf("rows[0].value = %v", got)
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 0 {
			t.Fatalf("prepared.ops = %s", opsText(t, prepared.Ops()))
		}
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("uses one proxy identity per accessed container", func(t *testing.T) {
		// tracker.test.ts:674-681. A handle is a value; two reads of one container give equal handles.
		tracker := Track(jsonObject(t, `{"nested":{"value":1},"rows":[{"value":2}]}`))
		change := tracker.BeginChange()
		state := change.State()
		//lint:ignore SA4000 two reads of one container must give the same handle; comparing them is the test.
		if *state.Object("nested") != *state.Object("nested") || *state.Array("rows") != *state.Array("rows") || *state.Array("rows").Object(0) != *state.Array("rows").Object(0) {
			t.Fatal("two reads of one container gave different handles")
		}
		if *state.Object("nested") == *change.State() {
			t.Fatal("different containers gave the same handle")
		}
		change.Abort()
	})
}

// jsText is String(value) for the JSON primitives a default Array.prototype.sort compares.
func jsText(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}
