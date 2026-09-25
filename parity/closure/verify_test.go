package closure

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// verificationOnlyStore builds a complete store for one behavior pig already
// matches: mapping, reachability, a bound passing test, an admissible assertion,
// and an EvidenceRequest: but no attested evidence yet, so the obligation starts
// open. targetValue controls the source under test so a caller can force the
// bound test to fail. It returns the repository root, the store path, the
// request ID, and the obligation ID.
func verificationOnlyStore(t *testing.T, targetValue int) (root, storePath, requestID, obligationID string) {
	t.Helper()
	root = initializeVerifyRepository(t, targetValue)
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
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:verify", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: commit, ToolchainHash: toolchainHash, EnvironmentHash: environmentHash}
	pin := &Pin{Kind: KindPin, ID: "pin:verify", SnapshotID: snapshot.ID, Repository: "pig", Commit: commit, Path: "target.go", SemanticID: "target.Value", StartLine: 1, EndLine: 3, QuoteHash: hash("target"), APIHash: hash("api"), BodyHash: hash("body")}
	target := &Target{Kind: KindTarget, ID: "target:verify", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "target.Value"}
	behavior := &Behavior{Kind: KindBehavior, ID: "behavior:verify", Name: "verify", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &Facet{Kind: KindFacet, ID: "facet:result", Name: "result"}
	rule := &Rule{Kind: KindRule, ID: "rule:verify", Name: "verify result", DefinitionHash: hash("rule")}
	obligation := &Obligation{Kind: KindObligation, ID: "obligation:verify", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	decision := &Decision{Kind: KindDecision, ID: "decision:verify", DecisionType: "mapping", ScopeIDs: []string{behavior.ID}, Rationale: "verification-only behavior", Authority: "reviewer"}
	mapping := &Mapping{Kind: KindMapping, ID: "mapping:verify", BehaviorID: behavior.ID, TargetIDs: []string{target.ID}, Status: "decided", DecisionID: decision.ID}
	reachability := &Reachability{Kind: KindReachability, ID: "reachability:verify", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "entrypoint", RootPinIDs: []string{pin.ID}}
	testRecord := &Test{Kind: KindTest, ID: "test:verify", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash("test")}
	assertion := &Assertion{Kind: KindAssertion, ID: "assertion:verify", TestID: testRecord.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "test"}
	request := &EvidenceRequest{
		Kind: KindEvidenceRequest, ID: "request:verify", SnapshotID: snapshot.ID,
		ObligationIDs: []string{obligation.ID}, AssertionIDs: []string{assertion.ID},
		Commands:    []EvidenceCommand{{Name: "go", Args: []string{"test", "-coverprofile=cover.out", "./...", "-run", "^TestValue$"}}},
		Environment: []string{}, Witnesses: []EvidenceWitnessRequest{{
			WitnessType: "go-covered-range", TargetIDs: []string{target.ID}, SubjectPinIDs: []string{pin.ID}, CommandIndexes: []int{1}, ArtifactPath: "cover.out",
		}},
		Durability: 1, Comparator: "exit-zero", TimeoutSeconds: 60,
	}
	graph, err := Build([]Record{snapshot, pin, target, behavior, facet, rule, obligation, decision, mapping, reachability, testRecord, assertion, request})
	if err != nil {
		t.Fatal(err)
	}
	if verdict := graph.Verdicts[obligation.ID]; verdict.State != VerdictOpen {
		t.Fatalf("pre-verification verdict = %s, want open", verdict.State)
	}
	storePath = filepath.Join(t.TempDir(), "graph.db")
	if err := RebuildStore(t.Context(), storePath, graph); err != nil {
		t.Fatal(err)
	}
	return root, storePath, request.ID, obligation.ID
}

// initializeVerifyRepository builds a git repository whose module path is
// github.com/MichaelKinsy/PiG so that Go coverage paths normalize to repository
// pins. Value returns targetValue, letting a caller force the bound test to
// fail. It stubs the executor-source files ExecuteRequest hashes.
func initializeVerifyRepository(t *testing.T, targetValue int) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                        "module github.com/MichaelKinsy/PiG\n\ngo 1.26\n",
		"target.go":                     "package target\n\nfunc Value() int { return " + strconv.Itoa(targetValue) + " }\n",
		"target_test.go":                "package target\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif got := Value(); got != 1 {\n\t\tt.Fatalf(\"Value() = %d, want 1\", got)\n\t}\n}\n",
		"parity/closure/attestation.go": "package closure\n",
		"parity/closure/coverage.go":    "package closure\n",
		"parity/closure/decode.go":      "package closure\n",
		"parity/closure/executor.go":    "package closure\n",
		"parity/closure/graph.go":       "package closure\n",
		"parity/closure/mutation.go":    "package closure\n",
		"parity/closure/types.go":       "package closure\n",
		"parity/cmd/closure/main.go":    "package main\n",
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
	gitOutput(t, root, "config", "user.email", "verify@example.invalid")
	gitOutput(t, root, "config", "user.name", "Verify Test")
	gitOutput(t, root, append([]string{"add", "--"}, added...)...)
	gitOutput(t, root, "commit", "-m", "fixture")
	return root
}

// TestVerifyRequestIntoStoreDrivesObligationOpenToProven proves the autonomous
// verification link: a verification-only obligation that starts open becomes
// proven purely by running its bound test through the harness: no manual CLI,
// no hand-written attestation.
func TestVerifyRequestIntoStoreDrivesObligationOpenToProven(t *testing.T) {
	root, storePath, requestID, obligationID := verificationOnlyStore(t, 1)
	run, verdicts, err := VerifyRequestIntoStore(t.Context(), root, storePath, requestID, "evidence", "attest verify evidence")
	if err != nil {
		t.Fatalf("VerifyRequestIntoStore(): %v", err)
	}
	if run.Result != "pass" {
		t.Fatalf("evidence result = %s, want pass", run.Result)
	}
	if len(verdicts) != 1 || verdicts[0].ObligationID != obligationID || verdicts[0].State != VerdictProven {
		t.Fatalf("verdicts = %#v, want proven %s", verdicts, obligationID)
	}
	// The store now carries the proof: a fresh graph load re-derives proven.
	graph, err := loadStoreGraph(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := graph.Verdicts[obligationID]; verdict.State != VerdictProven {
		t.Fatalf("persisted verdict = %s, want proven", verdict.State)
	}
}

// TestVerifyRequestIntoStoreCannotProveFailingBehavior proves the harness cannot
// fabricate proof: when the bound test fails, the obligation is re-derived from
// the attested failing run and lands contradicted, never proven.
func TestVerifyRequestIntoStoreCannotProveFailingBehavior(t *testing.T) {
	root, storePath, requestID, _ := verificationOnlyStore(t, 2)
	run, verdicts, err := VerifyRequestIntoStore(t.Context(), root, storePath, requestID, "evidence", "attest failing evidence")
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

// TestVerifyRequestIntoStoreRejectsDirtyTree proves the harness inherits the
// canonical-tree guard: uncommitted changes block evidence execution entirely.
func TestVerifyRequestIntoStoreRejectsDirtyTree(t *testing.T) {
	root, storePath, requestID, _ := verificationOnlyStore(t, 1)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package target\n\nfunc Value() int { return 1 } // dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyRequestIntoStore(t.Context(), root, storePath, requestID, "evidence", "attest"); err == nil || !strings.Contains(err.Error(), "changes") {
		t.Fatalf("dirty-tree error = %v, want tree-change rejection", err)
	}
}
