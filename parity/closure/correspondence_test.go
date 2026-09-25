package closure

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
	"github.com/MichaelKinsy/PiG/parity/knowngaps"
)

func TestAddCorrespondenceDenominatorGeneratesExactOpenGraph(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:correspondence-test",
		UpstreamCommit: coding.UpstreamCommit, TargetCommit: targetCommit,
		ToolchainHash: HashBytes([]byte("correspondence-toolchain")), EnvironmentHash: HashBytes([]byte("correspondence-environment")),
	}
	records := []Record{snapshot}
	first, err := AddCorrespondenceDenominator(t.Context(), root, snapshot, records)
	if correspondenceBlockedByKnownGaps(t, root, err) {
		return
	}
	second, err := AddCorrespondenceDenominator(t.Context(), root, snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	if encodedRecords(t, first) != encodedRecords(t, second) {
		t.Fatal("correspondence denominator is not deterministic")
	}
	graph, err := Build(first)
	if err != nil {
		t.Fatal(err)
	}
	var behaviors, mappings, obligations, directFacts, normalizedSettingFacts, normalizedFunctionFacts int
	for _, record := range first {
		switch value := record.(type) {
		case *Behavior:
			if strings.HasPrefix(value.ID, "behavior:correspondence:") {
				behaviors++
			}
		case *Mapping:
			if strings.HasPrefix(value.ID, "mapping:correspondence:") {
				mappings++
			}
		case *Obligation:
			if strings.HasPrefix(value.ID, "obligation:correspondence:") {
				obligations++
			}
		case *Fact:
			switch value.FactType {
			case "correspondence:direct":
				directFacts++
			case "correspondence:normalized-setting":
				normalizedSettingFacts++
			case "correspondence:normalized-function":
				normalizedFunctionFacts++
			}
		}
	}
	// 43/43/35/33/10: the reviewed current settings inventory has 33 rows
	// (independently counted from settings-selector.ts; see
	// parity/cmd/correspondence/main_test.go's TestCompareCurrentPin), three
	// more than this pin's prior 30, which raises the behaviors/mappings/
	// direct-fact counts by the same three settings
	// (cache-warming-mode, model-thinking, hide-thinking's fix for the
	// pre-existing "thinking" typo below). Reproduce by running this test.
	if behaviors != 43 || mappings != 43 || directFacts != 35 || normalizedSettingFacts != 33 || normalizedFunctionFacts != 10 {
		t.Fatalf("generated behaviors/mappings/facts = %d/%d/%d/%d/%d, want 43/43/35/33/10", behaviors, mappings, directFacts, normalizedSettingFacts, normalizedFunctionFacts)
	}
	// 208 matches parity/cmd/correspondence/main_test.go's independently
	// reproduced work-packet obligationIds count for the same 33-row
	// settings inventory.
	if obligations != 208 {
		t.Fatalf("generated obligations = %d, want 208", obligations)
	}
	lineageFact := graph.Records["fact:correspondence:setting-lineage:autocompact"].(*Fact)
	if len(lineageFact.PinIDs) != 7 {
		t.Fatalf("autocompact lineage pin count = %d, want 7", len(lineageFact.PinIDs))
	}
	var lineage correspondence.AlignmentQuestion
	if err := json.Unmarshal(lineageFact.Value, &lineage); err != nil {
		t.Fatal(err)
	}
	if lineage.Source.ID != "autocompact" || lineage.SourceDispatch.ID != "autocompact" || lineage.TargetDispatch.ID != "autocompact" {
		t.Fatalf("autocompact lineage = %#v", lineage)
	}
	settingVerdict := graph.Verdicts["obligation:correspondence:setting:autocompact:current-state"]
	if settingVerdict.State != VerdictOpen || settingVerdict.Reason != "no admissible passing evidence" {
		t.Fatalf("derived setting verdict = %#v", settingVerdict)
	}
	functionVerdict := graph.Verdicts["obligation:correspondence:function:compact:call-order"]
	if functionVerdict.State != VerdictOpen || functionVerdict.Reason != "no admissible passing evidence" {
		t.Fatalf("function candidate verdict = %#v", functionVerdict)
	}
	mergeVerdict := graph.Verdicts["obligation:correspondence:function:mergeSettings:precedence"]
	if mergeVerdict.State != VerdictOpen || mergeVerdict.Reason != "no accepted mapping" {
		t.Fatalf("function hypothesis verdict = %#v", mergeVerdict)
	}
	compactMapping := graph.Records["mapping:correspondence:function:compact"].(*Mapping)
	compactReachability := graph.Records["reachability:correspondence:function:compact"].(*Reachability)
	applyReachability := graph.Records["reachability:correspondence:function:applyOverrides"].(*Reachability)
	if compactMapping.Status != "derived" || compactReachability.Class != "prod-reachable" || len(compactReachability.RootPinIDs) != 1 {
		t.Fatalf("compact mapping/reachability = %#v / %#v", compactMapping, compactReachability)
	}
	if applyReachability.Class != "unknown" || applyReachability.Uncertainty == "" {
		t.Fatalf("applyOverrides reachability = %#v", applyReachability)
	}
	settingMapping := graph.Records["mapping:correspondence:setting:autocompact"].(*Mapping)
	settingMapping.Status = "hypothesis"
	settingMapping.RuleID = ""
	settingMapping.FactIDs = nil
	mutated, err := Build(first)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := mutated.Verdicts[settingVerdict.ObligationID]; verdict.Reason != "no accepted mapping" {
		t.Fatalf("mapping mutation verdict = %#v", verdict)
	}
}

