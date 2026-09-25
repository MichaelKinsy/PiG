// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// baselineEntry is one known hit. It is keyed by check, file, enclosing
// declaration and the flagged source line, not by line number, so unrelated
// edits do not churn the file.
type baselineEntry struct {
	Check   string `toml:"check"`
	File    string `toml:"file"`
	Func    string `toml:"func"`
	Snippet string `toml:"snippet"`
	Finding string `toml:"finding"`
	Count   int    `toml:"count,omitempty"`
}

func (e baselineEntry) key() string { return e.Check + "|" + e.File + "|" + e.Func + "|" + e.Snippet }

func (e baselineEntry) count() int {
	if e.Count == 0 {
		return 1
	}
	return e.Count
}

// findingEntry names the owner of a finding so each open hit is tied to the
// slice that fixes it.
type findingEntry struct {
	ID      string `toml:"id"`
	Owner   string `toml:"owner"`
	Summary string `toml:"summary"`
}

type baseline struct {
	Finding []findingEntry  `toml:"finding"`
	Hit     []baselineEntry `toml:"hit"`
}

// findingRe accepts an audit finding id from the divergence hunt reports
// (AGENT-01, AI-07, ...), a GUARD-NN finding first reported by this guard,
// or a recorded divergence D<N>.
var findingRe = regexp.MustCompile(`^(?:(?:AGENT|AI|EXT|MODES|TOOL|CFG|RES|MAIN|TUI|GUARD)-[0-9]{2}|D[0-9]+)$`)

func loadBaseline(path string, e *env, checks []check) (map[string]baselineEntry, error) {
	var b baseline
	if _, err := toml.DecodeFile(path, &b); err != nil {
		if os.IsNotExist(err) {
			return map[string]baselineEntry{}, nil
		}
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	known := map[string]bool{}
	for _, c := range checks {
		known[c.Name] = true
	}
	out := map[string]baselineEntry{}
	var errs []string
	defined := map[string]bool{}
	used := map[string]bool{}
	for _, f := range b.Finding {
		switch {
		case !findingRe.MatchString(f.ID):
			errs = append(errs, fmt.Sprintf("finding %q: id must be an audit id (AGENT-01), GUARD-NN or D<N>", f.ID))
		case f.Owner == "" || f.Summary == "":
			errs = append(errs, fmt.Sprintf("finding %s: owner and summary are required", f.ID))
		case defined[f.ID]:
			errs = append(errs, fmt.Sprintf("finding %s: defined twice", f.ID))
		}
		defined[f.ID] = true
	}
	for i, h := range b.Hit {
		used[h.Finding] = true
		if !defined[h.Finding] && findingRe.MatchString(h.Finding) {
			errs = append(errs, fmt.Sprintf("entry %d (%s %s %s): finding %s has no [[finding]] entry", i+1, h.Check, h.File, h.Func, h.Finding))
		}
		where := fmt.Sprintf("entry %d (%s %s %s)", i+1, h.Check, h.File, h.Func)
		switch {
		case !known[h.Check]:
			errs = append(errs, where+": unknown check")
		case h.File == "" || h.Func == "" || h.Snippet == "":
			errs = append(errs, where+": file, func and snippet are required")
		case !findingRe.MatchString(h.Finding):
			errs = append(errs, where+": finding must be an audit id (AGENT-01), GUARD-NN or D<N>, got "+fmt.Sprintf("%q", h.Finding))
		case strings.HasPrefix(h.Finding, "D") && !e.Divergences[h.Finding]:
			errs = append(errs, where+": "+h.Finding+" is not recorded in DIVERGENCES.md")
		case h.Count < 0 || h.Count == 1:
			errs = append(errs, where+": count is omitted for one hit and must be >= 2 otherwise")
		}
		if _, dup := out[h.key()]; dup {
			errs = append(errs, where+": duplicate entry; use count")
		}
		out[h.key()] = h
	}
	for _, f := range b.Finding {
		if !used[f.ID] {
			errs = append(errs, fmt.Sprintf("finding %s has no hits left; remove it", f.ID))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("baseline %s is invalid:\n  %s", path, strings.Join(errs, "\n  "))
	}
	return out, nil
}

// compare splits hits into those the baseline does not cover and baseline
// entries that no longer match (fixed findings whose entry must be removed).
func compare(hits []Hit, base map[string]baselineEntry) (unknown []Hit, stale []string) {
	seen := map[string]int{}
	for _, h := range hits {
		k := h.Key()
		seen[k]++
		if seen[k] > base[k].count() || base[k].Check == "" {
			unknown = append(unknown, h)
		}
	}
	for k, e := range base {
		if n := seen[k]; n < e.count() {
			stale = append(stale, fmt.Sprintf("[%s] %s %s (%s): %q expected %d, found %d", e.Check, e.File, e.Func, e.Finding, display(e.Snippet), e.count(), n))
		}
	}
	sort.Strings(stale)
	return unknown, stale
}

// renderBaseline prints hits as baseline entries; findings maps a key to its
// finding id and defaults to "UNTRIAGED", which the loader rejects.
func renderBaseline(hits []Hit, findings map[string]string) string {
	type agg struct {
		h Hit
		n int
	}
	var order []string
	byKey := map[string]*agg{}
	for _, h := range hits {
		if a, ok := byKey[h.Key()]; ok {
			a.n++
			continue
		}
		byKey[h.Key()] = &agg{h: h, n: 1}
		order = append(order, h.Key())
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byKey[order[i]].h, byKey[order[j]].h
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	var sb strings.Builder
	for _, k := range order {
		a := byKey[k]
		f := findings[k]
		if f == "" {
			f = "UNTRIAGED"
		}
		fmt.Fprintf(&sb, "\n[[hit]]\ncheck = %q\nfile = %q\nfunc = %q\nsnippet = %q\nfinding = %q\n", a.h.Check, a.h.File, a.h.Func, a.h.Snippet, f)
		if a.n > 1 {
			fmt.Fprintf(&sb, "count = %d\n", a.n)
		}
	}
	return sb.String()
}
