package delta

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the remaining cases of packages/chord/test/delta-tracker/tracker.test.ts. See tracker_upstream_test.go for the Go mapping of the Proxy overlay. Cases that only exercise Proxy traps, native Array coercion, JavaScript key ordering or engine-specific sort calls are listed in the test-mapping rationale instead of here.

func TestTrackerTransactionalOverlayLifecycle(t *testing.T) {
	t.Run("materializes an immutable next revision and adopts it by pointer swap", func(t *testing.T) {
		initial := parse(t, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		tracker := trackCopy(initial)
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("count", 2.0)
		nested := obj(t, state.Value("nested"))
		nested.Set("text", nested.Value("text").(string)+"b")
		state.Set("values", append(arr(t, state.Value("values")), 2.0))
		expectJSON(t, initial, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		prepared := mustPrepareCopy(t, change)
		next := prepared.Value
		if prepared.BaseRevision != 0 {
			t.Fatalf("baseRevision=%d", prepared.BaseRevision)
		}
		expectSame(t, prepared.Base, initial, "prepared.base")
		expectNotSame(t, next, initial, "next")
		expectJSON(t, next, `{"count":2,"nested":{"text":"ab"},"values":[1,2]}`)
		expectJSON(t, prepared.Ops, `[["s",["count"],2],["a",["nested","text"],"b"],["p",["values"],1,0,[2]]]`)
		expectSame(t, tracker.Value(), initial, "tracker.value before adopt")
		expectNoErr(t, tracker.Adopt(prepared))
		expectJSON(t, initial, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		expectSame(t, tracker.Value(), next, "tracker.value")
		expectSame(t, prepared.Value, next, "prepared.value")
		expectSame(t, prepared.Base, initial, "prepared.base after")
		_, err := change.State()
		expectTypeError(t, err)
	})

	t.Run("keeps a transaction live across await and seals it only at prepare", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"left":0,"nested":{"right":0}}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("left", 1.0)
		obj(t, state.Value("nested")).Set("right", 2.0)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"left":1,"nested":{"right":2}}`)
		_, err := change.State()
		expectErr(t, err, "settled")
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("allows Promise resolution to inspect a settled draft without reporting a false failure", func(t *testing.T) {
		// tracker.test.ts:89-116. The Promise-thenable probing is JavaScript only. The document key named "then" and the large-array traversal keep their Go meaning.
		tracker := trackCopy(parse(t, `{"then":"document-value","value":1}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		if state.Value("then") != "document-value" {
			t.Fatalf("then=%v", state.Value("then"))
		}
		state.Set("value", 2.0)
		prepared := mustPrepareCopy(t, change)
		_, err := change.State()
		expectErr(t, err, "settled")
		expectNoErr(t, tracker.Adopt(prepared))
		expectJSON(t, tracker.Value(), `{"then":"document-value","value":2}`)

		large := trackCopy(chordjson.ObjectOf("rows", rows(4_100, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		largeChange := large.BeginChange()
		largeRows := arr(t, obj(t, mustState(t, largeChange)).Value("rows"))
		for _, row := range largeRows {
			if obj(t, row).Value("value").(float64) < 0 {
				t.Fatal("negative row")
			}
		}
		obj(t, largeRows[0]).Set("value", -1.0)
		largePrepared := mustPrepareCopy(t, largeChange)
		_, err = largeChange.State()
		expectErr(t, err, "settled")
		expectNoErr(t, large.Adopt(largePrepared))
	})

	t.Run("aborts idempotently and revokes held descendants", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"child":{"value":1}}`))
		change := tracker.BeginChange()
		obj(t, obj(t, mustState(t, change)).Value("child")).Set("value", 2.0)
		change.Abort()
		change.Abort()
		expectJSON(t, tracker.Value(), `{"child":{"value":1}}`)
		_, err := change.State()
		expectTypeError(t, err)
		_, err = change.Prepare()
		expectErr(t, err, "settled")
	})

	t.Run("allows competing contexts and invalidates loser views", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0}`))
		first, second := tracker.BeginChange(), tracker.BeginChange()
		obj(t, mustState(t, first)).Set("value", 1.0)
		obj(t, mustState(t, second)).Set("value", 2.0)
		firstPrepared, secondPrepared := mustPrepareCopy(t, first), mustPrepareCopy(t, second)
		held := secondPrepared.Value
		expectNoErr(t, tracker.Adopt(firstPrepared))
		expectJSON(t, tracker.Value(), `{"value":1}`)
		expectJSON(t, held, `{"value":2}`)
		expectErr(t, tracker.Adopt(secondPrepared), "stale")
		expectErr(t, tracker.Adopt(firstPrepared), "already been used")
	})

	t.Run("invalidates competing open overlays without changing their base revision", func(t *testing.T) {
		initial := parse(t, `{"rows":[{"value":0},{"value":1}]}`)
		tracker := trackCopy(initial)
		staleChange := tracker.BeginChange()
		obj(t, arr(t, obj(t, mustState(t, staleChange)).Value("rows"))[1]).Set("value", 2.0)

		winner := tracker.BeginChange()
		obj(t, arr(t, obj(t, mustState(t, winner)).Value("rows"))[0]).Set("value", 3.0)
		expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, winner)))

		expectJSON(t, initial, `{"rows":[{"value":0},{"value":1}]}`)
		expectJSON(t, tracker.Value(), `{"rows":[{"value":3},{"value":1}]}`)
		_, err := staleChange.State()
		expectErr(t, err, "settled")
		_, err = staleChange.Prepare()
		expectErr(t, err, "settled")
		staleChange.Abort()
	})

	t.Run("adopts object edits as ordinary fast-layout immutable revisions", func(t *testing.T) {
		initial := parse(t, `{"first":1,"second":2}`)
		tracker := trackCopy(initial)
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("first", 3.0)
		state.Delete("second")
		state.Set("third", 4.0)
		expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, change)))
		expectNotSame(t, tracker.Value(), initial, "value")
		expectJSON(t, initial, `{"first":1,"second":2}`)
		expectJSON(t, tracker.Value(), `{"first":3,"third":4}`)
	})

	t.Run("supports adopt-then-publish ordering and retained publication reads", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0,"nested":{"count":0}}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("value", 1.0)
		obj(t, state.Value("nested")).Set("count", 1.0)
		prepared := mustPrepareCopy(t, change)
		operations := prepared.Ops
		expectNoErr(t, tracker.Adopt(prepared))
		if len(operations) != 2 || len(prepared.Ops) != 2 {
			t.Fatalf("ops=%v", operations)
		}
		published := prepared.Value
		expectSame(t, published, tracker.Value(), "published")
		expectJSON(t, published, `{"value":1,"nested":{"count":1}}`)

		next := tracker.BeginChange()
		obj(t, mustState(t, next)).Set("value", 2.0)
		expectNoErr(t, tracker.Adopt(mustPrepareCopy(t, next)))
		expectNotSame(t, published, tracker.Value(), "published after")
		expectJSON(t, prepared.Value, `{"value":1,"nested":{"count":1}}`)
		expectJSON(t, tracker.Value(), `{"value":2,"nested":{"count":1}}`)
	})

	t.Run("keeps replacement base/value structurally compatible and readable after adoption", func(t *testing.T) {
		original, replacement := parse(t, `{"value":0}`), parse(t, `{"value":1}`)
		tracker := trackCopy(original)
		prepared := tracker.PrepareReplace(replacement)
		expectSame(t, prepared.Base, original, "base")
		expectSame(t, prepared.Value, replacement, "value")
		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, prepared.Base, original, "base after")
		expectSame(t, prepared.Value, replacement, "value after")
		expectSame(t, prepared.Value, tracker.Value(), "tracker.value")
	})

	t.Run("keeps aborted and stale materialized candidates readable", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0}`))
		abortedChange := tracker.BeginChange()
		obj(t, mustState(t, abortedChange)).Set("value", 1.0)
		aborted := mustPrepareCopy(t, abortedChange)
		abortedView := aborted.Value
		aborted.Abort()
		expectJSON(t, abortedView, `{"value":1}`)
		expectJSON(t, aborted.Value, `{"value":1}`)

		loserChange := tracker.BeginChange()
		obj(t, mustState(t, loserChange)).Set("value", 2.0)
		loser := mustPrepareCopy(t, loserChange)
		loserView := loser.Value
		expectNoErr(t, tracker.Adopt(tracker.PrepareReplace(parse(t, `{"value":3}`))))
		expectJSON(t, loserView, `{"value":2}`)
		expectJSON(t, loser.Value, `{"value":2}`)
	})

	t.Run("lets a settled Change abort its prepared result without retaining its context", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0,"nested":{"value":1}}`))
		change := tracker.BeginChange()
		obj(t, mustState(t, change)).Set("value", 1.0)
		prepared := mustPrepareCopy(t, change)
		operations := prepared.Ops
		change.Abort()
		change.Abort()
		if len(prepared.Ops) != len(operations) {
			t.Fatal("ops changed")
		}
		expectJSON(t, prepared.Value, `{"value":1,"nested":{"value":1}}`)
		expectErr(t, tracker.Adopt(prepared), "aborted")
	})

	t.Run("rejects foreign and aborted prepared values", func(t *testing.T) {
		first, second := trackCopy(parse(t, `{"value":0}`)), trackCopy(parse(t, `{"value":0}`))
		prepared := first.PrepareReplace(parse(t, `{"value":1}`))
		expectErr(t, second.Adopt(prepared), "different tracker")
		prepared.Abort()
		prepared.Abort()
		expectErr(t, first.Adopt(prepared), "aborted")
	})

	t.Run("reads deleted own properties as absent", func(t *testing.T) {
		// tracker.test.ts:267-291 also probes inherited prototype properties, which Go maps do not have.
		tracker := trackCopy(parse(t, `{"value":1}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Delete("value")
		state.Set("trackerInherited", 1.0)
		state.Delete("trackerInherited")
		if _, present := state.Get("value"); present {
			t.Fatal("deleted property is present")
		}
		prepared := mustPrepareCopy(t, change)
		expectNoErr(t, tracker.Adopt(prepared))
		if obj(t, tracker.Value()).Len() != 0 {
			t.Fatalf("keys=%v", tracker.Value())
		}
	})

	t.Run("detaches old handles for deeply equal object and array assignments before normalizing", func(t *testing.T) {
		initial := parse(t, `{"object":{"nested":{"value":1}},"array":[{"value":1}],"rows":[{"value":1}]}`)
		tracker := trackCopy(initial)
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("object", parse(t, `{"nested":{"value":1}}`))
		state.Set("array", parse(t, `[{"value":1}]`))
		arr(t, state.Value("rows"))[0] = parse(t, `{"value":1}`)
		prepared := mustPrepareCopy(t, change)
		if len(prepared.Ops) != 0 {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, tracker.Value(), initial, "value")
		expectSame(t, obj(t, tracker.Value()).Value("object"), obj(t, initial).Value("object"), "object")
		expectSame(t, obj(t, tracker.Value()).Value("array"), obj(t, initial).Value("array"), "array")
		expectSame(t, arr(t, obj(t, tracker.Value()).Value("rows"))[0], arr(t, obj(t, initial).Value("rows"))[0], "rows[0]")
	})

	t.Run("normalizes a deeply equal replacement without changing committed identity", func(t *testing.T) {
		initial := parse(t, `{"nested":{"value":1},"rows":[1,2,3]}`)
		tracker := trackCopy(initial)
		prepared := tracker.PrepareReplace(parse(t, `{"nested":{"value":1},"rows":[1,2,3]}`))
		if len(prepared.Ops) != 0 {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, tracker.Value(), initial, "value")
	})

	t.Run("takes O(1) immutable ownership for replacement and its operation payload", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0,"rows":[]}`))
		replacement := parse(t, `{"value":1,"rows":[{"value":2}]}`)
		prepared := tracker.PrepareReplace(replacement)
		expectSame(t, prepared.Base, tracker.Value(), "base")
		expectSame(t, prepared.Value, replacement, "value")
		expectSame(t, prepared.Ops[0][1], replacement, "payload")
		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, tracker.Value(), replacement, "tracker.value")
	})
}

