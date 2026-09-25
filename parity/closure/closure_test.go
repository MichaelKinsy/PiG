package closure

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestDecodeRejectsUnknownFieldsAndVerdictInputs(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		_, err := DecodeJSONL(strings.NewReader(`{"kind":"facet","id":"facet:result","name":"result","extra":true}` + "\n"))
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("DecodeJSONL() error = %v, want unknown field", err)
		}
	})

	t.Run("derived verdict", func(t *testing.T) {
		_, err := DecodeJSONL(strings.NewReader(`{"kind":"verdict","id":"obligation:result","state":"proven"}` + "\n"))
		if err == nil || !strings.Contains(err.Error(), `record kind "verdict" is not a canonical input`) {
			t.Fatalf("DecodeJSONL() error = %v, want derived-verdict rejection", err)
		}
	})

	t.Run("executor attestation", func(t *testing.T) {
		_, err := DecodeJSONL(strings.NewReader(`{"kind":"evidence-attestation","id":"attestation:forged"}` + "\n"))
		if err == nil || !strings.Contains(err.Error(), `executor-imported only`) {
			t.Fatalf("DecodeJSONL() error = %v, want authored-attestation rejection", err)
		}
	})

	t.Run("agent hypothesis", func(t *testing.T) {
		_, err := DecodeJSONL(strings.NewReader(`{"kind":"hypothesis","id":"hypothesis:agent:forged","snapshotId":"snapshot:test","hypothesisType":"agent:adversary-proposal","subjectId":"behavior:run","pinIds":["pin:upstream"],"value":{}}` + "\n"))
		if err == nil || !strings.Contains(err.Error(), `bundle-imported only`) {
			t.Fatalf("DecodeJSONL() error = %v, want agent-hypothesis rejection", err)
		}
	})
}

func TestBuildRejectsDuplicateAndUnresolvedReferences(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		record := &Facet{Kind: KindFacet, ID: "facet:result", Name: "result"}
		_, err := Build([]Record{record, record})
		if err == nil || !strings.Contains(err.Error(), "duplicate record") {
			t.Fatalf("Build() error = %v, want duplicate record", err)
		}
	})

	t.Run("unresolved", func(t *testing.T) {
		_, err := Build([]Record{
			&Behavior{Kind: KindBehavior, ID: "behavior:missing-pin", Name: "missing", OriginPinIDs: []string{"pin:missing"}, Profile: "application"},
		})
		if err == nil || !strings.Contains(err.Error(), `unresolved reference "pin:missing"`) {
			t.Fatalf("Build() error = %v, want unresolved reference", err)
		}
	})
}

func TestBuildRejectsCrossRecordContradictions(t *testing.T) {
	t.Run("pin commit", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if pin, ok := record.(*Pin); ok && pin.ID == "pin:upstream" {
				pin.Commit = strings.Repeat("c", 40)
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "commit does not match snapshot") {
			t.Fatalf("Build() error = %v, want commit mismatch", err)
		}
	})

	t.Run("mapping decision scope", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if decision, ok := record.(*Decision); ok {
				decision.ScopeIDs = []string{"target:run"}
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "does not decide mapping") {
			t.Fatalf("Build() error = %v, want mapping decision mismatch", err)
		}
	})

	t.Run("unrequested evidence assertion", func(t *testing.T) {
		records := provedFixture(t)
		records = append(records, &Assertion{
			Kind: KindAssertion, ID: "assertion:other", TestID: "test:run", Class: "A2",
			BehaviorID: "behavior:run", FacetID: "facet:result", Oracle: "authored-contract",
		})
		for _, record := range records {
			if evidence, ok := record.(*EvidenceRun); ok {
				evidence.AssertionIDs = []string{"assertion:other"}
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "was not requested") {
			t.Fatalf("Build() error = %v, want unrequested assertion", err)
		}
	})
	t.Run("evidence command", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if evidence, ok := record.(*EvidenceRun); ok {
				evidence.Commands[0].CommandHash = HashBytes([]byte("wrong command"))
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "differs from request") {
			t.Fatalf("Build() error = %v, want command mismatch", err)
		}
	})

	t.Run("evidence result", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if evidence, ok := record.(*EvidenceRun); ok {
				evidence.Result = "fail"
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "does not match execution") {
			t.Fatalf("Build() error = %v, want result mismatch", err)
		}
	})
}

