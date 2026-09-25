package closure

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type foundationStateTestBinding struct {
	id          string
	path        string
	symbol      string
	start       string
	end         string
	packagePath string
	asserts     []providerWireAssertionBinding
}

func AddCompactionSettingsAssertions(root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	result := slices.Clone(records)
	pins := make(map[string]*Pin)
	for _, binding := range foundationStateTestBindings() {
		pin, err := makeSourceRangePin(root, snapshot, sourceRange{
			repository: "pig", path: binding.path, semanticID: binding.symbol, start: binding.start, end: binding.end,
		})
		if err != nil {
			return nil, fmt.Errorf("add foundation-state test %s: %w", binding.id, err)
		}
		pins[pin.ID] = pin
		testID := "test:foundation-state:" + binding.id
		result = append(result, &Test{Kind: KindTest, ID: testID, SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: pin.BodyHash})
		for _, assertion := range binding.asserts {
			result = append(result, &Assertion{
				Kind: KindAssertion, ID: "assertion:foundation-state:" + binding.id + ":" + assertion.behavior + ":" + assertion.facet,
				TestID: testID, Class: assertion.class, BehaviorID: "behavior:foundation-state:" + assertion.behavior,
				FacetID: "facet:" + assertion.facet, Oracle: assertion.oracle,
			})
		}
	}
	for _, pinID := range slices.Sorted(maps.Keys(pins)) {
		result = append(result, pins[pinID])
	}
	return result, nil
}

