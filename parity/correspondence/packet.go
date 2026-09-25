package correspondence

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const AlignmentReviewerRole = "alignment-reviewer"

type AlignmentWorkPacket struct {
	ID        string                      `json:"id"`
	Role      string                      `json:"role"`
	RuleID    string                      `json:"ruleId"`
	Source    SourceIdentity              `json:"source"`
	Target    SourceIdentity              `json:"target"`
	Questions []AlignmentQuestion         `json:"questions"`
	Functions []FunctionAlignmentQuestion `json:"functions"`
}

type AlignmentQuestion struct {
	ID                    string             `json:"id"`
	Source                DataItem           `json:"source"`
	Target                DataItem           `json:"target"`
	SourceDispatch        DispatchCase       `json:"sourceDispatch"`
	TargetDispatch        DispatchCase       `json:"targetDispatch"`
	SourceRuntimeCallback ProductionCallback `json:"sourceRuntimeCallback"`
	TargetRuntimeCallback ProductionCallback `json:"targetRuntimeCallback"`
	RequiredAnalyses      []string           `json:"requiredAnalyses"`
}

type FunctionAlignmentQuestion struct {
	ID               string   `json:"id"`
	Source           Function `json:"source"`
	Target           Function `json:"target"`
	RequiredAnalyses []string `json:"requiredAnalyses"`
}

type AlignmentReviewBundle struct {
	ID       string                   `json:"id"`
	Role     string                   `json:"role"`
	PacketID string                   `json:"packetId"`
	Findings []AlignmentReviewFinding `json:"findings"`
}

type AlignmentReviewFinding struct {
	QuestionID string              `json:"questionId"`
	Proposal   string              `json:"proposal"`
	Rationale  string              `json:"rationale"`
	Citations  []AlignmentCitation `json:"citations"`
}