func TestDerivedMappingRequiresResolvedDirectCorrespondence(t *testing.T) {
	t.Run("proves without decision", func(t *testing.T) {
		records := derivedMappingFixture(t)
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		verdict := graph.Verdicts["obligation:result"]
		if verdict.State != VerdictProven {
			t.Fatalf("verdict = %s (%s), want proven", verdict.State, verdict.Reason)
		}
		for _, id := range []string{"rule:correspondence", "fact:correspondence"} {
			if !slices.Contains(verdict.SupportHashes, graph.RecordHashes[id]) {
				t.Fatalf("verdict support omits %s", id)
			}
		}
	})

	t.Run("rule mutation reopens stale evidence", func(t *testing.T) {
		records := derivedMappingFixture(t)
		for _, record := range records {
			if rule, ok := record.(*Rule); ok && rule.ID == "rule:correspondence" {
				rule.DefinitionHash = HashBytes([]byte("changed-correspondence-rule"))
			}
		}
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		verdict := graph.Verdicts["obligation:result"]
		if verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
			t.Fatalf("verdict = %s (%s), want stale evidence open", verdict.State, verdict.Reason)
		}
	})

	t.Run("fact mutation reopens stale evidence", func(t *testing.T) {
		records := derivedMappingFixture(t)
		for _, record := range records {
			if fact, ok := record.(*Fact); ok && fact.ID == "fact:correspondence" {
				fact.PinIDs = []string{"pin:fixture", "pin:target", "pin:upstream"}
			}
		}
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		verdict := graph.Verdicts["obligation:result"]
		if verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
			t.Fatalf("verdict = %s (%s), want stale evidence open", verdict.State, verdict.Reason)
		}
	})

	t.Run("observed fact is rejected", func(t *testing.T) {
		records := derivedMappingFixture(t)
		for _, record := range records {
			if fact, ok := record.(*Fact); ok && fact.ID == "fact:correspondence" {
				fact.Resolution = "observed"
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "not resolved direct correspondence") {
			t.Fatalf("Build() error = %v, want unresolved correspondence rejection", err)
		}
	})

	t.Run("fact hashes must bind pinned source", func(t *testing.T) {
		records := derivedMappingFixture(t)
		for _, record := range records {
			if fact, ok := record.(*Fact); ok && fact.ID == "fact:correspondence" {
				fact.Value = directCorrespondenceValue(t, "unbound-source", "target-quote")
			}
		}
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "does not bind its source pin") {
			t.Fatalf("Build() error = %v, want unbound hash rejection", err)
		}
	})

	t.Run("competing reviewed and derived mappings are rejected", func(t *testing.T) {
		records := derivedMappingFixture(t)
		records = append(records,
			&Decision{Kind: KindDecision, ID: "decision:competing", DecisionType: "mapping", ScopeIDs: []string{"behavior:run"}, Rationale: "manual override", Authority: "reviewer"},
			&Mapping{Kind: KindMapping, ID: "mapping:competing", BehaviorID: "behavior:run", TargetIDs: []string{"target:run"}, Status: "decided", DecisionID: "decision:competing"},
		)
		_, err := Build(records)
		if err == nil || !strings.Contains(err.Error(), "competing accepted mappings") {
			t.Fatalf("Build() error = %v, want competing mapping rejection", err)
		}
	})
}

