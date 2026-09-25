package closure

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCurrentDenominatorsAccountsEveryLedgerRow(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:denominator-test", UpstreamCommit: strings.Repeat("a", 40),
		TargetCommit: strings.Repeat("b", 40), ToolchainHash: HashBytes([]byte("denominator-test-toolchain")), EnvironmentHash: HashBytes([]byte("denominator-test-environment")),
	}
	records, report, err := ImportCurrentDenominators(root, snapshot)
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	want, sourceFiles := fileDenominators(t, root)
	if len(report.Counts) != len(want) {
		t.Fatalf("dataset count = %d, want %d: %v", len(report.Counts), len(want), report.Counts)
	}
	provisional := 0
	for dataset, expectation := range want {
		provisional += expectation.provisional
		if report.Counts[dataset] != expectation.items {
			t.Errorf("%s count = %d, want %d", dataset, report.Counts[dataset], expectation.items)
		}
	}
	if report.ProvisionalClaims != provisional {
		t.Errorf("provisional claims = %d, want %d", report.ProvisionalClaims, provisional)
	}
	if len(report.SourceFiles) != sourceFiles {
		t.Errorf("source files = %d, want %d", len(report.SourceFiles), sourceFiles)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(imported): %v", err)
	}
	for _, id := range []string{
		"fact:denominator-order:semantic-mapping:mappings",
		"fact:denominator-order:semantic-delta:changes",
		"fact:denominator-order:behavior-input-mapping:mappings",
		"fact:denominator-order:behavior-input-mapping:renderMappings",
		"fact:denominator-order:async-contract:files",
		"fact:denominator-order:upstream-sync:files",
		"fact:denominator-order:behavior-contract:contract",
		"fact:denominator-order:port-map:rows",
		"fact:denominator-order:coverage-row:rows",
		"fact:denominator-order:format-version:fields",
	} {
		if graph.Records[id] == nil {
			t.Errorf("missing report row order fact %s", id)
		}
	}
	if len(graph.Verdicts) != 0 {
		t.Fatalf("denominator import derived %d verdicts, want 0 before CE3", len(graph.Verdicts))
	}
	databasePath := filepath.Join(t.TempDir(), "denominator.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(): %v", err)
	}
	status, err := ReadStatus(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("ReadStatus(): %v", err)
	}
	if !strings.Contains(string(status), fmt.Sprintf("provisional\t%d\n", provisional)) {
		t.Fatalf("status does not count provisional imported claims:\n%s", status[:min(len(status), 1000)])
	}
}
