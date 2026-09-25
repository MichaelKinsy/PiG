package closure

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

type closureVerticalBehavior struct {
	id             string
	name           string
	facets         []string
	upstreamRanges []sourceRange
	targets        []providerWireTarget
}

func AddCompactionSettingsBehaviors(root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	definitions := compactionSettingsDefinitions()
	result := slices.Clone(records)
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.RecordID()] = struct{}{}
	}
	facetNames := make(map[string]struct{})
	for _, definition := range definitions {
		for _, facet := range definition.facets {
			facetNames[facet] = struct{}{}
		}
	}
	for _, facet := range slices.Sorted(maps.Keys(facetNames)) {
		facetID := "facet:" + facet
		if _, ok := existing[facetID]; !ok {
			result = append(result, &Facet{Kind: KindFacet, ID: facetID, Name: facet})
			existing[facetID] = struct{}{}
		}
		ruleID := "rule:foundation-state:" + facet
		result = append(result, &Rule{Kind: KindRule, ID: ruleID, Name: "foundation state " + facet, DefinitionHash: HashBytes([]byte("foundation-state:" + facet + ":decided-mapping+production-reachability+bound-assertion+execution-witness"))})
	}

	pins := make(map[string]*Pin)
	for _, definition := range definitions {
		originPinIDs, err := addSourceRanges(root, snapshot, pins, definition.upstreamRanges)
		if err != nil {
			return nil, fmt.Errorf("add %s behavior: %w", definition.id, err)
		}
		behaviorID := "behavior:foundation-state:" + definition.id
		result = append(result, &Behavior{Kind: KindBehavior, ID: behaviorID, Name: definition.name, OriginPinIDs: originPinIDs, Profile: "application"})
		var targetIDs []string
		for _, targetDefinition := range definition.targets {
			targetPinIDs, err := addSourceRanges(root, snapshot, pins, []sourceRange{targetDefinition.rangeRef})
			if err != nil {
				return nil, fmt.Errorf("add %s target: %w", targetDefinition.id, err)
			}
			rootPinIDs, err := addSourceRanges(root, snapshot, pins, []sourceRange{targetDefinition.rootRef})
			if err != nil {
				return nil, fmt.Errorf("add %s reachability: %w", targetDefinition.id, err)
			}
			factIDs, err := findDenominatorFacts(records, []string{"go-interface\x00" + targetDefinition.goFact})
			if err != nil {
				return nil, fmt.Errorf("add %s target fact: %w", targetDefinition.id, err)
			}
			targetID := "target:foundation-state:" + targetDefinition.id
			targetIDs = append(targetIDs, targetID)
			if _, ok := existing[targetID]; !ok {
				result = append(result, &Target{Kind: KindTarget, ID: targetID, SnapshotID: snapshot.ID, PinIDs: targetPinIDs, FactIDs: factIDs, Language: "go", Symbol: targetDefinition.symbol})
				existing[targetID] = struct{}{}
			}
			result = append(result, &Reachability{
				Kind: KindReachability, ID: "reachability:foundation-state:" + definition.id + ":" + targetDefinition.id,
				BehaviorID: behaviorID, TargetID: targetID, Class: "prod-reachable", Method: "production-call-path", RootPinIDs: rootPinIDs,
			})
		}
		slices.Sort(targetIDs)
		result = append(result, &Mapping{Kind: KindMapping, ID: "mapping:foundation-state:" + definition.id, BehaviorID: behaviorID, TargetIDs: targetIDs, Status: "hypothesis"})
		for _, facet := range definition.facets {
			result = append(result, &Obligation{
				Kind: KindObligation, ID: "obligation:foundation-state:" + definition.id + ":" + facet,
				BehaviorID: behaviorID, FacetID: "facet:" + facet, RuleID: "rule:foundation-state:" + facet, OriginPinIDs: originPinIDs,
			})
		}
	}
	for _, pinID := range slices.Sorted(maps.Keys(pins)) {
		if _, ok := existing[pinID]; !ok {
			result = append(result, pins[pinID])
			existing[pinID] = struct{}{}
		}
	}
	return result, nil
}

