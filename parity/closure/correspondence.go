package closure

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

// AddCorrespondenceDenominator compiles the normalized Pi/Pig correspondence
// inventories into deterministic facts and open closure obligations. Direct
// setting identities may derive mappings; function candidates remain hypotheses.
func AddCorrespondenceDenominator(ctx context.Context, root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("correspondence denominator requires a snapshot")
	}
	if snapshot.UpstreamCommit != coding.UpstreamCommit {
		return nil, fmt.Errorf("correspondence snapshot upstream commit %s does not match %s", snapshot.UpstreamCommit, coding.UpstreamCommit)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("find node for correspondence denominator: %w", err)
	}
	source, err := correspondence.ExtractTypeScript(
		ctx,
		nodePath,
		filepath.Join(root, "parity", "interface-extractor", "src", "extract-correspondence.mjs"),
		filepath.Join(root, ".upstream", "current"),
		coding.UpstreamVersion,
	)
	if err != nil {
		return nil, err
	}
	target, err := correspondence.ExtractGo(ctx, root, snapshot.TargetCommit)
	if err != nil {
		return nil, err
	}
	rules := correspondence.CompactionSettingsRules()
	report, err := correspondence.Compare(source, target, rules)
	if err != nil {
		return nil, err
	}
	if len(report.Findings) != 0 {
		return nil, fmt.Errorf("correspondence denominator has %d findings", len(report.Findings))
	}
	packet, err := correspondence.BuildSettingsAlignmentPacket(source, target, report)
	if err != nil {
		return nil, err
	}
	return addCorrespondencePacket(snapshot, records, rules, report, packet)
}

