package delta

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the "delta tracker transactional overlay lifecycle" cases of packages/chord/test/delta-tracker/tracker.test.ts onto the overlay Tracker. Go mapping: Change.State is the Object handle for upstream's Proxy draft, a string `+=` is Get then Set, and a revoked draft panics with ErrRevoked where upstream throws TypeError("Cannot use a settled overlay").
//
// mutation-checked: Prepared.BaseRevision returning a constant fails it.
func TestTrackerOverlayLifecycle(t *testing.T) {
	t.Run("materializes an immutable next revision and adopts it by pointer swap", func(t *testing.T) {
		// tracker.test.ts:45-72; Prepared.baseRevision is tracker.ts:157-158.
		initial := jsonObject(t, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		tracker := Track(initial)
		if tracker.Value() != initial {
			t.Fatal("tracker.value is not the imported revision")
		}
		change := tracker.BeginChange()
		state := change.State()
		if err := state.Set("count", 2.0); err != nil {
			t.Fatal(err)
		}
		nested := state.Object("nested")
		if err := nested.Set("text", nested.Get("text").(string)+"b"); err != nil {
			t.Fatal(err)
		}
		if _, err := state.Array("values").Push(2.0); err != nil {
			t.Fatal(err)
		}
		expectJSONText(t, initial, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		prepared := mustPrepare(t, change)
		next := prepared.Value()
		if got := prepared.BaseRevision(); got != 0 {
			t.Fatalf("prepared.baseRevision = %d, want 0", got)
		}
		if prepared.Base() != initial {
			t.Fatal("prepared.base is not the base revision")
		}
		if next == initial {
			t.Fatal("prepared.value is the base revision")
		}
		expectJSONText(t, next, `{"count":2,"nested":{"text":"ab"},"values":[1,2]}`)
		expectJSONText(t, opsAsJSON(prepared.Ops()), `[["s",["count"],2],["a",["nested","text"],"b"],["p",["values"],1,0,[2]]]`)
		if tracker.Value() != initial {
			t.Fatal("prepare changed tracker.value")
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
		expectJSONText(t, initial, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		if tracker.Value() != next || prepared.Value() != next || prepared.Base() != initial {
			t.Fatal("adoption must install prepared.value by pointer swap and keep prepared.base")
		}
		assertRevoked(t, func() { state.Get("count") })
	})

	t.Run("a preparation records the revision it was prepared on", func(t *testing.T) {
		// tracker.ts:157-158 baseRevision is the tracker revision the change began on; tracker.ts:261 rejects adoption when it is no longer current.
		tracker := Track(jsonObject(t, `{"n":0}`))
		for revision := range 3 {
			change := tracker.BeginChange()
			if err := change.State().Set("n", float64(revision+1)); err != nil {
				t.Fatal(err)
			}
			prepared := mustPrepare(t, change)
			if got := prepared.BaseRevision(); got != revision {
				t.Fatalf("prepared.baseRevision = %d, want %d", got, revision)
			}
			if err := tracker.Adopt(prepared); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("keeps a transaction live across await and seals it only at prepare", func(t *testing.T) {
		// tracker.test.ts:75-87. Go has no await; the second write runs after another goroutine has run, and the draft stays open until Prepare.
		tracker := Track(jsonObject(t, `{"left":0,"nested":{"right":0}}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("left", 1.0))
		done := make(chan struct{})
		go func() { close(done) }()
		<-done
		mustNoErr(t, change.State().Object("nested").Set("right", 2.0))
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value(), `{"left":1,"nested":{"right":2}}`)
		assertRevoked(t, func() { _ = change.State().Set("left", 3.0) })
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("allows Promise resolution to inspect a settled draft without reporting a false failure", func(t *testing.T) {
		// tracker.test.ts:89-116. Promise resolution probes a draft's "then" property; Go has no thenables, so the case keeps its Go meaning: a document key named "then" is ordinary data, and a settled draft held after a 4,100-row traversal is revoked.
		tracker := Track(jsonObject(t, `{"then":"document-value","value":1}`))
		change := tracker.BeginChange()
		draft := change.State()
		if got := draft.Get("then"); got != "document-value" {
			t.Fatalf("draft.then = %v", got)
		}
		mustNoErr(t, draft.Set("value", 2.0))
		prepared := mustPrepare(t, change)
		assertRevoked(t, func() { draft.Get("value") })
		mustNoErr(t, tracker.Adopt(prepared))
		expectJSONText(t, tracker.Value(), `{"then":"document-value","value":2}`)

		large := Track(chordjson.ObjectOf("rows", rows(4_100, func(at int) any { return chordjson.ObjectOf("value", float64(at)) })))
		largeChange := large.BeginChange()
		largeDraft := largeChange.State()
		largeRows := largeDraft.Array("rows")
		for at := range largeRows.Len() {
			if largeRows.Object(at).Get("value").(float64) < 0 {
				t.Fatalf("rows[%d].value is negative", at)
			}
		}
		mustNoErr(t, largeRows.Object(0).Set("value", -1.0))
		largePrepared := mustPrepare(t, largeChange)
		assertRevoked(t, func() { largeDraft.Array("rows") })
		mustNoErr(t, large.Adopt(largePrepared))
	})

	t.Run("aborts idempotently and revokes held descendants", func(t *testing.T) {
		// tracker.test.ts:118-128
		tracker := Track(jsonObject(t, `{"child":{"value":1}}`))
		change := tracker.BeginChange()
		child := change.State().Object("child")
		mustNoErr(t, child.Set("value", 2.0))
		change.Abort()
		change.Abort()
		expectJSONText(t, tracker.Value(), `{"child":{"value":1}}`)
		assertRevoked(t, func() { child.Get("value") })
		_, err := change.Prepare()
		expectErr(t, err, "settled")
	})

	t.Run("allows competing contexts and invalidates loser views", func(t *testing.T) {
		// tracker.test.ts:130-144
		tracker := Track(jsonObject(t, `{"value":0}`))
		first, second := tracker.BeginChange(), tracker.BeginChange()
		mustNoErr(t, first.State().Set("value", 1.0))
		mustNoErr(t, second.State().Set("value", 2.0))
		firstPrepared, secondPrepared := mustPrepare(t, first), mustPrepare(t, second)
		held := secondPrepared.Value()
		mustNoErr(t, tracker.Adopt(firstPrepared))
		expectJSONText(t, tracker.Value(), `{"value":1}`)
		expectJSONText(t, held, `{"value":2}`)
		expectErr(t, tracker.Adopt(secondPrepared), "stale")
		expectErr(t, tracker.Adopt(firstPrepared), "already been used")
	})

	t.Run("invalidates competing open overlays without changing their base revision", func(t *testing.T) {
		// tracker.test.ts:146-163
		initial := jsonObject(t, `{"rows":[{"value":0},{"value":1}]}`)
		tracker := Track(initial)
		staleChange := tracker.BeginChange()
		held := staleChange.State().Array("rows").Object(1)
		mustNoErr(t, held.Set("value", 2.0))

		winner := tracker.BeginChange()
		mustNoErr(t, winner.State().Array("rows").Object(0).Set("value", 3.0))
		mustNoErr(t, tracker.Adopt(mustPrepare(t, winner)))

		expectJSONText(t, initial, `{"rows":[{"value":0},{"value":1}]}`)
		expectJSONText(t, tracker.Value(), `{"rows":[{"value":3},{"value":1}]}`)
		assertRevoked(t, func() { held.Get("value") })
		_, err := staleChange.Prepare()
		expectErr(t, err, "settled")
		staleChange.Abort()
	})

	t.Run("adopts object edits as ordinary fast-layout immutable revisions", func(t *testing.T) {
		// tracker.test.ts:165-183. A JsonObject property has no descriptor; the adopted revision is an ordinary *JsonObject in Pi's key order.
		initial := jsonObject(t, `{"first":1,"second":2}`)
		tracker := Track(initial)
		change := tracker.BeginChange()
		state := change.State()
		mustNoErr(t, state.Set("first", 3.0))
		state.Delete("second")
		mustNoErr(t, state.Set("third", 4.0))
		mustNoErr(t, tracker.Adopt(mustPrepare(t, change)))
		if tracker.Value() == initial {
			t.Fatal("the adopted revision is the base")
		}
		expectJSONText(t, initial, `{"first":1,"second":2}`)
		expectJSONText(t, tracker.Value(), `{"first":3,"third":4}`)
	})

	t.Run("supports adopt-then-publish ordering and retained publication reads", func(t *testing.T) {
		// tracker.test.ts:185-209
		tracker := Track(jsonObject(t, `{"value":0,"nested":{"count":0}}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("value", 1.0))
		mustNoErr(t, change.State().Object("nested").Set("count", 1.0))
		prepared := mustPrepare(t, change)
		operations := prepared.Ops()
		mustNoErr(t, tracker.Adopt(prepared))
		if len(operations) != 2 || &prepared.Ops()[0] != &operations[0] {
			t.Fatalf("prepared.ops after adopt = %v", prepared.Ops())
		}
		published := prepared.Value()
		if published != tracker.Value() {
			t.Fatal("the published value is not the adopted revision")
		}
		expectJSONText(t, published, `{"value":1,"nested":{"count":1}}`)

		next := tracker.BeginChange()
		mustNoErr(t, next.State().Set("value", 2.0))
		mustNoErr(t, tracker.Adopt(mustPrepare(t, next)))
		if published == tracker.Value() {
			t.Fatal("the retained publication is the next revision")
		}
		expectJSONText(t, prepared.Value(), `{"value":1,"nested":{"count":1}}`)
		expectJSONText(t, tracker.Value(), `{"value":2,"nested":{"count":1}}`)
	})

	t.Run("keeps replacement base/value structurally compatible and readable after adoption", func(t *testing.T) {
		// tracker.test.ts:211-222
		original, replacement := jsonObject(t, `{"value":0}`), jsonObject(t, `{"value":1}`)
		tracker := Track(original)
		prepared := tracker.PrepareReplace(replacement)
		if prepared.Base() != original || prepared.Value() != replacement {
			t.Fatal("prepareReplace must keep base and value by reference")
		}
		mustNoErr(t, tracker.Adopt(prepared))
		if prepared.Base() != original || prepared.Value() != replacement || tracker.Value() != replacement {
			t.Fatal("adoption must install the replacement by reference")
		}
	})

	t.Run("keeps aborted and stale materialized candidates readable", func(t *testing.T) {
		// tracker.test.ts:224-242
		tracker := Track(jsonObject(t, `{"value":0}`))
		abortedChange := tracker.BeginChange()
		mustNoErr(t, abortedChange.State().Set("value", 1.0))
		aborted := mustPrepare(t, abortedChange)
		abortedView := aborted.Value()
		aborted.Abort()
		expectJSONText(t, abortedView, `{"value":1}`)
		expectJSONText(t, aborted.Value(), `{"value":1}`)

		loserChange := tracker.BeginChange()
		mustNoErr(t, loserChange.State().Set("value", 2.0))
		loser := mustPrepare(t, loserChange)
		loserView := loser.Value()
		mustNoErr(t, tracker.Adopt(tracker.PrepareReplace(jsonObject(t, `{"value":3}`))))
		expectJSONText(t, loserView, `{"value":2}`)
		expectJSONText(t, loser.Value(), `{"value":2}`)
	})

	t.Run("lets a settled Change abort its prepared result without retaining its context", func(t *testing.T) {
		// tracker.test.ts:244-255
		tracker := Track(jsonObject(t, `{"value":0,"nested":{"value":1}}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("value", 1.0))
		prepared := mustPrepare(t, change)
		operations := prepared.Ops()
		change.Abort()
		change.Abort()
		if len(prepared.Ops()) != len(operations) || &prepared.Ops()[0] != &operations[0] {
			t.Fatal("aborting changed prepared.ops")
		}
		expectJSONText(t, prepared.Value(), `{"value":1,"nested":{"value":1}}`)
		expectErr(t, tracker.Adopt(prepared), "aborted")
	})

	t.Run("rejects foreign and aborted prepared values", func(t *testing.T) {
		// tracker.test.ts:257-265
		first, second := Track(jsonObject(t, `{"value":0}`)), Track(jsonObject(t, `{"value":0}`))
		prepared := first.PrepareReplace(jsonObject(t, `{"value":1}`))
		expectErr(t, second.Adopt(prepared), "different tracker")
		prepared.Abort()
		prepared.Abort()
		expectErr(t, first.Adopt(prepared), "aborted")
	})

	t.Run("reads deleted own properties as absent", func(t *testing.T) {
		// tracker.test.ts:267-291. A JsonObject has no prototype, so the inherited half (a committed read falling through to Object.prototype) has no Go form; the own-property half is asserted.
		tracker := Track(jsonObject(t, `{"value":1}`))
		change := tracker.BeginChange()
		state := change.State()
		state.Delete("value")
		mustNoErr(t, state.Set("trackerInherited", 1.0))
		state.Delete("trackerInherited")
		for _, key := range []string{"value", "trackerInherited"} {
			if _, present := state.Lookup(key); present {
				t.Fatalf("deleted %q is present", key)
			}
		}
		prepared := mustPrepare(t, change)
		if _, present := prepared.Value().Get("value"); present {
			t.Fatal("prepared.value keeps the deleted key")
		}
		mustNoErr(t, tracker.Adopt(prepared))
		if keys := tracker.Value().Keys(); len(keys) != 0 {
			t.Fatalf("Object.keys(tracker.value) = %v", keys)
		}
	})

	t.Run("detaches old handles for deeply equal object and array assignments before normalizing", func(t *testing.T) {
		// tracker.test.ts:293-325
		initial := jsonObject(t, `{"object":{"nested":{"value":1}},"array":[{"value":1}],"rows":[{"value":1}]}`)
		tracker := Track(initial)
		change := tracker.BeginChange()
		state := change.State()
		oldObject, oldArray, oldRow := state.Object("object"), state.Array("array"), state.Array("rows").Object(0)
		mustNoErr(t, state.Set("object", jsonOf(t, `{"nested":{"value":1}}`)))
		mustNoErr(t, state.Set("array", jsonOf(t, `[{"value":1}]`)))
		mustNoErr(t, state.Array("rows").Set(0, jsonOf(t, `{"value":1}`)))
		if *state.Object("object") == *oldObject || *state.Array("array") == *oldArray || *state.Array("rows").Object(0) == *oldRow {
			t.Fatal("an assignment kept the old handle attached")
		}
		mustNoErr(t, oldObject.Object("nested").Set("value", 9.0))
		mustNoErr(t, oldArray.Object(0).Set("value", 9.0))
		mustNoErr(t, oldRow.Set("value", 9.0))
		expectJSONText(t, state.Snapshot(), `{"object":{"nested":{"value":1}},"array":[{"value":1}],"rows":[{"value":1}]}`)
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != 0 {
			t.Fatalf("prepared.ops = %s", opsText(t, prepared.Ops()))
		}
		mustNoErr(t, tracker.Adopt(prepared))
		if tracker.Value() != initial {
			t.Fatal("a deep no-op changed the committed root")
		}
		for _, key := range []string{"object", "array"} {
			expectSame(t, field(t, tracker.Value(), key), field(t, initial, key), key)
		}
		expectSame(t, field(t, tracker.Value(), "rows", 0), field(t, initial, "rows", 0), "rows[0]")
	})

	t.Run("normalizes a deeply equal replacement without changing committed identity", func(t *testing.T) {
		// tracker.test.ts:327-334
		initial := jsonObject(t, `{"nested":{"value":1},"rows":[1,2,3]}`)
		tracker := Track(initial)
		prepared := tracker.PrepareReplace(jsonObject(t, `{"nested":{"value":1},"rows":[1,2,3]}`))
		if len(prepared.Ops()) != 0 {
			t.Fatalf("prepared.ops = %s", opsText(t, prepared.Ops()))
		}
		mustNoErr(t, tracker.Adopt(prepared))
		if tracker.Value() != initial {
			t.Fatal("a deeply equal replacement changed the committed root")
		}
	})

	t.Run("takes O(1) immutable ownership for replacement and its operation payload", func(t *testing.T) {
		// tracker.test.ts:336-348
		tracker := Track(jsonObject(t, `{"value":0,"rows":[]}`))
		replacement := jsonObject(t, `{"value":1,"rows":[{"value":2}]}`)
		prepared := tracker.PrepareReplace(replacement)
		if prepared.Base() != tracker.Value() || prepared.Value() != replacement {
			t.Fatal("prepareReplace must keep base and value by reference")
		}
		operations := prepared.Ops()
		if len(operations) != 1 || operations[0][0] != "r" || operations[0][1] != replacement {
			t.Fatalf("prepared.ops = %s, want [[\"r\", replacement]] by reference", opsText(t, operations))
		}
		mustNoErr(t, tracker.Adopt(prepared))
		if tracker.Value() != replacement {
			t.Fatal("adoption must install the replacement")
		}
	})
}
