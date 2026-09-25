package sdk

import (
	"encoding/json"
	"testing"
)

func sessionPush(t *testing.T, leaf string, count int, appended ...string) json.RawMessage {
	t.Helper()
	raws := make([]json.RawMessage, 0, len(appended))
	for _, a := range appended {
		raws = append(raws, json.RawMessage(a))
	}
	b, err := json.Marshal(map[string]any{"leafId": leaf, "entryCount": count, "entriesAppended": raws})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An extension that never reads the session log must never accumulate it.
// Every loaded extension used to hold its own full copy, so a large session
// cost hundreds of megabytes per extension for data most of them never touch.
func TestUnsubscribedMirrorHoldsNoEntries(t *testing.T) {
	var m sessionMirror
	m.applySessionUpdate(sessionPush(t, "a", 2, `{"id":"1"}`, `{"id":"2"}`))

	if got := len(m.getEntries()); got != 0 {
		t.Errorf("unsubscribed mirror retained %d entries; want 0", got)
	}
}

// The leaf is small and always tracked, so a subscribe that happens later can
// compute a branch without waiting for the next push.
func TestUnsubscribedMirrorStillTracksLeaf(t *testing.T) {
	var m sessionMirror
	m.applySessionUpdate(sessionPush(t, "leaf-7", 0))

	if m.leafID != "leaf-7" {
		t.Errorf("leafID = %q; want leaf-7", m.leafID)
	}
}

// Once subscribed, the push stream is applied as before.
func TestSubscribedMirrorAppliesPushes(t *testing.T) {
	var m sessionMirror
	m.subscribed.Store(true)
	m.applySessionUpdate(sessionPush(t, "a", 2, `{"id":"1"}`, `{"id":"2"}`))

	if got := len(m.getEntries()); got != 2 {
		t.Fatalf("subscribed mirror holds %d entries; want 2", got)
	}
}

// Responses and pushes reach the extension on separate goroutines, so seed and
// the push stream can land in either order. Both must leave the same log, and
// neither may truncate the other.
func TestSeedAndPushRaceLeavesTheWholeLog(t *testing.T) {
	log := []json.RawMessage{json.RawMessage(`{"id":"1"}`), json.RawMessage(`{"id":"2"}`)}

	t.Run("seed first", func(t *testing.T) {
		var m sessionMirror
		m.subscribed.Store(true)
		m.seed(log, len(log), "a")
		m.applySessionUpdate(sessionPush(t, "a", 2, `{"id":"1"}`, `{"id":"2"}`))
		if got := len(m.getEntries()); got != 2 {
			t.Errorf("got %d entries; want 2", got)
		}
	})

	// The push that follows a subscribe carries the log as of delivery, which
	// may already be ahead of the snapshot taken for the response. Seeding
	// after it must not roll the mirror back to the older, shorter snapshot.
	t.Run("push first, already ahead of the snapshot", func(t *testing.T) {
		var m sessionMirror
		m.subscribed.Store(true)
		m.applySessionUpdate(sessionPush(t, "a", 3, `{"id":"1"}`, `{"id":"2"}`, `{"id":"3"}`))
		m.seed(log, len(log), "a")
		if got := len(m.getEntries()); got != 3 {
			t.Errorf("seed rolled the mirror back to its stale snapshot: got %d entries, want 3", got)
		}
	})

	// After such a race the cursor must still line up, or the next append is
	// read as a session switch and the history is discarded.
	t.Run("push first, then a later append", func(t *testing.T) {
		var m sessionMirror
		m.subscribed.Store(true)
		m.applySessionUpdate(sessionPush(t, "a", 3, `{"id":"1"}`, `{"id":"2"}`, `{"id":"3"}`))
		m.seed(log, len(log), "a")
		m.applySessionUpdate(sessionPush(t, "a", 4, `{"id":"4"}`))
		if got := len(m.getEntries()); got != 4 {
			t.Errorf("the append after a seed/push race was lost: got %d entries, want 4", got)
		}
	})
}

// Seeding installs the leaf so the first getBranch after subscribe walks the
// real branch instead of falling back to every entry.
func TestSeedInstallsTheLeaf(t *testing.T) {
	var m sessionMirror
	m.seed([]json.RawMessage{json.RawMessage(`{"id":"1"}`)}, 1, "leaf-9")
	if m.leafID != "leaf-9" {
		t.Errorf("leafID = %q; want leaf-9", m.leafID)
	}
}

// The host sends entryCount 0 with no entries to extensions that have not
// subscribed. That shape says nothing about the log, so a subscriber must not
// read it as an empty session and discard what it already holds.
func TestEmptyPushDoesNotWipeAMirror(t *testing.T) {
	var m sessionMirror
	m.subscribed.Store(true)
	m.seed([]json.RawMessage{json.RawMessage(`{"id":"1"}`)}, 1, "a")

	m.applySessionUpdate(sessionPush(t, "a", 0))

	if got := len(m.getEntries()); got != 1 {
		t.Errorf("an empty push discarded the log: got %d entries, want 1", got)
	}
}

// The decoded branch is cached per branch revision. A cache that outlives the
// entries it was built from is worse than no cache: the extension acts on a
// conversation that no longer exists.
func TestDecodedBranchCacheIsInvalidated(t *testing.T) {
	newMirror := func() *sessionMirror {
		m := &sessionMirror{}
		m.subscribed.Store(true)
		m.applySessionUpdate(sessionPush(t, "a", 1, `{"id":"a","type":"message","message":{"role":"user"}}`))
		if got := len(m.getBranchEntries()); got != 1 {
			t.Fatalf("seed branch = %d entries, want 1", got)
		}
		return m
	}

	t.Run("append extends the branch", func(t *testing.T) {
		m := newMirror()
		m.applySessionUpdate(sessionPush(t, "b", 2, `{"id":"b","parentId":"a","type":"message","message":{"role":"assistant"}}`))
		if got := len(m.getBranchEntries()); got != 2 {
			t.Errorf("after append the branch has %d entries, want 2", got)
		}
	})

	t.Run("leaf move re-walks the branch", func(t *testing.T) {
		m := newMirror()
		m.applySessionUpdate(sessionPush(t, "a", 3,
			`{"id":"b","parentId":"a","type":"message","message":{"role":"assistant"}}`,
			`{"id":"c","parentId":"b","type":"message","message":{"role":"user"}}`))
		if got := len(m.getBranchEntries()); got != 1 {
			t.Fatalf("leaf a should yield 1 entry, got %d", got)
		}
		// Moving the leaf to the tip must expose the whole chain.
		m.applySessionUpdate(sessionPush(t, "c", 3))
		if got := len(m.getBranchEntries()); got != 3 {
			t.Errorf("after the leaf moved to c the branch has %d entries, want 3", got)
		}
	})

	t.Run("a seed replaces a decoded branch", func(t *testing.T) {
		m := &sessionMirror{}
		m.subscribed.Store(true)
		m.seed([]json.RawMessage{json.RawMessage(`{"id":"x","type":"message","message":{"role":"user"}}`)}, 1, "x")
		if got := len(m.getBranchEntries()); got != 1 {
			t.Fatalf("seeded branch = %d, want 1", got)
		}
		m.applySessionUpdate(sessionPush(t, "y", 2, `{"id":"y","parentId":"x","type":"message","message":{"role":"assistant"}}`))
		if got := len(m.getBranchEntries()); got != 2 {
			t.Errorf("after append the branch has %d entries, want 2", got)
		}
	})

	// The caller receives a copy; mutating it must not corrupt the cache.
	t.Run("callers cannot corrupt the cache", func(t *testing.T) {
		m := newMirror()
		first := m.getBranchEntries()
		first[0] = &BranchEntry{}
		if second := m.getBranchEntries(); second[0].Role == "" {
			t.Error("a caller's mutation reached the cached branch")
		}
	})
}

// Entries can change while the leaf does not: a re-subscribe reseeds the log,
// and appends can land on a branch other than the current one. A cache keyed on
// the leaf serves a stale branch in exactly those cases, which is why both
// caches key on a revision that every mutation bumps.
func TestBranchCacheIsInvalidatedWhenTheLeafDoesNotMove(t *testing.T) {
	m := &sessionMirror{}
	m.subscribed.Store(true)
	m.applySessionUpdate(sessionPush(t, "a", 1, `{"id":"a","type":"message","message":{"role":"user"}}`))
	if got := len(m.getBranchEntries()); got != 1 {
		t.Fatalf("initial branch = %d, want 1", got)
	}

	// Same leaf, more entries: an ancestor arrives late, so the branch through
	// the unchanged leaf is now longer.
	m.applySessionUpdate(sessionPush(t, "", 2, `{"id":"z","type":"message","message":{"role":"user"}}`))
	if m.revision == 0 {
		t.Fatal("an append left the revision unchanged")
	}
	if got := len(m.getEntries()); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}

	// The raw and decoded branches must agree; a cache surviving the append
	// would leave them inconsistent.
	if raw, decoded := len(m.getBranch()), len(m.getBranchEntries()); raw != decoded {
		t.Errorf("raw branch has %d entries but the decoded branch has %d", raw, decoded)
	}
}
