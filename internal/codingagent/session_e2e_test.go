// End-to-end conversation flow tests for
//
// These tests prove the round-trip the user actually cares about:
// hold a real *agent.Agent + *Session pair, simulate a multi-turn
// conversation by calling AppendMessage + agent.SetMessages the way
// interactive.go does, then exercise /fork and /resume and assert
// that the agent's in-memory message history matches what upstream's
// session-manager would produce.
//
// We don't need a live LLM here: we're testing the persistence
// bridge and branch-walking logic, not the LLM itself. The agent is
// constructed bare and we drive its message slice directly via the
// same SetMessages path the production code uses.

package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// simulateTurn mirrors what interactive.go's handleSubmit does on a
// successful agent.Send: persist the user message, append agent
// messages to history, persist the new tail. Returns the entry id of
// the user message we just persisted.
func simulateTurn(t *testing.T, sess *Session, agent *agent.Agent, userText, assistantText string) (userEntryID, asstEntryID string) {
	t.Helper()
	userMsg := mkUserMsg(userText)
	uid, err := sess.AppendMessage(userMsg)
	if err != nil {
		t.Fatal(err)
	}
	agent.SetMessages(append(agent.Messages(), userMsg))

	asstMsg := mkAssistantMsg(assistantText)
	aid, err := sess.AppendMessage(asstMsg)
	if err != nil {
		t.Fatal(err)
	}
	agent.SetMessages(append(agent.Messages(), asstMsg))
	return uid, aid
}

// TestE2E_ConversationFlowAndResumeFromDisk: send 3 turns, drop the
// agent, reload the session from disk, prove the rebuilt agent sees
// the same 6-message history.
func TestE2E_ConversationFlowAndResumeFromDisk(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-e2e", "")
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.NewAgent(agent.AgentOptions{})

	simulateTurn(t, sess, ag, "what is 2+2", "4")
	simulateTurn(t, sess, ag, "and 3+3", "6")
	simulateTurn(t, sess, ag, "and 5+5", "10")

	if got := len(ag.Messages()); got != 6 {
		t.Fatalf("agent messages=%d want 6", got)
	}

	// Simulate process restart: drop agent, reload session via SessionManager.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := agent.NewAgent(agent.AgentOptions{})
	rebuilt.SetMessages(loaded.BuildContext(nil))

	got := rebuilt.Messages()
	if len(got) != 6 {
		t.Fatalf("rebuilt messages=%d want 6", len(got))
	}
	for i, want := range []string{"what is 2+2", "4", "and 3+3", "6", "and 5+5", "10"} {
		txt := extractAnyText(got[i])
		if !strings.Contains(txt, want) {
			t.Errorf("rebuilt[%d]=%q want contains %q", i, txt, want)
		}
	}
}

// TestE2E_ForkRewindsAgentHistoryAndExcludesAbandonedTail: reproduce
// the bug we found in 3.1c \u2014 after /fork, agent.Messages() must NOT
// contain the abandoned tail. Without forkAndRebuild, this test fails.
func TestE2E_ForkRewindsAgentHistoryAndExcludesAbandonedTail(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-fork-e2e", "")
	agent := agent.NewAgent(agent.AgentOptions{})

	uid1, _ := simulateTurn(t, sess, agent, "first question", "first answer")
	simulateTurn(t, sess, agent, "DEAD-END question", "DEAD-END answer")
	simulateTurn(t, sess, agent, "ALSO ABANDONED", "also abandoned reply")

	if len(agent.Messages()) != 6 {
		t.Fatalf("pre-fork: agent has %d messages, want 6", len(agent.Messages()))
	}

	// Fork at the assistant reply to the first question. The user's
	// next turn should branch from there.
	uid1Asst := findFirstAssistantAfter(t, sess, uid1)
	forkPoint := uid1Asst

	if err := forkAndRebuild(sess, agent, forkPoint); err != nil {
		t.Fatal(err)
	}

	got := agent.Messages()
	if len(got) != 2 {
		t.Fatalf("post-fork: agent has %d messages, want 2 (just first turn)", len(got))
	}
	if !strings.Contains(extractAnyText(got[0]), "first question") {
		t.Errorf("got[0]=%q want 'first question'", extractAnyText(got[0]))
	}
	if !strings.Contains(extractAnyText(got[1]), "first answer") {
		t.Errorf("got[1]=%q want 'first answer'", extractAnyText(got[1]))
	}
	for _, m := range got {
		if strings.Contains(extractAnyText(m), "DEAD-END") || strings.Contains(extractAnyText(m), "ABANDONED") {
			t.Errorf("abandoned tail leaked into agent history: %q", extractAnyText(m))
		}
	}

	// Now send a NEW turn on the new branch and verify on-disk parent
	// chain points back through the fork point, NOT through the
	// abandoned tail.
	uid4, _ := simulateTurn(t, sess, agent, "alt question", "alt answer")
	if len(agent.Messages()) != 4 {
		t.Fatalf("after new turn: agent has %d messages, want 4", len(agent.Messages()))
	}

	// Reload from disk and verify BuildContext sees the alt branch.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	ctx := loaded.BuildContext(nil)
	if len(ctx) != 4 {
		t.Fatalf("reloaded context has %d, want 4", len(ctx))
	}
	for _, m := range ctx {
		if strings.Contains(extractAnyText(m), "DEAD-END") {
			t.Errorf("reloaded context still includes abandoned tail: %q", extractAnyText(m))
		}
	}
	// And both branches still on disk \u2014 the fork doesn't delete.
	allEntries := loadAllEntries(t, sess.Path())
	foundDeadEnd := false
	for _, e := range allEntries {
		if me, ok := e.AsMessage(); ok {
			if strings.Contains(extractMessageText(me), "DEAD-END") {
				foundDeadEnd = true
			}
		}
	}
	if !foundDeadEnd {
		t.Errorf("DEAD-END entry should still be on disk after fork (orphaned, not deleted)")
	}
	_ = uid4
}

