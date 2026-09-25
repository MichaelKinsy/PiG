package correspondence

import (
	"fmt"
	"slices"
	"strings"
)

type Rules struct {
	ID                  string               `json:"id"`
	TableTargets        map[string]string    `json:"tableTargets"`
	ConstantTargets     map[string]string    `json:"constantTargets"`
	FunctionTargets     map[string]string    `json:"functionTargets"`
	CalleeTargets       map[string]string    `json:"calleeTargets"`
	CancellableCalls    []string             `json:"cancellableCalls"`
	CallContracts       []string             `json:"callContracts"`
	TransitionContracts []TransitionContract `json:"transitionContracts"`
}

type TransitionContract struct {
	ID             string `json:"id"`
	Function       string `json:"function"`
	SourceKind     string `json:"sourceKind"`
	SourceTarget   string `json:"sourceTarget"`
	SourceContains string `json:"sourceContains"`
	TargetKind     string `json:"targetKind"`
	TargetTarget   string `json:"targetTarget"`
	TargetContains string `json:"targetContains"`
	Condition      string `json:"condition"`
}

type Report struct {
	RuleID   string         `json:"ruleId"`
	Source   SourceIdentity `json:"source"`
	Target   SourceIdentity `json:"target"`
	Mappings []MappingFact  `json:"mappings"`
	Findings []Finding      `json:"findings"`
}

type MappingFact struct {
	ID         string `json:"id"`
	SourceID   string `json:"sourceId"`
	TargetID   string `json:"targetId"`
	Kind       string `json:"kind"`
	SourceHash string `json:"sourceHash"`
	TargetHash string `json:"targetHash"`
}

type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	Detail   string `json:"detail"`
}

func CompactionSettingsRules() Rules {
	return Rules{
		ID: "pi-0.84-compaction-settings",
		TableTargets: map[string]string{
			"table:settings-selector": "table:settings-selector",
		},
		ConstantTargets: map[string]string{
			"SUMMARIZATION_PROMPT":             "SUMMARIZATION_PROMPT",
			"TURN_PREFIX_SUMMARIZATION_PROMPT": "turnPrefixSummarizationPrompt",
			"UPDATE_SUMMARIZATION_PROMPT":      "UPDATE_SUMMARIZATION_PROMPT",
			"SUMMARIZATION_SYSTEM_PROMPT":      "SummarizationSystemPrompt",
		},
		FunctionTargets: map[string]string{
			"compact": "compact", "generateTurnPrefixSummary": "generateTurnPrefixSummary",
			"mergeSettings": "mergeSettings", "setProjectTrusted": "setProjectTrusted", "reload": "reload",
			"applyOverrides": "applyOverrides", "persistScopedSettings": "persistScopedSettings",
			"saveGlobal": "saveGlobal", "saveProject": "saveProject", "settingsOrchestration": "settingsOrchestration",
		},
		CalleeTargets: map[string]string{
			"generateSummaryWithUsage": "generateSummary", "generateTurnPrefixSummary": "generateTurnPrefixSummary",
			"combineUsage": "combineUsage", "completeSummarization": "completeSummarization",
		},
		CancellableCalls: []string{"completeSummarization", "generateSummaryWithUsage", "generateTurnPrefixSummary"},
		CallContracts:    []string{"compact", "generateTurnPrefixSummary"},
		TransitionContracts: []TransitionContract{
			{ID: "split-summary", Function: "compact", SourceKind: "update", SourceTarget: "summary", SourceContains: "Turn Context (split turn)", TargetKind: "update", TargetTarget: "summary", TargetContains: "Turn Context (split turn)", Condition: "1:then"},
			{ID: "history-fallback", Function: "compact", SourceKind: "update", SourceTarget: "summary", SourceContains: "result.text", TargetKind: "update", TargetTarget: "summary", TargetContains: "generateSummary", Condition: "1:else"},
			{ID: "file-operations", Function: "compact", SourceKind: "update", SourceTarget: "summary", SourceContains: "formatFileOperations", TargetKind: "update", TargetTarget: "summary", TargetContains: "FormatFileOperations", Condition: "0:then"},
			{ID: "missing-first-kept-id", Function: "compact", SourceKind: "error", SourceContains: "First kept entry has no UUID - session may need migration", TargetKind: "return", TargetContains: "First kept entry has no UUID - session may need migration", Condition: "1:then"},
			{ID: "result", Function: "compact", SourceKind: "return", SourceContains: "firstKeptEntryId", TargetKind: "return", TargetContains: "FirstKeptEntryID", Condition: "0:then"},
		},
	}
}

