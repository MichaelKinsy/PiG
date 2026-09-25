package codingagent

import (
	"os"
	"testing"
)

// TestDeferredFlush_NoFileUntilAssistant verifies upstream's _persist
// hasAssistant gate: a fresh session writes nothing to disk until the
// first assistant message, so abandoned (opened, never answered)
// sessions leave no empty .jsonl file.
func TestDeferredFlush_NoFileUntilAssistant(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-deferred", "")
	if err != nil {
		t.Fatal(err)
	}

	// User/tool entries before any assistant reply stay buffered.
	if _, err := sess.AppendMessage(mkUserMsg("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path()); !os.IsNotExist(err) {
		t.Fatalf("file must not exist before assistant message; stat err = %v", err)
	}

	// The assistant message flushes header + ALL buffered entries (the
	// earlier user message is written retroactively, not lost).
	if _, err := sess.AppendMessage(mkAssistantMsg("hi back")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path()); err != nil {
		t.Fatalf("file must exist after assistant message: %v", err)
	}

	loaded, err := loadSessionFile(sess.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entries := loaded.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 persisted entries (user retroactively flushed + assistant), got %d", len(entries))
	}
	if entries[0].Base.Type != "message" {
		t.Errorf("entry[0] type = %q, want message", entries[0].Base.Type)
	}

	// Subsequent entries append to the now-flushed file.
	if _, err := sess.AppendMessage(mkUserMsg("again")); err != nil {
		t.Fatal(err)
	}
	loaded2, _ := loadSessionFile(sess.Path())
	if got := len(loaded2.Entries()); got != 3 {
		t.Fatalf("expected 3 entries after post-flush append, got %d", got)
	}
}

// TestDeferredFlush_AbandonedLeavesNoFile is the user-reported symptom:
// opening a session and never exchanging a message must not litter the
// session dir with empty files.
func TestDeferredFlush_AbandonedLeavesNoFile(t *testing.T) {
	sm := tempSessionMgr(t)
	if _, err := sm.Create("sess-abandoned", ""); err != nil {
		t.Fatal(err)
	}
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("abandoned session should leave no file; ListSessions returned %d", len(infos))
	}
}
