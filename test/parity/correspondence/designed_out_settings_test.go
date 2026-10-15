package correspondence

import (
	"slices"
	"testing"
)

func TestDesignedOutSettingsLineagePreservesPiOrderChecks(t *testing.T) {
	const lineage = "docs/parity/DIVERGENCES.md#D64"
	for _, tc := range []struct {
		name        string
		designedOut map[string]string
		targetIDs   []string
		finding     string
	}{
		{"approved omission", map[string]string{"theme": lineage}, []string{"autocompact", "transport"}, ""},
		{"undeclared omission", nil, []string{"autocompact", "transport"}, "missing-table-item"},
		{"remaining rows reordered", map[string]string{"theme": lineage}, []string{"transport", "autocompact"}, "table-order"},
		{"designed-out item still present", map[string]string{"theme": lineage}, []string{"autocompact", "theme", "transport"}, "designed-out-table-item"},
		{"designed-out item unknown to Pi", map[string]string{"rogue": lineage}, []string{"autocompact", "theme", "transport"}, "designed-out-table-item"},
		{"lineage missing", map[string]string{"theme": ""}, []string{"autocompact", "transport"}, "designed-out-table-item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, target := comparisonFixtures()
			byID := indexItems(source.Tables[0].Items)
			target.Tables[0].Items = nil
			for _, id := range tc.targetIDs {
				target.Tables[0].Items = append(target.Tables[0].Items, byID[id])
			}
			rules := fixtureRules()
			rules.DesignedOutTableItems = map[string]map[string]string{"table:settings-selector": tc.designedOut}
			report, err := Compare(source, target, rules)
			if err != nil {
				t.Fatal(err)
			}
			if tc.finding != "" {
				if !slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Kind == tc.finding }) {
					t.Fatalf("missing %s in %+v", tc.finding, report.Findings)
				}
				return
			}
			if len(report.Findings) != 0 {
				t.Fatalf("findings = %+v", report.Findings)
			}
			if !slices.ContainsFunc(report.Mappings, func(m MappingFact) bool {
				return m.Kind == "designed-out-table-item" && m.SourceID == "table:settings-selector#theme" && m.TargetID == lineage
			}) {
				t.Fatalf("omission has no explicit lineage: %+v", report.Mappings)
			}
			source.Tables[0].Callbacks = fixtureDispatch(source.Tables[0].Items, "callbacks.onChange")
			target.Tables[0].Callbacks = fixtureDispatch(target.Tables[0].Items, "apply")
			source.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(source.Tables[0].Items, "source")
			target.Tables[0].ProductionCallbacks = fixtureProductionCallbacks(target.Tables[0].Items, "target")
			packet, err := BuildSettingsAlignmentPacket(source, target, report)
			if err != nil {
				t.Fatal(err)
			}
			var questions []string
			for _, question := range packet.Questions {
				questions = append(questions, question.Source.ID)
			}
			if !slices.Equal(questions, []string{"autocompact", "transport"}) {
				t.Fatalf("alignment questions = %v, want the designed-out item excluded", questions)
			}
		})
	}
}