func Compare(source, target *Inventory, rules Rules) (*Report, error) {
	if source == nil || target == nil {
		return nil, fmt.Errorf("source and target correspondence inventories are required")
	}
	if source.Source.Language != LanguageTypeScript || target.Source.Language != LanguageGo {
		return nil, fmt.Errorf("correspondence comparison requires TypeScript source and Go target")
	}
	if rules.ID == "" {
		return nil, fmt.Errorf("correspondence rule identity is required")
	}
	report := &Report{RuleID: rules.ID, Source: source.Source, Target: target.Source, Mappings: []MappingFact{}, Findings: []Finding{}}
	compareTables(report, source, target, rules)
	compareConstants(report, source, target, rules)
	compareFunctions(report, source, target, rules)
	compareTransitionContracts(report, source, target, rules)
	slices.SortFunc(report.Mappings, func(left, right MappingFact) int { return strings.Compare(left.ID, right.ID) })
	slices.SortFunc(report.Findings, func(left, right Finding) int { return strings.Compare(left.ID, right.ID) })
	return report, nil
}

func compareTransitionContracts(report *Report, source, target *Inventory, rules Rules) {
	sourceFunctions := indexFunctions(source.Functions)
	targetFunctions := indexFunctions(target.Functions)
	for _, contract := range rules.TransitionContracts {
		sourceFunction, sourceOK := sourceFunctions[contract.Function]
		targetFunction, targetOK := targetFunctions[contract.Function]
		if !sourceOK || !targetOK {
			continue
		}
		sourceCount := matchingTransitions(sourceFunction.Transitions, contract.SourceKind, contract.SourceTarget, contract.SourceContains, contract.Condition)
		targetCount := matchingTransitions(targetFunction.Transitions, contract.TargetKind, contract.TargetTarget, contract.TargetContains, contract.Condition)
		if sourceCount != 1 || targetCount != 1 {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:transition:" + contract.ID, Severity: "error", Kind: "function-transition",
				SourceID: sourceFunction.ID, TargetID: targetFunction.ID,
				Detail: fmt.Sprintf("transition contract %s matched Pi=%d Pig=%d, want one each", contract.ID, sourceCount, targetCount),
			})
		}
	}
}

func matchingTransitions(transitions []FunctionTransition, kind, target, contains, condition string) int {
	count := 0
	for _, transition := range transitions {
		if transition.Kind != kind || transition.Target != target || !strings.Contains(transition.Expression, contains) {
			continue
		}
		profile := conditionProfile(transition.Conditions)
		if profile == condition {
			count++
		}
	}
	return count
}

