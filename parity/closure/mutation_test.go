package closure

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRequiredMutationPolicyProvesOnlyWhenEveryRelevantMutantIsKilled(t *testing.T) {
	records, run := mutationFixture(t, false)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	verdict := graph.Verdicts["obligation:result"]
	if verdict.State != VerdictProven {
		t.Fatalf("verdict = %s (%s), want proven", verdict.State, verdict.Reason)
	}
	for _, id := range []string{run.ID, run.RequestID, run.Results[0].MutantID} {
		if !slices.Contains(verdict.SupportHashes, graph.RecordHashes[id]) {
			t.Fatalf("verdict support omits %s", id)
		}
	}
	databasePath := filepath.Join(t.TempDir(), "mutation.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatal(err)
	}
	requestJSON, err := ReadMutationRequest(t.Context(), databasePath, run.RequestID)
	if err != nil || !strings.Contains(string(requestJSON), `"kind": "mutation-request"`) {
		t.Fatalf("ReadMutationRequest() = %s, %v", requestJSON, err)
	}
	readiness, err := MutationReadiness(t.Context(), databasePath)
	if err != nil || !strings.Contains(string(readiness), `"state": "ready"`) || !strings.Contains(string(readiness), run.ID) {
		t.Fatalf("MutationReadiness() = %s, %v", readiness, err)
	}

	tests := map[string]struct {
		mutate func(*MutationRun)
		state  VerdictState
		reason string
	}{
		"survived":       {mutate: func(mutated *MutationRun) { mutated.Commands[0].ExitCode = 0 }, state: VerdictContradicted, reason: "relevant mutant survived or failed for the wrong reason"},
		"hidden failure": {mutate: func(mutated *MutationRun) { mutated.Commands[0].Error = "test harness failed" }, state: VerdictContradicted, reason: "relevant mutant survived or failed for the wrong reason"},
		"stale support":  {mutate: func(mutated *MutationRun) { mutated.Support[0].ContentHash = HashBytes([]byte("stale")) }, state: VerdictOpen, reason: "no admissible mutation evidence"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			candidate, candidateRun := mutationFixture(t, false)
			test.mutate(candidateRun)
			var err error
			candidateRun.ID, err = mutationRunContentID(candidateRun)
			if err != nil {
				t.Fatal(err)
			}
			rebindMutationAttestation(t, candidate, candidateRun)
			candidateGraph, err := Build(candidate)
			if err != nil {
				t.Fatal(err)
			}
			verdict := candidateGraph.Verdicts["obligation:result"]
			if verdict.State != test.state || verdict.Reason != test.reason {
				t.Fatalf("verdict = %s (%s), want %s (%s)", verdict.State, verdict.Reason, test.state, test.reason)
			}
		})
	}
	t.Run("forged failure hash", func(t *testing.T) {
		candidate, candidateRun := mutationFixture(t, false)
		candidateRun.Results[0].FailureHash = HashBytes([]byte("forged failure"))
		candidateRun.ID, _ = mutationRunContentID(candidateRun)
		rebindMutationAttestation(t, candidate, candidateRun)
		graph, err := Build(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictContradicted {
			t.Fatalf("forged failure verdict = %#v", verdict)
		}
	})
	t.Run("fresh survivor masks prior killed run", func(t *testing.T) {
		candidate, prior := mutationFixture(t, false)
		survivor := *prior
		survivor.Commands = slices.Clone(prior.Commands)
		survivor.Results = slices.Clone(prior.Results)
		survivor.Commands[0].ExitCode = 0
		survivor.Results[0].FailureHash = mutationStdoutFailureHash(survivor.Commands[0].StdoutHash)
		survivor.Results[0].FailureTest = ""
		survivor.ID, _ = mutationRunContentID(&survivor)
		attestation := &MutationAttestation{Kind: KindMutationAttestation, RunID: survivor.ID, RepositoryCommit: strings.Repeat("e", 40), Path: "parity/survivor.jsonl", Artifacts: []EvidenceArtifact{}}
		attestation.ID, _ = mutationAttestationContentID(attestation)
		candidate = append(candidate, &survivor, attestation)
		graph, err := Build(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictContradicted {
			t.Fatalf("survivor verdict = %#v", verdict)
		}
	})
}

func rebindMutationAttestation(t *testing.T, records []Record, run *MutationRun) {
	t.Helper()
	for _, record := range records {
		attestation, ok := record.(*MutationAttestation)
		if !ok {
			continue
		}
		attestation.RunID = run.ID
		var err error
		attestation.ID, err = mutationAttestationContentID(attestation)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("mutation fixture has no attestation")
}

func TestReviewedManualMutationCanSatisfyMutationRequirement(t *testing.T) {
	records, _ := mutationFixture(t, true)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictProven {
		t.Fatalf("manual mutation verdict = %s (%s), want proven", verdict.State, verdict.Reason)
	}
}

func TestExpiredManualMutationDecisionCannotDischargeMutation(t *testing.T) {
	records, _ := mutationFixture(t, true)
	for _, record := range records {
		if decision, ok := record.(*Decision); ok && decision.DecisionType == "manual-mutation" {
			decision.ValidThroughCommit = strings.Repeat("c", 40)
		}
	}
	if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "lacks an exact reviewed decision") {
		t.Fatalf("expired manual mutation error = %v", err)
	}
}

func TestMutationRecordsRejectFacetIrrelevantOperator(t *testing.T) {
	records, _ := mutationFixture(t, false)
	for _, record := range records {
		if mutant, ok := record.(*Mutant); ok {
			mutant.Operator = "drop-render"
			mutant.ID, _ = mutantContentID(mutant)
		}
	}
	_, err := Build(records)
	if err == nil || !strings.Contains(err.Error(), "incomplete identity, mode, facet, operator, or failure hash") {
		t.Fatalf("Build() error = %v, want facet/operator rejection", err)
	}
}

func TestMutationConstructorsRejectInvalidLocalRecords(t *testing.T) {
	edit := MutationEdit{Path: "../target.go", OriginalHash: HashBytes([]byte("original")), Before: "before", After: "after", MutatedHash: HashBytes([]byte("mutated"))}
	if _, err := NewMutant("snapshot:test", "obligation:test", "test:test", "target:test", "pin:test", "result", "automatic", "change-value", edit, "TestValue", HashBytes([]byte("failure"))); err == nil || !strings.Contains(err.Error(), "repository-relative") {
		t.Fatalf("escaping mutant error = %v", err)
	}
	validEdit := MutationEdit{Path: "target.go", OriginalHash: HashBytes([]byte("original")), Before: "before", After: "after", MutatedHash: HashBytes([]byte("mutated"))}
	mutant, err := NewMutant("snapshot:test", "obligation:test", "test:test", "target:test", "pin:test", "result", "automatic", "change-value", validEdit, "TestValue", HashBytes([]byte("failure")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMutationRequest("snapshot:test", "obligation:test", "test:test", []*Mutant{mutant}, []EvidenceCommand{{Name: "go", Args: []string{"test", "./..."}}}, []string{}, 30); err == nil || !strings.Contains(err.Error(), "require -json") {
		t.Fatalf("unstructured mutation command error = %v", err)
	}
}

func TestMutationRunLengthMismatchReturnsValidationError(t *testing.T) {
	records, run := mutationFixture(t, false)
	run.Commands = append(run.Commands, run.Commands[0])
	run.ID, _ = mutationRunContentID(run)
	rebindMutationAttestation(t, records, run)
	if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "differs from mutation request") {
		t.Fatalf("length mismatch error = %v", err)
	}
}

func TestMutationReadinessDistinguishesMissingStaleSurvivingAndReady(t *testing.T) {
	assertState := func(t *testing.T, records []Record, state string) {
		t.Helper()
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "mutation.db")
		if err := RebuildStore(t.Context(), path, graph); err != nil {
			t.Fatal(err)
		}
		output, err := MutationReadiness(t.Context(), path)
		if err != nil || !strings.Contains(string(output), `"state": "`+state+`"`) {
			t.Fatalf("readiness = %s, %v, want %s", output, err, state)
		}
	}

	ready, _ := mutationFixture(t, false)
	assertState(t, ready, "ready")

	stale, _ := mutationFixture(t, false)
	stale = slices.DeleteFunc(stale, func(record Record) bool { return record.RecordKind() == KindMutationAttestation })
	assertState(t, stale, "stale-run")

	surviving, survivingRun := mutationFixture(t, false)
	survivingRun.Commands[0].ExitCode = 0
	survivingRun.Results[0].FailureHash = mutationStdoutFailureHash(survivingRun.Commands[0].StdoutHash)
	survivingRun.Results[0].FailureTest = ""
	survivingRun.ID, _ = mutationRunContentID(survivingRun)
	rebindMutationAttestation(t, surviving, survivingRun)
	assertState(t, surviving, "surviving-run")

	missingRun, _ := mutationFixture(t, false)
	missingRun = slices.DeleteFunc(missingRun, func(record Record) bool {
		return record.RecordKind() == KindMutationRun || record.RecordKind() == KindMutationAttestation
	})
	assertState(t, missingRun, "missing-run")

	missingRequest, _ := mutationFixture(t, false)
	missingRequest = slices.DeleteFunc(missingRequest, func(record Record) bool {
		return record.RecordKind() == KindMutant || record.RecordKind() == KindMutationRequest || record.RecordKind() == KindMutationRun || record.RecordKind() == KindMutationAttestation
	})
	assertState(t, missingRequest, "missing-request")
}

func TestReadAgentWorkScopeIncludesCanonicalEvidenceTestsAndFixtures(t *testing.T) {
	records := provedFixture(t)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scope.db")
	if err := RebuildStore(t.Context(), path, graph); err != nil {
		t.Fatal(err)
	}
	scope, err := ReadAgentWorkScope(t.Context(), path, "snapshot:test", []string{"obligation:result"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.EvidenceIDs) != 1 || !slices.Equal(scope.TestPaths, []string{"example.go", "fixture.json"}) || !slices.Equal(scope.FixturePaths, []string{"fixture.json"}) {
		t.Fatalf("agent scope = %#v", scope)
	}
}

func mutationFixture(t *testing.T, manual bool) ([]Record, *MutationRun) {
	t.Helper()
	records := provedFixture(t)
	for _, record := range records {
		if test, ok := record.(*Test); ok && test.ID == "test:run" {
			test.MutationPolicy = "required"
		}
	}
	mode := "automatic"
	operator := "change-value"
	stdoutHash := HashBytes([]byte("go test json"))
	stderrHash := HashBytes(nil)
	failureHash := HashBytes([]byte("expected mutation failure"))
	if manual {
		mode = "manual"
	}
	edit := MutationEdit{Path: "example.go", OriginalHash: HashBytes([]byte("original")), Before: "before", After: "after", MutatedHash: HashBytes([]byte("mutated"))}
	expectedTest := "TestMutation"
	if manual {
		expectedTest = ""
	}
	mutant, err := NewMutant("snapshot:test", "obligation:result", "test:run", "target:run", "pin:target", "result", mode, operator, edit, expectedTest, failureHash)
	if err != nil {
		t.Fatal(err)
	}
	commands := []EvidenceCommand{}
	if !manual {
		commands = []EvidenceCommand{{Name: "go", Args: []string{"test", "-json", "./target", "-run", "TestMutation"}}}
	}
	request, err := NewMutationRequest("snapshot:test", "obligation:result", "test:run", []*Mutant{mutant}, commands, []string{}, 60)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, mutant, request)
	var decision *Decision
	results := []MutationResult{{MutantID: mutant.ID, FailureHash: failureHash, BoundTest: expectedTest, MutatedPath: edit.Path, MutatedHash: edit.MutatedHash}}
	baselineResults := []EvidenceCommandResult{}
	commandResults := []EvidenceCommandResult{}
	if manual {
		decision = &Decision{Kind: KindDecision, ID: "decision:manual-mutation", DecisionType: "manual-mutation", ScopeIDs: []string{mutant.ID}, Rationale: "the terminal rendering fault requires visual byte inspection", Authority: "reviewer", ValidThroughCommit: strings.Repeat("b", 40), FailureHashes: []string{failureHash}}
		results[0].DecisionID = decision.ID
		records = append(records, decision)
	} else {
		commandHash, hashErr := evidenceCommandHash(0, 0, commands[0])
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		commandResults = []EvidenceCommandResult{{Run: 1, Index: 1, CommandHash: commandHash, StdoutHash: stdoutHash, StderrHash: stderrHash, ExitCode: 1}}
		baselineResults = []EvidenceCommandResult{{Run: 1, Index: 1, CommandHash: commandHash, StdoutHash: HashBytes(nil), StderrHash: HashBytes(nil), ExitCode: 0}}
		results[0].CommandIndex = 1
		results[0].FailureTest = expectedTest
	}
	base, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	run := &MutationRun{
		Kind: KindMutationRun, RequestID: request.ID, SnapshotID: "snapshot:test", RepositoryCommit: strings.Repeat("b", 40), Executor: "test-executor",
		ExecutorHash: HashBytes([]byte("executor")), Environment: []string{}, Baselines: baselineResults, Commands: commandResults, Results: results, Artifacts: []EvidenceArtifact{},
		Support: base.mutationBindings(request), ToolchainHash: HashBytes([]byte("toolchain")), EnvironmentHash: HashBytes([]byte("environment")),
	}
	run.ID, err = mutationRunContentID(run)
	if err != nil {
		t.Fatal(err)
	}
	attestation := &MutationAttestation{Kind: KindMutationAttestation, RunID: run.ID, RepositoryCommit: strings.Repeat("d", 40), Path: "parity/mutation.jsonl", Artifacts: []EvidenceArtifact{}}
	attestation.ID, err = mutationAttestationContentID(attestation)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, run, attestation)
	refreshEvidenceBindings(t, records)
	// Evidence binding refresh changes the test hash support after mutationPolicy
	// is set, so the mutation run must bind the same rebuilt graph.
	withoutRun := slices.DeleteFunc(slices.Clone(records), func(record Record) bool {
		return record.RecordKind() == KindMutationRun || record.RecordKind() == KindMutationAttestation
	})
	base, err = Build(withoutRun)
	if err != nil {
		t.Fatal(err)
	}
	run.Support = base.mutationBindings(request)
	run.ID, err = mutationRunContentID(run)
	if err != nil {
		t.Fatal(err)
	}
	attestation.RunID = run.ID
	attestation.ID, err = mutationAttestationContentID(attestation)
	if err != nil {
		t.Fatal(err)
	}
	return records, run
}
