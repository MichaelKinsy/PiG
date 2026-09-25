package closure

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddCompactionSettingsVerticalKeepsMappingsUnderReview(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:foundation-state-test", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: targetCommit,
		ToolchainHash: HashBytes([]byte("foundation-state-toolchain")), EnvironmentHash: HashBytes([]byte("foundation-state-environment")),
	}
	records, _, err := ImportCurrentDenominators(root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	records, err = AddCompactionSettingsBehaviors(root, snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	records, err = AddCompactionSettingsAssertions(root, snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	records, err = AddCompactionSettingsEvidenceRequests(records)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(graph.recordIDs(KindObligation)); got != 11 {
		t.Fatalf("obligation count = %d, want 11", got)
	}
	if got := len(graph.recordIDs(KindEvidenceRequest)); got != 7 {
		t.Fatalf("request count = %d, want 7", got)
	}
	mappingIDs := graph.recordIDs(KindMapping)
	if len(mappingIDs) != 8 {
		t.Fatalf("mapping count = %d, want 8", len(mappingIDs))
	}
	for _, mappingID := range mappingIDs {
		mapping := graph.Records[mappingID].(*Mapping)
		if mapping.Status != "hypothesis" || mapping.DecisionID != "" {
			t.Errorf("%s = %#v, want undecided hypothesis", mappingID, mapping)
		}
	}
	for obligationID, verdict := range graph.Verdicts {
		if verdict.State != VerdictOpen || verdict.Reason != "no accepted mapping" {
			t.Errorf("%s = %s (%s), want mapping review gap", obligationID, verdict.State, verdict.Reason)
		}
	}
}