type AlignmentCitation struct {
	Side       string `json:"side"`
	Path       string `json:"path"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	SourceHash string `json:"sourceHash"`
}

func DecodeAlignmentWorkPacket(reader io.Reader) (*AlignmentWorkPacket, error) {
	var packet AlignmentWorkPacket
	if err := decodeAgentJSON(reader, &packet, "alignment work packet"); err != nil {
		return nil, err
	}
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	return &packet, nil
}

func DecodeAlignmentReviewBundle(reader io.Reader, packet *AlignmentWorkPacket) (*AlignmentReviewBundle, error) {
	var bundle AlignmentReviewBundle
	if err := decodeAgentJSON(reader, &bundle, "alignment review bundle"); err != nil {
		return nil, err
	}
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func BuildSettingsAlignmentPacket(source, target *Inventory, report *Report) (*AlignmentWorkPacket, error) {
	if source == nil || target == nil || report == nil {
		return nil, fmt.Errorf("source, target, and correspondence report are required")
	}
	if len(report.Findings) != 0 {
		return nil, fmt.Errorf("cannot build alignment packet with %d correspondence findings", len(report.Findings))
	}
	sourceTables := indexTables(source.Tables)
	targetTables := indexTables(target.Tables)
	sourceTable := sourceTables["table:settings-selector"]
	targetTable := targetTables["table:settings-selector"]
	if sourceTable == nil || targetTable == nil {
		return nil, fmt.Errorf("settings selector tables are required")
	}
	targetItems := indexItems(targetTable.Items)
	sourceDispatch := indexDispatch(sourceTable.Callbacks)
	targetDispatch := indexDispatch(targetTable.Callbacks)
	sourceRuntime := indexProductionCallbacks(sourceTable.ProductionCallbacks)
	targetRuntime := indexProductionCallbacks(targetTable.ProductionCallbacks)
	questions := make([]AlignmentQuestion, 0, len(sourceTable.Items))
	for _, sourceItem := range sourceTable.Items {
		targetItem, itemOK := targetItems[sourceItem.ID]
		sourceCase, sourceCaseOK := sourceDispatch[sourceItem.ID]
		targetCase, targetCaseOK := targetDispatch[sourceItem.ID]
		sourceCallback, sourceCallbackOK := sourceRuntime[sourceItem.ID]
		targetCallback, targetCallbackOK := targetRuntime[sourceItem.ID]
		if !itemOK || !sourceCaseOK || !targetCaseOK || !sourceCallbackOK || !targetCallbackOK {
			return nil, fmt.Errorf("setting %s lacks complete source/target facts", sourceItem.ID)
		}
		analyses := []string{"current-state", "value-domain", "dispatch", "persistence", "runtime-effects"}
		if sourceItem.Gate != "" || targetItem.Gate != "" {
			analyses = append(analyses, "capability-gate")
		}
		if sourceItem.SubmenuExpression != "" || targetItem.SubmenuExpression != "" {
			analyses = append(analyses, "submenu-dispatch")
		}
		slices.Sort(analyses)
		questions = append(questions, AlignmentQuestion{
			ID: "alignment:settings:" + sourceItem.ID, Source: sourceItem, Target: targetItem,
			SourceDispatch: sourceCase, TargetDispatch: targetCase,
			SourceRuntimeCallback: sourceCallback, TargetRuntimeCallback: targetCallback, RequiredAnalyses: analyses,
		})
	}
	targetFunctions := indexFunctions(target.Functions)
	functionQuestions := make([]FunctionAlignmentQuestion, 0, len(source.Functions))
	for _, sourceFunction := range source.Functions {
		targetFunction, ok := targetFunctions[sourceFunction.Name]
		if !ok {
			return nil, fmt.Errorf("compaction function %s lacks a target candidate", sourceFunction.Name)
		}
		requiredAnalyses, err := functionAnalyses(sourceFunction)
		if err != nil {
			return nil, err
		}
		functionQuestions = append(functionQuestions, FunctionAlignmentQuestion{
			ID: "alignment:" + sourceFunction.Kind + ":" + sourceFunction.Name, Source: sourceFunction, Target: targetFunction,
			RequiredAnalyses: requiredAnalyses,
		})
	}
	packet := &AlignmentWorkPacket{
		Role: AlignmentReviewerRole, RuleID: report.RuleID, Source: report.Source, Target: report.Target, Questions: questions, Functions: functionQuestions,
	}
	packetID, err := alignmentPacketID(packet)
	if err != nil {
		return nil, err
	}
	packet.ID = packetID
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	return packet, nil
}

func (packet *AlignmentWorkPacket) Validate() error {
	if packet == nil || packet.Role != AlignmentReviewerRole || packet.RuleID == "" || len(packet.Questions) == 0 || len(packet.Functions) == 0 {
		return fmt.Errorf("alignment packet has incomplete identity")
	}
	questionIDs := make(map[string]struct{}, len(packet.Questions)+len(packet.Functions))
	for _, question := range packet.Questions {
		if question.ID == "" || question.Source.ID == "" || question.Source.ID != question.Target.ID || question.Source.ID != question.SourceDispatch.ID || question.Source.ID != question.TargetDispatch.ID || question.Source.ID != question.SourceRuntimeCallback.ID || question.Source.ID != question.TargetRuntimeCallback.ID {
			return fmt.Errorf("alignment packet question %q has inconsistent setting identity", question.ID)
		}
		if _, exists := questionIDs[question.ID]; exists {
			return fmt.Errorf("alignment packet has duplicate question %s", question.ID)
		}
		questionIDs[question.ID] = struct{}{}
		if !sortedUniqueStrings(question.RequiredAnalyses) {
			return fmt.Errorf("alignment packet question %s analyses are not sorted and unique", question.ID)
		}
		if err := validatePacketProductionCallback(question.SourceRuntimeCallback); err != nil {
			return fmt.Errorf("alignment packet question %s source callback: %w", question.ID, err)
		}
		if err := validatePacketProductionCallback(question.TargetRuntimeCallback); err != nil {
			return fmt.Errorf("alignment packet question %s target callback: %w", question.ID, err)
		}
	}
	for _, question := range packet.Functions {
		if question.ID == "" || question.Source.Name == "" || question.Source.Name != question.Target.Name || question.Source.Kind != question.Target.Kind || !sortedUniqueStrings(question.RequiredAnalyses) {
			return fmt.Errorf("alignment packet function question %q is incomplete", question.ID)
		}
		if _, exists := questionIDs[question.ID]; exists {
			return fmt.Errorf("alignment packet has duplicate question %s", question.ID)
		}
		questionIDs[question.ID] = struct{}{}
	}
	wantID, err := alignmentPacketID(packet)
	if err != nil {
		return err
	}
	if packet.ID != wantID {
		return fmt.Errorf("alignment packet ID does not match its content")
	}
	return nil
}

func validatePacketProductionCallback(callback ProductionCallback) error {
	if callback.ID == "" || callback.Handler == "" || len(callback.Segments) == 0 {
		return fmt.Errorf("production callback is incomplete")
	}
	for index, segment := range callback.Segments {
		if segment.Role == "" || segment.Path == "" || segment.StartLine < 1 || segment.EndLine < segment.StartLine || !validHash(segment.SourceHash) || segment.Reads == nil || segment.Writes == nil || segment.Calls == nil || segment.Transitions == nil || !sortedUniqueStrings(segment.Reads) || !sortedUniqueStrings(segment.Writes) {
			return fmt.Errorf("segment %d is incomplete", index)
		}
		if err := validateCallsTransitions(segment.Calls, segment.Transitions, segment.StartLine, segment.EndLine); err != nil {
			return fmt.Errorf("segment %d: %w", index, err)
		}
	}
	return nil
}

func functionAnalyses(function Function) ([]string, error) {
	if function.Kind == "compaction" {
		return []string{"argument-flow", "call-order", "cancellation", "error-state"}, nil
	}
	if function.Kind == "settings-orchestration" {
		return []string{"callback-effects", "production-reachability", "selector-inputs", "setter-dispatch"}, nil
	}
	switch function.Name {
	case "mergeSettings", "applyOverrides":
		return []string{"array-replacement", "nested-merge", "omitted-value", "precedence"}, nil
	case "persistScopedSettings", "saveGlobal", "saveProject":
		return []string{"error-surfacing", "external-edit-preservation", "field-granularity", "lock-serialization"}, nil
	case "reload":
		return []string{"error-retention", "load-order", "write-drain"}, nil
	case "setProjectTrusted":
		return []string{"error-retention", "project-state-reset", "trust-boundary"}, nil
	default:
		return nil, fmt.Errorf("settings manager function %s has no analysis contract", function.Name)
	}
}

type runtimeEffectConcept struct {
	sourceAny []string
	targetAny []string
}

var settingRuntimeEffectConcepts = map[string][]runtimeEffectConcept{
	"autocompact": {
		{sourceAny: []string{"this.session.setAutoCompactionEnabled"}, targetAny: []string{"sc.SettingsManager.UpdateGlobal"}},
		{sourceAny: []string{"this.footer.setAutoCompactEnabled"}, targetAny: []string{"m.statusLine.SetAutoCompactEnabled"}},
	},
	"show-images": {
		{sourceAny: []string{"child.setShowImages"}, targetAny: []string{"comp.SetShowImages"}},
	},
	"image-width-cells": {
		{sourceAny: []string{"child.setImageWidthCells"}, targetAny: []string{"comp.SetImageWidthCells"}},
	},
	"skill-commands": {
		{sourceAny: []string{"this.setupAutocompleteProvider"}, targetAny: []string{"m.editor.SetAutocomplete"}},
	},
	"show-hardware-cursor": {
		{sourceAny: []string{"this.ui.setShowHardwareCursor"}, targetAny: []string{"m.tuiInst.SetShowHardwareCursor"}},
	},
	"editor-padding": {
		{sourceAny: []string{"this.defaultEditor.setPaddingX", "this.editor.setPaddingX"}, targetAny: []string{"m.editor.SetPaddingX"}},
	},
	"output-padding": {
		{sourceAny: []string{"this.outputPad"}, targetAny: []string{"m.outputPad"}},
		{sourceAny: []string{"this.rebuildChatFromMessages", "child.setOutputPad"}, targetAny: []string{"m.rebuildChatFromSession", "comp.SetOutputPad"}},
	},
	"autocomplete-max-visible": {
		{sourceAny: []string{"this.defaultEditor.setAutocompleteMaxVisible", "this.editor.setAutocompleteMaxVisible"}, targetAny: []string{"m.editor.SetAutocompleteMaxVisible"}},
	},
	"clear-on-shrink": {
		{sourceAny: []string{"this.ui.setClearOnShrink"}, targetAny: []string{"m.tuiInst.SetClearOnShrink"}},
		{sourceAny: []string{"this.statusContainer.clear"}, targetAny: []string{"m.statusContainer.Clear"}},
	},
	"steering-mode": {
		{sourceAny: []string{"this.session.setSteeringMode"}, targetAny: []string{"m.agent.SetSteeringMode"}},
	},
	"follow-up-mode": {
		{sourceAny: []string{"this.session.setFollowUpMode"}, targetAny: []string{"m.agent.SetFollowUpMode"}},
	},
	"transport": {
		{sourceAny: []string{"this.session.agent.transport"}, targetAny: []string{"m.agent.SetTransport"}},
	},
	"http-idle-timeout": {
		{sourceAny: []string{"configureHttpDispatcher"}, targetAny: []string{"ai.ConfigureHTTPDispatcher"}},
		{sourceAny: []string{"this.showStatus"}, targetAny: []string{"showStatusOrAppend"}},
	},
	"cache-warming-mode": {
		{sourceAny: []string{"this.session.setCacheWarmingMode"}, targetAny: []string{"m.opts.SessionHandle.SetCacheWarmingMode"}},
		{sourceAny: []string{"this.showStatus"}, targetAny: []string{"showStatusOrAppend"}},
	},
	"hide-thinking": {
		{sourceAny: []string{"this.hideThinkingBlock", "child.setHideThinkingBlock"}, targetAny: []string{"m.toggleThinkingVisibility"}},
	},
	"mermaid-rendering": {
		{sourceAny: []string{"this.chatContainer.invalidate"}, targetAny: []string{"m.chatContainer.Invalidate"}},
		{sourceAny: []string{"this.ui.requestRender"}, targetAny: []string{"m.tuiInst.RequestRender"}},
	},
	"cache-miss-notices": {
		{sourceAny: []string{"this.rebuildChatFromMessages"}, targetAny: []string{"m.rebuildChatFromSession"}},
	},
	"model-thinking": {
		{sourceAny: []string{"this.session.setThinkingLevel"}, targetAny: []string{"m.agent.SetThinkingLevel"}},
		{sourceAny: []string{"this.footer.invalidate"}, targetAny: []string{"m.statusLine.SetThinkingLevel"}},
		{sourceAny: []string{"this.updateEditorBorderColor"}, targetAny: []string{"m.editor.Invalidate"}},
	},
	"thinking": {
		{sourceAny: []string{"this.session.setThinkingLevel"}, targetAny: []string{"m.agent.SetThinkingLevel"}},
		{sourceAny: []string{"this.footer.invalidate"}, targetAny: []string{"m.statusLine.SetThinkingLevel"}},
		{sourceAny: []string{"this.updateEditorBorderColor"}, targetAny: []string{"m.editor.Invalidate"}},
	},
	"tui-mode": {
		{sourceAny: []string{"this.switchTuiMode"}, targetAny: []string{"m.switchTuiMode"}},
		{sourceAny: []string{"this.showStatus"}, targetAny: []string{"showStatusOrAppend", "m.showStatus"}},
	},
	"fullscreen-scrollbar": {
		{sourceAny: []string{"this.applyFullscreenScrollbarSetting"}, targetAny: []string{"m.applyFullscreenScrollbarSetting"}},
	},
	"fullscreen-copy-on-select": {
		{sourceAny: []string{"this.renderer.setCopyOnSelect"}, targetAny: []string{"m.altScreen.SetCopyOnSelect"}},
	},
	"theme": {
		{sourceAny: []string{"this.themeController.setThemeSetting"}, targetAny: []string{"tui.SetThemeSetting"}},
		{sourceAny: []string{"this.themeController.setThemeSetting"}, targetAny: []string{"m.tuiInst.ForceFullRender"}},
	},
}

func RuntimeEffectCandidates(packet *AlignmentWorkPacket) ([]string, error) {
	if packet == nil {
		return nil, fmt.Errorf("alignment packet is required")
	}
	if err := packet.Validate(); err != nil {
		return nil, err
	}
	var candidates []string
	for _, question := range packet.Questions {
		sourceEffects := runtimeEffects(question.SourceRuntimeCallback.Segments, true)
		if len(sourceEffects) == 0 {
			continue
		}
		settingID := strings.TrimPrefix(question.ID, "alignment:settings:")
		concepts, ok := settingRuntimeEffectConcepts[settingID]
		if !ok {
			return nil, fmt.Errorf("setting %s has live effects but no normalization contract", settingID)
		}
		targetEffects := runtimeEffects(question.TargetRuntimeCallback.Segments, false)
		for _, concept := range concepts {
			if !containsAnyEffect(sourceEffects, concept.sourceAny) {
				return nil, fmt.Errorf("setting %s source runtime effect contract changed: want one of %v", settingID, concept.sourceAny)
			}
			if !containsAnyEffect(targetEffects, concept.targetAny) {
				candidates = append(candidates, question.ID)
				break
			}
		}
	}
	slices.Sort(candidates)
	return candidates, nil
}

func runtimeEffects(segments []EffectSegment, source bool) map[string]struct{} {
	effects := make(map[string]struct{})
	for _, segment := range segments {
		for _, write := range segment.Writes {
			effects[write] = struct{}{}
		}
		for _, call := range segment.Calls {
			if source && strings.HasPrefix(call.Callee, "this.settingsManager.") {
				continue
			}
			effects[call.Callee] = struct{}{}
		}
	}
	return effects
}

func containsAnyEffect(effects map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, ok := effects[candidate]; ok {
			return true
		}
	}
	return false
}

func alignmentPacketID(packet *AlignmentWorkPacket) (string, error) {
	clone := *packet
	clone.ID = ""
	encoded, err := json.Marshal(&clone)
	if err != nil {
		return "", fmt.Errorf("encode alignment packet: %w", err)
	}
	return "packet:" + strings.TrimPrefix(hashString(string(encoded)), "sha256:"), nil
}

func (bundle *AlignmentReviewBundle) Validate(packet *AlignmentWorkPacket) error {
	if bundle == nil || packet == nil || bundle.Role != AlignmentReviewerRole || bundle.PacketID != packet.ID || len(bundle.Findings) != len(packet.Questions)+len(packet.Functions) {
		return fmt.Errorf("alignment review bundle has incomplete identity or findings")
	}
	questions := make(map[string][]AlignmentCitation, len(packet.Questions)+len(packet.Functions))
	for _, question := range packet.Questions {
		questions[question.ID] = questionCitations(question)
	}
	for _, question := range packet.Functions {
		questions[question.ID] = functionQuestionCitations(question.Source, question.Target)
	}
	seen := make(map[string]struct{}, len(bundle.Findings))
	for _, finding := range bundle.Findings {
		citations, exists := questions[finding.QuestionID]
		if !exists || finding.Rationale == "" || !oneOfString(finding.Proposal, "aligned", "mismatch", "ambiguous", "blocked") || len(finding.Citations) < 2 {
			return fmt.Errorf("alignment review finding %q is incomplete", finding.QuestionID)
		}
		if _, duplicate := seen[finding.QuestionID]; duplicate {
			return fmt.Errorf("alignment review bundle duplicates %s", finding.QuestionID)
		}
		seen[finding.QuestionID] = struct{}{}
		sides := make(map[string]struct{}, 2)
		for _, citation := range finding.Citations {
			if !slices.Contains(citations, citation) {
				return fmt.Errorf("alignment review finding %s has an unbound citation", finding.QuestionID)
			}
			sides[citation.Side] = struct{}{}
		}
		if len(sides) != 2 {
			return fmt.Errorf("alignment review finding %s must cite source and target", finding.QuestionID)
		}
	}
	wantID, err := alignmentBundleID(bundle)
	if err != nil {
		return err
	}
	if bundle.ID != wantID {
		return fmt.Errorf("alignment review bundle ID does not match its content")
	}
	return nil
}

func BindAlignmentReviewBundle(packet *AlignmentWorkPacket, findings []AlignmentReviewFinding) (*AlignmentReviewBundle, error) {
	if packet == nil {
		return nil, fmt.Errorf("alignment packet is required")
	}
	bundle := &AlignmentReviewBundle{Role: AlignmentReviewerRole, PacketID: packet.ID, Findings: slices.Clone(findings)}
	id, err := alignmentBundleID(bundle)
	if err != nil {
		return nil, err
	}
	bundle.ID = id
	if err := bundle.Validate(packet); err != nil {
		return nil, err
	}
	return bundle, nil
}

func alignmentBundleID(bundle *AlignmentReviewBundle) (string, error) {
	clone := *bundle
	clone.ID = ""
	encoded, err := json.Marshal(&clone)
	if err != nil {
		return "", fmt.Errorf("encode alignment review bundle: %w", err)
	}
	return "bundle:" + strings.TrimPrefix(hashString(string(encoded)), "sha256:"), nil
}

func questionCitations(question AlignmentQuestion) []AlignmentCitation {
	citations := []AlignmentCitation{
		{Side: "source", Path: question.Source.Path, StartLine: question.Source.StartLine, EndLine: question.Source.EndLine, SourceHash: question.Source.SourceHash},
		{Side: "target", Path: question.Target.Path, StartLine: question.Target.StartLine, EndLine: question.Target.EndLine, SourceHash: question.Target.SourceHash},
	}
	for _, segment := range question.SourceRuntimeCallback.Segments {
		citations = append(citations, AlignmentCitation{Side: "source", Path: segment.Path, StartLine: segment.StartLine, EndLine: segment.EndLine, SourceHash: segment.SourceHash})
	}
	for _, segment := range question.TargetRuntimeCallback.Segments {
		citations = append(citations, AlignmentCitation{Side: "target", Path: segment.Path, StartLine: segment.StartLine, EndLine: segment.EndLine, SourceHash: segment.SourceHash})
	}
	return citations
}

func functionQuestionCitations(source, target Function) []AlignmentCitation {
	return []AlignmentCitation{
		{Side: "source", Path: source.Path, StartLine: source.StartLine, EndLine: source.EndLine, SourceHash: source.SourceHash},
		{Side: "target", Path: target.Path, StartLine: target.StartLine, EndLine: target.EndLine, SourceHash: target.SourceHash},
	}
}

func oneOfString(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

func indexDispatch(cases []DispatchCase) map[string]DispatchCase {
	indexed := make(map[string]DispatchCase, len(cases))
	for _, dispatch := range cases {
		indexed[dispatch.ID] = dispatch
	}
	return indexed
}

func indexProductionCallbacks(callbacks []ProductionCallback) map[string]ProductionCallback {
	indexed := make(map[string]ProductionCallback, len(callbacks))
	for _, callback := range callbacks {
		indexed[callback.ID] = callback
	}
	return indexed
}

func indexFunctions(functions []Function) map[string]Function {
	indexed := make(map[string]Function, len(functions))
	for _, function := range functions {
		indexed[function.Name] = function
	}
	return indexed
}