func TestTrackerPolicyView(t *testing.T) {
	t.Run("emits deletion and supports root arrays", func(t *testing.T) {
		objectTracker := trackCopy(parse(t, `{"keep":1,"remove":2}`))
		settleCopy(t, objectTracker, func(state any) any { obj(t, state).Delete("remove"); return nil })
		expectJSON(t, objectTracker.Value(), `{"keep":1}`)

		arrayTracker := trackCopy(parse(t, `[1,2,3]`))
		settleCopy(t, arrayTracker, func(state any) any {
			values := arr(t, state)
			slices.Reverse(values)
			return append(values, 4.0)
		})
		expectJSON(t, arrayTracker.Value(), `[3,2,1,4]`)
	})

	t.Run("normalizes deeply equal container assignments before direct candidate materialization", func(t *testing.T) {
		initial := parse(t, `{"child":{"a":1,"b":2},"count":0}`)
		tracker := trackCopy(initial)
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("child", parse(t, `{"b":2,"a":1}`))
		state.Set("count", 1.0)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Ops, `[["s",["count"],1]]`)
		// upstream: packages/chord/test/delta-tracker/tracker.test.ts:461-462: the equal child keeps the base key order, as a replica replaying the operations has it.
		if got := obj(t, obj(t, prepared.Value).Value("child")).Keys(); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("prepared child keys = %v, want [a b]", got)
		}
		if got := obj(t, obj(t, replay(t, initial, prepared.Ops)).Value("child")).Keys(); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("replayed child keys = %v, want [a b]", got)
		}
		expectSame(t, obj(t, prepared.Value).Value("child"), obj(t, initial).Value("child"), "child")
		expectEqual(t, prepared.Value, replay(t, initial, prepared.Ops))
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("keeps repeated identical writes instead of reverting their pending override", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"value":0,"values":[0]}`))
		settleCopy(t, tracker, func(state any) any {
			document := obj(t, state)
			document.Set("value", 1.0)
			document.Set("value", 1.0)
			values := arr(t, document.Value("values"))
			values[0] = nil
			values[0] = nil
			values = append(values, 2.0)
			values[1] = nil
			values[1] = nil
			document.Set("values", values)
			return nil
		})
		expectJSON(t, tracker.Value(), `{"value":1,"values":[null,null]}`)
	})

	t.Run("rejects array index gaps without mutation while allowing replacement and append", func(t *testing.T) {
		// tracker.test.ts:623-639. A Go slice has no holes to reject; replacement and append keep their meaning.
		tracker := trackCopy(parse(t, `{"values":[1,2]}`))
		change := tracker.BeginChange()
		values := arr(t, obj(t, mustState(t, change)).Value("values"))
		values[1] = 9.0
		obj(t, mustState(t, change)).Set("values", append(values, 3.0))
		expectJSON(t, tracker.Value(), `{"values":[1,2]}`)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"values":[1,9,3]}`)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
		expectJSON(t, tracker.Value(), `{"values":[1,9,3]}`)
	})

	t.Run("supports optional deletion, explicit null length growth, shrinking, and default sort", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"optional":"remove","values":[3,1,2]}`))
		settleCopy(t, tracker, func(state any) any {
			document := obj(t, state)
			document.Delete("optional")
			values := append(arr(t, document.Value("values")), nil, nil)
			expectJSON(t, values, `[3,1,2,null,null]`)
			values = values[:4]
			slices.SortFunc(values[:3], func(a, b any) int { return int(a.(float64) - b.(float64)) })
			document.Set("values", values)
			return nil
		})
		expectJSON(t, tracker.Value(), `{"values":[1,2,3,null]}`)
	})

	t.Run("enumerates wide objects without duplicate keys", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":{}}`))
		change := tracker.BeginChange()
		values := obj(t, obj(t, mustState(t, change)).Value("values"))
		for at := range 20_000 {
			values.Set(fmt.Sprintf("field%d", at), float64(at))
		}
		if values.Len() != 20_000 {
			t.Fatalf("keys=%d", values.Len())
		}
		change.Abort()
	})

	t.Run("does not dirty read-only traversals", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"nested":{"rows":[{"value":1}]}}`))
		change := tracker.BeginChange()
		rows := arr(t, obj(t, obj(t, mustState(t, change)).Value("nested")).Value("rows"))
		if obj(t, rows[0]).Value("value") != 1.0 {
			t.Fatal("read")
		}
		prepared := mustPrepareCopy(t, change)
		if len(prepared.Ops) != 0 {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("uses one container identity per accessed container", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"nested":{"value":1},"rows":[{"value":2}]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		expectSame(t, state.Value("nested"), obj(t, mustState(t, change)).Value("nested"), "nested")
		expectSame(t, arr(t, state.Value("rows"))[0], arr(t, obj(t, mustState(t, change)).Value("rows"))[0], "rows[0]")
		change.Abort()
	})
}

func TestTrackerByValuePlacements(t *testing.T) {
	t.Run("expands repeated source aliases into independent placements", func(t *testing.T) {
		// tracker.test.ts:788-811 edits through the aliases after placing them; Go aliases share edits, so the case keeps the independence of the prepared containers.
		tracker := trackCopy(parse(t, `{"left":null,"right":null,"rows":[]}`))
		shared := parse(t, `{"nested":{"value":1}}`)
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("left", shared)
		state.Set("right", shared)
		state.Set("rows", append(arr(t, state.Value("rows")), shared, shared))
		prepared := mustPrepareCopy(t, change)
		value := obj(t, prepared.Value)
		expectEqual(t, value, parse(t, `{"left":{"nested":{"value":1}},"right":{"nested":{"value":1}},"rows":[{"nested":{"value":1}},{"nested":{"value":1}}]}`))
		expectNotSame(t, value.Value("left"), value.Value("right"), "left/right")
		rowsValue := arr(t, value.Value("rows"))
		expectNotSame(t, rowsValue[0], rowsValue[1], "rows")
		expectAliasFree(t, prepared.Value)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("deep-clones draft-sourced placements within and across revisions", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"a":{"child":{"value":1}},"b":null,"rows":[{"child":{"value":1}},{"child":{"value":2}}]}`))
		first := tracker.BeginChange()
		state := obj(t, mustState(t, first))
		state.Set("b", state.Value("a"))
		rowsValue := arr(t, state.Value("rows"))
		rowsValue[1] = rowsValue[0]
		firstPrepared := mustPrepareCopy(t, first)
		value := obj(t, firstPrepared.Value)
		expectNotSame(t, value.Value("a"), value.Value("b"), "a/b")
		expectNotSame(t, obj(t, value.Value("a")).Value("child"), obj(t, value.Value("b")).Value("child"), "children")
		expectAliasFree(t, firstPrepared.Value)
		expectNoErr(t, tracker.Adopt(firstPrepared))

		base := clone(t, tracker.Value())
		second := tracker.BeginChange()
		secondRows := arr(t, obj(t, mustState(t, second)).Value("rows"))
		obj(t, obj(t, secondRows[1]).Value("child")).Set("value", 4.0)
		secondPrepared := mustPrepareCopy(t, second)
		expectJSON(t, arr(t, obj(t, secondPrepared.Value).Value("rows")), `[{"child":{"value":1}},{"child":{"value":4}}]`)
		expectEqual(t, replay(t, base, secondPrepared.Ops), secondPrepared.Value)
		expectAliasFree(t, secondPrepared.Value)
		expectNoErr(t, tracker.Adopt(secondPrepared))
	})

	t.Run("distinguishes raw committed references from draft references", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"source":{"value":1},"rawCopy":null,"draftCopy":null}`))
		raw := obj(t, tracker.Value()).Value("source")
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		obj(t, state.Value("source")).Set("value", 2.0)
		state.Set("rawCopy", raw)
		state.Set("draftCopy", state.Value("source"))
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"source":{"value":2},"rawCopy":{"value":1},"draftCopy":{"value":2}}`)
		expectJSON(t, raw, `{"value":1}`)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("folds edits to introduced object and array subtrees into placement payloads", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"nested":null,"rows":[]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		nested := parse(t, `{"rows":[{"value":1}]}`)
		obj(t, arr(t, obj(t, nested).Value("rows"))[0]).Set("value", 2.0)
		obj(t, nested).Set("rows", append(arr(t, obj(t, nested).Value("rows")), parse(t, `{"value":3}`)))
		state.Set("nested", nested)
		row := parse(t, `{"values":[1]}`)
		obj(t, row).Set("values", append(arr(t, obj(t, row).Value("values")), 2.0))
		state.Set("rows", append(arr(t, state.Value("rows")), row))
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Ops, `[["s",["nested"],{"rows":[{"value":2},{"value":3}]}],["p",["rows"],0,0,[{"values":[1,2]}]]]`)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("folds pathological operation counts without payload-cost estimation", func(t *testing.T) {
		wide := chordjson.NewObject(0)
		for at := range 5_000 {
			wide.Set(fmt.Sprintf("field%d", at), 0.0)
		}
		wideTracker := trackCopy(wide)
		wideBase := clone(t, wideTracker.Value())
		wideChange := wideTracker.BeginChange()
		for key := range obj(t, mustState(t, wideChange)).All() {
			obj(t, mustState(t, wideChange)).Set(key, 1.0)
		}
		widePrepared := mustPrepareCopy(t, wideChange)
		if len(widePrepared.Ops) != 1 || Verb(widePrepared.Ops[0]) != "r" {
			t.Fatalf("ops=%d", len(widePrepared.Ops))
		}
		expectEqual(t, replay(t, wideBase, widePrepared.Ops), widePrepared.Value)
		expectNoErr(t, wideTracker.Adopt(widePrepared))

		sparseTracker := trackCopy(chordjson.ObjectOf("rows", rows(15_000, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		sparseBase := clone(t, sparseTracker.Value())
		sparseChange := sparseTracker.BeginChange()
		sparseRows := arr(t, obj(t, mustState(t, sparseChange)).Value("rows"))
		for at := 0; at < 15_000; at += 3 {
			obj(t, sparseRows[at]).Set("value", float64(-at-1))
		}
		sparsePrepared := mustPrepareCopy(t, sparseChange)
		if len(sparsePrepared.Ops) != 1 || Verb(sparsePrepared.Ops[0]) != "r" {
			t.Fatalf("ops=%d", len(sparsePrepared.Ops))
		}
		expectEqual(t, replay(t, sparseBase, sparsePrepared.Ops), sparsePrepared.Value)
		expectNoErr(t, sparseTracker.Adopt(sparsePrepared))
	})
}

func TestTrackerPieceArrays(t *testing.T) {
	t.Run("keeps adoption independent from mutable detached permutation metadata", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[3,1,2]}`))
		change := tracker.BeginChange()
		values := arr(t, obj(t, mustState(t, change)).Value("values"))
		slices.SortFunc(values, func(a, b any) int { return int(a.(float64) - b.(float64)) })
		prepared := mustPrepareCopy(t, change)
		at := slices.IndexFunc(prepared.Ops, func(operation Op) bool { return Verb(operation) == "m" })
		if at < 0 {
			t.Fatalf("expected permutation, got %v", prepared.Ops)
		}
		slices.Reverse(prepared.Ops[at][2].([]any))
		expectNoErr(t, tracker.Adopt(prepared))
		expectJSON(t, obj(t, tracker.Value()).Value("values"), `[1,2,3]`)
	})

	t.Run("matches native splice with zero or one argument", func(t *testing.T) {
		noArguments := trackCopy(parse(t, `{"values":[1,2,3]}`))
		noArgumentsChange := noArguments.BeginChange()
		_ = mustState(t, noArgumentsChange)
		noArgumentsPrepared := mustPrepareCopy(t, noArgumentsChange)
		expectJSON(t, noArgumentsPrepared.Value, `{"values":[1,2,3]}`)
		if len(noArgumentsPrepared.Ops) != 0 {
			t.Fatalf("ops=%v", noArgumentsPrepared.Ops)
		}
		expectNoErr(t, noArguments.Adopt(noArgumentsPrepared))

		oneArgument := trackCopy(parse(t, `{"values":[1,2,3,4]}`))
		oneArgumentChange := oneArgument.BeginChange()
		state := obj(t, mustState(t, oneArgumentChange))
		values := arr(t, state.Value("values"))
		expectJSON(t, values[1:], `[2,3,4]`)
		state.Set("values", values[:1])
		oneArgumentPrepared := mustPrepareCopy(t, oneArgumentChange)
		expectJSON(t, oneArgumentPrepared.Value, `{"values":[1]}`)
		expectEqual(t, replay(t, oneArgument.Value(), oneArgumentPrepared.Ops), oneArgumentPrepared.Value)
		expectNoErr(t, oneArgument.Adopt(oneArgumentPrepared))

		pastEnd := trackCopy(parse(t, `{"values":[1,2,3]}`))
		pastEndChange := pastEnd.BeginChange()
		obj(t, mustState(t, pastEndChange)).Set("values", slices.Clone(arr(t, obj(t, mustState(t, pastEndChange)).Value("values"))[:3]))
		pastEndPrepared := mustPrepareCopy(t, pastEndChange)
		expectJSON(t, pastEndPrepared.Value, `{"values":[1,2,3]}`)
		expectNoErr(t, pastEnd.Adopt(pastEndPrepared))
	})

	t.Run("supports all structural mutators and native return values", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[3,1,2]}`))
		settleCopy(t, tracker, func(state any) any {
			document := obj(t, state)
			values := append(arr(t, document.Value("values")), 4.0)
			values = values[:len(values)-1]
			values = slices.Insert(values, 0, 0.0)
			values = values[1:]
			values = slices.Replace(values, 1, 2, 5.0, 4.0)
			slices.SortFunc(values, func(a, b any) int { return int(a.(float64) - b.(float64)) })
			slices.Reverse(values)
			values[1], values[2] = 9.0, 9.0
			copy(values[1:3], slices.Clone(values[0:2]))
			document.Set("values", values)
			return nil
		})
		expectJSON(t, tracker.Value(), `{"values":[5,5,9,2]}`)
	})

	t.Run("tracks held handles after reindex and suppresses detached writes", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[{"value":"a"},{"value":"b"},{"value":"c"}]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := arr(t, state.Value("values"))
		held := obj(t, values[1])
		values = slices.Insert(values, 0, parse(t, `{"value":"front"}`))
		held.Set("value", "moved")
		if obj(t, values[2]).Value("value") != "moved" {
			t.Fatal("held handle did not follow the element")
		}
		values = slices.Delete(values, 2, 3)
		held.Set("value", "detached")
		state.Set("values", values)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"values":[{"value":"front"},{"value":"a"},{"value":"c"}]}`)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("replays introduced, moved, then edited array descendants", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[]}`))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := append(arr(t, state.Value("values")), parse(t, `{"id":1,"nested":[1]}`))
		held := obj(t, values[0])
		values = slices.Insert(values, 0, parse(t, `{"id":0,"nested":[]}`))
		held.Set("nested", append(arr(t, held.Value("nested")), 2.0))
		slices.Reverse(values)
		held.Set("nested", append(arr(t, held.Value("nested")), 3.0))
		state.Set("values", values)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"values":[{"id":1,"nested":[1,2,3]},{"id":0,"nested":[]}]}`)
		expectEqual(t, replay(t, base, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("combines movement and edits with permutation plus final paths", func(t *testing.T) {
		// tracker.test.ts:1019-1036 asserts the first operation is the permutation. Edited elements lose their identity here, so the batch is a splice plus edits; replay and values are asserted.
		tracker := trackCopy(parse(t, `{"values":[{"rank":3,"edited":0},{"rank":1,"edited":0},{"rank":2,"edited":0}]}`))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		values := arr(t, obj(t, mustState(t, change)).Value("values"))
		held := obj(t, values[0])
		slices.SortFunc(values, func(a, b any) int { return int(obj(t, a).Value("rank").(float64) - obj(t, b).Value("rank").(float64)) })
		held.Set("edited", 1.0)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"values":[{"rank":1,"edited":0},{"rank":2,"edited":0},{"rank":3,"edited":1}]}`)
		expectEqual(t, replay(t, base, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("folds dense child and direct-index edits into one full-array operation", func(t *testing.T) {
		// tracker.test.ts:1097-1117 asserts one "p" operation; operation shape is not canonical, so replay equality is asserted.
		rowsTracker := trackCopy(chordjson.ObjectOf("rows", rows(1_000, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		settleCopy(t, rowsTracker, func(state any) any {
			for _, row := range arr(t, obj(t, state).Value("rows")) {
				obj(t, row).Set("value", obj(t, row).Value("value").(float64)+1)
			}
			return nil
		})
		valuesTracker := trackCopy(chordjson.ObjectOf("values", rows(1_000, func(at int) any { return float64(at) })))
		settleCopy(t, valuesTracker, func(state any) any {
			values := arr(t, obj(t, state).Value("values"))
			for at := range 600 {
				values[at] = float64(-at - 1)
			}
			return nil
		})
	})

	t.Run("folds only a deeply nested dense array region", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("rows", rows(2_000, func(at int) any { return chordjson.ObjectOf("nested", chordjson.ObjectOf("value", float64(at))) })))
		settleCopy(t, tracker, func(state any) any {
			rowsValue := arr(t, obj(t, state).Value("rows"))
			obj(t, obj(t, rowsValue[0]).Value("nested")).Set("value", -1.0)
			for at := 500; at < 1_000; at++ {
				obj(t, obj(t, rowsValue[at]).Value("nested")).Set("value", float64(-at))
			}
			return nil
		})
	})

	t.Run("emits multiple disjoint dense regions and preserves operations outside their boundaries", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("values", rows(1_400, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		settleCopy(t, tracker, func(state any) any {
			values := arr(t, obj(t, state).Value("values"))
			for _, at := range []int{97, 358, 897, 1_158} {
				obj(t, values[at]).Set("value", float64(-at-1))
			}
			for at := 100; at < 356; at++ {
				obj(t, values[at]).Set("value", float64(-at-1))
			}
			for at := 900; at < 1_156; at++ {
				obj(t, values[at]).Set("value", float64(-at-1))
			}
			return nil
		})
	})

	t.Run("normalizes a 20,000-operation queue transaction to two operations", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("values", rows(20_000, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := arr(t, state.Value("values"))
		for at := range 10_000 {
			values = append(values[1:], chordjson.ObjectOf("value", float64(20_000+at)))
		}
		state.Set("values", values)
		prepared := mustPrepareCopy(t, change)
		if len(prepared.Ops) != 2 {
			t.Fatalf("ops=%d", len(prepared.Ops))
		}
		expectEqual(t, replay(t, base, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("handles adversarial fragmentation and resolves held handles without piece scans", func(t *testing.T) {
		const size = 20_000
		tracker := trackCopy(chordjson.ObjectOf("values", rows(size, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		expected := clone(t, tracker.Value()).(*chordjson.Object)
		base := clone(t, tracker.Value())
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := arr(t, state.Value("values"))
		heldAt := []int{1, 1_001, 5_001, 10_001, 15_001, 19_999}
		held := make([]*chordjson.Object, len(heldAt))
		for at, index := range heldAt {
			held[at] = obj(t, values[index])
		}
		expectedValues := arr(t, expected.Value("values"))
		for index := 0; index < size; index += 2 {
			values[index] = chordjson.ObjectOf("value", float64(-index-1))
			expectedValues[index] = chordjson.ObjectOf("value", float64(-index-1))
		}
		for _, row := range held {
			row.Set("value", row.Value("value").(float64)+100_000)
		}
		for _, index := range heldAt {
			obj(t, expectedValues[index]).Set("value", obj(t, expectedValues[index]).Value("value").(float64)+100_000)
		}
		prepared := mustPrepareCopy(t, change)
		expectEqual(t, prepared.Value, expected)
		expectEqual(t, replay(t, base, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("normalizes restored overrides and cancelled structural edits to no operations", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[1,2,3]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		values := arr(t, state.Value("values"))
		values[1] = 9.0
		values[1] = 2.0
		slices.Reverse(values)
		slices.Reverse(values)
		values = append(values, 4.0)
		state.Set("values", values[:len(values)-1])
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"values":[1,2,3]}`)
		if len(prepared.Ops) != 0 {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("handles null sparse overrides without confusing absence", func(t *testing.T) {
		tracker := trackCopy(chordjson.ObjectOf("values", rows(10_000, func(at int) any { return float64(at) })))
		settleCopy(t, tracker, func(state any) any {
			values := arr(t, obj(t, state).Value("values"))
			values[17], values[9_000] = nil, nil
			return nil
		})
		values := arr(t, obj(t, tracker.Value()).Value("values"))
		if values[17] != nil || values[9_000] != nil {
			t.Fatal("null overrides lost")
		}
	})

	t.Run("supports self-overlapping fill and copyWithin by value", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"values":[{"n":0},{"n":1},{"n":2},{"n":3}]}`))
		settleCopy(t, tracker, func(state any) any {
			values := arr(t, obj(t, state).Value("values"))
			copied := []any{clone(t, values[0]), clone(t, values[1]), clone(t, values[2])}
			copy(values[1:], copied)
			first := clone(t, values[1])
			values[0], values[1] = first, clone(t, first)
			obj(t, values[0]).Set("n", 9.0)
			return nil
		})
		values := arr(t, obj(t, tracker.Value()).Value("values"))
		expectJSON(t, values, `[{"n":9},{"n":0},{"n":1},{"n":2}]`)
		expectNotSame(t, values[0], values[1], "values[0]/[1]")
	})

	for _, method := range []string{"unshift", "splice"} {
		t.Run(fmt.Sprintf("inserts 100,000 items with %s", method), func(t *testing.T) {
			tracker := trackCopy(parse(t, `{"values":[-1]}`))
			base := clone(t, tracker.Value())
			change := tracker.BeginChange()
			state := obj(t, mustState(t, change))
			items := rows(100_000, func(at int) any { return float64(at) })
			position := 1
			if method == "unshift" {
				position = 0
			}
			state.Set("values", slices.Insert(arr(t, state.Value("values")), position, items...))
			prepared := mustPrepareCopy(t, change)
			values := arr(t, obj(t, prepared.Value).Value("values"))
			last := 100_000
			if method == "unshift" {
				last = 99_999
			}
			if len(values) != 100_001 || values[last] != 99_999.0 {
				t.Fatalf("len=%d", len(values))
			}
			expectEqual(t, replay(t, base, prepared.Ops), prepared.Value)
			expectNoErr(t, tracker.Adopt(prepared))
		})
	}
}

func TestTrackerRandomizedTransactions(t *testing.T) {
	type edit func(document *chordjson.Object, choice, value int)
	mutate := func(document *chordjson.Object, choice, value int) {
		item := func() any { return chordjson.ObjectOf("id", float64(value), "score", float64(value%7)) }
		values := arr(t, document.Value("values"))
		meta := obj(t, document.Value("meta"))
		switch choice {
		case 0:
			document.Set("text", document.Value("text").(string)+fmt.Sprintf("-%d", value))
		case 1:
			document.Set("values", append(values, item()))
		case 2:
			document.Set("values", slices.Insert(values, 0, item()))
		case 3:
			if len(values) > 0 {
				document.Set("values", values[1:])
			}
		case 4:
			if len(values) > 0 {
				document.Set("values", values[:len(values)-1])
			}
		case 5:
			index, remove := 0, 0
			if len(values) > 0 {
				index, remove = value%(len(values)+1), value%2
			}
			document.Set("values", slices.Replace(values, index, min(index+remove, len(values)), item()))
		case 6:
			slices.Reverse(values)
		case 7:
			slices.SortStableFunc(values, func(a, b any) int { return int(obj(t, a).Value("id").(float64) - obj(t, b).Value("id").(float64)) })
		case 8:
			if len(values) > 0 {
				obj(t, values[value%len(values)]).Set("score", float64(value))
			}
		case 9:
			meta.Set("revision", meta.Value("revision").(float64)+1)
			meta.Set("label", fmt.Sprintf("r-%d", value))
		default:
			meta.Delete("label")
		}
	}
	var _ edit = mutate

	t.Run("matches policy, detached replay, and adopted state after multi-operation transactions", func(t *testing.T) {
		for seed := 1; seed <= 40; seed++ {
			rng := mulberry32(int32(seed))
			initial := chordjson.ObjectOf(
				"values", rows(4, func(id int) any { return chordjson.ObjectOf("id", float64(id), "score", 0.0) }),
				"text", "start",
				"meta", chordjson.ObjectOf("revision", 0.0),
			)
			tracker := trackCopy(initial)
			expected := clone(t, initial).(*chordjson.Object)
			for transaction := range 25 {
				baseRoot := tracker.Value()
				base := clone(t, baseRoot)
				change := tracker.BeginChange()
				for operation := range 5 {
					choice := int(rng() * 11)
					value := seed*10_000 + transaction*10 + operation
					mutate(expected, choice, value)
					mutate(obj(t, mustState(t, change)), choice, value)
				}
				prepared := mustPrepareCopy(t, change)
				policy := clone(t, prepared.Value)
				where := fmt.Sprintf("seed %d transaction %d", seed, transaction)
				if !equalJson(baseRoot, base) {
					t.Fatalf("prepare changed base %s", where)
				}
				if !equalJson(policy, expected) {
					t.Fatalf("policy %s", where)
				}
				if !equalJson(replay(t, base, prepared.Ops), policy) {
					t.Fatalf("replay %s", where)
				}
				expectNoErr(t, tracker.Adopt(prepared))
				if !equalJson(baseRoot, base) {
					t.Fatalf("adopt changed base %s", where)
				}
				expectSame(t, tracker.Value(), prepared.Value, "adopt "+where)
				expectAliasFree(t, tracker.Value())
			}
		}
	})
}

func TestTrackerObjectEmissionScaling(t *testing.T) {
	t.Run("orders many reverse-depth object edits through the bounded fallback", func(t *testing.T) {
		var initial any = chordjson.ObjectOf("value", 0.0)
		for range 512 {
			initial = chordjson.ObjectOf("value", 0.0, "next", initial)
		}
		tracker := trackCopy(initial)
		change := tracker.BeginChange()
		var nodes []*chordjson.Object
		for node := obj(t, mustState(t, change)); node != nil; {
			nodes = append(nodes, node)
			next, _ := node.Value("next").(*chordjson.Object)
			node = next
		}
		for at, node := range slices.Backward(nodes) {
			node.Set("value", float64(at+1))
		}
		prepared := mustPrepareCopy(t, change)
		// tracker.test.ts:1393 asserts one operation per node. Node paths are up to 513 segments deep, so the batch of 513 sets costs more than the revision and DiffRevisions sends one root replacement instead; operation shape is not canonical, so replay equality is the assertion.
		if len(prepared.Ops) != len(nodes) && (len(prepared.Ops) != 1 || Verb(prepared.Ops[0]) != "r") {
			t.Fatalf("ops=%d nodes=%d", len(prepared.Ops), len(nodes))
		}
		expectEqual(t, replay(t, initial, prepared.Ops), prepared.Value)
		expectNoErr(t, tracker.Adopt(prepared))
		expectAliasFree(t, tracker.Value())
	})
}

func TestTrackerSecurityAndStorageStyleCloning(t *testing.T) {
	t.Run("handles reserved own keys without prototype pollution", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"safe":{"__proto__":{"value":1}}}`))
		change := tracker.BeginChange()
		obj(t, obj(t, obj(t, mustState(t, change)).Value("safe")).Value("__proto__")).Set("value", 2.0)
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, prepared.Value, `{"safe":{"__proto__":{"value":2}}}`)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
		for _, operation := range prepared.Ops {
			for _, segment := range PathOf(operation) {
				if text, ok := segment.(string); ok && ReservedSegments[text] {
					t.Fatalf("emitted reserved segment in %v", operation)
				}
			}
		}
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("supports a MemoryStorage-style recursive clone of Prepared.value", func(t *testing.T) {
		tracker := trackCopy(parse(t, `{"rows":[{"value":1}]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		rowsValue := arr(t, state.Value("rows"))
		obj(t, rowsValue[0]).Set("value", 2.0)
		state.Set("rows", append(rowsValue, parse(t, `{"value":3}`)))
		prepared := mustPrepareCopy(t, change)
		expectJSON(t, clone(t, prepared.Value), `{"rows":[{"value":2},{"value":3}]}`)
		expectNoErr(t, tracker.Adopt(prepared))
	})
}

// Regression: an aliased draft container keeps its origin, and Go visits map keys in random order, so the alias that got the origin could land at the position of the other one and both positions restored the same base container.
func TestTrackerAliasedDraftPlacementsStayIndependentWhateverTheKeyOrder(t *testing.T) {
	for range 300 {
		tracker := trackCopy(parse(t, `{"a":{"child":{"value":1}},"b":null,"c":null}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state.Set("b", state.Value("a"))
		state.Set("c", state.Value("a"))
		prepared := mustPrepareCopy(t, change)
		expectAliasFree(t, prepared.Value)
		expectJSON(t, prepared.Value, `{"a":{"child":{"value":1}},"b":{"child":{"value":1}},"c":{"child":{"value":1}}}`)
		expectEqual(t, replay(t, tracker.Value(), prepared.Ops), prepared.Value)
	}
}