func addCorrespondencePacket(snapshot *Snapshot, records []Record, rules correspondence.Rules, report *correspondence.Report, packet *correspondence.AlignmentWorkPacket) ([]Record, error) {
	result := slices.Clone(records)
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.RecordID()] = struct{}{}
	}
	appendRecord := func(record Record) {
		if _, found := existing[record.RecordID()]; found {
			return
		}
		result = append(result, record)
		existing[record.RecordID()] = struct{}{}
	}

	ruleValue, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("encode correspondence rules: %w", err)
	}
	mappingRuleID := "rule:correspondence:" + report.RuleID
	appendRecord(&Rule{Kind: KindRule, ID: mappingRuleID, Name: report.RuleID, DefinitionHash: HashBytes(ruleValue)})
	for _, question := range packet.Questions {
		for _, analysis := range question.RequiredAnalyses {
			if err := addCorrespondenceFacetRule(appendRecord, analysis); err != nil {
				return nil, err
			}
		}
	}
	for _, question := range packet.Functions {
		for _, analysis := range question.RequiredAnalyses {
			if err := addCorrespondenceFacetRule(appendRecord, analysis); err != nil {
				return nil, err
			}
		}
	}

	orchestrationQuestion, err := functionByName(packet.Functions, "settingsOrchestration")
	if err != nil {
		return nil, err
	}
	orchestrationPin := correspondenceFunctionPin(snapshot, "target", orchestrationQuestion.Target)
	appendRecord(orchestrationPin)
	mappingFacts := make(map[string]correspondence.MappingFact, len(report.Mappings))
	for _, fact := range report.Mappings {
		mappingFacts[fact.SourceID] = fact
	}
	for _, question := range packet.Questions {
		behaviorID := "behavior:correspondence:setting:" + question.Source.ID
		targetID := "target:correspondence:setting:" + question.Target.ID
		sourcePin := correspondenceDataPin(snapshot, "source", question.Source)
		targetPin := correspondenceDataPin(snapshot, "target", question.Target)
		appendRecord(sourcePin)
		appendRecord(targetPin)
		sourcePinIDs := []string{sourcePin.ID}
		for index, segment := range question.SourceRuntimeCallback.Segments {
			pin := correspondenceSegmentPin(snapshot, "source", question.SourceRuntimeCallback.Handler, index, segment)
			appendRecord(pin)
			sourcePinIDs = append(sourcePinIDs, pin.ID)
		}
		targetPinIDs := []string{targetPin.ID}
		for index, segment := range question.TargetRuntimeCallback.Segments {
			pin := correspondenceSegmentPin(snapshot, "target", question.TargetRuntimeCallback.Handler, index, segment)
			appendRecord(pin)
			targetPinIDs = append(targetPinIDs, pin.ID)
		}
		slices.Sort(sourcePinIDs)
		slices.Sort(targetPinIDs)
		mappingFact, found := mappingFacts["table:settings-selector#"+question.Source.ID]
		if !found {
			return nil, fmt.Errorf("setting %s lacks direct correspondence fact", question.Source.ID)
		}
		factID := "fact:correspondence:setting:" + question.Source.ID
		factValue, err := json.Marshal(map[string]string{
			"ruleId": mappingRuleID, "sourceId": mappingFact.SourceID, "targetId": mappingFact.TargetID,
			"sourceHash": mappingFact.SourceHash, "targetHash": mappingFact.TargetHash,
		})
		if err != nil {
			return nil, err
		}
		pinIDs := []string{sourcePin.ID, targetPin.ID}
		slices.Sort(pinIDs)
		appendRecord(&Fact{
			Kind: KindFact, ID: factID, SnapshotID: snapshot.ID, FactType: "correspondence:direct",
			SubjectID: behaviorID, Resolution: "resolved", PinIDs: pinIDs, Value: factValue,
		})
		lineageFactID := "fact:correspondence:setting-lineage:" + question.Source.ID
		lineageValue, err := json.Marshal(question)
		if err != nil {
			return nil, err
		}
		lineagePinIDs := append(slices.Clone(sourcePinIDs), targetPinIDs...)
		slices.Sort(lineagePinIDs)
		appendRecord(&Fact{
			Kind: KindFact, ID: lineageFactID, SnapshotID: snapshot.ID, FactType: "correspondence:normalized-setting",
			SubjectID: behaviorID, Resolution: "resolved", PinIDs: lineagePinIDs, Value: lineageValue,
		})
		behaviorFactIDs := []string{factID, lineageFactID}
		slices.Sort(behaviorFactIDs)
		appendRecord(&Behavior{
			Kind: KindBehavior, ID: behaviorID, Name: question.Source.Label,
			OriginPinIDs: sourcePinIDs, FactIDs: behaviorFactIDs, Profile: "application",
		})
		appendRecord(&Target{
			Kind: KindTarget, ID: targetID, SnapshotID: snapshot.ID, PinIDs: targetPinIDs,
			FactIDs: behaviorFactIDs, Language: "go", Symbol: question.Target.ID,
		})
		appendRecord(&Mapping{
			Kind: KindMapping, ID: "mapping:correspondence:setting:" + question.Source.ID,
			BehaviorID: behaviorID, TargetIDs: []string{targetID}, Status: "derived", RuleID: mappingRuleID, FactIDs: []string{factID},
		})
		appendRecord(&Reachability{
			Kind: KindReachability, ID: "reachability:correspondence:setting:" + question.Source.ID,
			BehaviorID: behaviorID, TargetID: targetID, Class: "prod-reachable", Method: "compiled-settings-orchestration", RootPinIDs: []string{orchestrationPin.ID},
		})
		if err := addCorrespondenceObligations(appendRecord, behaviorID, sourcePin.ID, question.RequiredAnalyses); err != nil {
			return nil, err
		}
	}
	for _, question := range packet.Functions {
		behaviorID := "behavior:correspondence:function:" + question.Source.Name
		targetID := "target:correspondence:function:" + question.Target.Name
		sourcePin := correspondenceFunctionPin(snapshot, "source", question.Source)
		targetPin := correspondenceFunctionPin(snapshot, "target", question.Target)
		appendRecord(sourcePin)
		appendRecord(targetPin)
		sourcePinIDs := []string{sourcePin.ID}
		for index, caller := range question.Source.Callers {
			pin := correspondenceCallerPin(snapshot, "source", question.Source.Name, index, caller)
			appendRecord(pin)
			sourcePinIDs = append(sourcePinIDs, pin.ID)
		}
		targetPinIDs := []string{targetPin.ID}
		var targetCallerPinIDs []string
		for index, caller := range question.Target.Callers {
			pin := correspondenceCallerPin(snapshot, "target", question.Target.Name, index, caller)
			appendRecord(pin)
			targetPinIDs = append(targetPinIDs, pin.ID)
			targetCallerPinIDs = append(targetCallerPinIDs, pin.ID)
		}
		slices.Sort(sourcePinIDs)
		slices.Sort(targetPinIDs)
		factID := "fact:correspondence:function:" + question.Source.Name
		factValue, err := json.Marshal(struct {
			Source correspondence.Function `json:"source"`
			Target correspondence.Function `json:"target"`
		}{Source: question.Source, Target: question.Target})
		if err != nil {
			return nil, err
		}
		pinIDs := append(slices.Clone(sourcePinIDs), targetPinIDs...)
		slices.Sort(pinIDs)
		appendRecord(&Fact{
			Kind: KindFact, ID: factID, SnapshotID: snapshot.ID, FactType: "correspondence:normalized-function",
			SubjectID: behaviorID, Resolution: "resolved", PinIDs: pinIDs, Value: factValue,
		})
		behaviorFactIDs := []string{factID}
		mappingStatus := "hypothesis"
		mappingRuleID := ""
		var mappingFactIDs []string
		if mappingFact, found := mappingFacts[question.Source.ID]; found {
			directFactID := "fact:correspondence:function-direct:" + question.Source.Name
			directValue, marshalErr := json.Marshal(map[string]string{
				"ruleId": "rule:correspondence:" + report.RuleID, "sourceId": mappingFact.SourceID, "targetId": mappingFact.TargetID,
				"sourceHash": mappingFact.SourceHash, "targetHash": mappingFact.TargetHash,
			})
			if marshalErr != nil {
				return nil, marshalErr
			}
			appendRecord(&Fact{
				Kind: KindFact, ID: directFactID, SnapshotID: snapshot.ID, FactType: "correspondence:direct",
				SubjectID: behaviorID, Resolution: "resolved", PinIDs: pinIDs, Value: directValue,
			})
			behaviorFactIDs = append(behaviorFactIDs, directFactID)
			mappingStatus = "derived"
			mappingRuleID = "rule:correspondence:" + report.RuleID
			mappingFactIDs = []string{directFactID}
		}
		slices.Sort(behaviorFactIDs)
		appendRecord(&Behavior{
			Kind: KindBehavior, ID: behaviorID, Name: question.Source.Name,
			OriginPinIDs: sourcePinIDs, FactIDs: behaviorFactIDs, Profile: "application",
		})
		appendRecord(&Target{
			Kind: KindTarget, ID: targetID, SnapshotID: snapshot.ID, PinIDs: targetPinIDs,
			FactIDs: behaviorFactIDs, Language: "go", Symbol: question.Target.Name,
		})
		appendRecord(&Mapping{
			Kind: KindMapping, ID: "mapping:correspondence:function:" + question.Source.Name,
			BehaviorID: behaviorID, TargetIDs: []string{targetID}, Status: mappingStatus, RuleID: mappingRuleID, FactIDs: mappingFactIDs,
		})
		reachability := &Reachability{
			Kind: KindReachability, ID: "reachability:correspondence:function:" + question.Source.Name,
			BehaviorID: behaviorID, TargetID: targetID, Class: "unknown", Method: "compiled-reviewed-callsite", RootPinIDs: []string{},
			Uncertainty: "no reviewed production callsite was compiled",
		}
		if len(question.Target.Callers) > 0 {
			reachability.Class = "prod-reachable"
			reachability.RootPinIDs = slices.Clone(targetCallerPinIDs)
			reachability.Uncertainty = ""
		}
		appendRecord(reachability)
		if err := addCorrespondenceObligations(appendRecord, behaviorID, sourcePin.ID, question.RequiredAnalyses); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func addCorrespondenceFacetRule(appendRecord func(Record), analysis string) error {
	facet, err := correspondenceAnalysisFacet(analysis)
	if err != nil {
		return err
	}
	appendRecord(&Facet{Kind: KindFacet, ID: "facet:" + facet, Name: facet})
	appendRecord(&Rule{
		Kind: KindRule, ID: "rule:correspondence-analysis:" + analysis,
		Name:           "correspondence analysis " + analysis,
		DefinitionHash: HashBytes([]byte("correspondence-analysis:" + analysis + ":mapping+reachability+assertion+witness")),
	})
	return nil
}

func addCorrespondenceObligations(appendRecord func(Record), behaviorID, sourcePinID string, analyses []string) error {
	for _, analysis := range analyses {
		facet, err := correspondenceAnalysisFacet(analysis)
		if err != nil {
			return err
		}
		appendRecord(&Obligation{
			Kind: KindObligation, ID: strings.Replace(behaviorID, "behavior:", "obligation:", 1) + ":" + analysis,
			BehaviorID: behaviorID, FacetID: "facet:" + facet,
			RuleID: "rule:correspondence-analysis:" + analysis, OriginPinIDs: []string{sourcePinID},
		})
	}
	return nil
}

func correspondenceAnalysisFacet(analysis string) (string, error) {
	switch analysis {
	case "argument-flow", "array-replacement", "current-state", "nested-merge", "omitted-value", "precedence", "project-state-reset", "runtime-effects", "trust-boundary", "value-domain":
		return "state", nil
	case "callback-effects", "dispatch", "setter-dispatch", "submenu-dispatch":
		return "dispatch", nil
	case "call-order", "load-order", "write-drain":
		return "order", nil
	case "cancellation":
		return "cancel", nil
	case "error-retention", "error-state", "error-surfacing":
		return "error", nil
	case "external-edit-preservation", "field-granularity", "persistence":
		return "persistence", nil
	case "lock-serialization":
		return "concurrency", nil
	case "capability-gate", "selector-inputs":
		return "input", nil
	case "production-reachability":
		return "realization", nil
	default:
		return "", fmt.Errorf("unclassified correspondence analysis %s", analysis)
	}
}

func correspondenceDataPin(snapshot *Snapshot, side string, item correspondence.DataItem) *Pin {
	commit := snapshot.UpstreamCommit
	repository := "upstream"
	if side == "target" {
		commit = snapshot.TargetCommit
		repository = "pig"
	}
	return correspondencePin(snapshot, side, repository, commit, "table:settings-selector#"+item.ID, item.Path, item.StartLine, item.EndLine, item.SourceHash)
}

func correspondenceFunctionPin(snapshot *Snapshot, side string, function correspondence.Function) *Pin {
	commit := snapshot.UpstreamCommit
	repository := "upstream"
	if side == "target" {
		commit = snapshot.TargetCommit
		repository = "pig"
	}
	return correspondencePin(snapshot, side, repository, commit, function.ID, function.Path, function.StartLine, function.EndLine, function.SourceHash)
}

func correspondenceSegmentPin(snapshot *Snapshot, side, handler string, index int, segment correspondence.EffectSegment) *Pin {
	commit := snapshot.UpstreamCommit
	repository := "upstream"
	if side == "target" {
		commit = snapshot.TargetCommit
		repository = "pig"
	}
	semanticID := fmt.Sprintf("runtime:%s:%s:%d", handler, segment.Role, index)
	return correspondencePin(snapshot, side, repository, commit, semanticID, segment.Path, segment.StartLine, segment.EndLine, segment.SourceHash)
}

func correspondenceCallerPin(snapshot *Snapshot, side, function string, index int, caller correspondence.FunctionCaller) *Pin {
	commit := snapshot.UpstreamCommit
	repository := "upstream"
	if side == "target" {
		commit = snapshot.TargetCommit
		repository = "pig"
	}
	semanticID := fmt.Sprintf("caller:%s:%s:%d", function, caller.Symbol, index)
	return correspondencePin(snapshot, side, repository, commit, semanticID, caller.Path, caller.StartLine, caller.StartLine, caller.SourceHash)
}

func correspondencePin(snapshot *Snapshot, side, repository, commit, semanticID, path string, startLine, endLine int, quoteHash string) *Pin {
	identity := strings.Join([]string{side, repository, commit, semanticID, path, fmt.Sprint(startLine), fmt.Sprint(endLine), quoteHash}, "\x00")
	return &Pin{
		Kind: KindPin, ID: "pin:correspondence:" + strings.TrimPrefix(HashBytes([]byte(identity)), "sha256:"),
		SnapshotID: snapshot.ID, Repository: repository, Commit: commit, Path: path, SemanticID: semanticID,
		StartLine: startLine, EndLine: endLine, QuoteHash: quoteHash, BodyHash: quoteHash,
	}
}

func functionByName(questions []correspondence.FunctionAlignmentQuestion, name string) (correspondence.FunctionAlignmentQuestion, error) {
	for _, question := range questions {
		if question.Source.Name == name {
			return question, nil
		}
	}
	return correspondence.FunctionAlignmentQuestion{}, fmt.Errorf("missing correspondence function %s", name)
}
