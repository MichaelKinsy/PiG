package correspondence

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAgentWorkPacketsAreReadOnlyScopedAndContentAddressed(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	scope.TestPaths = []string{"parity/translation_test.go"}
	unresolved := []string{"alignment:compaction:compact"}
	for _, role := range []string{AnalystRole, ContractSynthesizerRole, TranslatorRole, AdversaryRole} {
		packet, err := BuildAgentWorkPacket(alignment, role, unresolved, scope)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(packet.ID, "packet:") || len(packet.Questions) != 5 || len(packet.WritePaths) != 0 || !slices.Equal(packet.UnresolvedEdges, unresolved) {
			t.Fatalf("%s packet = %#v", role, packet)
		}
		if slices.Contains(packet.ForbiddenActions, "propose-gap") || !slices.Contains(packet.ForbiddenActions, "derive-verdict") || !slices.Contains(packet.ForbiddenActions, "accept-mapping") {
			t.Fatalf("%s authority = %v", role, packet.ForbiddenActions)
		}
		packet.WritePaths = []string{"internal/codingagent/settings.go"}
		if err := packet.Validate(); err == nil || !strings.Contains(err.Error(), "not read-only") {
			t.Fatalf("writable %s packet error = %v", role, err)
		}
		packet.WritePaths = []string{}
		packet.ObligationIDs = packet.ObligationIDs[1:]
		if err := packet.Validate(); err == nil || !strings.Contains(err.Error(), "obligation scope differs") {
			t.Fatalf("truncated %s scope error = %v", role, err)
		}
	}
}

func TestAlignmentUsesCommonAgentSubmissionContract(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildAgentWorkPacket(alignment, AlignmentReviewerRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	findings := make([]AlignmentBundleFinding, 0, len(packet.Questions))
	for _, question := range packet.Questions {
		findings = append(findings, AlignmentBundleFinding{
			QuestionID: question.ID, Proposal: "aligned", Rationale: "source and target citations are aligned",
			Citations: question.Citations,
		})
	}
	bundle, err := BindAlignmentBundle(packet, findings)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := BindAgentBundleSubmission(packet, bundle)
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := submission.Proposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != len(findings) || proposals[0].Role != AlignmentReviewerRole {
		t.Fatalf("alignment proposals = %#v", proposals)
	}
	if _, err := DecodeAgentBundleSubmission(mustJSON(t, submission)); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value *AgentBundleSubmission) *strings.Reader {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(encoded))
}

