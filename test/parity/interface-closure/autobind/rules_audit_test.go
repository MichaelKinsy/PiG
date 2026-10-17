package main

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// TestRulesFamilyAudit is trust check 1 for the optionality and union rule families: it applies the rules to the rows closed by hand
// (disposition ported, a recorded Go target) and writes the disagreements to $AUTOBIND_RULES_AUDIT/rules-optional-unions.tsv. It needs
// the whole module type-checked, so it runs only when AUTOBIND_RULES_AUDIT names an output directory.
func TestRulesFamilyAudit(t *testing.T) {
	out := os.Getenv("AUTOBIND_RULES_AUDIT")
	if out == "" {
		t.Skip("set AUTOBIND_RULES_AUDIT to an output directory")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	l, err := loadLedger(root, pigversion.UpstreamVersion)
	if err != nil {
		t.Fatal(err)
	}
	reach, err := loadReach(root, "")
	if err != nil {
		t.Fatal(err)
	}
	ix, err := buildIndex(root, reach, []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	renames := renameTable{}
	if err := readJSON(filepath.Join(root, renamesFile), &renames); err != nil {
		t.Fatal(err)
	}
	det := newDetector(ix, l, renames, tsAliases(root))

	var lines []string
	tally := map[string]int{}
	record := func(id, family string, v rules.Finding, target string) {
		outcome := map[rules.Tri]string{rules.Yes: "yes", rules.No: "no", rules.Unknown: "unknown"}[v.Result]
		tally[family+" "+outcome+" "+v.Rule]++
		if v.Result != rules.Yes {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", id, family, outcome, v.Rule, target, strings.ReplaceAll(v.Why, "\t", " ")))
		}
	}
	for _, e := range l.entries {
		row := l.mapping[e.ID]
		if row == nil || row.Disposition != "ported" || len(row.Targets) == 0 {
			continue
		}
		parts := parseID(e.ID)
		switch {
		case e.Role == "property" && parts.Member != "":
			m := det.renamedMember(row.Targets[0], e.Shape.Name)
			if m == nil {
				continue
			}
			v, ok := fieldVerdict(ix, m, e)
			if ok {
				record(e.ID, "optional", v, row.Targets[0])
			}
		case e.Role == "" && e.Kind == "type-alias":
			body := det.aliases.lookup(parts.Pkg, e.Name)
			if body == nil {
				continue
			}
			u, ok := rules.ParseLiteralUnion(*body, func(n string) *string { return det.aliases.lookup(parts.Pkg, n) })
			if !ok {
				continue
			}
			for _, s := range det.candidates(e, parts.Pkg, "type") {
				tn, isType := s.Obj.(*types.TypeName)
				if !isType {
					continue
				}
				record(e.ID, "literal-union", rules.CheckLiteralUnion(u, tn.Type(), rules.ConstsOf(tn.Type()), rules.StringUnionOptions{}), row.Targets[0])
				break
			}
		}
	}
	sort.Strings(lines)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "rules-optional-unions.tsv"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-32s %d", k, tally[k])
	}
}

// fieldVerdict applies the optionality rules to the Go struct field m for the upstream property e.
func fieldVerdict(ix *index, m *sym, e *upstreamEntry) (rules.Finding, bool) {
	v, ok := m.Obj.(*types.Var)
	if !ok || m.Owner == "" {
		return rules.Finding{}, false
	}
	for _, s := range ix.topLevel([]string{m.Dir}) {
		if s.Kind != "type" || s.Name != m.Owner {
			continue
		}
		tag, found := fieldTag(s.Obj.Type(), v, 0)
		if !found {
			return rules.Finding{}, false
		}
		up := rules.ParseOptionality(e.Shape.Optional, e.Shape.Type)
		return rules.CheckOptionalProperty(up, rules.GoMember{Type: v.Type(), Tag: tag}, rules.OptionalOptions{}), true
	}
	return rules.Finding{}, false
}

// fieldTag finds the struct tag of field v in the struct t or its embedded structs.
func fieldTag(t types.Type, v *types.Var, depth int) (string, bool) {
	st, ok := t.Underlying().(*types.Struct)
	if !ok || depth > 4 {
		return "", false
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Pos() == v.Pos() && f.Name() == v.Name() && f.Pkg() == v.Pkg() || f.Origin() == v.Origin() && f.Pos() != token.NoPos && f.Pos() == v.Pos() {
			return st.Tag(i), true
		}
		if f.Embedded() {
			ft := f.Type()
			if p, ok := ft.(*types.Pointer); ok {
				ft = p.Elem()
			}
			if tag, ok := fieldTag(ft, v, depth+1); ok {
				return tag, true
			}
		}
	}
	return "", false
}
