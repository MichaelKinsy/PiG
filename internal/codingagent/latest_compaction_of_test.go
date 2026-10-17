package codingagent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// requireLatestCompactionMatchesTheBranch fails when the newest compaction read from the view differs from the one a walk of the branch finds.
func requireLatestCompactionMatchesTheBranch(t *testing.T, s *Session, after string) {
	t.Helper()
	want := GetLatestCompactionEntry(s.GetContextBranch())
	got := LatestCompactionEntryOf(s.BuildSessionProjectionView())
	if (got == nil) != (want == nil) || got != nil && (got.ID != want.ID || got.Timestamp != want.Timestamp || got.Summary != want.Summary) {
		t.Fatalf("after %s the projection's latest compaction is %+v, the branch's is %+v", after, got, want)
	}
}

// The newest compaction of the branch is the first entry of its projection whatever built the branch: none, one, a second whose kept range holds the first, a leaf moved to before them, a fork, and an empty branch.
func TestLatestCompactionEntryOfEqualsTheBranchWalk(t *testing.T) {
	s := NewSession("latest-compaction", "/project")
	requireLatestCompactionMatchesTheBranch(t, s, "an empty session")
	var ids []string
	add := func(message agent.AgentMessage) {
		t.Helper()
		id, err := s.AppendMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for i := range 6 {
		add(mkUserMsg(fmt.Sprintf("ask %d", i)))
		add(mkAssistantMsg(fmt.Sprintf("answer %d", i)))
		requireLatestCompactionMatchesTheBranch(t, s, "a turn without a compaction")
	}
	if want := GetLatestCompactionEntry(s.GetContextBranch()); want != nil {
		t.Fatalf("a branch without a compaction has %+v", want)
	}
	first, err := s.AppendCompaction("first", ids[4], 100, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireLatestCompactionMatchesTheBranch(t, s, "a first compaction")
	add(mkUserMsg("after the first"))
	requireLatestCompactionMatchesTheBranch(t, s, "a message after the first compaction")
	// The second compaction keeps entries from before the first, so the first stays in the projected entries behind the second.
	second, err := s.AppendCompaction("second", ids[2], 100, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireLatestCompactionMatchesTheBranch(t, s, "a second compaction keeping the first")
	if got := LatestCompactionEntryOf(s.BuildSessionProjectionView()); got == nil || got.ID != second {
		t.Fatalf("latest compaction %+v, want the second (%s)", got, second)
	}
	add(mkAssistantMsg("after the second"))
	requireLatestCompactionMatchesTheBranch(t, s, "a message after the second compaction")
	if err := s.SetLeafID(&ids[3]); err != nil {
		t.Fatal(err)
	}
	requireLatestCompactionMatchesTheBranch(t, s, "a leaf before both compactions")
	if got := LatestCompactionEntryOf(s.BuildSessionProjectionView()); got != nil {
		t.Fatalf("a branch before both compactions has %+v", got)
	}
	if err := s.Branch(first); err != nil {
		t.Fatal(err)
	}
	requireLatestCompactionMatchesTheBranch(t, s, "a leaf on the first compaction")
	if got := LatestCompactionEntryOf(s.BuildSessionProjectionView()); got == nil || got.ID != first {
		t.Fatalf("latest compaction %+v, want the first (%s)", got, first)
	}
	s.ResetLeaf()
	requireLatestCompactionMatchesTheBranch(t, s, "a reset leaf")
}