func compareFunctions(report *Report, source, target *Inventory, rules Rules) {
	sourceFunctions := indexFunctions(source.Functions)
	targetFunctions := indexFunctions(target.Functions)
	claimedTargets := make(map[string]struct{}, len(rules.FunctionTargets))
	for _, targetName := range rules.FunctionTargets {
		claimedTargets[targetName] = struct{}{}
	}
	for sourceName := range sourceFunctions {
		if _, claimed := rules.FunctionTargets[sourceName]; !claimed {
			report.Findings = append(report.Findings, Finding{ID: "finding:function:unclaimed-source:" + sourceName, Severity: "error", Kind: "unclaimed-source-function", SourceID: sourceName, Detail: "Pi function is absent from the correspondence rules"})
		}
	}
	for targetName := range targetFunctions {
		if _, claimed := claimedTargets[targetName]; !claimed {
			report.Findings = append(report.Findings, Finding{ID: "finding:function:unclaimed-target:" + targetName, Severity: "error", Kind: "unclaimed-target-function", TargetID: targetName, Detail: "Pig function lacks a Pi correspondence or additive lineage"})
		}
	}
	for sourceName, targetName := range rules.FunctionTargets {
		sourceFunction, sourceOK := sourceFunctions[sourceName]
		targetFunction, targetOK := targetFunctions[targetName]
		if !sourceOK || !targetOK {
			report.Findings = append(report.Findings, Finding{ID: "finding:function:" + sourceName, Severity: "error", Kind: "missing-function", SourceID: sourceName, TargetID: targetName, Detail: fmt.Sprintf("source present=%t target present=%t", sourceOK, targetOK)})
			continue
		}
		if !slices.Contains(rules.CallContracts, sourceName) {
			continue
		}
		report.Mappings = append(report.Mappings, MappingFact{
			ID: "correspondence:function:" + sourceName, SourceID: sourceFunction.ID, TargetID: targetFunction.ID,
			Kind: "normalized-function-contract", SourceHash: sourceFunction.SourceHash, TargetHash: targetFunction.SourceHash,
		})
		sourceSequence, sourceCalls := normalizedCalls(sourceFunction.Calls, rules.CalleeTargets, false)
		targetSequence, targetCalls := normalizedCalls(targetFunction.Calls, rules.CalleeTargets, true)
		if !slices.Equal(sourceSequence, targetSequence) {
			report.Findings = append(report.Findings, Finding{ID: "finding:function-order:" + sourceName, Severity: "error", Kind: "function-call-order", SourceID: sourceFunction.ID, TargetID: targetFunction.ID, Detail: fmt.Sprintf("Pi calls %q differ from Pig calls %q", sourceSequence, targetSequence)})
		}
		if sourceProfile, targetProfile := callConditionProfile(sourceCalls), callConditionProfile(targetCalls); !slices.Equal(sourceProfile, targetProfile) {
			report.Findings = append(report.Findings, Finding{ID: "finding:function-branches:" + sourceName, Severity: "error", Kind: "function-call-branches", SourceID: sourceFunction.ID, TargetID: targetFunction.ID, Detail: fmt.Sprintf("Pi call branches %q differ from Pig call branches %q", sourceProfile, targetProfile)})
		}
		compareCancellationFlow(report, sourceFunction, targetFunction, sourceCalls, targetCalls, rules)
	}
}

func callConditionProfile(calls []FunctionCall) []string {
	profile := make([]string, len(calls))
	for index, call := range calls {
		profile[index] = conditionProfile(call.Conditions)
	}
	return profile
}

func conditionProfile(conditions []string) string {
	branch := "then"
	if len(conditions) > 0 && strings.HasPrefix(conditions[len(conditions)-1], "else:") {
		branch = "else"
	}
	return fmt.Sprintf("%d:%s", len(conditions), branch)
}

