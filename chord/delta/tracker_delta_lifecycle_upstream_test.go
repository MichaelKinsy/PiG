package delta

import (
	"testing"
)

// Ports the "immutable tracker lifecycle" cases of packages/chord/test/delta.test.ts:18-160 onto the overlay Tracker. Go mapping as in tracker_upstream_test.go; a revoked draft panics with ErrRevoked where upstream's revoked Proxy throws a TypeError.

func TestDeltaImmutableTrackerLifecycle(t *testing.T) {
	t.Run("keeps a draft alive across await and adopts only a prepared change", func(t *testing.T) {
		// delta.test.ts:19-45. The draft stays usable after another goroutine has run, as upstream's stays usable across an await.
		input := jsonObject(t, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		tracker := Track(input)
		expectSameContainer(t, tracker.Value(), input, "tracker.value")

		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("count", 2.0))
		done := make(chan struct{})
		go func() { defer close(done) }()
		<-done
		nested := change.State().Object("nested")
		mustNoErr(t, nested.Set("text", nested.Get("text").(string)+"b"))
		_, err := change.State().Array("values").Push(2.0)
		mustNoErr(t, err)
		expectJSONText(t, tracker.Value(), `{"count":1,"nested":{"text":"a"},"values":[1]}`)

		prepared := mustPrepare(t, change)
		expectSameContainer(t, prepared.Base(), tracker.Value(), "prepared.base")
		expectJSONText(t, prepared.Value(), `{"count":2,"nested":{"text":"ab"},"values":[1,2]}`)
		if got := opsText(t, prepared.Ops()); got != `[["s",["count"],2],["a",["nested","text"],"b"],["p",["values"],1,0,[2]]]` {
			t.Fatalf("ops %s", got)
		}
		assertRevoked(t, func() { change.State() })
		expectJSONText(t, tracker.Value(), `{"count":1,"nested":{"text":"a"},"values":[1]}`)

		mustNoErr(t, tracker.Adopt(prepared))
		expectSameContainer(t, tracker.Value(), prepared.Value(), "tracker.value")
	})

	t.Run("aborts and revokes without changing the committed value", func(t *testing.T) {
		// delta.test.ts:48-60
		tracker := Track(jsonObject(t, `{"child":{"value":1}}`))
		change := tracker.BeginChange()
		child := change.State().Object("child")
		mustNoErr(t, child.Set("value", 2.0))
		change.Abort()
		expectJSONText(t, tracker.Value().Value("child"), `{"value":1}`)
		assertRevoked(t, func() { child.Get("value") })
		change.Abort()
		_, err := change.Prepare()
		expectErr(t, err, "settled")
		tracker.BeginChange().Abort()
	})

	t.Run("grows arrays with explicit nulls and revokes drafts after preparation", func(t *testing.T) {
		// delta.test.ts:62-70
		tracker := Track(jsonObject(t, `{"values":[1,2]}`))
		change := tracker.BeginChange()
		change.State().Array("values").SetLen(4)
		prepared := mustPrepare(t, change)
		expectJSONText(t, prepared.Value().Value("values"), `[1,2,null,null]`)
		assertRevoked(t, func() { change.State() })
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("normalizes a deep no-op to exact previous identity", func(t *testing.T) {
		// delta.test.ts:72-81
		tracker := Track(jsonObject(t, `{"value":{"nested":[1,2]}}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("value", jsonObject(t, `{"nested":[1,2]}`)))
		prepared := mustPrepare(t, change)
		expectSameContainer(t, prepared.Value(), prepared.Base(), "prepared.value")
		if len(prepared.Ops()) != 0 {
			t.Fatalf("ops %s", opsText(t, prepared.Ops()))
		}
		mustNoErr(t, tracker.Adopt(prepared))
		expectSameContainer(t, tracker.Value(), prepared.Base(), "tracker.value")
	})

	t.Run("takes immutable ownership of replacement input and applies no-op normalization", func(t *testing.T) {
		// delta.test.ts:83-94
		tracker := Track(jsonObject(t, `{"nested":{"value":1}}`))
		replacement := jsonObject(t, `{"nested":{"value":2}}`)
		prepared := tracker.PrepareReplace(replacement)
		expectSameContainer(t, prepared.Value(), replacement, "prepared.value")
		if len(prepared.Ops()) != 1 || Verb(prepared.Ops()[0]) != "r" {
			t.Fatalf("ops %s", opsText(t, prepared.Ops()))
		}
		expectSameContainer(t, prepared.Ops()[0][1], replacement, "op payload")
		mustNoErr(t, tracker.Adopt(prepared))

		noOp := tracker.PrepareReplace(jsonObject(t, `{"nested":{"value":2}}`))
		expectSameContainer(t, noOp.Value(), tracker.Value(), "noOp.value")
		if len(noOp.Ops()) != 0 {
			t.Fatalf("ops %s", opsText(t, noOp.Ops()))
		}
	})

	t.Run("shares immutable operation placements with the prepared candidate", func(t *testing.T) {
		// delta.test.ts:96-106
		tracker := Track(jsonObject(t, `{"rows":[]}`))
		change := tracker.BeginChange()
		_, err := change.State().Array("rows").Push(JsonObjectOf("id", 1.0))
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		splice := prepared.Ops()[0]
		if Verb(splice) != "p" {
			t.Fatalf("expected splice, got %s", opsText(t, prepared.Ops()))
		}
		expectSameContainer(t, splice[4].([]any)[0], field(t, prepared.Value(), "rows", 0), "placement")
	})

	t.Run("rejects foreign, stale, and repeated preparations while allowing competing changes", func(t *testing.T) {
		// delta.test.ts:108-127
		first, second := Track(jsonObject(t, `{"value":0}`)), Track(jsonObject(t, `{"value":0}`))
		change, competing := first.BeginChange(), first.BeginChange()
		mustNoErr(t, change.State().Set("value", 1.0))
		mustNoErr(t, competing.State().Set("value", 2.0))
		prepared, competingPrepared := mustPrepare(t, change), mustPrepare(t, competing)
		expectErr(t, second.Adopt(prepared), "different tracker")
		mustNoErr(t, first.Adopt(prepared))
		expectErr(t, first.Adopt(prepared), "already been used")
		expectErr(t, first.Adopt(competingPrepared), "stale")

		stale := first.PrepareReplace(jsonObject(t, `{"value":2}`))
		winner := first.PrepareReplace(jsonObject(t, `{"value":3}`))
		mustNoErr(t, first.Adopt(winner))
		expectErr(t, first.Adopt(stale), "stale")
		expectErr(t, first.Adopt(stale), "stale")
	})

	t.Run("invalidates a prepared result when its change is aborted", func(t *testing.T) {
		// delta.test.ts:129-137
		tracker := Track(jsonObject(t, `{"value":0}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("value", 1.0))
		prepared := mustPrepare(t, change)
		change.Abort()
		expectErr(t, tracker.Adopt(prepared), "aborted")
		change.Abort()
	})

	t.Run("makes competing same-base preparations stale after adopting a no-op", func(t *testing.T) {
		// delta.test.ts:139-148
		tracker := Track(jsonObject(t, `{"value":{"count":1}}`))
		first := tracker.PrepareReplace(jsonObject(t, `{"value":{"count":1}}`))
		competing := tracker.PrepareReplace(jsonObject(t, `{"value":{"count":1}}`))
		expectSameContainer(t, first.Value(), first.Base(), "first")
		expectSameContainer(t, competing.Value(), competing.Base(), "competing")
		mustNoErr(t, tracker.Adopt(first))
		expectErr(t, tracker.Adopt(first), "already been used")
		expectErr(t, tracker.Adopt(competing), "stale")
	})

	t.Run("emits an owned root replacement without traversing large replacement input", func(t *testing.T) {
		// delta.test.ts:150-159
		rows := make([]any, 10_000)
		for value := range rows {
			rows[value] = JsonObjectOf("value", float64(value), "stable", JsonObjectOf("value", float64(value)))
		}
		tracker := Track(JsonObjectOf("rows", rows))
		replaced := append([]any(nil), field(t, tracker.Value(), "rows").([]any)...)
		replaced[5_000] = JsonObjectOf("value", -1.0, "stable", field(t, replaced[5_000], "stable"))
		replacement := JsonObjectOf("rows", replaced)
		prepared := tracker.PrepareReplace(replacement)
		expectSameContainer(t, prepared.Value(), replacement, "prepared.value")
		if len(prepared.Ops()) != 1 || Verb(prepared.Ops()[0]) != "r" {
			t.Fatalf("ops %d", len(prepared.Ops()))
		}
		expectSameContainer(t, prepared.Ops()[0][1], replacement, "op payload")
		applied, err := ApplyImmutable(prepared.Base(), prepared.Ops())
		mustNoErr(t, err)
		expectJSONText(t, applied, textOf(t, prepared.Value()))
	})
}
