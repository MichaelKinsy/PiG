package correspondence

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestBuildSettingsAlignmentPacketBindsCompleteDynamicFacts(t *testing.T) {
	source, target := comparisonFixtures()
	source.Tables[0].Callbacks = fixtureDispatch(source.Tables[0].Items, "callbacks.onChange")
	target.Tables[0].Callbacks = fixtureDispatch(target.Tables[0].Items, "apply")
	source.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(source.Tables[0].Items, "source")
	target.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(target.Tables[0].Items, "target")
	rules := fixtureRules()
	report, err := Compare(source, target, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("findings = %#v", report.Findings)
	}
	packet, err := BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		t.Fatal(err)
	}
	if err := packet.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(packet.ID, "packet:") || len(packet.Questions) != 3 {
		t.Fatalf("packet identity/questions = %q/%d", packet.ID, len(packet.Questions))
	}
	if len(packet.Functions) != 2 || packet.Functions[0].Source.Name != "compact" {
		t.Fatalf("function questions = %#v", packet.Functions)
	}
	question := packet.Questions[0]
	if question.Source.ID != "autocompact" || question.SourceDispatch.Calls[0] != "callbacks.onChange" || question.TargetDispatch.Writes[0] != "apply" || question.SourceRuntimeCallback.Handler != "source" || question.TargetRuntimeCallback.Handler != "target" {
		t.Fatalf("first question = %#v", question)
	}
	wantAnalyses := []string{"current-state", "dispatch", "persistence", "runtime-effects", "value-domain"}
	if !slices.Equal(question.RequiredAnalyses, wantAnalyses) {
		t.Fatalf("analyses = %v, want %v", question.RequiredAnalyses, wantAnalyses)
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAlignmentWorkPacket(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != packet.ID {
		t.Fatalf("decoded ID = %s, want %s", decoded.ID, packet.ID)
	}

	packet.Questions[0].TargetDispatch.Writes[0] = "changed"
	if err := packet.Validate(); err == nil || !strings.Contains(err.Error(), "ID does not match") {
		t.Fatalf("mutated packet validation error = %v", err)
	}
}

func TestDecodeAlignmentWorkPacketRejectsUnknownFields(t *testing.T) {
	_, err := DecodeAlignmentWorkPacket(strings.NewReader(`{"id":"packet:x","unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeAlignmentWorkPacket() error = %v, want unknown field", err)
	}
}

func TestAlignmentReviewBundleIsTypedCitedAndNonAuthoritative(t *testing.T) {
	source, target := comparisonFixtures()
	source.Tables[0].Callbacks = fixtureDispatch(source.Tables[0].Items, "callbacks.onChange")
	target.Tables[0].Callbacks = fixtureDispatch(target.Tables[0].Items, "apply")
	source.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(source.Tables[0].Items, "source")
	target.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(target.Tables[0].Items, "target")
	rules := fixtureRules()
	report, err := Compare(source, target, rules)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		t.Fatal(err)
	}
	var findings []AlignmentReviewFinding
	for _, question := range packet.Questions {
		findings = append(findings, AlignmentReviewFinding{
			QuestionID: question.ID, Proposal: "ambiguous", Rationale: "dynamic semantics require execution",
			Citations: []AlignmentCitation{citationFor("source", question.Source), citationFor("target", question.Target)},
		})
	}
	for _, question := range packet.Functions {
		findings = append(findings, AlignmentReviewFinding{
			QuestionID: question.ID, Proposal: "ambiguous", Rationale: "ordering and cancellation require review",
			Citations: []AlignmentCitation{functionCitationFor("source", question.Source), functionCitationFor("target", question.Target)},
		})
	}
	bundle, err := BindAlignmentReviewBundle(packet, findings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAlignmentReviewBundle(strings.NewReader(string(encoded)), packet); err != nil {
		t.Fatal(err)
	}

	bundle.Findings[0].Proposal = "accepted"
	if err := bundle.Validate(packet); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("authoritative proposal validation error = %v", err)
	}
	bundle.Findings[0].Proposal = "ambiguous"
	bundle.Findings[0].Citations[1].SourceHash = hashString("unbound")
	if err := bundle.Validate(packet); err == nil || !strings.Contains(err.Error(), "unbound citation") {
		t.Fatalf("unbound citation validation error = %v", err)
	}
}

func TestBuildSettingsAlignmentPacketRejectsOpenCorrespondenceFindings(t *testing.T) {
	source, target := comparisonFixtures()
	report := &Report{Findings: []Finding{{ID: "finding:open"}}}
	if _, err := BuildSettingsAlignmentPacket(source, target, report); err == nil || !strings.Contains(err.Error(), "1 correspondence findings") {
		t.Fatalf("BuildSettingsAlignmentPacket() error = %v", err)
	}
}

func TestRuntimeEffectCandidatesDetectAbsentAndMismatchedLivePaths(t *testing.T) {
	packet := agentPacketAlignmentFixture(t)
	questionIndex := slices.IndexFunc(packet.Questions, func(question AlignmentQuestion) bool {
		return question.ID == "alignment:settings:transport"
	})
	if questionIndex < 0 {
		t.Fatal("transport alignment question missing")
	}
	question := &packet.Questions[questionIndex]
	question.SourceRuntimeCallback.Segments[0].Calls = []FunctionCall{}
	question.SourceRuntimeCallback.Segments[0].Writes = []string{"this.session.agent.transport"}
	packetID, err := alignmentPacketID(packet)
	if err != nil {
		t.Fatal(err)
	}
	packet.ID = packetID
	candidates, err := RuntimeEffectCandidates(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(candidates, []string{question.ID}) {
		t.Fatalf("missing runtime candidates = %v", candidates)
	}
	targetCase := question.TargetRuntimeCallback.Segments[0]
	targetCase.Role = "runtime-case"
	targetCase.Calls = []FunctionCall{{
		Ordinal: 1, Callee: "m.tuiInst.Render", Arguments: []string{}, Conditions: []string{}, StartLine: question.Target.StartLine,
	}}
	question.TargetRuntimeCallback.Segments = append(question.TargetRuntimeCallback.Segments, targetCase)
	packetID, err = alignmentPacketID(packet)
	if err != nil {
		t.Fatal(err)
	}
	packet.ID = packetID
	candidates, err = RuntimeEffectCandidates(packet)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(candidates, []string{question.ID}) {
		t.Fatalf("mismatched runtime candidates = %v", candidates)
	}
	targetCase.Calls[0].Callee = "m.agent.SetTransport"
	question.TargetRuntimeCallback.Segments[len(question.TargetRuntimeCallback.Segments)-1] = targetCase
	packetID, err = alignmentPacketID(packet)
	if err != nil {
		t.Fatal(err)
	}
	packet.ID = packetID
	candidates, err = RuntimeEffectCandidates(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("implemented runtime candidates = %v", candidates)
	}
}

func fixtureRules() Rules {
	return Rules{
		ID:               "settings-packet-test",
		TableTargets:     map[string]string{"table:settings-selector": "table:settings-selector"},
		ConstantTargets:  map[string]string{"SUMMARIZATION_PROMPT": "SUMMARIZATION_PROMPT"},
		FunctionTargets:  map[string]string{"compact": "compact", "generateTurnPrefixSummary": "generateTurnPrefixSummary"},
		CalleeTargets:    map[string]string{},
		CancellableCalls: []string{},
		CallContracts:    []string{"compact", "generateTurnPrefixSummary"},
	}
}

func fixtureDispatch(items []DataItem, effect string) []DispatchCase {
	result := make([]DispatchCase, len(items))
	for index, item := range items {
		result[index] = DispatchCase{ID: item.ID, Reads: []string{}, Writes: []string{effect}, Calls: []string{effect}}
	}
	slices.SortFunc(result, func(left, right DispatchCase) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func fixtureProductionCallbacks(items []DataItem, handler string) []ProductionCallback {
	result := make([]ProductionCallback, len(items))
	for index, item := range items {
		result[index] = ProductionCallback{
			ID: item.ID, Handler: handler,
			Segments: []EffectSegment{{
				Role: "runtime-callback", Path: item.Path, StartLine: item.StartLine, EndLine: item.EndLine,
				SourceHash: item.SourceHash, Reads: []string{}, Writes: []string{}, Calls: []FunctionCall{}, Transitions: []FunctionTransition{},
			}},
		}
	}
	slices.SortFunc(result, func(left, right ProductionCallback) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func citationFor(side string, item DataItem) AlignmentCitation {
	return AlignmentCitation{Side: side, Path: item.Path, StartLine: item.StartLine, EndLine: item.EndLine, SourceHash: item.SourceHash}
}

func functionCitationFor(side string, function Function) AlignmentCitation {
	return AlignmentCitation{Side: side, Path: function.Path, StartLine: function.StartLine, EndLine: function.EndLine, SourceHash: function.SourceHash}
}
