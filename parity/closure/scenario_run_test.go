package closure

import (
	"encoding/json"
	"strings"
	"testing"
)

// scenarioRunFixture builds a snapshot, one scenario denominator fact, and a
// content-addressed ScenarioRun with the given outcome. It returns the records
// and the scenario path.
func scenarioRunFixture(t *testing.T, outcome string, runs int) ([]Record, string) {
	t.Helper()
	hash := HashBytes([]byte("scenario-run"))
	path := "parity/scenarios/selectors/01-behavior.toml"
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:sr", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash}
	pin := &Pin{Kind: KindPin, ID: "pin:sr", SnapshotID: snapshot.ID, Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/scenarios/selectors", SemanticID: "denominator:scenario", StartLine: 1, EndLine: 10, QuoteHash: hash}
	fact := &Fact{Kind: KindFact, ID: "fact:sr", SnapshotID: snapshot.ID, FactType: "denominator:scenario", SubjectID: path, Resolution: "observed", PinIDs: []string{pin.ID}, Value: json.RawMessage(`{"description":"behavior","covers":["packages/a.ts"],"tags":[]}`)}
	run := &ScenarioRun{
		Kind: KindScenarioRun, SnapshotID: snapshot.ID, ScenarioFactID: fact.ID,
		Outcome: outcome, Runs: runs, Comparator: "output_equal", CommandHash: hash,
		Artifacts: []EvidenceArtifact{{Path: "pig.stdout", Hash: hash}},
	}
	id, err := scenarioRunContentID(run)
	if err != nil {
		t.Fatal(err)
	}
	run.ID = id
	return []Record{snapshot, pin, fact, run}, path
}

func TestPassingScenarioRunProvesScenarioInReports(t *testing.T) {
	records, path := scenarioRunFixture(t, ScenarioOutcomePass, 3)
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	outcomes, err := deriveScenarioOutcomes(graph)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes[path]; got.state != VerdictProven || got.lastRun != "3 pass" {
		t.Fatalf("outcome = %+v, want proven / 3 pass", got)
	}
	family, err := RenderReport(graph, FamilyCoverageDataset)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(family), "| 3 pass | 1 | 0 | 0 | 0 | 0 |") {
		t.Fatalf("family report missing proven row:\n%s", family)
	}
}

func TestFailingScenarioRunContradictsScenario(t *testing.T) {
	records, path := scenarioRunFixture(t, ScenarioOutcomeFail, 1)
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	outcomes, err := deriveScenarioOutcomes(graph)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes[path]; got.state != VerdictContradicted || got.lastRun != "1 fail" {
		t.Fatalf("outcome = %+v, want contradicted / 1 fail", got)
	}
	quality, err := RenderReport(graph, ScenarioQualityDataset)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(quality), "| 0 | 0 | 0 | 0 | 1 | recorded scenario run failed |") {
		t.Fatalf("scenario-quality report missing contradicted row:\n%s", quality)
	}
}

func TestBuildRejectsForgedOrMalformedScenarioRun(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ScenarioRun)
		want string
	}{
		{"forged id", func(r *ScenarioRun) { r.ID = "scenario-run:deadbeef" }, "not content-addressed"},
		{"bad outcome", func(r *ScenarioRun) { r.Outcome = "maybe" }, "invalid outcome"},
		{"zero runs", func(r *ScenarioRun) { r.Runs = 0 }, "at least one run"},
		{"empty comparator", func(r *ScenarioRun) { r.Comparator = "" }, "invalid comparator"},
		{"bad command hash", func(r *ScenarioRun) { r.CommandHash = "nope" }, "invalid commandHash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, _ := scenarioRunFixture(t, ScenarioOutcomePass, 1)
			run := records[len(records)-1].(*ScenarioRun)
			tc.mut(run)
			// Re-address unless the case is specifically testing a forged id.
			if tc.name != "forged id" {
				id, err := scenarioRunContentID(run)
				if err != nil {
					t.Fatal(err)
				}
				run.ID = id
			}
			if _, err := Build(records); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildRejectsScenarioRunReferencingNonScenarioFact(t *testing.T) {
	records, _ := scenarioRunFixture(t, ScenarioOutcomePass, 1)
	// Repoint the run at a fact that is not a scenario denominator.
	fact := records[2].(*Fact)
	fact.FactType = "denominator:port-map"
	run := records[len(records)-1].(*ScenarioRun)
	id, err := scenarioRunContentID(run)
	if err != nil {
		t.Fatal(err)
	}
	run.ID = id
	if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "must reference a scenario denominator fact") {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestPublicDecodeRejectsScenarioRunAsForgeableProof(t *testing.T) {
	records, _ := scenarioRunFixture(t, ScenarioOutcomePass, 1)
	run := records[len(records)-1].(*ScenarioRun)
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	// The public import path must refuse a proof-granting scenario run.
	if _, err := DecodeJSONL(strings.NewReader(string(encoded) + "\n")); err == nil || !strings.Contains(err.Error(), "executor-imported only") {
		t.Fatalf("public decode error = %v, want executor-imported only", err)
	}
}