func normalizedCalls(calls []FunctionCall, targets map[string]string, targetSide bool) ([]string, []FunctionCall) {
	sequence := []string{}
	selected := []FunctionCall{}
	for _, call := range calls {
		name := call.Callee
		if targetSide {
			matched := false
			for _, targetName := range targets {
				if name == targetName {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		} else {
			mapped, ok := targets[name]
			if !ok {
				continue
			}
			name = mapped
		}
		sequence = append(sequence, name)
		selected = append(selected, call)
	}
	return sequence, selected
}

func compareCancellationFlow(report *Report, source, target Function, sourceCalls, targetCalls []FunctionCall, rules Rules) {
	if len(source.CancellationInputs) == 0 || len(target.CancellationInputs) == 0 {
		report.Findings = append(report.Findings, Finding{ID: "finding:function-cancellation-input:" + source.Name, Severity: "error", Kind: "function-cancellation-input", SourceID: source.ID, TargetID: target.ID, Detail: "source or target lacks a cancellation input"})
		return
	}
	for _, call := range sourceCalls {
		if !slices.Contains(rules.CancellableCalls, call.Callee) {
			continue
		}
		if !call.Awaited || !argumentsContain(call.Arguments, source.CancellationInputs) {
			report.Findings = append(report.Findings, Finding{ID: fmt.Sprintf("finding:function-cancellation-source:%s:%d", source.Name, call.Ordinal), Severity: "error", Kind: "function-cancellation-flow", SourceID: source.ID, TargetID: target.ID, Detail: "Pi cancellable call is unawaited or does not receive its cancellation input"})
		}
	}
	for _, call := range targetCalls {
		sourceCallee := ""
		for candidate, mapped := range rules.CalleeTargets {
			if mapped == call.Callee {
				sourceCallee = candidate
				break
			}
		}
		if !slices.Contains(rules.CancellableCalls, sourceCallee) {
			continue
		}
		if !argumentsContain(call.Arguments, target.CancellationInputs) {
			report.Findings = append(report.Findings, Finding{ID: fmt.Sprintf("finding:function-cancellation-target:%s:%d", source.Name, call.Ordinal), Severity: "error", Kind: "function-cancellation-flow", SourceID: source.ID, TargetID: target.ID, Detail: "Pig blocking call does not receive its cancellation input"})
		}
	}
}

func argumentsContain(arguments, names []string) bool {
	for _, argument := range arguments {
		for _, name := range names {
			if strings.Contains(argument, name) {
				return true
			}
		}
	}
	return false
}

func compareTables(report *Report, source, target *Inventory, rules Rules) {
	sourceTables := indexTables(source.Tables)
	targetTables := indexTables(target.Tables)
	for sourceID := range sourceTables {
		if _, claimed := rules.TableTargets[sourceID]; !claimed {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:table:unclaimed-source:" + sourceID, Severity: "error", Kind: "unclaimed-source-table", SourceID: sourceID,
				Detail: "Pi table is absent from the correspondence rules",
			})
		}
	}
	claimedTargets := make(map[string]struct{}, len(rules.TableTargets))
	for _, targetID := range rules.TableTargets {
		claimedTargets[targetID] = struct{}{}
	}
	for targetID := range targetTables {
		if _, claimed := claimedTargets[targetID]; !claimed {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:table:unclaimed-target:" + targetID, Severity: "error", Kind: "unclaimed-target-table", TargetID: targetID,
				Detail: "Pig table lacks a Pi correspondence or additive lineage",
			})
		}
	}
	for sourceID, targetID := range rules.TableTargets {
		sourceTable := sourceTables[sourceID]
		targetTable := targetTables[targetID]
		if sourceTable == nil || targetTable == nil {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:table:" + sourceID, Severity: "error", Kind: "missing-table", SourceID: sourceID, TargetID: targetID,
				Detail: fmt.Sprintf("source present=%t target present=%t", sourceTable != nil, targetTable != nil),
			})
			continue
		}
		sourceItems := indexItems(sourceTable.Items)
		targetItems := indexItems(targetTable.Items)
		for _, sourceItem := range sourceTable.Items {
			targetItem, ok := targetItems[sourceItem.ID]
			if !ok {
				report.Findings = append(report.Findings, Finding{
					ID: "finding:table-item:missing:" + sourceItem.ID, Severity: "error", Kind: "missing-table-item",
					SourceID: sourceID + "#" + sourceItem.ID, TargetID: targetID + "#" + sourceItem.ID,
					Detail: "Pig table has no item with the Pi identity",
				})
				continue
			}
			report.Mappings = append(report.Mappings, MappingFact{
				ID: "correspondence:table-item:" + sourceItem.ID, SourceID: sourceID + "#" + sourceItem.ID,
				TargetID: targetID + "#" + targetItem.ID, Kind: "direct-table-identity", SourceHash: sourceItem.SourceHash, TargetHash: targetItem.SourceHash,
			})
			compareTableItem(report, sourceID, targetID, sourceItem, targetItem)
		}
		for _, targetItem := range targetTable.Items {
			if _, ok := sourceItems[targetItem.ID]; !ok {
				report.Findings = append(report.Findings, Finding{
					ID: "finding:table-item:extra:" + targetItem.ID, Severity: "error", Kind: "extra-table-item",
					SourceID: sourceID, TargetID: targetID + "#" + targetItem.ID,
					Detail: "Pig table item lacks a Pi counterpart or additive lineage",
				})
			}
		}
		sourceOrder := itemIDs(sourceTable.Items)
		targetOrder := itemIDs(targetTable.Items)
		if !slices.Equal(sourceOrder, targetOrder) {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:table-order:" + sourceID, Severity: "error", Kind: "table-order",
				SourceID: sourceID, TargetID: targetID,
				Detail: fmt.Sprintf("Pi order %q differs from Pig order %q", sourceOrder, targetOrder),
			})
		}
	}
}

