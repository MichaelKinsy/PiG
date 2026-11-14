// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// driftRow is the part of a Drift that Result.Drift decides.
type driftRow struct {
	Symbol, File string
	Line, Count  int
	Rewrites     bool
}

func driftRows(drifts []upgrade.Drift) []driftRow {
	rows := make([]driftRow, len(drifts))
	for i, drift := range drifts {
		rows[i] = driftRow{drift.Symbol, drift.File, drift.Line, drift.Count, drift.Rewrites}
	}
	return rows
}

// Result.Drift lists a symbol once however many rewrites, remaining uses and skipped uses name it, so no two of the
// values it sorts have one key: the order is the symbols' order whatever sort runs, and a plan with no drift gives nil.
func TestResultDriftListsEachSymbolOnceInSymbolOrder(t *testing.T) {
	getters, ok := upgrade.RuleByID(upgrade.RuleContextGetters)
	if !ok {
		t.Fatal("no rule for the Context getters")
	}
	result := &upgrade.Result{
		Files: []upgrade.FileChange{
			{Path: "b.go", Rewrites: []upgrade.Rewrite{
				{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetSessionID", Line: 9},
				{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetCwd", Line: 12},
			}},
			{Path: "a.go", Rewrites: []upgrade.Rewrite{{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetSessionID", Line: 3}}},
		},
		Remaining: []upgrade.Drift{
			{Rule: upgrade.RuleContextUsage, Symbol: "ContextUsage.Tokens", File: "c.go", Line: 4, Count: 2},
			{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetCwd", File: "c.go", Line: 7, Count: 1},
		},
		Skipped: []upgrade.Skipped{
			{Rule: upgrade.RuleContextGetters, Symbol: "Context.ConfigHome", File: "d.go", Line: 1},
			{Rule: upgrade.RuleContextUsage, Symbol: "ContextUsage.Tokens", File: "d.go", Line: 2},
			{Rule: upgrade.RuleContextGetters, Symbol: "Context.ConfigHome", File: "e.go", Line: 5},
		},
	}
	want := []driftRow{
		{"Context.ConfigHome", "d.go", 1, 1, false},
		{"Context.GetCwd", "b.go", 12, 2, false},
		{"Context.GetSessionID", "b.go", 9, 2, getters.Rewrites},
		{"ContextUsage.Tokens", "c.go", 4, 2, false},
	}
	if got := driftRows(result.Drift()); !slices.Equal(got, want) {
		t.Fatalf("Drift() = %+v\nwant      %+v", got, want)
	}
	if got := (&upgrade.Result{}).Drift(); got != nil {
		t.Fatalf("Drift() of an empty plan = %#v, want nil", got)
	}
}

// Forty symbols, more than the twelve an insertion sort takes, come out in symbol order from any order of uses.
func TestResultDriftSortsManySymbols(t *testing.T) {
	result := &upgrade.Result{}
	var want []string
	for i := range 40 {
		want = append(want, fmt.Sprintf("S%02d", i))
		// 17 and 40 share no factor: every symbol once, in a scattered order.
		result.Remaining = append(result.Remaining, upgrade.Drift{Symbol: fmt.Sprintf("S%02d", i*17%40), Count: 1})
	}
	var got []string
	for _, drift := range result.Drift() {
		got = append(got, drift.Symbol)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Drift() symbols = %v\nwant %v", got, want)
	}
}
