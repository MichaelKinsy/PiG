package codingagent

import "testing"

// TestCompactionTrimIndex verifies that resume rendering begins at the
// latest compaction's kept tail (FirstKeptEntryID), matching the aligned
// context produced by BuildContext. Without the trim, pig re-renders the
// pre-compaction history that compaction summarized away: wrong visually
// and the source of the long resume render on compacted sessions.
func TestCompactionTrimIndex(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-trim", "")
	if err != nil {
		t.Fatal(err)
	}

	_, _ = sess.AppendMessage(mkUserMsg("u1"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a1"))
	keepID, _ := sess.AppendMessage(mkUserMsg("u2")) // FirstKeptEntryID
	_, _ = sess.AppendMessage(mkAssistantMsg("a2"))
	_, _ = sess.AppendMessage(mkUserMsg("u3"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a3"))
	// Compaction keeps from u2 onward; u1/a1 collapse to the summary.
	if _, err := sess.AppendCompaction("summary text", keepID, 12345, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = sess.AppendMessage(mkUserMsg("u4"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a4"))

	leaf := sess.LeafID()
	if leaf == nil {
		t.Fatal("no leaf")
	}
	raw := sess.Branch(*leaf)
	branch := make([]parsedEntry, len(raw))
	wantStart := -1
	for i, e := range raw {
		branch[i] = parsedEntry{entry: e}
		if e.Base.ID == keepID {
			wantStart = i
		}
	}
	if wantStart < 0 {
		t.Fatal("keepID not on branch")
	}

	got := compactionTrimIndex(branch)
	if got != wantStart {
		t.Fatalf("compactionTrimIndex = %d, want %d (FirstKeptEntryID position)", got, wantStart)
	}

	// Rendered message entries (from the trim point) should equal the
	// aligned context minus the one synthetic compaction-summary message
	// (which pig renders as a chip, not a message).
	trimMsgs := 0
	for i := got; i < len(branch); i++ {
		if branch[i].entry.Base.Type == "message" {
			trimMsgs++
		}
	}
	ctx := sess.BuildContext(nil)
	if trimMsgs != len(ctx)-1 {
		t.Errorf("trimmed message count = %d, want %d (BuildContext %d - 1 summary)", trimMsgs, len(ctx)-1, len(ctx))
	}

	// The trim must actually drop the pre-compaction history.
	if got == 0 {
		t.Error("expected trim to skip pre-compaction entries, got renderStart=0")
	}
}

// TestCompactionTrimIndexNoCompaction verifies an uncompacted branch
// renders in full (renderStart 0).
func TestCompactionTrimIndexNoCompaction(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-nocompact", "")
	_, _ = sess.AppendMessage(mkUserMsg("u1"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a1"))

	leaf := sess.LeafID()
	raw := sess.Branch(*leaf)
	branch := make([]parsedEntry, len(raw))
	for i, e := range raw {
		branch[i] = parsedEntry{entry: e}
	}
	if got := compactionTrimIndex(branch); got != 0 {
		t.Errorf("compactionTrimIndex without compaction = %d, want 0", got)
	}
}
