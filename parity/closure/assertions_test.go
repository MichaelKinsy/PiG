package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractGoAssertionCandidatesKeepsOnlyFailureSitesInPinnedRange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample_test.go")
	source := `package sample
import "testing"
func helper(t *testing.T) { t.Fatal("outside") }
func TestValue(tt *testing.T) {
	if false { tt.Errorf("inside") }
	if false { require.Equal(t, 1, 2) }
	_ = cmp.Diff(1, 2)
}
func TestOther(t *testing.T) { t.Fatal("outside") }
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	test := &Test{ID: "test:value"}
	pin := &Pin{Repository: "pig", Path: "sample_test.go", StartLine: 4, EndLine: 7}
	candidates, err := ExtractAssertionCandidates(root, test, pin)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d, want 3: %#v", len(candidates), candidates)
	}
	if candidates[0].Callee != "tt.Errorf" || candidates[1].Callee != "require.Equal" || candidates[2].Callee != "cmp.Diff" {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[2].Enforcing {
		t.Fatalf("bare cmp.Diff incorrectly classified as enforcing: %#v", candidates[2])
	}
	for _, candidate := range candidates {
		if !ValidHash(candidate.TextHash) {
			t.Errorf("candidate hash = %q", candidate.TextHash)
		}
	}
}

func TestExtractScenarioAssertionCandidatesIgnoresDurabilityMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenario.toml")
	source := `name = "sample"
[assert]
runs = 3
runtime_ratio_max = 2.0
output_equal = true
both_not_contain = ["panic"]
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, err := ExtractAssertionCandidates(root, &Test{ID: "test:scenario"}, &Pin{
		Repository: "pig", Path: "scenario.toml", StartLine: 1, EndLine: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Callee != "output_equal" || candidates[1].Callee != "both_not_contain" {
		t.Fatalf("scenario candidates = %#v", candidates)
	}
}

func TestAssertionAuditReportsAssertionlessBoundTests(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample_test.go")
	if err := os.WriteFile(path, []byte("package sample\nfunc TestEmpty() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:test", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: HashBytes([]byte("tools")), EnvironmentHash: HashBytes([]byte("environment"))}
	pin := &Pin{Kind: KindPin, ID: "pin:empty-test", SnapshotID: snapshot.ID, Repository: "pig", Commit: snapshot.TargetCommit,
		Path: "sample_test.go", SemanticID: "sample.TestEmpty", StartLine: 2, EndLine: 2,
		QuoteHash: HashBytes([]byte("func TestEmpty() {}")), APIHash: HashBytes([]byte("TestEmpty")), BodyHash: HashBytes([]byte("{}"))}
	testRecord := &Test{Kind: KindTest, ID: "test:empty", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: pin.BodyHash}
	records := []Record{snapshot, pin,
		&Facet{Kind: KindFacet, ID: "facet:result", Name: "result"},
		&Behavior{Kind: KindBehavior, ID: "behavior:run", Name: "run", OriginPinIDs: []string{pin.ID}, Profile: "application"},
		testRecord, &Assertion{
			Kind: KindAssertion, ID: "assertion:empty", TestID: testRecord.ID, Class: "A2",
			BehaviorID: "behavior:run", FacetID: "facet:result", Oracle: "authored-contract",
		}}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "graph.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatal(err)
	}
	report, err := AssertionAudit(t.Context(), databasePath, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "test:empty\tsample_test.go\t1\t0\tassertionless") {
		t.Fatalf("assertion audit:\n%s", report)
	}
}
