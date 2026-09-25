package closure

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPopulateTraceWitnessBindsTypedOrderedArtifact(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "evidence")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := EvidenceTraceArtifact{
		WitnessType: "state-transition",
		Captures: []EvidenceTraceArtifactCapture{{
			TargetID: "target:one",
			Events: []EvidenceTraceEvent{
				{Ordinal: 1, Kind: "bind", Subject: "summary", ValueHash: HashBytes([]byte("first"))},
				{Ordinal: 2, Kind: "return", Subject: "result", ValueHash: HashBytes([]byte("second"))},
			},
		}},
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "trace.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	witness := &ExecutionWitness{WitnessType: "state-transition", TargetIDs: []string{"target:one"}}
	plan := EvidenceWitnessRequest{WitnessType: "state-transition", TargetIDs: []string{"target:one"}, CommandIndexes: []int{1}, ArtifactPath: "trace.json"}
	if err := populateTraceWitness(root, output, 0, witness, plan); err != nil {
		t.Fatal(err)
	}
	if len(witness.Artifacts) != 1 || witness.Artifacts[0].Hash != HashBytes(data) {
		t.Fatalf("trace artifacts = %#v", witness.Artifacts)
	}
}

func TestDecodeTraceArtifactFailsClosed(t *testing.T) {
	validHash := HashBytes([]byte("value"))
	for name, input := range map[string]string{
		"unknown field":    `{"witnessType":"state-transition","captures":[],"extra":true}`,
		"unsupported type": `{"witnessType":"provider-capture","captures":[{"targetId":"target:x","events":[{"ordinal":1,"kind":"x","subject":"x","valueHash":"` + validHash + `"}]}]}`,
		"missing events":   `{"witnessType":"state-transition","captures":[{"targetId":"target:x","events":[]}]}`,
		"bad ordinal":      `{"witnessType":"state-transition","captures":[{"targetId":"target:x","events":[{"ordinal":2,"kind":"x","subject":"x","valueHash":"` + validHash + `"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeTraceArtifact([]byte(input)); err == nil {
				t.Fatal("decodeTraceArtifact() accepted malformed trace")
			}
		})
	}
}

func TestExecuteRequestProducesContentAddressedUntrustedRun(t *testing.T) {
	root := initializeExecutorRepository(t)
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
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:executor", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: commit, ToolchainHash: toolchainHash, EnvironmentHash: environmentHash}
	pin := &Pin{Kind: KindPin, ID: "pin:executor", SnapshotID: snapshot.ID, Repository: "pig", Commit: commit, Path: "target.go", SemanticID: "target.Run", StartLine: 1, EndLine: 1, QuoteHash: hash("target"), APIHash: hash("api"), BodyHash: hash("body")}
	target := &Target{Kind: KindTarget, ID: "target:executor", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "target.Run"}
	behavior := &Behavior{Kind: KindBehavior, ID: "behavior:executor", Name: "executor", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &Facet{Kind: KindFacet, ID: "facet:result", Name: "result"}
	rule := &Rule{Kind: KindRule, ID: "rule:executor", Name: "executor result", DefinitionHash: hash("rule")}
	obligation := &Obligation{Kind: KindObligation, ID: "obligation:executor", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	testRecord := &Test{Kind: KindTest, ID: "test:executor", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash("test")}
	assertion := &Assertion{Kind: KindAssertion, ID: "assertion:executor", TestID: testRecord.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "test"}
	request := &EvidenceRequest{
		Kind: KindEvidenceRequest, ID: "request:executor", SnapshotID: snapshot.ID,
		ObligationIDs: []string{obligation.ID}, AssertionIDs: []string{assertion.ID},
		Commands:    []EvidenceCommand{{Name: "go", Args: []string{"version"}}, {Name: "go", Args: []string{"version"}}},
		Environment: []string{}, Witnesses: []EvidenceWitnessRequest{{
			WitnessType: "provider-capture", TargetIDs: []string{target.ID}, SubjectPinIDs: []string{pin.ID}, CommandIndexes: []int{1, 2},
		}},
		Durability: 1, Comparator: "output-equal", TimeoutSeconds: 30,
	}
	graph, err := Build([]Record{snapshot, pin, target, behavior, facet, rule, obligation, testRecord, assertion, request})
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "graph.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatal(err)
	}
	evidence, err := ExecuteRequest(t.Context(), databasePath, root, request.ID, "evidence")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Result != "pass" || len(evidence.Commands) != 2 || len(evidence.WitnessIDs) != 1 || !slices.Equal(evidence.SubjectPinIDs, []string{pin.ID}) {
		t.Fatalf("evidence = %#v", evidence)
	}
	changedExecutor := *evidence
	changedExecutor.ExecutorHash = HashBytes([]byte("changed executor"))
	changedID, err := evidenceContentID(&changedExecutor)
	if err != nil {
		t.Fatal(err)
	}
	if changedID == evidence.ID {
		t.Fatal("executor change did not change evidence identity")
	}
	data, err := os.ReadFile(filepath.Join(root, "evidence", "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSONL(strings.NewReader(string(data)))
	if err != nil || len(decoded) != 2 {
		t.Fatalf("decode evidence: records=%d err=%v", len(decoded), err)
	}
	if !strings.Contains(string(data), `"witnessType":"provider-capture"`) || !strings.Contains(string(data), `"subjectPinIds":["pin:executor"]`) {
		t.Fatalf("typed evidence witness missing: %s", data)
	}
	combined := make([]Record, 0, len(graph.Records)+len(decoded))
	for _, record := range graph.Records {
		combined = append(combined, record)
	}
	combined = append(combined, decoded...)
	untrusted, err := Build(combined)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := untrusted.Verdicts[obligation.ID]; verdict.State != VerdictOpen {
		t.Fatalf("unattested evidence verdict = %s, want open", verdict.State)
	}
	if _, err := LoadEvidenceAttestation(t.Context(), root, "evidence/evidence.jsonl"); err == nil || !strings.Contains(err.Error(), "not committed") {
		t.Fatalf("untracked attestation error = %v", err)
	}
	gitOutput(t, root, "add", "evidence")
	gitOutput(t, root, "commit", "-m", "attest evidence")
	attested, err := LoadEvidenceAttestation(t.Context(), root, "evidence/evidence.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(attested) != 3 || attested[2].RecordKind() != KindEvidenceAttestation {
		t.Fatalf("attested records = %#v", attested)
	}
	combined = combined[:0]
	for _, record := range graph.Records {
		combined = append(combined, record)
	}
	combined = append(combined, attested...)
	if _, err := Build(combined); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(root, "evidence", "run-001-command-001.stdout")
	if err := os.WriteFile(artifactPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, root, "add", "evidence/run-001-command-001.stdout")
	gitOutput(t, root, "commit", "-m", "tamper evidence")
	if _, err := LoadEvidenceAttestation(t.Context(), root, "evidence/evidence.jsonl"); err == nil || !strings.Contains(err.Error(), "hash differs") {
		t.Fatalf("tampered artifact error = %v", err)
	}
}

// mutationExecutorFixture is a committed one-mutant repository with its
// closure records rebuilt into a store, ready for ExecuteMutationRequest.
type mutationExecutorFixture struct {
	root         string
	original     []byte
	records      []Record
	request      *MutationRequest
	databasePath string
}

func newMutationExecutorFixture(t *testing.T) mutationExecutorFixture {
	t.Helper()
	root := initializeExecutorRepository(t)
	commit := gitOutput(t, root, "rev-parse", "HEAD")
	environment := []string{"PIG_MUTATION_HELPER=1"}
	toolchainHash, err := CurrentToolchainHash(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	environmentHash, err := CurrentEnvironmentHash(environment)
	if err != nil {
		t.Fatal(err)
	}
	hash := func(value string) string { return HashBytes([]byte(value)) }
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:mutation-executor", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: commit, ToolchainHash: toolchainHash, EnvironmentHash: environmentHash}
	pin := &Pin{Kind: KindPin, ID: "pin:mutation-executor", SnapshotID: snapshot.ID, Repository: "pig", Commit: commit, Path: "target.go", SemanticID: "target.Run", StartLine: 1, EndLine: 1, QuoteHash: hash("target")}
	target := &Target{Kind: KindTarget, ID: "target:mutation-executor", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "target.Run"}
	behavior := &Behavior{Kind: KindBehavior, ID: "behavior:mutation-executor", Name: "mutation executor", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &Facet{Kind: KindFacet, ID: "facet:result", Name: "result"}
	rule := &Rule{Kind: KindRule, ID: "rule:mutation-executor", Name: "mutation executor", DefinitionHash: hash("rule")}
	obligation := &Obligation{Kind: KindObligation, ID: "obligation:mutation-executor", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	decision := &Decision{Kind: KindDecision, ID: "decision:mutation-executor", DecisionType: "mapping", ScopeIDs: []string{behavior.ID}, Rationale: "exact mutation target", Authority: "reviewer"}
	mapping := &Mapping{Kind: KindMapping, ID: "mapping:mutation-executor", BehaviorID: behavior.ID, TargetIDs: []string{target.ID}, Status: "decided", DecisionID: decision.ID}
	reachability := &Reachability{Kind: KindReachability, ID: "reachability:mutation-executor", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "test", RootPinIDs: []string{pin.ID}}
	testRecord := &Test{Kind: KindTest, ID: "test:mutation-executor", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash("test"), MutationPolicy: "required"}
	assertion := &Assertion{Kind: KindAssertion, ID: "assertion:mutation-executor", TestID: testRecord.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "contract"}
	original := []byte("package target\n\nfunc Value() int { return 1 }\n")
	mutated := []byte("package target\n\nfunc Value() int { return 2 }\n")
	edit := MutationEdit{Path: "target.go", OriginalHash: HashBytes(original), Before: "return 1", After: "return 2", MutatedHash: HashBytes(mutated)}
	expectedFailure, err := ExpectedGoTestFailureHash("TestValue", []string{"    target_test.go:7: Value() = 2, want 1\n"})
	if err != nil {
		t.Fatal(err)
	}
	mutant, err := NewMutant(snapshot.ID, obligation.ID, testRecord.ID, target.ID, pin.ID, "result", "automatic", "change-value", edit, "TestValue", expectedFailure)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewMutationRequest(snapshot.ID, obligation.ID, testRecord.ID, []*Mutant{mutant}, []EvidenceCommand{{Name: "go", Args: []string{"test", "-json", "./...", "-run", "^TestValue$"}}}, environment, 30)
	if err != nil {
		t.Fatal(err)
	}
	records := []Record{snapshot, pin, target, behavior, facet, rule, obligation, decision, mapping, reachability, testRecord, assertion, mutant, request}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "mutation.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatal(err)
	}
	return mutationExecutorFixture{root: root, original: original, records: records, request: request, databasePath: databasePath}
}

// loadCommittedMutationRun commits the executor output and builds the graph
// that admits it, as a reviewer's closure run would.
func loadCommittedMutationRun(t *testing.T, fixture mutationExecutorFixture) *Graph {
	t.Helper()
	gitOutput(t, fixture.root, "add", "mutation")
	gitOutput(t, fixture.root, "commit", "-m", "record mutation")
	loaded, err := LoadMutationRun(t.Context(), fixture.root, "mutation/mutation.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	derived, err := Build(append(slices.Clone(fixture.records), loaded...))
	if err != nil {
		t.Fatal(err)
	}
	return derived
}

func TestExecuteMutationRequestDerivesKilledMutantFromExactFailure(t *testing.T) {
	fixture := newMutationExecutorFixture(t)
	root, request := fixture.root, fixture.request
	run, err := ExecuteMutationRequest(t.Context(), fixture.databasePath, root, request.ID, "mutation")
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil || !slices.Equal(unchanged, fixture.original) {
		t.Fatalf("canonical source changed: %q, %v", unchanged, err)
	}
	if len(run.Artifacts) != 5 || len(run.Baselines) != 1 || run.Baselines[0].ExitCode != 0 || run.Results[0].FailureTest != "TestValue" {
		t.Fatalf("mutation run lacks isolated baseline or artifacts: %#v", run)
	}
	path := filepath.Join(root, "mutation", "mutation.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, decodeErr := DecodeJSONL(file)
	_ = file.Close()
	if decodeErr == nil || !strings.Contains(decodeErr.Error(), "executor-imported only") {
		t.Fatalf("authored mutation decode error = %v", decodeErr)
	}
	if _, err := LoadMutationRun(t.Context(), root, "mutation/mutation.jsonl"); err == nil || !strings.Contains(err.Error(), "not committed") {
		t.Fatalf("untracked mutation error = %v", err)
	}
	derived := loadCommittedMutationRun(t, fixture)
	if !derived.mutationRunKillsAll(request, run) || !derived.mutationRunFresh(request, run) {
		t.Fatalf("mutation run is not admissible: %#v", run)
	}
	artifactPath := filepath.Join(root, "mutation", "mutant-001.patch")
	if err := os.WriteFile(artifactPath, []byte("forged patch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, root, "add", "mutation/mutant-001.patch")
	gitOutput(t, root, "commit", "-m", "tamper mutation artifact")
	if _, err := LoadMutationRun(t.Context(), root, "mutation/mutation.jsonl"); err == nil || !strings.Contains(err.Error(), "hash differs") {
		t.Fatalf("tampered mutation artifact error = %v", err)
	}
}

func TestCurrentEnvironmentHashBindsTypedRequirements(t *testing.T) {
	first, err := CurrentEnvironmentHash([]string{"PROVIDER_MODE=faux"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	second, err := CurrentEnvironmentHash([]string{"PROVIDER_MODE=faux"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("ambient HOME/PATH changed typed environment identity")
	}
	changed, err := CurrentEnvironmentHash([]string{"PROVIDER_MODE=live"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("explicit environment requirement did not change identity")
	}
	if _, err := CurrentEnvironmentHash([]string{"B=2", "A=1"}); err == nil {
		t.Fatal("unsorted environment requirements were accepted")
	}
}

func TestExecuteRequestRejectsDirtyTree(t *testing.T) {
	root := initializeExecutorRepository(t)
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireCanonicalTree(t.Context(), root, gitOutput(t, root, "rev-parse", "HEAD")); err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("dirty tree error = %v", err)
	}
}

func TestExecuteRequestRejectsUntrackedTreeInput(t *testing.T) {
	root := initializeExecutorRepository(t)
	if err := os.WriteFile(filepath.Join(root, "untracked_test.go"), []byte("package target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireCanonicalTree(t.Context(), root, gitOutput(t, root, "rev-parse", "HEAD")); err == nil || !strings.Contains(err.Error(), "tracked or untracked changes") {
		t.Fatalf("untracked tree error = %v", err)
	}
}

func TestBoundedBufferReportsOverflowWithoutUnboundedGrowth(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	written, err := buffer.Write([]byte("123456"))
	if err != nil || written != 6 || buffer.buffer.String() != "1234" || !buffer.overflow {
		t.Fatalf("buffer = %q overflow=%t written=%d err=%v", buffer.buffer.String(), buffer.overflow, written, err)
	}
}

func initializeExecutorRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "parity", "closure"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"go.mod":                        "module example.com/mutation\n\ngo 1.26\n",
		"target.go":                     "package target\n\nfunc Value() int { return 1 }\n",
		"target_test.go":                "package target\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif got := Value(); got != 1 {\n\t\tt.Fatalf(\"Value() = %d, want 1\", got)\n\t}\n}\n",
		"parity/closure/attestation.go": "package closure\n",
		"parity/closure/coverage.go":    "package closure\n",
		"parity/closure/decode.go":      "package closure\n",
		"parity/closure/executor.go":    "package closure\n",
		"parity/closure/graph.go":       "package closure\n",
		"parity/closure/mutation.go":    "package closure\n",
		"parity/closure/types.go":       "package closure\n",
		"parity/cmd/closure/main.go":    "package main\n",
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "executor@example.invalid")
	gitOutput(t, root, "config", "user.name", "Executor Test")
	gitOutput(t, root, "add", "go.mod", "target.go", "target_test.go", "parity/closure/attestation.go", "parity/closure/coverage.go", "parity/closure/decode.go", "parity/closure/executor.go", "parity/closure/graph.go", "parity/closure/mutation.go", "parity/closure/types.go", "parity/cmd/closure/main.go")
	gitOutput(t, root, "commit", "-m", "fixture")
	return root
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