func TestProofRequiresMeaningfulAssertionAndExecution(t *testing.T) {
	t.Run("A1", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if assertion, ok := record.(*Assertion); ok {
				assertion.Class = "A1"
			}
		}
		refreshEvidenceBindings(t, records)
		graph, err := Build(records)
		if err != nil {
			t.Fatalf("Build(): %v", err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen {
			t.Fatalf("A1 verdict = %s, want open", verdict.State)
		}
	})

	t.Run("authored A3 wire oracle", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			switch value := record.(type) {
			case *Facet:
				value.Name = "wire"
				value.ID = "facet:wire"
			case *Obligation:
				value.FacetID = "facet:wire"
			case *Assertion:
				value.FacetID = "facet:wire"
				value.Class = "A3"
				value.Oracle = "authored-self-agreement"
			}
		}
		refreshEvidenceBindings(t, records)
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen {
			t.Fatalf("authored A3 wire verdict = %s, want open", verdict.State)
		}
	})

	t.Run("no execution witness", func(t *testing.T) {
		records := provedFixture(t)
		for _, record := range records {
			if request, ok := record.(*EvidenceRequest); ok {
				request.Witnesses = []EvidenceWitnessRequest{}
			}
			if evidence, ok := record.(*EvidenceRun); ok {
				evidence.WitnessIDs = []string{}
				evidence.SubjectPinIDs = []string{}
				evidence.Artifacts = slices.Clone(evidence.Artifacts[:2])
				var err error
				evidence.ID, err = evidenceContentID(evidence)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, record := range records {
			attestation, ok := record.(*EvidenceAttestation)
			if !ok {
				continue
			}
			for _, candidate := range records {
				if evidence, evidenceOK := candidate.(*EvidenceRun); evidenceOK {
					attestation.EvidenceID = evidence.ID
					attestation.WitnessIDs = []string{}
					attestation.Artifacts = slices.Clone(evidence.Artifacts)
				}
			}
			var err error
			attestation.ID, err = evidenceAttestationContentID(attestation)
			if err != nil {
				t.Fatal(err)
			}
		}
		records = slices.DeleteFunc(records, func(record Record) bool {
			_, isWitness := record.(*ExecutionWitness)
			return isWitness
		})
		refreshEvidenceBindings(t, records)
		graph, err := Build(records)
		if err != nil {
			t.Fatalf("Build(): %v", err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen {
			t.Fatalf("witnessless verdict = %s, want open", verdict.State)
		}
	})

	for _, facetName := range []string{"cancel", "order", "state", "persistence", "input", "layout", "render"} {
		t.Run("coverage cannot prove "+facetName, func(t *testing.T) {
			records := provedFixture(t)
			facetID := "facet:" + facetName
			for _, record := range records {
				switch value := record.(type) {
				case *Facet:
					value.ID = facetID
					value.Name = facetName
				case *Obligation:
					value.FacetID = facetID
				case *Assertion:
					value.FacetID = facetID
				}
			}
			refreshEvidenceBindings(t, records)
			graph, err := Build(records)
			if err != nil {
				t.Fatal(err)
			}
			if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
				t.Fatalf("coverage-only %s verdict = %s (%s), want open", facetName, verdict.State, verdict.Reason)
			}
		})
	}
}

func TestEvidenceReadinessRequiresFacetCompatibleWitness(t *testing.T) {
	records := provedFixture(t)
	for _, record := range records {
		switch value := record.(type) {
		case *Facet:
			value.ID = "facet:cancel"
			value.Name = "cancel"
		case *Obligation:
			value.FacetID = "facet:cancel"
		case *Assertion:
			value.FacetID = "facet:cancel"
		}
	}
	refreshEvidenceBindings(t, records)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "closure.db")
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatal(err)
	}
	readiness, err := EvidenceReadiness(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(readiness, []byte("obligation:result\trequest:run\tassertion:run\tno-admissible-witness")) {
		t.Fatalf("readiness did not reject coverage-only cancellation:\n%s", readiness)
	}
}

func TestFreshFailureContradictsPassingEvidence(t *testing.T) {
	records := provedFixture(t)
	var pass *EvidenceRun
	for _, record := range records {
		if evidence, ok := record.(*EvidenceRun); ok {
			pass = evidence
		}
	}
	failure := *pass
	failure.Commands = slices.Clone(pass.Commands)
	failure.Commands[0].ExitCode = 1
	failure.Result = "fail"
	transcript, err := json.Marshal(failure.Commands)
	if err != nil {
		t.Fatal(err)
	}
	failure.TranscriptHash = HashBytes(transcript)
	failure.ID, err = evidenceContentID(&failure)
	if err != nil {
		t.Fatal(err)
	}
	failureAttestation := &EvidenceAttestation{
		Kind: KindEvidenceAttestation, EvidenceID: failure.ID, WitnessIDs: slices.Clone(failure.WitnessIDs),
		RepositoryCommit: strings.Repeat("c", 40), Path: "parity/failure.jsonl", Artifacts: slices.Clone(failure.Artifacts),
	}
	failureAttestation.ID, err = evidenceAttestationContentID(failureAttestation)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, &failure, failureAttestation)
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictContradicted {
		t.Fatalf("verdict = %s, want contradicted", verdict.State)
	}
	failureHash := graph.RecordHashes[failure.ID]
	records = append(records, &Decision{
		Kind: KindDecision, ID: "decision:adjudicate", DecisionType: "adjudicate", ScopeIDs: []string{"obligation:result"},
		Rationale: "reviewed failing evidence is superseded by the current passing run", Authority: "reviewer", FailureHashes: []string{failureHash},
	})
	adjudicated, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := adjudicated.Verdicts["obligation:result"]; verdict.State != VerdictProven {
		t.Fatalf("adjudicated verdict = %s, want proven", verdict.State)
	}
}

