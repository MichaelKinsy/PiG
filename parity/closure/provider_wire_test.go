package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddProviderWireBehaviorsGeneratesPinnedMappingHypotheses(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:provider-wire-test", UpstreamCommit: strings.Repeat("a", 40),
		TargetCommit: targetCommit, ToolchainHash: HashBytes([]byte("provider-wire-toolchain")), EnvironmentHash: HashBytes([]byte("provider-wire-environment")),
	}
	records, _, err := ImportCurrentDenominators(root, snapshot)
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	baseRecords := len(records)
	records, err = AddProviderWireBehaviors(root, snapshot, records)
	if err != nil {
		t.Fatalf("AddProviderWireBehaviors(): %v", err)
	}
	const behaviorRecords = 62 // Four facet/rule pairs, five behaviors, five targets, six reachability edges, five mappings, 13 obligations, and 20 source pins.
	if got := len(records) - baseRecords; got != behaviorRecords {
		t.Fatalf("provider-wire behavior records = %d, want %d", got, behaviorRecords)
	}
	records, err = AddProviderWireAssertions(root, snapshot, records)
	if err != nil {
		t.Fatalf("AddProviderWireAssertions(): %v", err)
	}
	const assertionRecords = 38 // Seven tests, 22 assertions, and nine source or fixture pins.
	if got := len(records) - baseRecords - behaviorRecords; got != assertionRecords {
		t.Fatalf("provider-wire assertion records = %d, want %d", got, assertionRecords)
	}
	records, err = AddProviderWireEvidenceRequests(records)
	if err != nil {
		t.Fatalf("AddProviderWireEvidenceRequests(): %v", err)
	}
	const evidenceRequests = 7
	if got := len(records) - baseRecords - behaviorRecords - assertionRecords; got != evidenceRequests {
		t.Fatalf("provider-wire evidence requests = %d, want %d", got, evidenceRequests)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	if got := len(graph.recordIDs(KindAssertion)); got != 22 {
		t.Fatalf("assertion count = %d, want 22", got)
	}
	if got := len(graph.recordIDs(KindTest)); got != 7 {
		t.Fatalf("test count = %d, want 7", got)
	}
	if got := len(graph.recordIDs(KindEvidenceRequest)); got != 7 {
		t.Fatalf("evidence request count = %d, want 7", got)
	}
	for _, requestID := range graph.recordIDs(KindEvidenceRequest) {
		request := graph.Records[requestID].(*EvidenceRequest)
		if request.Durability != 1 || len(request.Commands) == 0 {
			t.Errorf("%s lacks canonical command or durability: %#v", requestID, request)
		}
	}
	differential := graph.Records["request:provider-wire:differential"].(*EvidenceRequest)
	if len(differential.Commands) != 2 || differential.Commands[0].Name != "node" || differential.Commands[1].Name != "go" {
		t.Fatalf("differential commands = %#v", differential.Commands)
	}
	if len(graph.Verdicts) != 13 {
		t.Fatalf("verdict count = %d, want 13", len(graph.Verdicts))
	}
	for id, verdict := range graph.Verdicts {
		if verdict.State != VerdictOpen || verdict.Reason != "no accepted mapping" {
			t.Errorf("%s = %s (%s), want open mapping gap", id, verdict.State, verdict.Reason)
		}
	}
	wantBehaviors := []string{
		"behavior:provider-wire:baseten-chat-template",
		"behavior:provider-wire:finish-reason-errors",
		"behavior:provider-wire:nullable-header-deletion",
		"behavior:provider-wire:sampling-precedence",
		"behavior:provider-wire:vllm-thinking-budget",
	}
	for _, id := range wantBehaviors {
		behavior, ok := graph.Records[id].(*Behavior)
		if !ok {
			t.Errorf("missing behavior %s", id)
			continue
		}
		if len(behavior.OriginPinIDs) == 0 || len(behavior.FactIDs) == 0 {
			t.Errorf("%s lacks source pins or denominator facts", id)
		}
		mapping := graph.mappingForBehavior(id)
		if mapping == nil || mapping.Status != "hypothesis" || !graph.productionTargetsReachable(id, mapping.TargetIDs) {
			t.Errorf("%s lacks mapping hypothesis or production reachability", id)
		}
	}
	databasePath := filepath.Join(t.TempDir(), "provider-wire.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(): %v", err)
	}
	review, err := MappingReview(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("MappingReview(): %v", err)
	}
	if lines := strings.Count(string(review), "\n"); lines != 7 {
		t.Fatalf("mapping review lines = %d, want 7:\n%s", lines, review)
	}
	if !strings.Contains(string(review), "behavior:provider-wire:sampling-precedence\thypothesis") {
		t.Fatalf("mapping review omits sampling hypothesis:\n%s", review)
	}
	assertionReview, err := AssertionAudit(t.Context(), databasePath, root)
	if err != nil {
		t.Fatalf("AssertionAudit(): %v", err)
	}
	if strings.Contains(string(assertionReview), "assertionless") {
		t.Fatalf("provider-wire assertion audit contains assertionless binding:\n%s", assertionReview)
	}
	if lines := strings.Count(string(assertionReview), "\n"); lines != 8 {
		t.Fatalf("provider-wire assertion audit lines = %d, want 8:\n%s", lines, assertionReview)
	}
}