// TestE2E_TwoBranchesCoexistOnDisk: fork twice from same point, send
// a different message on each branch, verify both subtrees coexist
// in the same JSONL via parentId chains.
func TestE2E_TwoBranchesCoexistOnDisk(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-multi-branch", "")
	agent := agent.NewAgent(agent.AgentOptions{})

	uid1, aid1 := simulateTurn(t, sess, agent, "root question", "root answer")
	_ = uid1

	// Branch A
	if err := forkAndRebuild(sess, agent, aid1); err != nil {
		t.Fatal(err)
	}
	simulateTurn(t, sess, agent, "branch A path", "answer A")

	// Branch B \u2014 fork back to the same point.
	if err := forkAndRebuild(sess, agent, aid1); err != nil {
		t.Fatal(err)
	}
	simulateTurn(t, sess, agent, "branch B path", "answer B")

	// Agent now sees only branch B (the latest fork's path-to-leaf).
	got := agent.Messages()
	hasB := false
	for _, m := range got {
		if strings.Contains(extractAnyText(m), "branch B") {
			hasB = true
		}
		if strings.Contains(extractAnyText(m), "branch A") {
			t.Errorf("branch A leaked into branch B's agent history: %q", extractAnyText(m))
		}
	}
	if !hasB {
		t.Error("branch B not in agent history")
	}

	// Both branches must be on disk.
	allEntries := loadAllEntries(t, sess.Path())
	foundA, foundB := false, false
	for _, e := range allEntries {
		if me, ok := e.AsMessage(); ok {
			txt := extractMessageText(me)
			if strings.Contains(txt, "branch A") {
				foundA = true
			}
			if strings.Contains(txt, "branch B") {
				foundB = true
			}
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected both branches on disk; foundA=%v foundB=%v", foundA, foundB)
	}

	// Tree builder should reflect both branches as siblings off aid1.
	tree := sess.Tree()
	leafCount := countLeafs(tree)
	if leafCount < 2 {
		t.Errorf("expected at least 2 leaf nodes (one per branch), got %d", leafCount)
	}
}

// TestE2E_CloneCreatesIndependentFile: clone the current path,
// continue conversation in the clone, prove the source file is
// unchanged and the clone has its own entries.
func TestE2E_CloneCreatesIndependentFile(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-source", "")
	agent := agent.NewAgent(agent.AgentOptions{})
	simulateTurn(t, sess, agent, "shared turn 1", "shared reply 1")
	simulateTurn(t, sess, agent, "shared turn 2", "shared reply 2")

	leaf := sess.LeafID()
	if leaf == nil {
		t.Fatal("nil leaf")
		return
	}
	clone, err := sm.Clone(sess, *leaf)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Path() == sess.Path() {
		t.Fatal("clone reused source path")
	}

	// Continue in the clone (agent context already matches \u2014 clone is
	// linear path-to-leaf which equals what agent has).
	simulateTurn(t, clone, agent, "clone-only turn", "clone-only reply")

	srcEntries := loadAllEntries(t, sess.Path())
	for _, e := range srcEntries {
		if me, ok := e.AsMessage(); ok {
			if strings.Contains(extractMessageText(me), "clone-only") {
				t.Error("clone-only message leaked into source file")
			}
		}
	}
	cloneEntries := loadAllEntries(t, clone.Path())
	hasClone := false
	hasShared := false
	for _, e := range cloneEntries {
		if me, ok := e.AsMessage(); ok {
			txt := extractMessageText(me)
			if strings.Contains(txt, "clone-only") {
				hasClone = true
			}
			if strings.Contains(txt, "shared turn 1") {
				hasShared = true
			}
		}
	}
	if !hasClone {
		t.Error("clone-only message missing from clone file")
	}
	if !hasShared {
		t.Error("shared history missing from clone file (linear path-to-leaf should include it)")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func extractAnyText(m agent.AgentMessage) string {
	switch {
	case m.User != nil:
		var b strings.Builder
		for _, c := range m.User.Content {
			if t, ok := c.(ai.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	case m.Assistant != nil:
		var b strings.Builder
		for _, c := range m.Assistant.Content {
			if t, ok := c.(ai.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	}
	return ""
}

func findFirstAssistantAfter(t *testing.T, sess *Session, userID string) string {
	t.Helper()
	leaf := sess.LeafID()
	if leaf == nil {
		t.Fatal("no leaf")
		return ""
	}
	path := sess.Branch(*leaf)
	seenUser := false
	for _, e := range path {
		if e.Base.ID == userID {
			seenUser = true
			continue
		}
		if !seenUser {
			continue
		}
		if me, ok := e.AsMessage(); ok && me.Message.Assistant != nil {
			return e.Base.ID
		}
	}
	t.Fatalf("no assistant entry found after %s", userID)
	return ""
}

func loadAllEntries(t *testing.T, path string) []SessionEntry {
	t.Helper()
	sm := &SessionManager{}
	loaded, err := sm.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.entries
}

func countLeafs(n *SessionTreeNode) int {
	if n == nil {
		return 0
	}
	if len(n.Children) == 0 {
		// Root with no children = empty tree.
		if n.Entry.Base.ID == "" {
			return 0
		}
		return 1
	}
	c := 0
	for _, k := range n.Children {
		c += countLeafs(k)
	}
	return c
}
