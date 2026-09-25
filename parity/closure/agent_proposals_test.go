package closure

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

func TestAddAgentProposalsBindsBothCitedSides(t *testing.T) {
	records := correspondenceMappingFixture(t)
	proposal := correspondence.AgentProposal{
		SubmissionID: "submission:test", PacketID: "packet:test", SnapshotID: "snapshot:test",
		Source: correspondence.SourceIdentity{Language: correspondence.LanguageTypeScript, Revision: coding.UpstreamVersion},
		Target: correspondence.SourceIdentity{Language: correspondence.LanguageGo, Revision: strings.Repeat("b", 40)},
		Role:   correspondence.AdversaryRole, QuestionID: "alignment:example:run", ProposalType: "uncovered-case",
		Rationale: "the result boundary is not challenged", Analyses: []string{"result"},
		Citations: []correspondence.AlignmentCitation{
			{Side: "source", Path: "packages/example.ts", StartLine: 1, EndLine: 3, SourceHash: HashBytes([]byte("quote"))},
			{Side: "target", Path: "example.go", StartLine: 10, EndLine: 12, SourceHash: HashBytes([]byte("target-quote"))},
		},
		Payload: json.RawMessage(`{"fault":"uncovered-case"}`),
	}
	converted, err := AddAgentProposals(records, []correspondence.AgentProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := Build(converted)
	if err != nil {
		t.Fatal(err)
	}
	findings := graph.unresolvedAdversaryFindings("behavior:correspondence:function:run")
	if len(findings) != 1 || !slices.Equal(findings[0].PinIDs, []string{"pin:target", "pin:upstream"}) {
		t.Fatalf("converted findings = %#v", findings)
	}

	proposal.Target.Revision = strings.Repeat("c", 40)
	if _, err := AddAgentProposals(records, []correspondence.AgentProposal{proposal}); err == nil || !strings.Contains(err.Error(), "differs from snapshot") {
		t.Fatalf("stale proposal error = %v", err)
	}
}

func TestUnresolvedAdversaryFindingBlocksMappingPromotion(t *testing.T) {
	records := correspondenceMappingFixture(t)
	finding := adversaryHypothesis(t)
	records = append(records, finding)

	blocked, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := blocked.Verdicts["obligation:correspondence:function:run:result"]; verdict.State != VerdictOpen || verdict.Reason != "unresolved adversary finding" || !slices.Contains(verdict.SupportHashes, blocked.RecordHashes[finding.ID]) {
		t.Fatalf("blocked verdict = %#v", verdict)
	}

	databasePath := t.TempDir() + "/closure.db"
	if err := RebuildStore(t.Context(), databasePath, blocked); err != nil {
		t.Fatal(err)
	}
	review, err := MappingReview(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "behavior:correspondence:function:run\tdecided\tblocked-by-adversary") || !strings.Contains(string(review), finding.ID) {
		t.Fatalf("mapping review does not expose blocker:\n%s", review)
	}

	decision := &Decision{
		Kind: KindDecision, ID: "decision:dismiss-adversary", DecisionType: "dismiss-agent-finding",
		ScopeIDs: []string{finding.ID}, Rationale: "the pinned target transition mechanically covers the cited case", Authority: "reviewer",
	}
	records = append(records, decision)
	stale, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := stale.Verdicts["obligation:correspondence:function:run:result"]; verdict.State != VerdictOpen || verdict.Reason != "no admissible passing evidence" {
		t.Fatalf("old evidence after disposition = %#v, want stale", verdict)
	}
	refreshEvidenceBindings(t, records)
	resolved, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := resolved.Verdicts["obligation:correspondence:function:run:result"]; verdict.State != VerdictProven || !slices.Contains(verdict.SupportHashes, resolved.RecordHashes[finding.ID]) || !slices.Contains(verdict.SupportHashes, resolved.RecordHashes[decision.ID]) {
		t.Fatalf("resolved verdict = %#v", verdict)
	}
}

func TestConfirmedOrMalformedAdversaryFindingStaysBlocking(t *testing.T) {
	launderedRecords := correspondenceMappingFixture(t)
	laundered := adversaryHypothesis(t)
	laundered.HypothesisType = "agent:analyst-proposal"
	launderedRecords = append(launderedRecords, laundered)
	if _, err := Build(launderedRecords); err == nil || !strings.Contains(err.Error(), "role does not match") {
		t.Fatalf("proposal role mismatch error = %v", err)
	}

	records := correspondenceMappingFixture(t)
	records = append(records, adversaryHypothesis(t))
	finding := records[len(records)-1].(*Hypothesis)
	records = append(records, &Decision{
		Kind: KindDecision, ID: "decision:confirm-adversary", DecisionType: "confirm-agent-finding",
		ScopeIDs: []string{finding.ID}, Rationale: "the gap is reproducible and remains open", Authority: "reviewer",
	})
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	verdict := graph.Verdicts["obligation:correspondence:function:run:result"]
	if verdict.Reason != "unresolved adversary finding" {
		t.Fatalf("confirmed finding verdict = %#v", verdict)
	}
	if !slices.Contains(verdict.SupportHashes, graph.RecordHashes["decision:confirm-adversary"]) {
		t.Fatalf("confirmed finding support = %v, want decision hash", verdict.SupportHashes)
	}

	finding.PinIDs = []string{"pin:target", "pin:upstream"}
	var value agentProposalValue
	if err := json.Unmarshal(finding.Value, &value); err != nil {
		t.Fatal(err)
	}
	value.Citations[0].SourceHash = HashBytes([]byte("laundered"))
	finding.Value, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "citations do not bind") {
		t.Fatalf("citation laundering error = %v", err)
	}
}

