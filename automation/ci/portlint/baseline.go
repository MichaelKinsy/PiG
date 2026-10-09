// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/automation/ci/portlint/checks"
)

// baselineEntry is one known finding, keyed by check, file, enclosing
// declaration and the flagged source line, not by line number, so unrelated
// edits do not churn the file.
type baselineEntry struct {
	Check   string `toml:"check"`
	File    string `toml:"file"`
	Func    string `toml:"func"`
	Snippet string `toml:"snippet"`
	Count   int    `toml:"count,omitempty"`
}

func (e baselineEntry) key() string { return e.Check + "|" + e.File + "|" + e.Func + "|" + e.Snippet }

func (e baselineEntry) count() int {
	if e.Count == 0 {
		return 1
	}
	return e.Count
}

type baselineFile struct {
	Hit []baselineEntry `toml:"hit"`
}

func loadBaseline(path string) (map[string]baselineEntry, error) {
	var b baselineFile
	if _, err := toml.DecodeFile(path, &b); err != nil {
		if os.IsNotExist(err) {
			return map[string]baselineEntry{}, nil
		}
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	known := map[string]bool{}
	for _, c := range checks.All() {
		known[c.Analyzer.Name] = true
	}
	out := map[string]baselineEntry{}
	for _, h := range b.Hit {
		if !known[h.Check] {
			return nil, fmt.Errorf("baseline entry for unknown check %q in %s", h.Check, h.File)
		}
		if _, dup := out[h.key()]; dup {
			return nil, fmt.Errorf("baseline lists %s twice; use count", h.key())
		}
		out[h.key()] = h
	}
	return out, nil
}

// compare returns the hits the baseline does not cover and the baseline entries with no matching hit.
func compare(hits []Hit, base map[string]baselineEntry) (unknown []Hit, stale []string) {
	n := map[string]int{}
	for _, h := range hits {
		n[h.Key()]++
		if n[h.Key()] > base[h.Key()].count() || base[h.Key()].Check == "" {
			unknown = append(unknown, h)
		}
	}
	for k, e := range base {
		if n[k] < e.count() {
			stale = append(stale, fmt.Sprintf("%s (baseline %d, found %d)", k, e.count(), n[k]))
		}
	}
	sort.Strings(stale)
	return unknown, stale
}

func renderBaseline(hits []Hit) string {
	var sb strings.Builder
	sb.WriteString("# SPDX-License-Identifier: MIT\n#\n")
	sb.WriteString("# Port-lint ratchet (automation/ci/portlint, `make port-lint`). Findings may only shrink:\n# a fix deletes its entry in the same change; never add an entry for new code.\n# Regenerate the whole file with `go run ./automation/ci/portlint -print` only when removing entries.\n")
	counts := map[string]int{}
	var order []Hit
	for _, h := range hits {
		if counts[h.Key()] == 0 {
			order = append(order, h)
		}
		counts[h.Key()]++
	}
	for _, h := range order {
		fmt.Fprintf(&sb, "\n[[hit]]\ncheck = %s\nfile = %s\nfunc = %s\nsnippet = %s\n", q(h.Check), q(h.File), q(h.Func), q(h.Snippet))
		if c := counts[h.Key()]; c > 1 {
			fmt.Fprintf(&sb, "count = %d\n", c)
		}
	}
	return sb.String()
}

func q(s string) string { return strconv.Quote(s) }

func printReport(hits []Hit) {
	fmt.Println(countsTable(hits))
	for _, h := range hits {
		fmt.Printf("%s:%d: [%s] %s\n    %s\n", h.File, h.Line, h.Check, h.Message, h.Snippet)
	}
}

func countsLine(hits []Hit) string {
	n := map[string]int{}
	for _, h := range hits {
		n[h.Check]++
	}
	var parts []string
	for _, c := range checks.All() {
		parts = append(parts, fmt.Sprintf("%s=%d", c.Analyzer.Name, n[c.Analyzer.Name]))
	}
	return strings.Join(parts, " ")
}

func countsTable(hits []Hit) string {
	n := map[string]int{}
	for _, h := range hits {
		n[h.Check]++
	}
	var sb strings.Builder
	sb.WriteString("| check | severity | findings |\n|---|---|---:|\n")
	for _, c := range checks.All() {
		fmt.Fprintf(&sb, "| %s | %s | %d |\n", c.Analyzer.Name, c.Severity, n[c.Analyzer.Name])
	}
	fmt.Fprintf(&sb, "| total | | %d |\n", len(hits))
	return sb.String()
}
