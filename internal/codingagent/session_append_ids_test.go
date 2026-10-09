package codingagent

import (
	"slices"
	"testing"
)

// session-manager.ts appendThinkingLevelChange and appendLabelChange return the new entry's id; getBranch(fromId?) walks to the root from fromId, or from the leaf when fromId is omitted.
func TestAppendThinkingLevelChangeAndLabelChangeReturnTheNewEntryID(t *testing.T) {
	s := NewSession("s", t.TempDir())
	first, err := s.AppendMessage(contextEditUser("one"))
	if err != nil {
		t.Fatal(err)
	}
	thinkingID, err := s.AppendThinkingLevelChange("high")
	if err != nil {
		t.Fatal(err)
	}
	if leaf := s.GetLeafID(); leaf == nil || *leaf != thinkingID || thinkingID == first {
		t.Fatalf("thinking id %q, leaf %v, first %q", thinkingID, leaf, first)
	}
	if entry, ok := s.GetEntry(thinkingID); !ok || entry.Base().Type != "thinking_level_change" {
		t.Fatalf("thinking entry %+v ok=%v", entry, ok)
	}
	label := "checkpoint"
	labelID, err := s.AppendLabelChange(first, &label)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := s.GetEntry(labelID); !ok || entry.Base().Type != "label" || labelID == thinkingID {
		t.Fatalf("label entry %+v ok=%v", entry, ok)
	}
	if leaf := s.GetLeafID(); leaf == nil || *leaf != labelID {
		t.Fatalf("leaf %v, want %q", leaf, labelID)
	}
	if id, err := s.AppendLabelChange("missing", &label); err == nil || id != "" {
		t.Fatalf("missing target: id %q err %v", id, err)
	}
}

func TestGetBranchStartsFromTheGivenEntryOrTheLeaf(t *testing.T) {
	s := NewSession("s", t.TempDir())
	first, _ := s.AppendMessage(contextEditUser("one"))
	second, _ := s.AppendMessage(contextEditUser("two"))
	third, _ := s.AppendMessage(contextEditUser("three"))
	ids := func(entries []SessionEntry) []string {
		var out []string
		for _, entry := range entries {
			out = append(out, entry.Base().ID)
		}
		return out
	}
	if got := ids(s.GetBranch()); !slices.Equal(got, []string{first, second, third}) {
		t.Errorf("leaf branch %v", got)
	}
	if got := ids(s.GetBranch(second)); !slices.Equal(got, []string{first, second}) {
		t.Errorf("branch from second %v", got)
	}
	if got := s.GetBranch("missing"); len(got) != 0 {
		t.Errorf("branch from missing id %v", ids(got))
	}
}