func TestEvidenceBindingsReopenOnlyChangedSupport(t *testing.T) {
	tests := map[string]func([]Record){
		"source": func(records []Record) {
			for _, record := range records {
				if pin, ok := record.(*Pin); ok && pin.ID == "pin:upstream" {
					pin.QuoteHash = HashBytes([]byte("changed source"))
				}
			}
		},
		"target": func(records []Record) {
			for _, record := range records {
				if pin, ok := record.(*Pin); ok && pin.ID == "pin:target" {
					pin.BodyHash = HashBytes([]byte("changed target"))
				}
			}
		},
		"test": func(records []Record) {
			for _, record := range records {
				if test, ok := record.(*Test); ok {
					test.DefinitionHash = HashBytes([]byte("changed test"))
				}
			}
		},
		"fixture": func(records []Record) {
			for _, record := range records {
				if pin, ok := record.(*Pin); ok && pin.ID == "pin:fixture" {
					pin.BodyHash = HashBytes([]byte("changed fixture"))
				}
			}
		},
		"rule": func(records []Record) {
			for _, record := range records {
				if rule, ok := record.(*Rule); ok {
					rule.DefinitionHash = HashBytes([]byte("changed rule"))
				}
			}
		},
		"decision": func(records []Record) {
			for _, record := range records {
				if decision, ok := record.(*Decision); ok {
					decision.Rationale = "changed mapping rationale"
				}
			}
		},
		"pack": func(records []Record) {
			for _, record := range records {
				if snapshot, ok := record.(*Snapshot); ok {
					snapshot.RulePackHashes = []string{HashBytes([]byte("changed pack"))}
				}
			}
		},
		"environment": func(records []Record) {
			for _, record := range records {
				if snapshot, ok := record.(*Snapshot); ok {
					snapshot.EnvironmentHash = HashBytes([]byte("changed environment"))
				}
			}
		},
		"toolchain": func(records []Record) {
			for _, record := range records {
				if snapshot, ok := record.(*Snapshot); ok {
					snapshot.ToolchainHash = HashBytes([]byte("changed toolchain"))
				}
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			records := provedFixture(t)
			mutate(records)
			graph, err := Build(records)
			if err != nil {
				t.Fatal(err)
			}
			if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
				t.Fatalf("stale support verdict = %s (%s), want open evidence gap", verdict.State, verdict.Reason)
			}
		})
	}

	t.Run("unrelated", func(t *testing.T) {
		records := provedFixture(t)
		records = append(records, &Fact{
			Kind: KindFact, ID: "fact:unrelated", SnapshotID: "snapshot:test", FactType: "test", SubjectID: "unrelated",
			Resolution: "observed", PinIDs: []string{"pin:target"}, Value: json.RawMessage(`{"unrelated":true}`),
		})
		graph, err := Build(records)
		if err != nil {
			t.Fatal(err)
		}
		if verdict := graph.Verdicts["obligation:result"]; verdict.State != VerdictProven {
			t.Fatalf("unrelated record verdict = %s, want proven", verdict.State)
		}
	})
}

func TestInputOrderDoesNotChangeDerivationOrReport(t *testing.T) {
	records := provedFixture(t)
	first, err := Build(records)
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	second, err := Build(records)
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	firstReport, err := RenderStatus(first.EffectiveVerdicts())
	if err != nil {
		t.Fatalf("RenderStatus(first): %v", err)
	}
	secondReport, err := RenderStatus(second.EffectiveVerdicts())
	if err != nil {
		t.Fatalf("RenderStatus(second): %v", err)
	}
	if !bytes.Equal(firstReport, secondReport) {
		t.Fatalf("reports differ by input order:\nfirst:\n%s\nsecond:\n%s", firstReport, secondReport)
	}
	if !bytes.Contains(firstReport, []byte("obligation:result\tproven")) {
		t.Fatalf("report does not contain derived proof:\n%s", firstReport)
	}
}

