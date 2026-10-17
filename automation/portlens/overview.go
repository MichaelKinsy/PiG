// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Overview is the data of the overview view.
type Overview struct {
	Pin       Pin
	Head      string
	HeadDate  string
	PiMirror  bool
	Ledgers   []LedgerSummary
	Ratchets  []Ratchet
	Trends    []Trend
	Flip      []FlipItem
	FlipLabel string
}

// PkgCounts is the row counts of one package of a pin ledger.
type PkgCounts struct {
	Index               int
	Pkg                 string
	Total               int
	Ported, Derived     int
	DesignedOut, Gap    int
	GapBaseline         int
	ProofBaseline       int
	ClassCounts         map[string]int
	UnprovenRead        int
	ProofReadForPackage bool
}

// LedgerSummary is the overview of one pin ledger.
type LedgerSummary struct {
	Version     string
	Committed   bool
	Role        string
	Total       int
	Ported      int
	Derived     int
	DesignedOut int
	Gap         int
	GapBaseline int
	Packages    []PkgCounts
	Classes     []ClassCount
	ProofRead   bool
}

// ClassCount is the number of closed rows that require one proof class.
type ClassCount struct {
	Class    string
	Name     string
	Required int
	Unproven int
}

// Ratchet is one count that may only go down.
type Ratchet struct {
	Name    string
	Source  string
	Count   int
	Unit    string
	Command string
	Note    string
}

// Trend is a numeric series with its labels.
type Trend struct {
	Name   string
	Source string
	Points []TrendPoint
}

// TrendPoint is one sample of a trend.
type TrendPoint struct {
	Label string
	Value int
}

// FlipItem is one surface of the flip from the committed pin to the follow target.
type FlipItem struct {
	Name    string
	Done    int
	Pending int
	Source  string
}

func classLabel(c string) string {
	switch c {
	case ClassDesignOut:
		return "Designed out (reason and audit)"
	case ClassWire:
		return "Wire (conformance row)"
	case ClassRegistration:
		return "Registration (field survival)"
	case ClassUnion:
		return "Union (exhaustive handling)"
	case ClassBehaviour:
		return "Behaviour (mutation-checked test)"
	}
	return "None (data shape)"
}

var classOrder = []string{ClassDesignOut, ClassWire, ClassRegistration, ClassUnion, ClassBehaviour, ClassNone}

func summarize(pl *PinLedger, role string) LedgerSummary {
	ls := LedgerSummary{Version: pl.Version, Committed: pl.Committed, Role: role, Total: pl.Total, GapBaseline: pl.GapBaselineRows, ProofRead: pl.ProofRead}
	byPkg := map[string]*PkgCounts{}
	classes := map[string]*ClassCount{}
	for _, c := range classOrder {
		classes[c] = &ClassCount{Class: c, Name: classLabel(c)}
	}
	for _, p := range pl.Packages {
		byPkg[p] = &PkgCounts{Index: len(byPkg), Pkg: p, GapBaseline: pl.GapBaseline[p], ProofBaseline: pl.ProofBaseline[normalizePkg(p)], ClassCounts: map[string]int{}}
	}
	for _, r := range pl.Rows {
		pc := byPkg[r.Pkg]
		pc.Total++
		switch r.Status {
		case StatusPorted:
			pc.Ported++
			ls.Ported++
		case StatusDerived:
			pc.Derived++
			ls.Derived++
		case StatusDesignedOut:
			pc.DesignedOut++
			ls.DesignedOut++
		case StatusGap:
			pc.Gap++
			ls.Gap++
		}
		if r.Status != StatusGap {
			classes[r.Class].Required++
			pc.ClassCounts[r.Class]++
			if r.Unproven != "" {
				classes[r.Class].Unproven++
				pc.UnprovenRead++
			}
		}
	}
	for _, p := range pl.Packages {
		ls.Packages = append(ls.Packages, *byPkg[p])
	}
	for _, c := range classOrder {
		ls.Classes = append(ls.Classes, *classes[c])
	}
	return ls
}

// normalizePkg maps a ledger package name to the proof baseline's name.
func normalizePkg(p string) string { return p }

// countEntries counts the entries of a TOML ratchet file: array-of-table headers, or the keys of a named table.
func countTomlEntries(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	n := 0
	for ln := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "[[") {
			n++
		}
	}
	return n
}