func TestProviderWireMappingsRequireReviewedDecisions(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:provider-wire-decision-test", UpstreamCommit: strings.Repeat("a", 40),
		TargetCommit: targetCommit, ToolchainHash: HashBytes([]byte("provider-wire-decision-toolchain")), EnvironmentHash: HashBytes([]byte("provider-wire-decision-environment")),
	}
	records, _, err := ImportCurrentDenominators(root, snapshot)
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	decisionFile, err := os.Open(filepath.Join(root, "parity", "closuredata", "decisions", "provider-wire.jsonl"))
	if err != nil {
		t.Fatalf("open reviewed decisions: %v", err)
	}
	decisions, decodeErr := DecodeJSONL(decisionFile)
	closeErr := decisionFile.Close()
	if decodeErr != nil {
		t.Fatalf("decode reviewed decisions: %v", decodeErr)
	}
	if closeErr != nil {
		t.Fatalf("close reviewed decisions: %v", closeErr)
	}
	if len(decisions) != len(providerWireDefinitions()) {
		t.Fatalf("reviewed decision count = %d, want %d", len(decisions), len(providerWireDefinitions()))
	}
	records = append(records, decisions...)
	records, err = AddProviderWireBehaviors(root, snapshot, records)
	if err != nil {
		t.Fatalf("AddProviderWireBehaviors(): %v", err)
	}
	records, err = AddProviderWireAssertions(root, snapshot, records)
	if err != nil {
		t.Fatalf("AddProviderWireAssertions(): %v", err)
	}
	records, err = AddProviderWireEvidenceRequests(records)
	if err != nil {
		t.Fatalf("AddProviderWireEvidenceRequests(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	for id, verdict := range graph.Verdicts {
		if verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
			t.Errorf("%s = %s (%s), want open evidence gap", id, verdict.State, verdict.Reason)
		}
	}
	databasePath := filepath.Join(t.TempDir(), "provider-wire-reviewed.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(): %v", err)
	}
	readiness, err := EvidenceReadiness(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("EvidenceReadiness(): %v", err)
	}
	if ready := strings.Count(string(readiness), "\tready\n"); ready != 13 {
		t.Fatalf("ready obligation count = %d, want 13:\n%s", ready, readiness)
	}
	if strings.Contains(string(readiness), "no-admissible-assertion") {
		t.Fatalf("readiness contains inadmissible assertion gap:\n%s", readiness)
	}
}

func TestSourceRangePinRejectsMarkerDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package target\nfunc actual() {}\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	snapshot := &Snapshot{ID: "snapshot:test", TargetCommit: strings.Repeat("b", 40)}
	_, err := makeSourceRangePin(root, snapshot, sourceRange{
		repository: "pig", path: "target.go", semanticID: "target.missing", start: "func missing()", end: "",
	})
	if err == nil || !strings.Contains(err.Error(), "start marker not found") {
		t.Fatalf("makeSourceRangePin() error = %v, want marker drift", err)
	}
}
