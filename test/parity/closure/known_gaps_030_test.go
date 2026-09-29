package closure

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An owner-approved known difference is imported scrutiny, not behavioral proof
// or a closure waiver. Keep the 2026-09-28 decisions visible in that state.
func TestApproved030GapsRemainProvisionalNotWaived(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("approved-030-known-gaps"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:approved-030-known-gaps",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RenderReport(graph, DivergenceDashboardDataset)
	if err != nil {
		t.Fatal(err)
	}
	rows := markdownTableRows(t, report, "| divergence | title | imported scrutiny |")
	// These IDs are the owner's explicit decision set, not an observed ledger count.
	for _, id := range []string{"D78", "D82", "D83"} {
		index := slices.IndexFunc(rows, func(row []string) bool { return len(row) > 0 && row[0] == id })
		if index < 0 {
			t.Errorf("approved known gap %s is missing from the dashboard", id)
			continue
		}
		row := rows[index]
		if len(row) != 9 || !slices.Equal(row[2:8], []string{"approved", "0", "0", "1", "0", "0"}) {
			t.Errorf("%s must remain approved/provisional with no proven or waived credit: %v", id, row)
		}
	}
}