func compactionSettingsDefinitions() []closureVerticalBehavior {
	compactionRoot := sourceRange{repository: "pig", path: "coding/session.go", semanticID: "coding.Session.compaction-call", start: "prep := compaction.PrepareCompaction(entries, settings)", end: "if err == nil && compactCtx.Err() != nil {"}
	settingsRoot := sourceRange{repository: "pig", path: "internal/codingagent/interactive_commands.go", semanticID: "codingagent.interactive.settings-list", start: "ShowSettingsList: func(items []tui.SettingItem)", end: "return m.runModalSettingsList(sl)"}
	compactTarget := providerWireTarget{
		id: "compaction-compact", symbol: "compaction.Compact", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent/compaction#Compact",
		rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/compaction/compaction.go", semanticID: "compaction.Compact", start: "func Compact(", end: ""}, rootRef: compactionRoot,
	}
	cancellationTarget := providerWireTarget{
		id: "compaction-turn-prefix-cancellation", symbol: "compaction.generateTurnPrefixSummary", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent/compaction#generateTurnPrefixSummary",
		rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/compaction/compaction.go", semanticID: "compaction.generateTurnPrefixSummary", start: "func generateTurnPrefixSummary(", end: "func summarizationFailure("}, rootRef: compactionRoot,
	}
	prepareTarget := providerWireTarget{
		id: "compaction-prepare", symbol: "compaction.PrepareCompaction", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent/compaction#PrepareCompaction",
		rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/compaction/compaction.go", semanticID: "compaction.PrepareCompaction", start: "func PrepareCompaction(", end: "// ─── LLM Summarization"}, rootRef: compactionRoot,
	}
	settingsStateTarget := providerWireTarget{
		id: "settings-display-state", symbol: "codingagent.SettingsManager display settings", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent#SettingsManager.GetCompactionSettings",
		rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/settings.go", semanticID: "codingagent.SettingsManager.display-settings", start: "func (sm *SettingsManager) GetCompactionSettings(", end: "// RetryConfig holds resolved retry settings"}, rootRef: settingsRoot,
	}
	settingsPersistenceTarget := providerWireTarget{
		id: "settings-global-persistence", symbol: "codingagent.SettingsManager.UpdateGlobal", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent#SettingsManager.UpdateGlobal",
		rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/settings.go", semanticID: "codingagent.SettingsManager.UpdateGlobal", start: "func (sm *SettingsManager) UpdateGlobal(", end: "func (sm *SettingsManager) UpdateProject("},
		rootRef:  sourceRange{repository: "pig", path: "internal/codingagent/slash_session_handlers.go", semanticID: "codingagent.settings.UpdateGlobal", start: "if err := sc.SettingsManager.UpdateGlobal(func(gs *Settings)", end: "sc.SettingsManager.Reload()"},
	}
	settingsListTarget := providerWireTarget{
		id: "settings-list-input-render", symbol: "tui.SettingsList", goFact: "go:github.com/MichaelKinsy/PiG/tui#SettingsList",
		rangeRef: sourceRange{repository: "pig", path: "tui/settings_list.go", semanticID: "tui.SettingsList.input-render", start: "func (s *SettingsList) Render(", end: "func renderSettingsSearchInput("}, rootRef: settingsRoot,
	}
	return []closureVerticalBehavior{
		{
			id: "compaction-split-order", name: "split-turn history summary completes before prefix summary", facets: []string{"order", "result"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/core/compaction/compaction.ts", semanticID: "compaction.compact.split-turn", start: "export async function compact(", end: "async function generateTurnPrefixSummary("}}, targets: []providerWireTarget{compactTarget},
		},
		{
			id: "compaction-cancellation", name: "turn-prefix cancellation surfaces the exact abort error", facets: []string{"cancel", "error"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/core/compaction/compaction.ts", semanticID: "compaction.generateTurnPrefixSummary", start: "async function generateTurnPrefixSummary(", end: ""}}, targets: []providerWireTarget{cancellationTarget},
		},
		{
			id: "compaction-history-boundary", name: "prior compaction retained-root history remains summarized", facets: []string{"history", "restoration"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/core/compaction/compaction.ts", semanticID: "compaction.prepareCompaction", start: "export function prepareCompaction(", end: "// ============================================================================\n// Main compaction function"}}, targets: []providerWireTarget{prepareTarget},
		},
		{
			id: "settings-display-state", name: "display settings preserve exact defaults and merged state", facets: []string{"state"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/core/settings-manager.ts", semanticID: "settings-manager.display-settings", start: "\tgetTuiMode(): TuiMode", end: "\tgetWarnings(): WarningSettings"}}, targets: []providerWireTarget{settingsStateTarget},
		},
		{
			id: "settings-display-persistence", name: "display setting changes persist through the global settings path", facets: []string{"persistence"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/core/settings-manager.ts", semanticID: "settings-manager.persist-settings", start: "\tprivate persistScopedSettings(", end: "\tprivate save(): void"}}, targets: []providerWireTarget{settingsPersistenceTarget},
		},
		{
			id: "settings-selector-input", name: "settings search and value selection follow selector input state", facets: []string{"input"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/modes/interactive/components/settings-selector.ts", semanticID: "settings-selector.component", start: "export class SettingsSelectorComponent", end: ""}}, targets: []providerWireTarget{settingsListTarget},
		},
		{
			id: "settings-selector-layout", name: "settings values begin after the 30-cell label column", facets: []string{"layout"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/tui/src/components/settings-list.ts", semanticID: "tui.settings-list.layout", start: "export class SettingsList", end: ""}}, targets: []providerWireTarget{settingsListTarget},
		},
		{
			id: "settings-selector-render", name: "the production settings selector renders byte-faithfully", facets: []string{"render"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/coding-agent/src/modes/interactive/components/settings-selector.ts", semanticID: "settings-selector.render", start: "export class SettingsSelectorComponent", end: ""}}, targets: []providerWireTarget{settingsListTarget},
		},
	}
}

func foundationStateCoveragePath(requestName string) string {
	return filepath.ToSlash(filepath.Join("tmp", "closure", "evidence-work", "foundation-state-"+requestName+".coverprofile"))
}