func TestStoreRebuildIsDeterministicAndCorruptSupportFailsClosed(t *testing.T) {
	ctx := context.Background()
	graph, err := Build(provedFixture(t))
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	dbPath := t.TempDir() + "/closure.db"
	if err := RebuildStore(ctx, dbPath, graph); err != nil {
		t.Fatalf("RebuildStore(first): %v", err)
	}
	frontier, err := Frontier(ctx, dbPath, "pin:target")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(frontier, []byte("pin:target\tobligation:result\tproven\tpin:target -> obligation:result")) {
		t.Fatalf("frontier omits supported obligation:\n%s", frontier)
	}
	first, err := ReadStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("ReadStatus(first): %v", err)
	}
	if err := RebuildStore(ctx, dbPath, graph); err != nil {
		t.Fatalf("RebuildStore(second): %v", err)
	}
	second, err := ReadStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("ReadStatus(second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("store report changed after rebuild:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	if err := mutateStore(ctx, dbPath, "UPDATE verdicts SET state = ?, reason = ? WHERE obligation_id = ?", VerdictWaived, "forged", "obligation:result"); err != nil {
		t.Fatalf("forge stored verdict: %v", err)
	}
	forged, err := ReadStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("ReadStatus(forged): %v", err)
	}
	if !bytes.Contains(forged, []byte("obligation:result\tproven")) {
		t.Fatalf("stored verdict overrode derivation:\n%s", forged)
	}

	verdict := graph.Verdicts["obligation:result"]
	if verdict.State != VerdictProven || len(verdict.SupportHashes) == 0 {
		t.Fatalf("derived verdict = %#v, want supported proof", verdict)
	}
	if err := mutateStore(ctx, dbPath, "DELETE FROM records WHERE content_hash = ?", verdict.SupportHashes[0]); err != nil {
		t.Fatalf("delete stored support: %v", err)
	}
	corrupt, err := ReadStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("ReadStatus(corrupt): %v", err)
	}
	if bytes.Contains(corrupt, []byte("obligation:result\tproven")) || !bytes.Contains(corrupt, []byte("obligation:result\topen")) {
		t.Fatalf("corrupt support did not fail closed:\n%s", corrupt)
	}
	if err := VerifyStore(ctx, dbPath); err == nil || !strings.Contains(err.Error(), "unresolved support") {
		t.Fatalf("VerifyStore() error = %v, want unresolved support", err)
	}

	if err := RebuildStore(ctx, dbPath, graph); err != nil {
		t.Fatalf("RebuildStore(after corruption): %v", err)
	}
	if err := mutateStore(ctx, dbPath, "DELETE FROM verdict_support WHERE obligation_id = ?", "obligation:result"); err != nil {
		t.Fatalf("delete all stored support: %v", err)
	}
	missing, err := ReadStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("ReadStatus(missing support): %v", err)
	}
	if !bytes.Contains(missing, []byte("obligation:result\topen\tunresolved support")) {
		t.Fatalf("supportless verdict did not fail closed:\n%s", missing)
	}
}

func TestSafeStorePathRejectsSymlinkedParentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	testenv.Symlink(t, outside, filepath.Join(root, "cache"))
	_, err := SafeStorePath(root, "cache/graph.db")
	if err == nil || !strings.Contains(err.Error(), "symlink escapes root") {
		t.Fatalf("SafeStorePath() error = %v, want symlink escape", err)
	}
}

func mutateStore(ctx context.Context, path, statement string, args ...any) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(ctx, statement, args...)
	return err
}

