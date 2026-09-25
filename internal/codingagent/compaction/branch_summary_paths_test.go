package compaction

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Upstream: packages/agent/test/harness/branch-summarization.test.ts. The target is on a sibling branch, not itself the common ancestor.
func TestCollectEntriesForBranchSummarySiblingTarget(t *testing.T) {
	session := &memSession{
		entries: map[string]codingagent.SessionEntry{
			"root":        makeMessageEntry("root", nil, 1),
			"common":      makeMessageEntry("common", new("root"), 1),
			"abandoned-1": makeMessageEntry("abandoned-1", new("common"), 1),
			"abandoned-2": makeMessageEntry("abandoned-2", new("abandoned-1"), 1),
			"target":      makeMessageEntry("target", new("common"), 1),
		},
		chains: map[string][]string{
			"abandoned-2": {"root", "common", "abandoned-1", "abandoned-2"},
			"target":      {"root", "common", "target"},
		},
	}
	result := CollectEntriesForBranchSummary(session, "abandoned-2", "target")
	if result.CommonAncestorID != "common" {
		t.Errorf("common ancestor = %q, want common", result.CommonAncestorID)
	}
	ids := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		ids = append(ids, entry.Base.ID)
	}
	// Exact order also excludes root, the common ancestor, and the target branch.
	if want := []string{"abandoned-1", "abandoned-2"}; !slices.Equal(ids, want) {
		t.Fatalf("abandoned entries = %v, want %v", ids, want)
	}
}

// Upstream: packages/agent/test/harness/branch-summarization.test.ts "returns no entries when there was no previous leaf" uses a real target entry.
func TestCollectEntriesForBranchSummaryNoPreviousLeaf(t *testing.T) {
	session := &memSession{
		entries: map[string]codingagent.SessionEntry{
			"target": makeMessageEntry("target", nil, 1),
		},
		chains: map[string][]string{"target": {"target"}},
	}
	result := CollectEntriesForBranchSummary(session, "", "target")
	if len(result.Entries) != 0 || result.CommonAncestorID != "" {
		t.Fatalf("result = %#v, want no entries and no common ancestor", result)
	}
}
