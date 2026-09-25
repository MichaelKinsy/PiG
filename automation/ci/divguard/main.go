// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command divguard fails CI on hidden, arbitrary divergences from Pi: invented
// limits, timeouts and defaults, silent exits, swallowed errors and dropped
// events that no DIVERGENCES.md entry records. It exists because a silent
// MaxTurns=100 cap once shipped with nothing flagging it.
//
// Each check is a syntactic pattern over the Go sources. Every current hit is
// listed in baseline.toml with the audit finding (AGENT-01, AI-07, ...) or
// divergence (D<N>) that owns it. The baseline is a ratchet: a hit it does not
// list fails, and an entry that no longer matches fails too, so fixing a
// finding means deleting its entry in the same change.
//
// A hit is allowed at the source by a recorded divergence marker,
// `// pig divergence (D<N>): ...`, on the flagged line or the line above.
// A literal that matches upstream is allowed by `// upstream: <file>:<symbol>`
// in the same place; the file must exist in .upstream/current, the symbol must
// appear in it, and the literal's value must appear in it too.
//
// Run it with `make divergence-guard`; -list prints every hit, and -print
// prints them as baseline entries.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checks is every divergence-guard check, in report order.
var checks = []check{magicLiteral, cancelDropSend, defaultDropSend, streamParseContinue, stopReasonSuccess, handRolledSSE, bareRecover, discardedIOError, agentRunOutsideSession, hookFailOpen, errorStatusSubstring}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "", "baseline file (default <root>/automation/ci/divguard/baseline.toml)")
	upstream := flag.String("upstream", "", "upstream mirror (default <root>/.upstream/current)")
	list := flag.Bool("list", false, "print every hit, baselined or not")
	printBaseline := flag.Bool("print", false, "print every hit as a baseline entry, keeping known findings")
	flag.Parse()
	if *baselinePath == "" {
		*baselinePath = filepath.Join(*root, "automation", "ci", "divguard", "baseline.toml")
	}
	if *upstream == "" {
		*upstream = filepath.Join(*root, ".upstream", "current")
	}
	os.Exit(run(*root, *baselinePath, *upstream, *list, *printBaseline))
}

func run(root, baselinePath, upstream string, list, printBaseline bool) int {
	e, err := loadEnv(root, upstream)
	if err != nil {
		fmt.Fprintln(os.Stderr, "divergence-guard:", err)
		return 2
	}
	hits, problems, err := scan(root, e, checks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "divergence-guard:", err)
		return 2
	}
	base, err := loadBaseline(baselinePath, e, checks)
	if err != nil && !printBaseline {
		fmt.Fprintln(os.Stderr, "divergence-guard:", err)
		return 2
	}
	if printBaseline {
		findings := map[string]string{}
		for k, b := range base {
			findings[k] = b.Finding
		}
		fmt.Print(renderBaseline(hits, findings))
		return 0
	}
	if list {
		for _, h := range hits {
			fmt.Printf("%s:%d: [%s] %s (%s)\n    %s\n", h.File, h.Line, h.Check, h.Message, base[h.Key()].Finding, display(h.Snippet))
		}
	}
	unknown, stale := compare(hits, base)
	failed := false
	if len(problems) > 0 {
		failed = true
		fmt.Println("FAIL: invalid allow markers:")
		for _, p := range problems {
			fmt.Println("  -", p)
		}
	}
	if len(unknown) > 0 {
		failed = true
		fmt.Println("FAIL: new divergence-guard hits. Fix them to match upstream, or record a numbered divergence and mark the line `// pig divergence (D<N>): ...`, or, for a literal that upstream really uses, mark it `// upstream: <file>:<symbol>`:")
		for _, h := range unknown {
			fmt.Printf("  %s:%d: [%s] %s\n      %s\n", h.File, h.Line, h.Check, h.Message, display(h.Snippet))
		}
	}
	if len(stale) > 0 {
		failed = true
		fmt.Printf("FAIL: %s lists hits that no longer match; remove the fixed entries:\n", baselinePath)
		for _, s := range stale {
			fmt.Println("  -", s)
		}
	}
	if failed {
		return 1
	}
	fmt.Printf("divergence-guard: %d checks, %d baselined hits (%s)\n", len(checks), len(hits), countsByCheck(hits))
	return 0
}

func countsByCheck(hits []Hit) string {
	n := map[string]int{}
	for _, c := range checks {
		n[c.Name] = 0
	}
	for _, h := range hits {
		n[h.Check]++
	}
	var parts []string
	for k, v := range n {
		parts = append(parts, fmt.Sprintf("%s %d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