func compareTableItem(report *Report, sourceTableID, targetTableID string, source, target DataItem) {
	sourceID := sourceTableID + "#" + source.ID
	targetID := targetTableID + "#" + target.ID
	if source.Label != target.Label {
		report.Findings = append(report.Findings, Finding{
			ID: "finding:table-label:" + source.ID, Severity: "error", Kind: "table-label", SourceID: sourceID, TargetID: targetID,
			Detail: fmt.Sprintf("Pi label %q differs from Pig label %q", source.Label, target.Label),
		})
	}
	if source.Description != "" && target.Description != "" && source.Description != target.Description {
		report.Findings = append(report.Findings, Finding{
			ID: "finding:table-description:" + source.ID, Severity: "error", Kind: "table-description", SourceID: sourceID, TargetID: targetID,
			Detail: fmt.Sprintf("Pi description %q differs from Pig description %q", source.Description, target.Description),
		})
	}
	if len(source.Values) > 0 && len(target.Values) > 0 && !slices.Equal(source.Values, target.Values) {
		report.Findings = append(report.Findings, Finding{
			ID: "finding:table-values:" + source.ID, Severity: "error", Kind: "table-values", SourceID: sourceID, TargetID: targetID,
			Detail: fmt.Sprintf("Pi values %q differ from Pig values %q", source.Values, target.Values),
		})
	}
}

func compareConstants(report *Report, source, target *Inventory, rules Rules) {
	sourceConstants := indexConstants(source.Constants)
	targetConstants := indexConstants(target.Constants)
	for sourceName, sourceConstant := range sourceConstants {
		if _, claimed := rules.ConstantTargets[sourceName]; !claimed {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:constant:unclaimed-source:" + sourceName, Severity: "error", Kind: "unclaimed-source-constant",
				SourceID: sourceConstant.ID, Detail: "Pi constant is absent from the correspondence rules",
			})
		}
	}
	claimedTargets := make(map[string]struct{}, len(rules.ConstantTargets))
	for _, targetName := range rules.ConstantTargets {
		claimedTargets[targetName] = struct{}{}
	}
	for targetName, targetConstant := range targetConstants {
		if _, claimed := claimedTargets[targetName]; !claimed {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:constant:unclaimed-target:" + targetName, Severity: "error", Kind: "unclaimed-target-constant",
				TargetID: targetConstant.ID, Detail: "Pig constant lacks a Pi correspondence or additive lineage",
			})
		}
	}
	for sourceName, targetName := range rules.ConstantTargets {
		sourceConstant := sourceConstants[sourceName]
		targetConstant := targetConstants[targetName]
		if sourceConstant == nil || targetConstant == nil {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:constant:" + sourceName, Severity: "error", Kind: "missing-constant",
				SourceID: sourceName, TargetID: targetName,
				Detail: fmt.Sprintf("source present=%t target present=%t", sourceConstant != nil, targetConstant != nil),
			})
			continue
		}
		report.Mappings = append(report.Mappings, MappingFact{
			ID: "correspondence:constant:" + sourceName, SourceID: sourceConstant.ID, TargetID: targetConstant.ID,
			Kind: "direct-constant", SourceHash: sourceConstant.SourceHash, TargetHash: targetConstant.SourceHash,
		})
		if sourceConstant.ValueHash != targetConstant.ValueHash || sourceConstant.UTF16Length != targetConstant.UTF16Length {
			report.Findings = append(report.Findings, Finding{
				ID: "finding:constant-value:" + sourceName, Severity: "error", Kind: "constant-value",
				SourceID: sourceConstant.ID, TargetID: targetConstant.ID,
				Detail: fmt.Sprintf("Pi value %s/%d differs from Pig value %s/%d", sourceConstant.ValueHash, sourceConstant.UTF16Length, targetConstant.ValueHash, targetConstant.UTF16Length),
			})
		}
	}
}

func indexTables(tables []DataTable) map[string]*DataTable {
	indexed := make(map[string]*DataTable, len(tables))
	for index := range tables {
		indexed[tables[index].ID] = &tables[index]
	}
	return indexed
}

func indexItems(items []DataItem) map[string]DataItem {
	indexed := make(map[string]DataItem, len(items))
	for _, item := range items {
		indexed[item.ID] = item
	}
	return indexed
}

func indexConstants(constants []Constant) map[string]*Constant {
	indexed := make(map[string]*Constant, len(constants))
	for index := range constants {
		indexed[constants[index].Name] = &constants[index]
	}
	return indexed
}

func itemIDs(items []DataItem) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}
