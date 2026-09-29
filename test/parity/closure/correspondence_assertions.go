package closure

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type correspondenceAssertionBinding struct {
	id          string
	path        string
	symbol      string
	start       string
	end         string
	packagePath string
	subjectKind string
	subject     string
	analyses    []string
	witnessType string
	pinPrefix   string
}

func AddCorrespondenceAssertions(root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	result := slices.Clone(records)
	pins := make(map[string]*Pin)
	testIDs := make(map[string]struct{})
	for _, binding := range correspondenceAssertionBindings() {
		pin, err := makeSourceRangePin(root, snapshot, sourceRange{
			repository: "pig", path: binding.path, semanticID: binding.symbol, start: binding.start, end: binding.end,
		})
		if err != nil {
			return nil, fmt.Errorf("add correspondence test %s: %w", binding.id, err)
		}
		pins[pin.ID] = pin
		testID := "test:correspondence:" + binding.id
		if _, exists := testIDs[testID]; !exists {
			result = append(result, &Test{
				Kind: KindTest, ID: testID, SnapshotID: snapshot.ID, PinID: pin.ID,
				FixturePinIDs: []string{}, DefinitionHash: pin.BodyHash,
			})
			testIDs[testID] = struct{}{}
		}
		for _, analysis := range binding.analyses {
			facet, err := correspondenceAnalysisFacet(analysis)
			if err != nil {
				return nil, err
			}
			result = append(result, &Assertion{
				Kind: KindAssertion, ID: "assertion:correspondence:" + binding.id + ":" + binding.subject + ":" + analysis,
				TestID: testID, Class: "A2", BehaviorID: "behavior:correspondence:" + binding.subjectKind + ":" + binding.subject,
				FacetID: "facet:" + facet, Oracle: "authored-upstream-contract",
			})
		}
	}
	for _, pinID := range slices.Sorted(maps.Keys(pins)) {
		result = append(result, pins[pinID])
	}
	return result, nil
}

func AddCorrespondenceEvidenceRequests(records []Record) ([]Record, error) {
	result := slices.Clone(records)
	recordByID := make(map[string]Record, len(records))
	for _, record := range records {
		recordByID[record.RecordID()] = record
	}
	bindingsByID := make(map[string][]correspondenceAssertionBinding)
	for _, binding := range correspondenceAssertionBindings() {
		bindingsByID[binding.id] = append(bindingsByID[binding.id], binding)
	}
	for _, id := range slices.Sorted(maps.Keys(bindingsByID)) {
		bindings := bindingsByID[id]
		testID := "test:correspondence:" + id
		test, ok := recordByID[testID].(*Test)
		if !ok {
			return nil, fmt.Errorf("correspondence request references missing test %s", testID)
		}
		var assertionIDs, obligationIDs, targetIDs, subjectPinIDs []string
		for _, binding := range bindings {
			targetID := "target:correspondence:" + binding.subjectKind + ":" + binding.subject
			target, ok := recordByID[targetID].(*Target)
			if !ok {
				return nil, fmt.Errorf("correspondence request %s references missing target %s", id, targetID)
			}
			targetIDs = append(targetIDs, targetID)
			for _, pinID := range target.PinIDs {
				pin := recordByID[pinID].(*Pin)
				if (binding.pinPrefix != "" && strings.HasPrefix(pin.SemanticID, binding.pinPrefix)) ||
					(binding.pinPrefix == "" && binding.subjectKind == "function" && strings.HasPrefix(pin.SemanticID, "function:")) ||
					(binding.pinPrefix == "" && binding.subjectKind == "setting" && pin.SemanticID == "table:settings-selector#"+binding.subject) {
					subjectPinIDs = append(subjectPinIDs, pinID)
				}
			}
			for _, analysis := range binding.analyses {
				assertionID := "assertion:correspondence:" + binding.id + ":" + binding.subject + ":" + analysis
				obligationID := "obligation:correspondence:" + binding.subjectKind + ":" + binding.subject + ":" + analysis
				if _, ok := recordByID[assertionID].(*Assertion); !ok {
					return nil, fmt.Errorf("correspondence request %s references missing assertion %s", id, assertionID)
				}
				if _, ok := recordByID[obligationID].(*Obligation); !ok {
					return nil, fmt.Errorf("correspondence request %s references missing obligation %s", id, obligationID)
				}
				assertionIDs = append(assertionIDs, assertionID)
				obligationIDs = append(obligationIDs, obligationID)
			}
		}
		slices.Sort(assertionIDs)
		slices.Sort(obligationIDs)
		slices.Sort(targetIDs)
		slices.Sort(subjectPinIDs)
		targetIDs = slices.Compact(targetIDs)
		subjectPinIDs = slices.Compact(subjectPinIDs)
		tracePath := "tmp/closure/correspondence-" + id + ".trace.json"
		args := []string{
			"test", bindings[0].packagePath, "-run", "^" + testFunctionName(bindings[0].symbol) + "$", "-count=1",
		}
		witnesses := []EvidenceWitnessRequest{{
			WitnessType: bindings[0].witnessType, TargetIDs: targetIDs, SubjectPinIDs: subjectPinIDs,
			CommandIndexes: []int{1}, ArtifactPath: tracePath,
		}}
		if bindings[0].packagePath == "./internal/codingagent" {
			args = append(args,
				"-args", "-settings-closure-trace=../../"+tracePath,
				"-settings-closure-trace-type="+bindings[0].witnessType,
				"-settings-closure-trace-targets="+strings.Join(targetIDs, ","),
			)
		} else {
			args = append(args,
				"-args", "-closure-trace=../../../"+tracePath,
				"-closure-trace-type="+bindings[0].witnessType,
				"-closure-trace-targets="+strings.Join(targetIDs, ","),
			)
		}
		result = append(result, &EvidenceRequest{
			Kind: KindEvidenceRequest, ID: "request:correspondence:" + id, SnapshotID: test.SnapshotID,
			ObligationIDs: obligationIDs, AssertionIDs: assertionIDs,
			Commands: []EvidenceCommand{{Name: "go", Args: args}}, Environment: []string{}, Witnesses: witnesses,
			Durability: 1, Comparator: "exit-zero", TimeoutSeconds: 120,
		})
	}
	return result, nil
}

