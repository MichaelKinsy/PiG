package correspondence

import (
	"slices"
	"testing"
)

func TestAdditiveSettingsLineagePreservesPiOrderChecks(t *testing.T) {
	rules := Rules{ID: "additive-settings", TableTargets: map[string]string{"settings": "settings"}, AdditiveTableItems: map[string]map[string]string{"settings": {"private": "docs/parity/DIVERGENCES.md#D80"}}}
	source := &Inventory{Source: SourceIdentity{Language: LanguageTypeScript}, Tables: []DataTable{{ID: "settings", Items: []DataItem{{ID: "first"}, {ID: "last"}}}}}
	for _, tc := range []struct {
		name    string
		ids     []string
		finding string
	}{
		{"approved addition", []string{"first", "private", "last"}, ""},
		{"upstream rows reordered", []string{"last", "private", "first"}, "table-order"},
		{"unclaimed addition", []string{"first", "private", "rogue", "last"}, "extra-table-item"},
		{"declared addition missing", []string{"first", "last"}, "missing-additive-table-item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := &Inventory{Source: SourceIdentity{Language: LanguageGo}, Tables: []DataTable{{ID: "settings"}}}
			for _, id := range tc.ids {
				target.Tables[0].Items = append(target.Tables[0].Items, DataItem{ID: id})
			}
			report, err := Compare(source, target, rules)
			if err != nil {
				t.Fatal(err)
			}
			if tc.finding == "" {
				if len(report.Findings) != 0 {
					t.Fatalf("findings = %+v", report.Findings)
				}
				if !slices.ContainsFunc(report.Mappings, func(m MappingFact) bool {
					return m.Kind == "additive-table-item" && m.SourceID == "docs/parity/DIVERGENCES.md#D80"
				}) {
					t.Fatal("addition has no explicit lineage")
				}
			} else if !slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Kind == tc.finding }) {
				t.Fatalf("missing %s in %+v", tc.finding, report.Findings)
			}
		})
	}
}