func countLines(path string) int {
	n := 0
	for _, l := range readLines(path) {
		if l != "" && !strings.HasPrefix(l, "#") {
			n++
		}
	}
	return n
}

func loadRatchets(root string, committed string) []Ratchet {
	var out []Ratchet
	add := func(r Ratchet) { out = append(out, r) }
	gaps := filepath.Join("test", "parity", "interface-closure", "gaps")
	add(Ratchet{Name: "Interface gap baseline", Source: filepath.ToSlash(filepath.Join(gaps, "interface-gaps-v"+committed+".tsv")), Count: len(readLines(filepath.Join(root, gaps, "interface-gaps-v"+committed+".tsv"))), Unit: "rows", Command: "make interface-gaps", Note: "A package may not grow past its baseline."})
	sum := 0
	for _, ln := range readLines(filepath.Join(root, gaps, "proof-class-baseline.tsv")) {
		if _, n, ok := strings.Cut(ln, "\t"); ok {
			v, _ := strconv.Atoi(n)
			sum += v
		}
	}
	add(Ratchet{Name: "Proof-class unproven baseline", Source: filepath.ToSlash(filepath.Join(gaps, "proof-class-baseline.tsv")), Count: sum, Unit: "rows", Command: "make ledger-check", Note: "Closed rows whose required proof class has no proof yet."})
	add(Ratchet{Name: "PORT_MAP unproven ticks", Source: "test/parity/portmap-unproven-baseline.txt", Count: countLines(filepath.Join(root, "test/parity/portmap-unproven-baseline.txt")), Unit: "ticks", Command: "make portmap-check"})
	for _, r := range []struct{ name, rel, cmd, unit string }{
		{"Divergence guard", "automation/ci/divguard/baseline.toml", "make divergence-guard", "hits"},
		{"Port lint", "automation/ci/portlint/baseline.toml", "make port-lint", "findings"},
		{"Any trap", "automation/ci/anytrap/baseline.toml", "make any-trap", "findings"},
		{"Allocation baseline", "automation/ci/allocbase/baseline.toml", "make alloc-baseline", "benchmarks"},
	} {
		add(Ratchet{Name: r.name, Source: r.rel, Count: countTomlEntries(filepath.Join(root, r.rel)), Unit: r.unit, Command: r.cmd})
	}
	var rat struct {
		Ceilings map[string]any `toml:"ceilings"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "automation/perf/ratchet.toml"), &rat); err == nil {
		add(Ratchet{Name: "Performance ceilings", Source: "automation/perf/ratchet.toml", Count: len(rat.Ceilings), Unit: "ceilings", Command: "make perf-ratchet-check", Note: "Counts of allocations, bytes and descriptors; a ceiling only moves down."})
	}
	b := loadBaselineEntries(root)
	add(Ratchet{Name: "Durable contract baseline", Source: b.Path, Count: b.Entries, Unit: "known differences", Command: "make durable-contract", Note: "Differential rows that differ from Pi today."})
	add(Ratchet{Name: "Known parity gaps", Source: "test/parity/known-gaps.toml", Count: countTomlEntries(filepath.Join(root, "test/parity/known-gaps.toml")), Unit: "gaps", Command: "make parity"})
	return out
}

var (
	totalsGapRe   = regexp.MustCompile(`GAP (\d+) of (\d+)`)
	totalsGapsRe  = regexp.MustCompile(`(\d+) gaps, baseline (\d+)`)
	totalsRatioRe = regexp.MustCompile(`TOTALS (\d+)/(\d+)`)
	totalsPinRe   = regexp.MustCompile(`\[Pi ([0-9][0-9a-z.+-]*)`)
	totalsTimeRe  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})`)
)

// parseTotalsLog reads gap counts from a ledger totals log. Only the pin label, the time and the numbers are kept.
func parseTotalsLog(path string) *Trend {
	lines := readLines(path)
	if len(lines) == 0 {
		return nil
	}
	t := &Trend{Name: "Ledger GAP per recorded totals line", Source: "ledger totals log"}
	for _, ln := range lines {
		if !strings.Contains(ln, "TOTALS") {
			continue
		}
		label := ""
		if m := totalsTimeRe.FindStringSubmatch(ln); m != nil {
			label = m[1]
		}
		if m := totalsPinRe.FindStringSubmatch(ln); m != nil {
			label += " Pi " + m[1]
		}
		var v int
		switch {
		case totalsGapRe.MatchString(ln):
			m := totalsGapRe.FindStringSubmatch(ln)
			atoi(m[1], &v)
		case totalsGapsRe.MatchString(ln):
			m := totalsGapsRe.FindStringSubmatch(ln)
			atoi(m[1], &v)
		case totalsRatioRe.MatchString(ln):
			m := totalsRatioRe.FindStringSubmatch(ln)
			var done, total int
			atoi(m[1], &done)
			atoi(m[2], &total)
			v = total - done
		default:
			continue
		}
		t.Points = append(t.Points, TrendPoint{Label: strings.TrimSpace(label), Value: v})
	}
	t.Points = downsample(t.Points, 120)
	if len(t.Points) == 0 {
		return nil
	}
	return t
}

// gitGapTrend reads the number of rows of a gap baseline file at each commit that changed it.
func gitGapTrend(root, rel, name string) *Trend {
	out, err := gitOut(root, "log", "-80", "--abbrev=10", "--format=%h %cs", "--", rel)
	if err != nil || out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	t := &Trend{Name: name, Source: "git history of " + rel}
	for _, line := range slices.Backward(lines) {
		h, date, _ := strings.Cut(line, " ")
		blob, err := gitOut(root, "show", h+":"+rel)
		if err != nil {
			continue
		}
		n := 0
		if blob != "" {
			n = strings.Count(blob, "\n") + 1
		}
		t.Points = append(t.Points, TrendPoint{Label: date + " " + h, Value: n})
	}
	if len(t.Points) < 2 {
		return nil
	}
	return t
}

func loadFlip(root string, target string) []FlipItem {
	var out []FlipItem
	// upstream-sync files
	var us struct {
		Files []struct {
			Disposition string `toml:"disposition"`
		} `toml:"files"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "test", "parity", "upstream-sync", "v"+target+".toml"), &us); err == nil {
		it := FlipItem{Name: "Upstream source files changed by the pin", Source: "test/parity/upstream-sync/v" + target + ".toml"}
		for _, f := range us.Files {
			if f.Disposition == "pending" {
				it.Pending++
			} else {
				it.Done++
			}
		}
		out = append(out, it)
	}
	var tm struct {
		Entries []struct {
			Disposition string `json:"disposition"`
		} `json:"entries"`
	}
	if err := readJSON(filepath.Join(root, "test", "parity", "interfaces", "test-mapping-v"+target+".json"), &tm); err == nil {
		it := FlipItem{Name: "Upstream test files", Source: "test/parity/interfaces/test-mapping-v" + target + ".json"}
		for _, e := range tm.Entries {
			if e.Disposition == "pending" {
				it.Pending++
			} else {
				it.Done++
			}
		}
		out = append(out, it)
	}
	var bm struct {
		Mappings []struct {
			Disposition string `json:"disposition"`
		} `json:"mappings"`
		RenderMappings []struct {
			Disposition string `json:"disposition"`
		} `json:"renderMappings"`
	}
	if err := readJSON(filepath.Join(root, "test", "parity", "interfaces", "behavior-input-mapping-v"+target+".json"), &bm); err == nil {
		it := FlipItem{Name: "Behavior inputs and render surfaces", Source: "test/parity/interfaces/behavior-input-mapping-v" + target + ".json"}
		for _, e := range bm.Mappings {
			if e.Disposition == "pending" {
				it.Pending++
			} else {
				it.Done++
			}
		}
		for _, e := range bm.RenderMappings {
			if e.Disposition == "pending" {
				it.Pending++
			} else {
				it.Done++
			}
		}
		out = append(out, it)
	}
	return out
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// downsample keeps at most n points spread evenly over the series, always including the first and the last.
func downsample(p []TrendPoint, n int) []TrendPoint {
	if len(p) <= n || n < 2 {
		return p
	}
	out := make([]TrendPoint, 0, n)
	for i := range n {
		out = append(out, p[i*(len(p)-1)/(n-1)])
	}
	return out
}