func foundationStateTestBindings() []foundationStateTestBinding {
	const authored = "authored-upstream-contract"
	return []foundationStateTestBinding{
		{
			id: "compaction-split-order", path: "internal/codingagent/compaction/compaction_test.go", symbol: "compaction.TestCompactSplitTurnAwaitsHistoryBeforePrefix",
			start: "func TestCompactSplitTurnAwaitsHistoryBeforePrefix(", end: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(", packagePath: "./internal/codingagent/compaction",
			asserts: []providerWireAssertionBinding{{behavior: "compaction-split-order", facet: "order", class: "A2", oracle: authored}, {behavior: "compaction-split-order", facet: "result", class: "A2", oracle: authored}},
		},
		{
			id: "compaction-cancellation", path: "internal/codingagent/compaction/compaction_test.go", symbol: "compaction.TestCompactTurnPrefixCancellationSurfacesUpstreamError",
			start: "func TestCompactTurnPrefixCancellationSurfacesUpstreamError(", end: "type cancelledCompleter struct", packagePath: "./internal/codingagent/compaction",
			asserts: []providerWireAssertionBinding{{behavior: "compaction-cancellation", facet: "cancel", class: "A2", oracle: authored}, {behavior: "compaction-cancellation", facet: "error", class: "A2", oracle: authored}},
		},
		{
			id: "compaction-history", path: "internal/codingagent/compaction/compaction_test.go", symbol: "compaction.TestPrepareCompaction_PriorCompactionKeptFromRoot",
			start: "func TestPrepareCompaction_PriorCompactionKeptFromRoot(", end: "func TestPrepareCompaction_NilWhenLastEntryIsCompaction(", packagePath: "./internal/codingagent/compaction",
			asserts: []providerWireAssertionBinding{{behavior: "compaction-history-boundary", facet: "history", class: "A2", oracle: authored}, {behavior: "compaction-history-boundary", facet: "restoration", class: "A2", oracle: authored}},
		},
		{
			id: "settings-state", path: "internal/codingagent/settings_test.go", symbol: "codingagent.TestSettings_084DisplaySettingsRoundTripAndDefaults",
			start: "func TestSettings_084DisplaySettingsRoundTripAndDefaults(", end: "func TestSettingsManager_084DisplaySettingsPersist(", packagePath: "./internal/codingagent",
			asserts: []providerWireAssertionBinding{{behavior: "settings-display-state", facet: "state", class: "A2", oracle: authored}},
		},
		{
			id: "settings-persistence", path: "internal/codingagent/settings_test.go", symbol: "codingagent.TestSettingsManager_084DisplaySettingsPersist",
			start: "func TestSettingsManager_084DisplaySettingsPersist(", end: "func TestSettingsManager_NewGetters(", packagePath: "./internal/codingagent",
			asserts: []providerWireAssertionBinding{{behavior: "settings-display-persistence", facet: "persistence", class: "A2", oracle: authored}},
		},
		{
			id: "settings-input", path: "tui/settings_list_test.go", symbol: "tui.TestSettingsListFilterReducesItems",
			start: "func TestSettingsListFilterReducesItems(", end: "func TestSettingsListShowsScrollCounter(", packagePath: "./tui",
			asserts: []providerWireAssertionBinding{{behavior: "settings-selector-input", facet: "input", class: "A2", oracle: authored}},
		},
		{
			id: "settings-layout", path: "tui/settings_list_test.go", symbol: "tui.TestSettingsListUsesThirtySixCellLabelCap",
			start: "func TestSettingsListUsesThirtySixCellLabelCap(", end: "func TestSettingsListWithoutSearch(", packagePath: "./tui",
			asserts: []providerWireAssertionBinding{{behavior: "settings-selector-layout", facet: "layout", class: "A2", oracle: authored}},
		},
	}
}

func AddCompactionSettingsEvidenceRequests(records []Record) ([]Record, error) {
	result := slices.Clone(records)
	recordByID := make(map[string]Record, len(records))
	obligationByFacet := make(map[string]string)
	for _, record := range records {
		recordByID[record.RecordID()] = record
		if obligation, ok := record.(*Obligation); ok && strings.HasPrefix(obligation.BehaviorID, "behavior:foundation-state:") {
			obligationByFacet[obligation.BehaviorID+"\x00"+obligation.FacetID] = obligation.ID
		}
	}
	for _, binding := range foundationStateTestBindings() {
		testID := "test:foundation-state:" + binding.id
		test, ok := recordByID[testID].(*Test)
		if !ok {
			return nil, fmt.Errorf("foundation-state request references missing test %s", testID)
		}
		var assertionIDs, obligationIDs []string
		targetSet := make(map[string]struct{})
		pinSet := make(map[string]struct{})
		for _, assertionBinding := range binding.asserts {
			assertionID := "assertion:foundation-state:" + binding.id + ":" + assertionBinding.behavior + ":" + assertionBinding.facet
			assertionIDs = append(assertionIDs, assertionID)
			behaviorID := "behavior:foundation-state:" + assertionBinding.behavior
			obligationID := obligationByFacet[behaviorID+"\x00facet:"+assertionBinding.facet]
			if obligationID == "" {
				return nil, fmt.Errorf("foundation-state assertion %s has no obligation", assertionID)
			}
			obligationIDs = append(obligationIDs, obligationID)
			for _, record := range records {
				mapping, mappingOK := record.(*Mapping)
				if !mappingOK || mapping.BehaviorID != behaviorID {
					continue
				}
				for _, targetID := range mapping.TargetIDs {
					target := recordByID[targetID].(*Target)
					targetSet[targetID] = struct{}{}
					for _, pinID := range target.PinIDs {
						pinSet[pinID] = struct{}{}
					}
				}
			}
		}
		slices.Sort(assertionIDs)
		slices.Sort(obligationIDs)
		obligationIDs = slices.Compact(obligationIDs)
		coveragePath := foundationStateCoveragePath(binding.id)
		command := EvidenceCommand{Name: "go", Args: []string{"test", binding.packagePath, "-run", "^" + testFunctionName(binding.symbol) + "$", "-count=1", "-coverprofile=" + coveragePath}}
		result = append(result, &EvidenceRequest{
			Kind: KindEvidenceRequest, ID: "request:foundation-state:" + binding.id, SnapshotID: test.SnapshotID,
			ObligationIDs: obligationIDs, AssertionIDs: assertionIDs, Commands: []EvidenceCommand{command}, Environment: []string{},
			Witnesses:  []EvidenceWitnessRequest{{WitnessType: "go-covered-range", TargetIDs: slices.Sorted(maps.Keys(targetSet)), SubjectPinIDs: slices.Sorted(maps.Keys(pinSet)), CommandIndexes: []int{1}, ArtifactPath: coveragePath}},
			Durability: 1, Comparator: "exit-zero", TimeoutSeconds: 120,
		})
	}
	return result, nil
}

func testFunctionName(symbol string) string {
	if index := strings.LastIndexByte(symbol, '.'); index >= 0 {
		return symbol[index+1:]
	}
	return symbol
}