func TestAddCorrespondenceDenominatorRejectsNilSnapshot(t *testing.T) {
	if _, err := AddCorrespondenceDenominator(t.Context(), ".", nil, nil); err == nil {
		t.Fatal("AddCorrespondenceDenominator() accepted nil snapshot")
	}
}

func TestAddCorrespondenceDenominatorRejectsWrongUpstreamCommit(t *testing.T) {
	snapshot := &Snapshot{UpstreamCommit: strings.Repeat("a", 40)}
	_, err := AddCorrespondenceDenominator(t.Context(), ".", snapshot, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("AddCorrespondenceDenominator() error = %v", err)
	}
}

func TestCorrespondenceAssertionsRequestTypedCompactionEvidence(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	snapshot := &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:correspondence-assertions",
		UpstreamCommit: coding.UpstreamCommit, TargetCommit: targetCommit,
		ToolchainHash: HashBytes([]byte("correspondence-toolchain")), EnvironmentHash: HashBytes([]byte("correspondence-environment")),
	}
	records, err := AddCorrespondenceDenominator(t.Context(), root, snapshot, []Record{snapshot})
	if correspondenceBlockedByKnownGaps(t, root, err) {
		return
	}
	records, err = AddCorrespondenceAssertions(root, snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	records, err = AddCorrespondenceEvidenceRequests(records)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	orderRequest := graph.Records["request:correspondence:compaction-order"].(*EvidenceRequest)
	cancellationRequest := graph.Records["request:correspondence:compaction-cancellation"].(*EvidenceRequest)
	settingsRequest := graph.Records["request:correspondence:settings-display-persistence"].(*EvidenceRequest)
	liveSettingsRequest := graph.Records["request:correspondence:settings-live-effects"].(*EvidenceRequest)
	if len(orderRequest.ObligationIDs) != 2 || len(orderRequest.AssertionIDs) != 2 || len(orderRequest.Witnesses) != 1 || orderRequest.Witnesses[0].WitnessType != "state-transition" {
		t.Fatalf("order request = %#v", orderRequest)
	}
	if len(cancellationRequest.ObligationIDs) != 4 || len(cancellationRequest.AssertionIDs) != 4 || len(cancellationRequest.Witnesses) != 1 || cancellationRequest.Witnesses[0].WitnessType != "cancellation-trace" {
		t.Fatalf("cancellation request = %#v", cancellationRequest)
	}
	if len(settingsRequest.ObligationIDs) != 3 || len(settingsRequest.AssertionIDs) != 3 || len(settingsRequest.Witnesses) != 1 || settingsRequest.Witnesses[0].WitnessType != "persistence-roundtrip" || len(settingsRequest.Witnesses[0].TargetIDs) != 3 || len(settingsRequest.Witnesses[0].SubjectPinIDs) != 3 {
		t.Fatalf("settings request = %#v", settingsRequest)
	}
	if len(liveSettingsRequest.ObligationIDs) != 8 || len(liveSettingsRequest.AssertionIDs) != 8 || len(liveSettingsRequest.Witnesses) != 1 || liveSettingsRequest.Witnesses[0].WitnessType != "state-transition" || len(liveSettingsRequest.Witnesses[0].TargetIDs) != 8 || len(liveSettingsRequest.Witnesses[0].SubjectPinIDs) != 8 {
		t.Fatalf("live settings request = %#v", liveSettingsRequest)
	}
	for _, request := range []*EvidenceRequest{orderRequest, cancellationRequest} {
		if len(request.Witnesses[0].TargetIDs) != 2 || len(request.Witnesses[0].SubjectPinIDs) != 2 || !strings.Contains(strings.Join(request.Commands[0].Args, " "), "-closure-trace-type="+request.Witnesses[0].WitnessType) {
			t.Fatalf("typed request %s = %#v", request.ID, request)
		}
	}
}

func encodedRecords(t *testing.T, records []Record) string {
	t.Helper()
	var output strings.Builder
	encoder := json.NewEncoder(&output)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return output.String()
}

// correspondenceBlockedByKnownGaps reports whether listed correspondence gaps
// block the denominator, which refuses any correspondence finding.
func correspondenceBlockedByKnownGaps(t *testing.T, root string, err error) bool {
	t.Helper()
	blocked, problem := knowngaps.Blocked(root, "correspondence", err, func(findings int) string {
		return fmt.Sprintf("correspondence denominator has %d findings", findings)
	})
	if problem != nil {
		t.Fatal(problem)
	}
	if blocked {
		t.Log("listed correspondence gaps block the correspondence denominator")
	}
	return blocked
}