func refreshEvidenceBindings(t *testing.T, records []Record) {
	t.Helper()
	baseRecords := slices.DeleteFunc(slices.Clone(records), func(record Record) bool {
		return record.RecordKind() == KindEvidenceRun || record.RecordKind() == KindEvidenceAttestation
	})
	base, err := Build(baseRecords)
	if err != nil {
		t.Fatal(err)
	}
	replaced := make(map[string]string)
	for _, record := range records {
		evidence, ok := record.(*EvidenceRun)
		if !ok {
			continue
		}
		oldID := evidence.ID
		request := base.Records[evidence.RequestID].(*EvidenceRequest)
		evidence.Bindings = base.evidenceBindings(request)
		evidence.ID, err = evidenceContentID(evidence)
		if err != nil {
			t.Fatal(err)
		}
		replaced[oldID] = evidence.ID
	}
	for _, record := range records {
		attestation, ok := record.(*EvidenceAttestation)
		if !ok {
			continue
		}
		if replacement := replaced[attestation.EvidenceID]; replacement != "" {
			attestation.EvidenceID = replacement
		}
		attestation.ID, err = evidenceAttestationContentID(attestation)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func provedFixture(t *testing.T) []Record {
	t.Helper()
	h := func(value string) string { return HashBytes([]byte(value)) }
	command := EvidenceCommand{Name: "go", Args: []string{"test", "./target"}}
	commandHash, err := evidenceCommandHash(0, 0, command)
	if err != nil {
		t.Fatal(err)
	}
	commandResults := []EvidenceCommandResult{{Run: 1, Index: 1, CommandHash: commandHash, StdoutHash: h("stdout"), StderrHash: h("stderr")}}
	transcript, err := json.Marshal(commandResults)
	if err != nil {
		t.Fatal(err)
	}
	witness := &ExecutionWitness{
		Kind: KindExecutionWitness, SnapshotID: "snapshot:test", RequestID: "request:run", WitnessType: "go-covered-range",
		TargetIDs: []string{"target:run"}, SubjectPinIDs: []string{"pin:target"},
		Artifacts:        []EvidenceArtifact{{Path: "witness-001.coverprofile", Hash: h("coverage")}},
		CoveredRanges:    []EvidenceCoveredRange{{TargetID: "target:run", PinID: "pin:target", Path: "example.go", StartLine: 10, EndLine: 12, Count: 1}},
		ProviderCaptures: []EvidenceProviderCapture{},
	}
	witness.ID, err = executionWitnessContentID(witness)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := []EvidenceArtifact{
		{Path: "run-001-command-001.stderr", Hash: h("stderr")},
		{Path: "run-001-command-001.stdout", Hash: h("stdout")},
		{Path: "witness-001.coverprofile", Hash: h("coverage")},
	}
	request := &EvidenceRequest{Kind: KindEvidenceRequest, ID: "request:run", SnapshotID: "snapshot:test", ObligationIDs: []string{"obligation:result"}, AssertionIDs: []string{"assertion:run"}, Commands: []EvidenceCommand{command}, Environment: []string{}, Witnesses: []EvidenceWitnessRequest{{WitnessType: "go-covered-range", TargetIDs: []string{"target:run"}, SubjectPinIDs: []string{"pin:target"}, CommandIndexes: []int{1}, ArtifactPath: "tmp/coverage.out"}}, Durability: 1, Comparator: "exit-zero", TimeoutSeconds: 60}
	records := []Record{
		&Snapshot{
			Kind: KindSnapshot, ID: "snapshot:test", UpstreamCommit: strings.Repeat("a", 40),
			TargetCommit: strings.Repeat("b", 40), ToolchainHash: h("toolchain"), EnvironmentHash: h("environment"),
		},
		&Pin{
			Kind: KindPin, ID: "pin:upstream", SnapshotID: "snapshot:test", Repository: "upstream",
			Commit: strings.Repeat("a", 40), Path: "packages/example.ts", SemanticID: "pkg:example#run",
			StartLine: 1, EndLine: 3, QuoteHash: h("quote"), APIHash: h("api"), BodyHash: h("body"),
		},
		&Pin{
			Kind: KindPin, ID: "pin:target", SnapshotID: "snapshot:test", Repository: "pig",
			Commit: strings.Repeat("b", 40), Path: "example.go", SemanticID: "example.Run",
			StartLine: 10, EndLine: 12, QuoteHash: h("target-quote"), APIHash: h("target-api"), BodyHash: h("target-body"),
		},
		&Pin{
			Kind: KindPin, ID: "pin:fixture", SnapshotID: "snapshot:test", Repository: "pig",
			Commit: strings.Repeat("b", 40), Path: "fixture.json", SemanticID: "fixture.run",
			StartLine: 1, EndLine: 1, QuoteHash: h("fixture-quote"), BodyHash: h("fixture-body"),
		},
		&Facet{Kind: KindFacet, ID: "facet:result", Name: "result"},
		&Rule{Kind: KindRule, ID: "rule:result", Name: "result requires A2 and production execution", DefinitionHash: h("rule")},
		&Behavior{Kind: KindBehavior, ID: "behavior:run", Name: "run", OriginPinIDs: []string{"pin:upstream"}, Profile: "application"},
		&Target{Kind: KindTarget, ID: "target:run", SnapshotID: "snapshot:test", PinIDs: []string{"pin:target"}, Language: "go", Symbol: "example.Run"},
		&Decision{Kind: KindDecision, ID: "decision:mapping", DecisionType: "mapping", ScopeIDs: []string{"behavior:run"}, Rationale: "exact production translation", Authority: "reviewer"},
		&Mapping{Kind: KindMapping, ID: "mapping:run", BehaviorID: "behavior:run", TargetIDs: []string{"target:run"}, Status: "decided", DecisionID: "decision:mapping"},
		&Reachability{Kind: KindReachability, ID: "reachability:run", BehaviorID: "behavior:run", TargetID: "target:run", Class: "prod-reachable", Method: "entrypoint", RootPinIDs: []string{"pin:target"}},
		&Obligation{Kind: KindObligation, ID: "obligation:result", BehaviorID: "behavior:run", FacetID: "facet:result", RuleID: "rule:result", OriginPinIDs: []string{"pin:upstream"}},
		&Test{Kind: KindTest, ID: "test:run", SnapshotID: "snapshot:test", PinID: "pin:target", FixturePinIDs: []string{"pin:fixture"}, DefinitionHash: h("test")},
		&Assertion{Kind: KindAssertion, ID: "assertion:run", TestID: "test:run", Class: "A2", BehaviorID: "behavior:run", FacetID: "facet:result", Oracle: "authored-contract"},
		witness,
		request,
	}
	base, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &EvidenceRun{
		Kind: KindEvidenceRun, RequestID: request.ID, SnapshotID: "snapshot:test", Result: "pass",
		AssertionIDs: []string{"assertion:run"}, WitnessIDs: []string{witness.ID}, SubjectPinIDs: []string{"pin:target"},
		Executor: "test-executor", ExecutorHash: h("executor"), Environment: []string{"CI=1"}, Commands: commandResults,
		Artifacts: artifacts, Bindings: base.evidenceBindings(request), ToolchainHash: h("toolchain"), EnvironmentHash: h("environment"), Durability: 1, TranscriptHash: HashBytes(transcript),
	}
	evidence.ID, err = evidenceContentID(evidence)
	if err != nil {
		t.Fatal(err)
	}
	attestation := &EvidenceAttestation{
		Kind: KindEvidenceAttestation, EvidenceID: evidence.ID, WitnessIDs: []string{witness.ID},
		RepositoryCommit: strings.Repeat("c", 40), Path: "parity/evidence.jsonl", Artifacts: slices.Clone(artifacts),
	}
	attestation.ID, err = evidenceAttestationContentID(attestation)
	if err != nil {
		t.Fatal(err)
	}
	return append(records, evidence, attestation)
}

func derivedMappingFixture(t *testing.T) []Record {
	t.Helper()
	records := provedFixture(t)
	records = slices.DeleteFunc(records, func(record Record) bool {
		decision, ok := record.(*Decision)
		return ok && decision.ID == "decision:mapping"
	})
	for _, record := range records {
		mapping, ok := record.(*Mapping)
		if !ok || mapping.ID != "mapping:run" {
			continue
		}
		mapping.Status = "derived"
		mapping.DecisionID = ""
		mapping.RuleID = "rule:correspondence"
		mapping.FactIDs = []string{"fact:correspondence"}
	}
	records = append(records,
		&Rule{Kind: KindRule, ID: "rule:correspondence", Name: "unique normalized direct correspondence", DefinitionHash: HashBytes([]byte("correspondence-rule"))},
		&Fact{
			Kind: KindFact, ID: "fact:correspondence", SnapshotID: "snapshot:test", FactType: "correspondence:direct",
			SubjectID: "behavior:run", Resolution: "resolved", PinIDs: []string{"pin:target", "pin:upstream"},
			Value: directCorrespondenceValue(t, "quote", "target-quote"),
		},
	)
	refreshEvidenceBindings(t, records)
	return records
}

func directCorrespondenceValue(t *testing.T, sourceHash, targetHash string) json.RawMessage {
	t.Helper()
	value, err := json.Marshal(directCorrespondenceFact{
		RuleID: "rule:correspondence", SourceID: "pkg:example#run", TargetID: "example.Run",
		SourceHash: HashBytes([]byte(sourceHash)), TargetHash: HashBytes([]byte(targetHash)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
