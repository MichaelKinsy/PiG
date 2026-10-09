package codingagent

import (
	"slices"
	"testing"
)

func entryIDs(entries []SessionEntry) []string {
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Base().ID)
	}
	return out
}

// session-manager.ts getLeafEntry returns the entry at the leaf, undefined before any entry or after resetLeaf; resetLeaf makes the next append a root.
// Pi: packages/coding-agent/src/core/session-manager.ts:1408 (Session.getLeafEntry).
func TestGetLeafEntryAndResetLeaf(t *testing.T) {
	s := NewSession("s", t.TempDir())
	if _, ok := s.GetLeafEntry(); ok {
		t.Fatal("empty session has a leaf entry")
	}
	first, _ := s.AppendMessage(contextEditUser("one"))
	second, _ := s.AppendMessage(contextEditUser("two"))
	if entry, ok := s.GetLeafEntry(); !ok || entry.Base().ID != second {
		t.Fatalf("leaf entry %+v ok=%v, want %s", entry, ok, second)
	}
	if err := s.Branch(first); err != nil {
		t.Fatal(err)
	}
	if entry, ok := s.GetLeafEntry(); !ok || entry.Base().ID != first {
		t.Fatalf("after branch: leaf entry %+v ok=%v, want %s", entry, ok, first)
	}
	s.ResetLeaf()
	if _, ok := s.GetLeafEntry(); ok || s.GetLeafID() != nil {
		t.Fatal("resetLeaf did not clear the leaf")
	}
	root, _ := s.AppendMessage(contextEditUser("new root"))
	if entry, _ := s.GetEntry(root); entry.Base().ParentID != nil {
		t.Fatalf("append after resetLeaf has parent %v, want nil", *entry.Base().ParentID)
	}
}

// session-manager.ts getChildren lists the direct children of an entry in append order, and none for an unknown or leaf entry.
// Pi: packages/coding-agent/src/core/session-manager.ts:1419 (Session.getChildren).
func TestGetChildrenListsDirectChildrenInAppendOrder(t *testing.T) {
	s := NewSession("s", t.TempDir())
	root, _ := s.AppendMessage(contextEditUser("root"))
	a, _ := s.AppendMessage(contextEditUser("a"))
	aChild, _ := s.AppendMessage(contextEditUser("a child"))
	if err := s.Branch(root); err != nil {
		t.Fatal(err)
	}
	b, _ := s.AppendMessage(contextEditUser("b"))
	if got := entryIDs(s.GetChildren(root)); !slices.Equal(got, []string{a, b}) {
		t.Errorf("children of root %v, want %v", got, []string{a, b})
	}
	if got := entryIDs(s.GetChildren(a)); !slices.Equal(got, []string{aChild}) {
		t.Errorf("children of a %v", got)
	}
	if got := s.GetChildren(b); len(got) != 0 {
		t.Errorf("children of a leaf %v", entryIDs(got))
	}
	if got := s.GetChildren("missing"); len(got) != 0 {
		t.Errorf("children of a missing id %v", entryIDs(got))
	}
}

// session-manager.ts getLabel returns the latest label set on an entry; a later empty or cleared label removes it.
// Pi: packages/coding-agent/src/core/session-manager.ts:1432 (Session.getLabel).
func TestGetLabelReturnsTheLatestLabelAndClears(t *testing.T) {
	s := NewSession("s", t.TempDir())
	first, _ := s.AppendMessage(contextEditUser("one"))
	second, _ := s.AppendMessage(contextEditUser("two"))
	if _, ok := s.GetLabel(first); ok {
		t.Fatal("unlabeled entry has a label")
	}
	one, two, empty := "one", "two", ""
	_, _ = s.AppendLabelChange(first, &one)
	_, _ = s.AppendLabelChange(second, &two)
	if label, ok := s.GetLabel(first); !ok || label != "one" {
		t.Fatalf("label of first %q ok=%v", label, ok)
	}
	renamed := "renamed"
	_, _ = s.AppendLabelChange(first, &renamed)
	if label, _ := s.GetLabel(first); label != "renamed" {
		t.Fatalf("label after rename %q", label)
	}
	if label, _ := s.GetLabel(second); label != "two" {
		t.Fatalf("label of second %q", label)
	}
	_, _ = s.AppendLabelChange(first, &empty)
	if _, ok := s.GetLabel(first); ok {
		t.Fatal("empty label did not clear")
	}
	_, _ = s.AppendLabelChange(second, nil)
	if _, ok := s.GetLabel(second); ok {
		t.Fatal("nil label did not clear")
	}
}

// session-manager.ts usesDefaultSessionDir is true when the session directory is the cwd's default directory.
// Pi: packages/coding-agent/src/core/session-manager.ts:1148 (Session.usesDefaultSessionDir).
func TestUsesDefaultSessionDir(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	dir := defaultSessionDir(cwd)
	def, err := NewSessionManagerWithDir(cwd, dir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !def.UsesDefaultSessionDir() {
		t.Errorf("session in %s: UsesDefaultSessionDir = false", dir)
	}
	custom, err := NewSessionManagerWithDir(cwd, t.TempDir()).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if custom.UsesDefaultSessionDir() {
		t.Error("session in a custom directory reports the default one")
	}
	if NewSession("s", cwd).UsesDefaultSessionDir() {
		t.Error("in-memory session reports the default directory")
	}
}

// session-manager.ts buildSessionContext and buildContextEntries read the branch ending at the current leaf: branching back shortens the context, resetLeaf empties it, and the settings come from that branch's change entries.
// Pi: packages/coding-agent/src/core/agent-session.ts:1797 (Session.buildSessionContext).
// Pi: packages/coding-agent/src/core/session-manager.ts:476 (Session.buildContextEntries).
func TestSessionBuildSessionContextAndContextEntriesFollowTheLeaf(t *testing.T) {
	s := NewSession("s", t.TempDir())
	first, _ := s.AppendMessage(contextEditUser("one"))
	if _, err := s.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendModelChange("anthropic", "claude"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.AppendMessage(contextEditUser("two"))

	context := s.BuildSessionContext()
	if len(context.Messages) != 2 || context.ThinkingLevel != "high" || context.Model == nil || context.Model.Provider != "anthropic" || context.Model.ModelID != "claude" {
		t.Fatalf("context = %+v", context)
	}
	if got := entryIDs(s.BuildContextEntries()); len(got) != 4 || got[0] != first || got[3] != second {
		t.Fatalf("context entries = %v", got)
	}

	if err := s.Branch(first); err != nil {
		t.Fatal(err)
	}
	context = s.BuildSessionContext()
	if len(context.Messages) != 1 || context.ThinkingLevel != "off" || context.Model != nil {
		t.Fatalf("context after branching to the first message = %+v", context)
	}
	if got := entryIDs(s.BuildContextEntries()); len(got) != 1 || got[0] != first {
		t.Fatalf("context entries after branching = %v", got)
	}

	s.ResetLeaf()
	if context = s.BuildSessionContext(); len(context.Messages) != 0 || context.ThinkingLevel != "off" || context.Model != nil {
		t.Fatalf("context after resetLeaf = %+v", context)
	}
	if got := s.BuildContextEntries(); len(got) != 0 {
		t.Fatalf("context entries after resetLeaf = %v", got)
	}
}
