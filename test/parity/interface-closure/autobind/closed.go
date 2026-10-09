package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// closedCheck is trust check 1: rows closed by hand are expected to be NOT-A-GAP. Every disagreement is written out with the rule
// that fired, so each is either a rule bug or a wrong manual closure.
func closedCheck(out string, l *ledger, ds []*decision, blind bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	byReason := map[string]int{}
	byCat := map[string]int{}
	byPkg := map[string][2]int{}
	var lines, openParents []string
	closed, agree := 0, 0
	byID := map[string]*decision{}
	for _, d := range ds {
		byID[d.ID] = d
	}
	for _, d := range ds {
		row := l.mapping[d.ID]
		if row == nil || row.Disposition != "ported" {
			continue
		}
		closed++
		c := byPkg[pkgKey(d)]
		if !d.Gap {
			agree++
			c[0]++
			byPkg[pkgKey(d)] = c
			continue
		}
		hand := strings.Join(row.Targets, ",")
		if d.Reason == reasonChild && onlyOpenChildren(l, byID, d.ID) {
			// The closure is not what disagrees: every gap below it is a row the hand ledger left pending or deferred.
			openParents = append(openParents, fmt.Sprintf("%s\t%s\t%s\thand=%s", d.ID, d.Pkg, d.Detail, hand))
			agree++
			c[0]++
			byPkg[pkgKey(d)] = c
			continue
		}
		c[1]++
		byPkg[pkgKey(d)] = c
		byReason[d.Reason]++
		cat := explain(d)
		byCat[cat]++
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\thand=%s\t%s", d.ID, d.Pkg, d.Reason, d.Candidate, d.Detail, hand, cat))
	}
	sort.Strings(lines)
	name := "closed-disagreements.tsv"
	if blind {
		name = "closed-disagreements-blind.tsv"
	}
	if err := os.WriteFile(filepath.Join(out, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	sort.Strings(openParents)
	if err := os.WriteFile(filepath.Join(out, "closed-open-children.tsv"), []byte(strings.Join(openParents, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("closed parents whose only gaps are open child rows (not disagreements): %d\n", len(openParents))
	fmt.Printf("closed rows: %d, agree (not a gap): %d, disagree (gap): %d, agreement %.1f%%\n", closed, agree, closed-agree, 100*float64(agree)/float64(max(closed, 1)))
	var rs []string
	for r := range byReason {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	for _, r := range rs {
		fmt.Printf("  %-20s %d\n", r, byReason[r])
	}
	var cs []string
	for k := range byCat {
		cs = append(cs, k)
	}
	sort.Strings(cs)
	fmt.Println("explanations:")
	for _, k := range cs {
		fmt.Printf("  %-30s %d\n", k, byCat[k])
	}
	var ps []string
	for p := range byPkg {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	for _, p := range ps {
		fmt.Printf("  package %-14s agree %d, disagree %d\n", p, byPkg[p][0], byPkg[p][1])
	}
	return nil
}

// explain names why a hand-closed row can disagree with the rules. The categories are a fixed function of the rule that fired.
func explain(d *decision) string {
	switch d.Reason {
	case reasonChild:
		return "derived-from-child"
	case reasonNoSymbol, reasonMember:
		return "hand-target-not-followable"
	case reasonExercise:
		return "no-test-or-caller-for-member"
	case reasonSignature:
		return "hand-accepted-differing-signature"
	case reasonType:
		return "hand-accepted-differing-type"
	}
	switch {
	case strings.Contains(d.Detail, "T9:"):
		return "undocumented-type-mapping"
	case strings.Contains(d.Detail, "S5:"):
		return "multi-value-result"
	}
	return "shape-outside-the-rules"
}

// onlyOpenChildren reports whether every root-cause gap below the row (a gap that is not itself a child-gap) is a row the ledger
// has not closed. A row with no root-cause gap below it does not qualify.
func onlyOpenChildren(l *ledger, byID map[string]*decision, id string) bool {
	roots, open := 0, 0
	var walk func(string)
	walk = func(parent string) {
		for _, c := range l.children[parent] {
			d := byID[c.ID]
			if d == nil || !d.Gap {
				continue
			}
			if d.Reason == reasonChild {
				walk(c.ID)
				continue
			}
			roots++
			if row := l.mapping[c.ID]; row == nil || row.Disposition != "ported" {
				open++
			}
		}
	}
	walk(id)
	return roots > 0 && roots == open
}