func correspondenceMappingFixture(t *testing.T) []Record {
	t.Helper()
	records := provedFixture(t)
	const behaviorID = "behavior:correspondence:function:run"
	const obligationID = "obligation:correspondence:function:run:result"
	for _, record := range records {
		switch value := record.(type) {
		case *Behavior:
			value.ID = behaviorID
		case *Mapping:
			value.BehaviorID = behaviorID
		case *Reachability:
			value.BehaviorID = behaviorID
		case *Obligation:
			value.ID = obligationID
			value.BehaviorID = behaviorID
		case *Assertion:
			value.BehaviorID = behaviorID
		case *Decision:
			if value.DecisionType == "mapping" {
				value.ScopeIDs = []string{behaviorID}
			}
		case *EvidenceRequest:
			value.ObligationIDs = []string{obligationID}
		}
	}
	refreshEvidenceBindings(t, records)
	return records
}

func adversaryHypothesis(t *testing.T) *Hypothesis {
	t.Helper()
	h := func(value string) string { return HashBytes([]byte(value)) }
	value := agentProposalValue{
		SubmissionID: "submission:test", PacketID: "packet:test", Role: correspondence.AdversaryRole, QuestionID: "alignment:example:run",
		ProposalType: "uncovered-case", Rationale: "the result boundary is not challenged", Analyses: []string{"result"},
		Citations: []correspondence.AlignmentCitation{
			{Side: "source", Path: "packages/example.ts", StartLine: 1, EndLine: 3, SourceHash: h("quote")},
			{Side: "target", Path: "example.go", StartLine: 10, EndLine: 12, SourceHash: h("target-quote")},
		},
		Payload: json.RawMessage(`{"fault":"uncovered-case"}`),
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	material := strings.Join([]string{value.SubmissionID, value.QuestionID, value.ProposalType}, "\x00")
	return &Hypothesis{
		Kind: KindHypothesis, ID: "hypothesis:agent:" + strings.TrimPrefix(HashBytes([]byte(material)), "sha256:"),
		SnapshotID: "snapshot:test", HypothesisType: adversaryProposalType, SubjectID: "behavior:correspondence:function:run",
		PinIDs: []string{"pin:target", "pin:upstream"}, Value: encoded,
	}
}
