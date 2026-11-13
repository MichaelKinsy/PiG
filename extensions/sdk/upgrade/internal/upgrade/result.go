// SPDX-License-Identifier: MIT

// pig additive (D109): Pi has no SDK to drift from; see docs/additive-features.md.

package upgrade

import (
	"maps"
	"slices"
	"sort"
)

// LoadOptions selects the packages to read.
type LoadOptions struct {
	// Dir is where `go list` runs: the extension's module, or a generated
	// module that imports it. Its go.mod must resolve the current SDK.
	Dir string
	// Roots are the extension's source directories. A package in one is type
	// checked from source and may be rewritten; every other package is read
	// from the compiler's export data.
	Roots []string
	// Flags are go list flags placed before the patterns, such as -modfile.
	Flags []string
	// Patterns are the packages to list. The default is "./...".
	Patterns []string
	// Command is the go command. The default is "go".
	Command string
	// Env is the environment of the go command. The default is the current one.
	Env []string
}

// Rewrite is one change the plan makes to a file.
type Rewrite struct {
	Rule   string
	Symbol string
	Line   int
}

// FileChange is one rewritten file, or one the plan adds: Created is set for a
// file with no Before.
type FileChange struct {
	Path          string
	Before, After []byte
	Rewrites      []Rewrite
	Created       bool
}

// Skipped is a use of a rewritable change that needs a hand edit.
type Skipped struct {
	Rule   string
	Symbol string
	File   string
	Line   int
	Reason string
	// Suggest is the form to write in its place, when the rule has one.
	Suggest string
}

// Result is what an upgrade does to an extension's source, and what it leaves
// for the author. The lines that Skipped, Remaining and Errors name are lines of
// the source before the rewrites: Skipped and Remaining are uses the compiler
// still rejects, and an upgrade that does not build restores that source.
type Result struct {
	// Files are the rewritten files, in path order. Nothing is written to disk.
	Files []FileChange
	// Skipped are uses the rules recognize but cannot rewrite safely.
	Skipped []Skipped
	// Remaining is the SDK drift still in the source after the rewrites,
	// classified from the compiler's errors. It includes the changes that have
	// no mechanical rewrite.
	Remaining []Drift
	// Errors are the compiler errors that are not SDK drift.
	Errors []string
}

// Changed reports whether the plan rewrites any file.
func (r *Result) Changed() bool { return len(r.Files) > 0 }

// Reply is what the analysis driver (command driver in this module) writes for one [LoadOptions]: the plan, or the error that stopped it. The pig command reads it in place of calling [Open], so it does not link the type checker.
type Reply struct {
	Result *Result
	// Error is the text of the error [Open] or [Session.Plan] returned.
	Error string
}

// Drift lists every SDK change the extension uses: those the plan rewrites,
// those it lists for the author, and those the compiler still rejects. A change
// is listed once, in symbol order, at its first use.
func (r *Result) Drift() []Drift {
	byRule := map[string]Drift{}
	note := func(rule, symbol, file string, line, count int) {
		drift, seen := byRule[symbol]
		if !seen {
			ruleInfo, _ := RuleByID(rule)
			change, _ := ChangeFor(symbol)
			drift = Drift{Rule: rule, Symbol: symbol, Old: change.Old, New: change.New, Since: change.Since, Rewrites: ruleInfo.Rewrites, File: file, Line: line}
		}
		drift.Count += count
		byRule[symbol] = drift
	}
	for _, change := range r.Files {
		for _, rewrite := range change.Rewrites {
			note(rewrite.Rule, rewrite.Symbol, change.Path, rewrite.Line, 1)
		}
	}
	remaining := map[string]bool{}
	for _, drift := range r.Remaining {
		remaining[drift.Symbol] = true
		note(drift.Rule, drift.Symbol, drift.File, drift.Line, drift.Count)
	}
	for _, skipped := range r.Skipped {
		if !remaining[skipped.Symbol] {
			note(skipped.Rule, skipped.Symbol, skipped.File, skipped.Line, 1)
		}
		remaining[skipped.Symbol] = true
	}
	// A change with a use left for the author needs a manual edit.
	for symbol := range remaining {
		drift := byRule[symbol]
		drift.Rewrites = false
		byRule[symbol] = drift
	}
	// One Drift per symbol: no two compare equal. sort.Slice shares one copy of the sort; slices.SortedFunc compiles one for this struct.
	drifts := slices.Collect(maps.Values(byRule))
	sort.Slice(drifts, func(i, j int) bool { return drifts[i].Symbol < drifts[j].Symbol })
	return drifts
}
