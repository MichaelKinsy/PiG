package codingagent

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// requireHasContextEditsMatchesTheEntries fails when the view's HasContextEdits differs from a scan of its entries, for the cached view and a fresh projection.
func requireHasContextEditsMatchesTheEntries(t *testing.T, s *Session, after string) {
	t.Helper()
	for name, projection := range map[string]SessionProjection{"view": s.BuildSessionProjectionView(), "fresh": freshProjectionView(s)} {
		want := slices.ContainsFunc(projection.Entries, func(entry ProjectedSessionEntry) bool { return entry.SourceEntry.Base().Type == "context_edit" })
		if projection.HasContextEdits != want {
			t.Fatalf("after %s the %s projection has HasContextEdits %v, its entries say %v", after, name, projection.HasContextEdits, want)
		}
	}
}

// HasContextEdits follows the projected entries through appends (which extend the cached view), a context edit (which rebuilds it), a compaction that drops the edit from the projected entries, and a leaf moved to before the edit.
func TestProjectionHasContextEditsEqualsTheProjectedEntries(t *testing.T) {
	s := NewSession("context-edits", "/project")
	var ids []string
	add := func(message agent.AgentMessage) {
		t.Helper()
		id, err := s.AppendMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	requireHasContextEditsMatchesTheEntries(t, s, "an empty session")
	for i := range 5 {
		add(mkUserMsg(fmt.Sprintf("ask %d", i)))
		add(mkAssistantMsg(fmt.Sprintf("answer %d", i)))
		requireHasContextEditsMatchesTheEntries(t, s, "a turn")
	}
	edit, err := s.AppendContextEdit(ids[2], &ContextEditReplacement{Content: json.RawMessage(`"edited"`)})
	if err != nil {
		t.Fatal(err)
	}
	requireHasContextEditsMatchesTheEntries(t, s, "a context edit")
	if !s.BuildSessionProjectionView().HasContextEdits {
		t.Fatal("a branch with a context edit reports none")
	}
	add(mkUserMsg("after the edit"))
	requireHasContextEditsMatchesTheEntries(t, s, "a message after the edit (the cached view extends)")
	// A compaction that keeps only entries after the edit leaves it out of the projected entries.
	if _, err := s.AppendCompaction("summary", ids[len(ids)-1], 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	requireHasContextEditsMatchesTheEntries(t, s, "a compaction past the edit")
	if s.BuildSessionProjectionView().HasContextEdits {
		t.Fatal("an edit before the compaction's first kept entry still counts")
	}
	add(mkAssistantMsg("after the compaction"))
	requireHasContextEditsMatchesTheEntries(t, s, "a message after the compaction")
	if err := s.SetLeafID(&edit); err != nil {
		t.Fatal(err)
	}
	requireHasContextEditsMatchesTheEntries(t, s, "a leaf on the edit")
	if err := s.SetLeafID(&ids[1]); err != nil {
		t.Fatal(err)
	}
	requireHasContextEditsMatchesTheEntries(t, s, "a leaf before the edit")
	s.ResetLeaf()
	requireHasContextEditsMatchesTheEntries(t, s, "a reset leaf")
}
