//go:build !race

package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// A boundary preview with drafts holds the branch and then the drafts (agent-session.ts _createBoundaryPreviewManager). Its context is the branch's own, so the preview is O(branch) as Pi's is, and each entry costs the projection's copy and the handler-facing lists, not an encode and decode of the entry's record: the preview took each entry through AppendEntry at 63 allocations and 5.4 KB per entry, a build at 16,000 entries took 0.6 s.
func TestBoundaryPreviewWithDraftsCostsAFewAllocationsPerEntry(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	const exchanges, entriesPerExchange = 250, 4
	result := testscaling.Check(t, testscaling.Case{Name: "boundary preview with a draft at history n", Size: exchanges, Bound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, 1).session
		drafts := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "draft"}}
		return func() {
			if _, _, _, err := s.projectBoundaryContext(drafts, icodingagent.EventTurnEnd); err != nil {
				panic(err)
			}
		}
	}})
	entries := float64(exchanges * entriesPerExchange)
	if perEntry := result.Small.Allocs / entries; perEntry > 30 {
		t.Errorf("a preview with drafts allocated %.1f objects per entry, want at most 30", perEntry)
	}
	if perEntry := result.Small.Bytes / entries; perEntry > 4000 {
		t.Errorf("a preview with drafts allocated %.0f bytes per entry, want at most 4000", perEntry)
	}
}
