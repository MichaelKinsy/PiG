// SPDX-License-Identifier: MIT

// Command hermeticcheck partitions Go packages into provably hermetic pure-Go
// packages (eligible for Go's native action-hash test cache without -count=1) and
// non-hermetic packages (which MUST always run with -count=1).
//
// The classifier is strictly fail-closed: any package that directly or transitively
// imports subprocess, network, CGO, plugin, OS-environment, Pi-oracle, or external
// fixture helpers across linux/darwin/windows build constraints is classified as
// non-hermetic. When -race is set, every package is forced non-hermetic.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hermeticcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)

	root := fs.String("root", ".", "repository root directory")
	platformsFlag := fs.String("platforms", "linux/amd64,darwin/arm64,windows/amd64", "comma-separated GOOS/GOARCH targets to inspect")
	cgoFlag := fs.String("cgo", "0,1", "comma-separated CGO_ENABLED values to inspect (default: 0,1 to cover both cgo-disabled and cgo-enabled runners)")
	listHermetic := fs.Bool("list-hermetic", false, "print one provably hermetic tested package import path per line")
	listNonHermetic := fs.Bool("list-nonhermetic", false, "print one non-hermetic tested package import path per line")
	relPaths := fs.Bool("rel", false, "print relative ./pkg paths instead of full import paths for --list-*")
	summaryFlag := fs.Bool("summary", false, "print partition counts, reason histogram, and timing share")
	reasonsFlag := fs.Bool("reasons", false, "include per-package non-hermeticity reasons with --summary or --list-nonhermetic")
	jsonFlag := fs.Bool("json", false, "emit full machine-readable JSON report")
	checkFlag := fs.Bool("check", false, "verify every package passed on the command line (or in --expect-hermetic) is provably hermetic; exit 1 if any is non-hermetic")
	expectFile := fs.String("expect-hermetic", "", "path to newline-delimited list of packages expected to be hermetic (used with --check)")
	raceFlag := fs.Bool("race", false, "treat the run as a -race run (forces every package non-hermetic)")
	includeEmpty := fs.Bool("include-empty", false, "include packages without _test.go files in output")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, `Usage: hermeticcheck [flags] [package patterns...]

Partitions Go packages into provably hermetic pure-Go packages (safe for Go's
action-hash test cache without -count=1) vs non-hermetic packages (forced -count=1).
Strictly fail-closed across linux, darwin, and windows build constraints.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}

	platforms, err := parsePlatforms(*platformsFlag)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hermeticcheck: %v\n", err)
		return 2
	}
	cgoValues, err := parseCGOValues(*cgoFlag)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hermeticcheck: %v\n", err)
		return 2
	}

	patterns := fs.Args()
	var expectList []string
	if *expectFile != "" {
		data, err := os.ReadFile(*expectFile)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hermeticcheck: read %s: %v\n", *expectFile, err)
			return 2
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			expectList = append(expectList, line)
		}
	}

	if *checkFlag && len(patterns) == 0 && len(expectList) > 0 {
		patterns = expectList
	}

	sum, err := Analyze(Config{
		Root:         *root,
		Patterns:     patterns,
		Platforms:    platforms,
		IncludeEmpty: *includeEmpty || *checkFlag,
		Race:         *raceFlag,
		CGOValues:    cgoValues,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hermeticcheck: %v\n", err)
		return 2
	}

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sum); err != nil {
			_, _ = fmt.Fprintf(stderr, "hermeticcheck: encode json: %v\n", err)
			return 2
		}
		if *checkFlag {
			return checkSummary(sum, expectList, stderr)
		}
		return 0
	}

	if *checkFlag {
		return checkSummary(sum, expectList, stderr)
	}

	if *listHermetic {
		for _, p := range sum.Packages {
			if p.Hermetic {
				_, _ = fmt.Fprintln(stdout, formatPath(p, *relPaths))
			}
		}
		return 0
	}

	if *listNonHermetic {
		for _, p := range sum.Packages {
			if !p.Hermetic {
				if *reasonsFlag {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\n", formatPath(p, *relPaths), summarizeReasons(p.Reasons))
				} else {
					_, _ = fmt.Fprintln(stdout, formatPath(p, *relPaths))
				}
			}
		}
		return 0
	}

	_ = summaryFlag
	printSummary(stdout, sum, *reasonsFlag, *relPaths)
	return 0
}

func checkSummary(sum *Summary, expectList []string, stderr io.Writer) int {
	wantSet := make(map[string]bool, len(expectList))
	for _, w := range expectList {
		wantSet[w] = true
	}
	matchedExpect := make(map[string]bool, len(expectList))
	failed := 0
	for _, p := range sum.Packages {
		if len(wantSet) > 0 {
			matched := false
			if wantSet[p.ImportPath] {
				matchedExpect[p.ImportPath] = true
				matched = true
			}
			if wantSet[p.RelPath] {
				matchedExpect[p.RelPath] = true
				matched = true
			}
			if !matched {
				continue
			}
		}
		if !p.Hermetic {
			failed++
			_, _ = fmt.Fprintf(stderr, "NON-HERMETIC %s: %s\n", p.RelPath, summarizeReasons(p.Reasons))
		}
	}
	for _, w := range expectList {
		if !matchedExpect[w] {
			failed++
			_, _ = fmt.Fprintf(stderr, "UNVERIFIED %s: expected package was not analyzed\n", w)
		}
	}
	if failed > 0 {
		_, _ = fmt.Fprintf(stderr, "hermeticcheck: %d package(s) failed hermeticity guard\n", failed)
		return 1
	}
	return 0
}

func printSummary(w io.Writer, sum *Summary, includeReasons, useRel bool) {
	pctHermetic := 0.0
	if sum.TestedPackages > 0 {
		pctHermetic = 100.0 * float64(sum.HermeticPackages) / float64(sum.TestedPackages)
	}
	_, _ = fmt.Fprintf(w, "Module: %s\n", sum.Module)
	_, _ = fmt.Fprintf(w, "Platforms: %s\n", strings.Join(sum.Platforms, ", "))
	if sum.RaceForced {
		_, _ = fmt.Fprintln(w, "Mode: -race (all packages forced non-hermetic / -count=1)")
	}
	_, _ = fmt.Fprintf(w, "Total packages: %d\n", sum.TotalPackages)
	_, _ = fmt.Fprintf(w, "Tested packages: %d\n", sum.TestedPackages)
	_, _ = fmt.Fprintf(w, "  Provably hermetic pure-Go (cacheable): %d (%.1f%%)\n", sum.HermeticPackages, pctHermetic)
	_, _ = fmt.Fprintf(w, "  Non-hermetic (forced -count=1):        %d (%.1f%%)\n", sum.NonHermeticPackages, 100.0-pctHermetic)
	_, _ = fmt.Fprintf(w, "Estimated hermetic test-time share: %.1f%%\n", sum.EstimatedTimeShare*100.0)
	if sum.KnownTimingSeconds > 0 {
		_, _ = fmt.Fprintf(w, "Measured sharded timing coverage: %.1fs total (%.1fs hermetic, %.1fs non-hermetic)\n",
			sum.KnownTimingSeconds, sum.HermeticTimingSec, sum.NonHermeticTimingSec)
	}
	if sum.TimingNotes != "" {
		_, _ = fmt.Fprintf(w, "Timing basis: %s\n", sum.TimingNotes)
	}

	counts := make(map[string]int)
	for _, p := range sum.Packages {
		if !p.HasTests || p.Hermetic {
			continue
		}
		seenCode := make(map[string]bool)
		for _, r := range p.Reasons {
			if !seenCode[r.Code] {
				seenCode[r.Code] = true
				counts[r.Code]++
			}
		}
	}
	if len(counts) > 0 {
		type kv struct {
			code  string
			count int
		}
		var hist []kv
		for c, n := range counts {
			hist = append(hist, kv{c, n})
		}
		sort.Slice(hist, func(i, j int) bool {
			if hist[i].count != hist[j].count {
				return hist[i].count > hist[j].count
			}
			return hist[i].code < hist[j].code
		})
		_, _ = fmt.Fprintln(w, "\nTop non-hermetic reason codes (packages affected):")
		for _, h := range hist {
			_, _ = fmt.Fprintf(w, "  %-34s %d\n", h.code, h.count)
		}
	}

	if includeReasons {
		_, _ = fmt.Fprintln(w, "\nNon-hermetic packages:")
		for _, p := range sum.Packages {
			if !p.Hermetic {
				_, _ = fmt.Fprintf(w, "  %s: %s\n", formatPath(p, useRel), summarizeReasons(p.Reasons))
			}
		}
	}
}

func formatPath(p PackageResult, useRel bool) string {
	if useRel && p.RelPath != "" {
		return p.RelPath
	}
	return p.ImportPath
}

func summarizeReasons(reasons []Reason) string {
	if len(reasons) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var parts []string
	for _, r := range reasons {
		msg := r.Code + ": " + r.Detail
		if !seen[msg] {
			seen[msg] = true
			parts = append(parts, msg)
		}
	}
	if len(parts) > 4 {
		return strings.Join(parts[:4], "; ") + fmt.Sprintf(" (+%d more)", len(parts)-4)
	}
	return strings.Join(parts, "; ")
}

func parsePlatforms(raw string) ([]TargetPlatform, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultPlatforms, nil
	}
	var out []TargetPlatform
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		goos, goarch, ok := strings.Cut(entry, "/")
		if !ok || goos == "" || goarch == "" {
			return nil, fmt.Errorf("invalid platform %q: expected GOOS/GOARCH", entry)
		}
		out = append(out, TargetPlatform{GOOS: goos, GOARCH: goarch})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid platforms in %q", raw)
	}
	return out, nil
}

func parseCGOValues(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{"0", "1"}, nil
	}
	seen := make(map[string]bool)
	var out []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry != "0" && entry != "1" {
			return nil, fmt.Errorf("invalid cgo value %q: expected 0 or 1", entry)
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid cgo values in %q", raw)
	}
	return out, nil
}