func correspondenceAssertionBindings() []correspondenceAssertionBinding {
	const path = "internal/codingagent/compaction/compaction_test.go"
	bindings := []correspondenceAssertionBinding{
		{
			id: "compaction-cancellation", path: path, symbol: "compaction.TestCompactTurnPrefixCancellationSurfacesUpstreamError",
			start: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(", end: "type cancelledCompleter struct",
			packagePath: "./internal/codingagent/compaction", subjectKind: "function", subject: "compact", analyses: []string{"cancellation", "error-state"}, witnessType: "cancellation-trace",
		},
		{
			id: "compaction-cancellation", path: path, symbol: "compaction.TestCompactTurnPrefixCancellationSurfacesUpstreamError",
			start: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(", end: "type cancelledCompleter struct",
			packagePath: "./internal/codingagent/compaction", subjectKind: "function", subject: "generateTurnPrefixSummary", analyses: []string{"cancellation", "error-state"}, witnessType: "cancellation-trace",
		},
		{
			id: "compaction-order", path: path, symbol: "compaction.TestCompactSplitTurnAwaitsHistoryBeforePrefix",
			start: "func TestCompactSplitTurnAwaitsHistoryBeforePrefix(", end: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(",
			packagePath: "./internal/codingagent/compaction", subjectKind: "function", subject: "compact", analyses: []string{"call-order"}, witnessType: "state-transition",
		},
		{
			id: "compaction-order", path: path, symbol: "compaction.TestCompactSplitTurnAwaitsHistoryBeforePrefix",
			start: "func TestCompactSplitTurnAwaitsHistoryBeforePrefix(", end: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(",
			packagePath: "./internal/codingagent/compaction", subjectKind: "function", subject: "generateTurnPrefixSummary", analyses: []string{"call-order"}, witnessType: "state-transition",
		},
		{
			id: "settings-display-persistence", path: "internal/codingagent/settings_test.go", symbol: "codingagent.TestSettingsManager_084DisplaySettingsPersist",
			start: "func TestSettingsManager_084DisplaySettingsPersist(", end: "func TestSettingsManager_NewGetters(",
			packagePath: "./internal/codingagent", subjectKind: "setting", subject: "fullscreen-scrollbar", analyses: []string{"persistence"}, witnessType: "persistence-roundtrip",
		},
		{
			id: "settings-display-persistence", path: "internal/codingagent/settings_test.go", symbol: "codingagent.TestSettingsManager_084DisplaySettingsPersist",
			start: "func TestSettingsManager_084DisplaySettingsPersist(", end: "func TestSettingsManager_NewGetters(",
			packagePath: "./internal/codingagent", subjectKind: "setting", subject: "mermaid-rendering", analyses: []string{"persistence"}, witnessType: "persistence-roundtrip",
		},
		{
			id: "settings-display-persistence", path: "internal/codingagent/settings_test.go", symbol: "codingagent.TestSettingsManager_084DisplaySettingsPersist",
			start: "func TestSettingsManager_084DisplaySettingsPersist(", end: "func TestSettingsManager_NewGetters(",
			packagePath: "./internal/codingagent", subjectKind: "setting", subject: "tui-mode", analyses: []string{"persistence"}, witnessType: "persistence-roundtrip",
		},
	}
	for _, subject := range []string{
		"autocomplete-max-visible", "cache-miss-notices", "clear-on-shrink", "editor-padding",
		"output-padding", "skill-commands", "hide-thinking", "transport",
	} {
		bindings = append(bindings, correspondenceAssertionBinding{
			id: "settings-live-effects", path: "internal/codingagent/settings_live_effects_test.go", symbol: "codingagent.TestSettingsLiveEffectsClosureTrace",
			start: "func TestSettingsLiveEffectsClosureTrace(", end: "func TestOnSettingAppliedRebuildsSkillAutocomplete(",
			packagePath: "./internal/codingagent", subjectKind: "setting", subject: subject, analyses: []string{"runtime-effects"}, witnessType: "state-transition",
			pinPrefix: "runtime:OnSettingApplied:runtime-case:",
		})
	}
	return bindings
}
