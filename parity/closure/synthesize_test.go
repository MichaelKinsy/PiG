package closure

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// synthesizeOnlyStore builds a complete store for one verification-only behavior
// : mapping, reachability, a bound passing test with a runnable Execution recipe,
// and an admissible assertion: but deliberately NO EvidenceRequest, so the only
// way to prove the obligation is to synthesize the request from the graph.
// value controls the source under test so a caller can force the bound test to
// fail. It returns the repository root, the store path, and the obligation ID.
func synthesizeOnlyStore(t *testing.T, value int) (root, storePath, obligationID string) {
	t.Helper()
	root = initializeSynthesizeRepository(t, value)
	commit := gitOutput(t, root, "rev-parse", "HEAD")
	hash := func(value string) string { return HashBytes([]byte(value)) }
	toolchainHash, err := CurrentToolchainHash(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	environmentHash, err := CurrentEnvironmentHash([]string{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:synthesize", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: commit, ToolchainHash: toolchainHash, EnvironmentHash: environmentHash}
	pin := &Pin{Kind: KindPin, ID: "pin:synthesize", SnapshotID: snapshot.ID, Repository: "pig", Commit: commit, Path: "target.go", SemanticID: "target.Value", StartLine: 1, EndLine: 3, QuoteHash: hash("target"), APIHash: hash("api"), BodyHash: hash("body")}
	target := &Target{Kind: KindTarget, ID: "target:synthesize", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "target.Value"}
	behavior := &Behavior{Kind: KindBehavior, ID: "behavior:synthesize", Name: "synthesize", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &Facet{Kind: KindFacet, ID: "facet:result", Name: "result"}
	rule := &Rule{Kind: KindRule, ID: "rule:synthesize", Name: "synthesize result", DefinitionHash: hash("rule")}
	obligation := &Obligation{Kind: KindObligation, ID: "obligation:synthesize", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	decision := &Decision{Kind: KindDecision, ID: "decision:synthesize", DecisionType: "mapping", ScopeIDs: []string{behavior.ID}, Rationale: "verification-only behavior", Authority: "reviewer"}
	mapping := &Mapping{Kind: KindMapping, ID: "mapping:synthesize", BehaviorID: behavior.ID, TargetIDs: []string{target.ID}, Status: "decided", DecisionID: decision.ID}
	reachability := &Reachability{Kind: KindReachability, ID: "reachability:synthesize", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "entrypoint", RootPinIDs: []string{pin.ID}}
	testRecord := &Test{
		Kind: KindTest, ID: "test:synthesize", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash("test"),
		Execution: &TestExecution{Package: ".", RunName: "TestValue", Comparator: "exit-zero"},
	}
	assertion := &Assertion{Kind: KindAssertion, ID: "assertion:synthesize", TestID: testRecord.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "test"}
	records := []Record{snapshot, pin, target, behavior, facet, rule, obligation, decision, mapping, reachability, testRecord, assertion}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := graph.Verdicts[obligation.ID]; verdict.State != VerdictOpen {
		t.Fatalf("pre-synthesize verdict = %s, want open", verdict.State)
	}
	storePath = filepath.Join(t.TempDir(), "graph.db")
	if err := RebuildStore(t.Context(), storePath, graph); err != nil {
		t.Fatal(err)
	}
	return root, storePath, obligation.ID
}

func initializeSynthesizeRepository(t *testing.T, value int) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                        "module github.com/MichaelKinsy/PiG\n\ngo 1.26\n",
		"target.go":                     "package target\n\nfunc Value() int { return " + strconv.Itoa(value) + " }\n",
		"target_test.go":                "package target\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif got := Value(); got != 1 {\n\t\tt.Fatalf(\"Value() = %d, want 1\", got)\n\t}\n}\n",
		"parity/closure/attestation.go": "package closure\n",
		"parity/closure/coverage.go":    "package closure\n",
		"parity/closure/decode.go":      "package closure\n",
		"parity/closure/executor.go":    "package closure\n",
		"parity/closure/graph.go":       "package closure\n",
		"parity/closure/mutation.go":    "package closure\n",
		"parity/closure/types.go":       "package closure\n",
		"parity/cmd/closure/main.go":    "package main\n",
		".gitignore":                    "tmp/\n",
	}
	added := make([]string, 0, len(files))
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		added = append(added, path)
	}
	slices.Sort(added)
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "synthesize@example.invalid")
	gitOutput(t, root, "config", "user.name", "Synthesize Test")
	gitOutput(t, root, append([]string{"add", "--"}, added...)...)
	gitOutput(t, root, "commit", "-m", "fixture")
	return root
}

// TestSynthesizeThenVerifyDrivesObligationOpenToProven proves the full
// autonomous chain with the EvidenceRequest derived from the graph rather than
// hand-authored: synthesize the request from the obligation's mapping and test
// recipe, persist it, run it through the verification harness, and confirm the
// obligation becomes proven purely from the synthesized request.
func TestSynthesizeThenVerifyDrivesObligationOpenToProven(t *testing.T) {
	root, storePath, obligationID := synthesizeOnlyStore(t, 1)
	graph, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	request, err := SynthesizeEvidenceRequest(graph, obligationID)
	if err != nil {
		t.Fatalf("SynthesizeEvidenceRequest(): %v", err)
	}
	if err := appendRecordToStore(t.Context(), storePath, request); err != nil {
		t.Fatal(err)
	}
	run, verdicts, err := VerifyRequestIntoStore(t.Context(), root, storePath, request.ID, "evidence", "attest synthesized evidence")
	if err != nil {
		t.Fatalf("VerifyRequestIntoStore(): %v", err)
	}
	if run.Result != "pass" {
		t.Fatalf("evidence result = %s, want pass", run.Result)
	}
	if len(verdicts) != 1 || verdicts[0].State != VerdictProven {
		t.Fatalf("verdicts = %#v, want proven", verdicts)
	}
	reloaded, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := reloaded.Verdicts[obligationID]; verdict.State != VerdictProven {
		t.Fatalf("persisted verdict = %s, want proven", verdict.State)
	}
}

// TestSynthesizeThenVerifyCannotProveFailingBehavior proves the synthesized path
// cannot fabricate proof: a synthesized request run against a failing behavior
// lands contradicted, never proven.
func TestSynthesizeThenVerifyCannotProveFailingBehavior(t *testing.T) {
	root, storePath, obligationID := synthesizeOnlyStore(t, 2)
	graph, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	request, err := SynthesizeEvidenceRequest(graph, obligationID)
	if err != nil {
		t.Fatalf("SynthesizeEvidenceRequest(): %v", err)
	}
	if err := appendRecordToStore(t.Context(), storePath, request); err != nil {
		t.Fatal(err)
	}
	run, verdicts, err := VerifyRequestIntoStore(t.Context(), root, storePath, request.ID, "evidence", "attest synthesized failing evidence")
	if err != nil {
		t.Fatalf("VerifyRequestIntoStore(): %v", err)
	}
	if run.Result != "fail" {
		t.Fatalf("evidence result = %s, want fail", run.Result)
	}
	if len(verdicts) != 1 || verdicts[0].State == VerdictProven {
		t.Fatalf("verdicts = %#v, must not be proven", verdicts)
	}
	if verdicts[0].State != VerdictContradicted {
		t.Fatalf("failing verdict = %s, want contradicted", verdicts[0].State)
	}
}

// TestSynthesizeEvidenceRequestFailsClosedWithoutRecipe proves the synthesizer
// refuses to guess a command when the bound test carries no Execution recipe.
func TestSynthesizeEvidenceRequestFailsClosedWithoutRecipe(t *testing.T) {
	root, storePath, obligationID := synthesizeOnlyStore(t, 1)
	_ = root
	graph, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	// Strip the runnable recipe: the test now carries no way to run mechanically.
	test := graph.Records["test:synthesize"].(*Test)
	test.Execution = nil
	if _, err := SynthesizeEvidenceRequest(graph, obligationID); err == nil || !strings.Contains(err.Error(), "runnable test") {
		t.Fatalf("synthesize without recipe error = %v, want runnable-test rejection", err)
	}
}

// TestSynthesizeEvidenceRequestFailsClosedWithoutMapping proves the synthesizer
// refuses an obligation with no accepted mapping rather than fabricating targets.
func TestSynthesizeEvidenceRequestFailsClosedWithoutMapping(t *testing.T) {
	_, storePath, obligationID := synthesizeOnlyStore(t, 1)
	graph, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	mapping := graph.Records["mapping:synthesize"].(*Mapping)
	mapping.Status = "proposed"
	if _, err := SynthesizeEvidenceRequest(graph, obligationID); err == nil || !strings.Contains(err.Error(), "no accepted mapping") {
		t.Fatalf("synthesize without mapping error = %v, want no accepted mapping", err)
	}
}

// appendRecordToStore loads the store graph, appends one record, and rebuilds
// the store so the new record is persisted for subsequent graph loads.
func appendRecordToStore(ctx context.Context, storePath string, record Record) error {
	graph, err := loadStoreGraph(ctx, storePath)
	if err != nil {
		return err
	}
	combined := make([]Record, 0, len(graph.Records)+1)
	for _, id := range sortedRecordIDs(graph.Records) {
		combined = append(combined, graph.Records[id])
	}
	combined = append(combined, record)
	rebuilt, err := Build(combined)
	if err != nil {
		return err
	}
	return RebuildStore(ctx, storePath, rebuilt)
}
