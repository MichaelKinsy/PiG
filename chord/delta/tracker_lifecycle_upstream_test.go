package delta

import "testing"

// Ports the "delta tracker transactional overlay lifecycle" cases of packages/chord/test/delta-tracker/tracker.test.ts onto the overlay Tracker. Go mapping: Change.State is the Object handle for upstream's Proxy draft, a string `+=` is Get then Set, and a revoked draft panics with ErrRevoked where upstream throws TypeError.
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
}
