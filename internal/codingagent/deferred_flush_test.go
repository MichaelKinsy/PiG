package codingagent

import (
	"os"
	"slices"
	"testing"
)

// TestDeferredFlush_FileCreatedAtFirstUserMessage verifies upstream 0.99.1's
// SessionManager._persist rule (session-manager.ts:1172-1185): a fresh session
// writes nothing until it holds a user or assistant message. The first user
// message flushes the header and every buffered entry (#10000: the prompt
// survives a first turn that never completes); later entries append.
func TestDeferredFlush_FileCreatedAtFirstUserMessage(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-deferred", "")
	if err != nil {
		t.Fatal(err)
	}

	// Setup entries stay buffered.
	if err := sess.AppendThinkingLevelChange("off"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path()); !os.IsNotExist(err) {
		t.Fatalf("file must not exist before a user or assistant message; stat err = %v", err)
	}

	// The user message flushes the header and the buffered setup entry.
	if _, err := sess.AppendMessage(mkUserMsg("hello")); err != nil {
		t.Fatal(err)
	}
	if got := readSessionFileRoles(t, sess.Path()); !slices.Equal(got, []string{"session", "thinking_level_change", "user"}) {
		t.Fatalf("roles after the first user message = %v", got)
	}

	// The reply appends to the flushed file.
	if _, err := sess.AppendMessage(mkAssistantMsg("hi back")); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSessionFile(sess.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(loaded.Entries()); got != 3 {
		t.Fatalf("expected 3 persisted entries, got %d", got)
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