func TestAgentBundlesAreCitedNonAuthoritativeProposals(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	question := alignment.Questions[0]
	citations := []AlignmentCitation{
		{Side: "source", Path: question.Source.Path, StartLine: question.Source.StartLine, EndLine: question.Source.EndLine, SourceHash: question.Source.SourceHash},
		{Side: "target", Path: question.Target.Path, StartLine: question.Target.StartLine, EndLine: question.Target.EndLine, SourceHash: question.Target.SourceHash},
	}

	analystPacket, err := BuildAgentWorkPacket(alignment, AnalystRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	analyst, err := BindAnalystBundle(analystPacket, []AnalystFinding{{
		QuestionID: question.ID, Proposal: "gap", Rationale: "runtime effect needs execution",
		Analyses: []string{"runtime-effects"}, Citations: citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(analyst.ID, "bundle:") {
		t.Fatalf("analyst bundle ID = %s", analyst.ID)
	}
	encodedAnalyst, err := json.Marshal(analyst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAnalystBundle(strings.NewReader(string(encodedAnalyst)), analystPacket); err != nil {
		t.Fatal(err)
	}
	analyst.Findings[0].Proposal = "accepted"
	if err := analyst.Validate(analystPacket); err == nil {
		t.Fatal("analyst bundle accepted an authoritative proposal")
	}

	contractPacket, err := BuildAgentWorkPacket(alignment, ContractSynthesizerRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := BindContractBundle(contractPacket, []ContractProposal{{
		QuestionID: question.ID, Analysis: "persistence", Class: "A2", Oracle: "authored-upstream-contract",
		TestRequirement: "round-trip exact changed field", WitnessType: "persistence-roundtrip", Citations: citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	encodedContract, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeContractBundle(strings.NewReader(string(encodedContract)), contractPacket); err != nil {
		t.Fatal(err)
	}
	contract.Proposals[0].WitnessType = "terminal-trace"
	if err := contract.Validate(contractPacket); err == nil {
		t.Fatal("contract bundle accepted a facet-incompatible witness")
	}

	translatorScope := scope
	translatorScope.TestPaths = []string{"parity/translation_test.go"}
	translatorPacket, err := BuildAgentWorkPacket(alignment, TranslatorRole, nil, translatorScope)
	if err != nil {
		t.Fatal(err)
	}
	translator, err := BindTranslatorBundle(translatorPacket, []TranslationProposal{{
		QuestionID: question.ID, Analysis: "runtime-effects", Rationale: "route the translated callback through the production target",
		ProductionEdits:   []TranslationEdit{{Path: question.Target.Path, OriginalHash: hashString("target original"), Replacement: "translated production replacement"}},
		TestEdits:         []TranslationEdit{{Path: "parity/translation_test.go", OriginalHash: hashString("test original"), Replacement: "independent regression replacement"}},
		EvidencePlan:      "execute the production dispatch and bind its event-delivery witness",
		MutationOperators: []string{"drop-dispatch"}, Citations: citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := BindAgentBundleSubmission(translatorPacket, translator)
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := submission.Proposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 || proposals[0].Role != TranslatorRole || proposals[0].ProposalType != "translation:runtime-effects" {
		t.Fatalf("translator proposals = %#v", proposals)
	}
	if _, err := DecodeAgentBundleSubmission(mustJSON(t, submission)); err != nil {
		t.Fatal(err)
	}
	translator.Translations[0].MutationOperators = []string{"drop-render"}
	if err := translator.Validate(translatorPacket); err == nil || !strings.Contains(err.Error(), "does not challenge") {
		t.Fatalf("facet-irrelevant translator mutation error = %v", err)
	}
	translator.Translations[0].MutationOperators = []string{}
	if err := translator.Validate(translatorPacket); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("empty translator mutations error = %v", err)
	}
	translator.Translations[0].MutationOperators = []string{"drop-dispatch"}
	translator.Translations[0].TestEdits[0].Path = "parity/../translation_test.go"
	if err := translator.Validate(translatorPacket); err == nil || !strings.Contains(err.Error(), "outside its test paths") {
		t.Fatalf("noncanonical translator path error = %v", err)
	}

	adversaryPacket, err := BuildAgentWorkPacket(alignment, AdversaryRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	adversary, err := BindAdversaryBundle(adversaryPacket, []AdversaryFinding{{
		QuestionID: question.ID, Fault: "uncovered-case", Rationale: "explicit false may collapse into absence",
		Analyses: []string{"current-state"}, Citations: citations,
	}})
	if err != nil {
		t.Fatal(err)
	}
	encodedAdversary, err := json.Marshal(adversary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAdversaryBundle(strings.NewReader(string(encodedAdversary)), adversaryPacket); err != nil {
		t.Fatal(err)
	}
	authoritative := strings.Replace(string(encodedAdversary), `"packetId":`, `"verdict":"proven","packetId":`, 1)
	if _, err := DecodeAdversaryBundle(strings.NewReader(authoritative), adversaryPacket); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("authoritative adversary output error = %v", err)
	}
	transcript := strings.Replace(string(encodedAdversary), `"packetId":`, `"transcript":"agent agreed","packetId":`, 1)
	if _, err := DecodeAdversaryBundle(strings.NewReader(transcript), adversaryPacket); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("adversary transcript error = %v", err)
	}
	adversary.Findings[0].Citations[1].SourceHash = hashString("laundered")
	if err := adversary.Validate(adversaryPacket); err == nil || !strings.Contains(err.Error(), "unbound citation") {
		t.Fatalf("citation laundering error = %v", err)
	}
}

func TestAgentBundleCitationBudgetAppliesToWholeBundle(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildAgentWorkPacket(alignment, AnalystRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	packet.Budget = AgentWorkBudget{MaxFindings: 2, MaxCitations: 2}
	packet.ID, err = agentContentID("packet", packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := packet.Validate(); err != nil {
		t.Fatal(err)
	}
	findings := make([]AnalystFinding, 0, 2)
	for _, question := range packet.Questions[:2] {
		findings = append(findings, AnalystFinding{
			QuestionID: question.ID, Proposal: "gap", Rationale: "the behavior needs execution",
			Analyses: question.RequiredAnalyses[:1], Citations: question.Citations[:2],
		})
	}
	if _, err := BindAnalystBundle(packet, findings); err == nil || !strings.Contains(err.Error(), "citation budget") {
		t.Fatalf("bundle citation budget error = %v", err)
	}
}

func TestAgentPacketRejectsUnknownRoleAndUnresolvedEdge(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAgentWorkPacket(alignment, "reviewer", nil, scope); err == nil || !strings.Contains(err.Error(), "unsupported agent role") {
		t.Fatalf("unknown role error = %v", err)
	}
	if _, err := BuildAgentWorkPacket(alignment, AnalystRole, []string{"alignment:missing"}, scope); err == nil || !strings.Contains(err.Error(), "outside scope") {
		t.Fatalf("unknown unresolved edge error = %v", err)
	}
	packet, err := BuildAgentWorkPacket(alignment, AnalystRole, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	malformed := strings.Replace(string(encoded), `"outputSchema":`, `"unknown":true,"outputSchema":`, 1)
	if _, err := DecodeAgentWorkPacket(strings.NewReader(malformed)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func agentPacketAlignmentFixture(t *testing.T) *AlignmentWorkPacket {
	t.Helper()
	source, target := comparisonFixtures()
	source.Tables[0].Callbacks = fixtureDispatch(source.Tables[0].Items, "callbacks.onChange")
	target.Tables[0].Callbacks = fixtureDispatch(target.Tables[0].Items, "apply")
	source.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(source.Tables[0].Items, "source")
	target.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(target.Tables[0].Items, "target")
	report, err := Compare(source, target, fixtureRules())
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		t.Fatal(err)
	}
	return packet
}
