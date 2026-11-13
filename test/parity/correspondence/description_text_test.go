package correspondence

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// A description that names the product differs from Pi's by the declared fragment only (D2, "Product name in messages").
func TestDescriptionTextLineageComparesTheRestOfPisDescription(t *testing.T) {
	const (
		lineage = "docs/parity/DIVERGENCES.md#D2"
		piText  = "Use the terminal cursor instead of Pi's drawn cursor"
		pigText = "Use the terminal cursor instead of PiG's drawn cursor"
	)
	pair := DescriptionText{Pi: "instead of Pi's drawn cursor", Pig: "instead of PiG's drawn cursor", Lineage: lineage}
	for _, tc := range []struct {
		name    string
		texts   map[string]DescriptionText
		target  string
		finding string
	}{
		{"declared product name", map[string]DescriptionText{"theme": pair}, pigText, ""},
		{"undeclared product name", nil, pigText, "table-description"},
		{"reworded beyond the declared fragment", map[string]DescriptionText{"theme": pair}, "Use the terminal's cursor instead of PiG's drawn cursor", "table-description"},
		{"description missing", map[string]DescriptionText{"theme": pair}, "", "table-description"},
		{"declared fragment not replaced", map[string]DescriptionText{"theme": pair}, piText, "table-description-text"},
		{"declared fragment unknown to Pi", map[string]DescriptionText{"theme": {Pi: "instead of Pi's painted cursor", Pig: "instead of PiG's drawn cursor", Lineage: lineage}}, pigText, "table-description-text"},
		{"declared fragment replaced by itself", map[string]DescriptionText{"theme": {Pi: pair.Pi, Pig: pair.Pi, Lineage: lineage}}, pigText, "table-description-text"},
		{"lineage missing", map[string]DescriptionText{"theme": {Pi: pair.Pi, Pig: pair.Pig}}, pigText, "table-description-text"},
		{"declared item unknown to both tables", map[string]DescriptionText{"theme": pair, "rogue": pair}, pigText, "table-description-text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, target := comparisonFixtures()
			source.Tables[0].Items[1].Description = piText
			target.Tables[0].Items[1].Description = tc.target
			rules := fixtureRules()
			rules.TableDescriptionTexts = map[string]map[string]DescriptionText{"table:settings-selector": tc.texts}
			report, err := Compare(source, target, rules)
			if err != nil {
				t.Fatal(err)
			}
			if tc.finding != "" {
				if len(report.Findings) != 1 || report.Findings[0].Kind != tc.finding {
					t.Fatalf("findings = %+v, want one %s", report.Findings, tc.finding)
				}
				return
			}
			if len(report.Findings) != 0 {
				t.Fatalf("findings = %+v", report.Findings)
			}
			if !slices.ContainsFunc(report.Mappings, func(m MappingFact) bool {
				return m.Kind == "table-description-text" && m.SourceID == lineage && m.TargetID == "table:settings-selector#theme"
			}) {
				t.Fatalf("description text has no explicit lineage: %+v", report.Mappings)
			}
		})
	}
}

// The current rules declare the one settings description that names the product, and every declared pair is a pair of
// d2ProductNameTexts, the mapping the product's own tests state PiG's texts through (internal/codingagent). That list
// lives in a test file, which this package cannot import, so its source is read.
func TestCompactionSettingsRulesDeclareOnlyD2ProductNamePairs(t *testing.T) {
	const lineage = "docs/parity/DIVERGENCES.md#D2"
	path, err := filepath.Abs("../../../internal/codingagent/d2_product_name_test.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var productPairs [][2]string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "d2ProductNameTexts" || len(spec.Values) != 1 {
			return true
		}
		list, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatalf("d2ProductNameTexts is not a composite literal in %s", path)
		}
		for _, element := range list.Elts {
			row, ok := element.(*ast.CompositeLit)
			if !ok || len(row.Elts) != 2 {
				t.Fatalf("d2ProductNameTexts holds a row that is not a pair in %s", path)
			}
			var pair [2]string
			for i, cell := range row.Elts {
				literal, ok := cell.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("d2ProductNameTexts holds a cell that is not a string literal in %s", path)
				}
				if pair[i], err = strconv.Unquote(literal.Value); err != nil {
					t.Fatal(err)
				}
			}
			productPairs = append(productPairs, pair)
		}
		return false
	})
	if len(productPairs) == 0 {
		t.Fatalf("no d2ProductNameTexts pairs read from %s", path)
	}
	texts := CompactionSettingsRules().TableDescriptionTexts
	if len(texts) != 1 || len(texts["table:settings-selector"]) != 1 {
		t.Fatalf("description texts = %+v, want only table:settings-selector#show-hardware-cursor", texts)
	}
	text, ok := texts["table:settings-selector"]["show-hardware-cursor"]
	if !ok {
		t.Fatalf("description texts = %+v, want show-hardware-cursor", texts)
	}
	if text.Lineage != lineage {
		t.Fatalf("lineage = %q, want %q", text.Lineage, lineage)
	}
	if !slices.Contains(productPairs, [2]string{text.Pi, text.Pig}) {
		t.Fatalf("declared pair %q -> %q is not a pair of d2ProductNameTexts %q", text.Pi, text.Pig, productPairs)
	}
}
