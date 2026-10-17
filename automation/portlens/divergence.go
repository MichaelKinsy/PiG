// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Divergence statuses.
const (
	DivActive    = "active"
	DivRetired   = "retired"
	DivWithdrawn = "withdrawn"
	DivAdditive  = "additive"
)

// Divergence classes.
const (
	ClassPiBug    = "Pi bug fix"
	ClassPerf     = "perf"
	ClassPlatform = "platform"
	ClassAdditive = "additive"
	ClassOther    = "other difference"
)

// Snippet is a highlighted source excerpt with its citation.
type Snippet struct {
	Path        string
	Start, End  int
	Lang        string
	HTML        string
	Truncated   bool
	Unavailable string
	// Cite is the citation text as written in the note.
	Cite string
}

// Link is a hyperlink with its label.
type Link struct {
	Label string
	URL   string
}

// TestRef is a test that a divergence record names.
type TestRef struct {
	Name string
	File string
	Line int
}

// Divergence is one D-number record.
type Divergence struct {
	ID          string
	Num         int
	Title       string
	Source      string
	Status      string
	Class       string
	ClassWhy    string
	NoteHTML    string
	Raw         string
	RemoveWhen  string
	Scrutinized bool
	PiIssues    []Link
	BugIDs      []string
	Tests       []TestRef
	Scenarios   []string
	CallSites   []Site
	PiSnips     []Snippet
	GoSnips     []Snippet
}

var (
	divHeadRe   = regexp.MustCompile(`^## (D(\d+))\s+(.+?)\s*$`)
	retiredRe   = regexp.MustCompile(`^- (D(\d+)) \x{2014} (.+)$`)
	testNameRe  = regexp.MustCompile("`((?:Test|Fuzz|Benchmark)[A-Za-z0-9_]*)(?:/[^`]*)?`")
	testBareRe  = regexp.MustCompile(`\b(Test[A-Z][A-Za-z0-9_]{6,})\b`)
	scenarioRe  = regexp.MustCompile("`((?:test/parity/scenarios/)?[a-z0-9][a-z0-9-]*/[0-9a-z][0-9a-z-]*(?:\\.toml)?)`")
	issueRe     = regexp.MustCompile(`pi-mono (?:issue |PR |pull )?#(\d+)`)
	urlIssueRe  = regexp.MustCompile(`https://github\.com/earendil-works/pi[a-z-]*/(issues|pull)/(\d+)`)
	removeRe    = regexp.MustCompile(`(?m)^Remove when:\s*(.+(?:\n[^\n#A-Z][^\n]*)*)`)
	perfWords   = regexp.MustCompile(`(?i)\b(perf|performance|cache[sd]?|bounded|pooled|faster|latency|memory|coalesc\w*|allocation\w*|throughput|startup|O\(n)\b`)
	platWords   = regexp.MustCompile(`(?i)\b(windows|macos|linux|android|termux|darwin|dacl|tmux|herdr|terminal|platform|signal|sigint|tty)\b`)
	whitespaces = regexp.MustCompile(`\s+`)
)

// loadDivergences parses DIVERGENCES.md and additive-features.md.
func loadDivergences(root string, bugs *BugLedger, scan *treeScan, syms *symIndex, pi *piSource, piVersion string) ([]*Divergence, error) {
	byNum := map[int]*Divergence{}
	var order []*Divergence
	add := func(d *Divergence) {
		if _, dup := byNum[d.Num]; dup {
			return
		}
		byNum[d.Num] = d
		order = append(order, d)
	}
	for _, src := range []struct {
		rel      string
		additive bool
	}{
		{"docs/parity/DIVERGENCES.md", false},
		{"docs/additive-features.md", true},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(src.rel)))
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(b), "\n")
		inRetired := false
		var cur *Divergence
		var body []string
		flush := func() {
			if cur != nil {
				cur.Raw = scrub(strings.Join(body, "\n"))
				add(cur)
			}
			cur, body = nil, nil
		}
		for _, ln := range lines {
			if strings.HasPrefix(ln, "## ") {
				flush()
				inRetired = strings.TrimSpace(ln) == "## Retired divergences"
				if m := divHeadRe.FindStringSubmatch(ln); m != nil {
					d := &Divergence{ID: m[1], Source: src.rel, Title: m[3], Status: DivActive}
					atoi(m[2], &d.Num)
					if src.additive {
						d.Status = DivAdditive
					}
					cur = d
				}
				continue
			}
			if inRetired {
				if m := retiredRe.FindStringSubmatch(ln); m != nil {
					d := &Divergence{ID: m[1], Source: src.rel, Status: DivRetired}
					atoi(m[2], &d.Num)
					title, rest, _ := strings.Cut(m[3], ". ")
					d.Title = strings.TrimSuffix(title, ".")
					d.Raw = strings.TrimSpace(m[3])
					if strings.Contains(rest, "Withdrawn") || strings.HasPrefix(rest, "Withdrawn") {
						d.Status = DivWithdrawn
					}
					add(d)
				}
				continue
			}
			if cur != nil {
				body = append(body, ln)
			}
		}
		flush()
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Num < order[j].Num })
	known := map[string]bool{}
	for _, d := range order {
		known[d.ID] = true
	}
	for _, d := range order {
		enrichDivergence(d, known, bugs, scan, syms, pi, piVersion)
	}
	return order, nil
}

func enrichDivergence(d *Divergence, known map[string]bool, bugs *BugLedger, scan *treeScan, syms *symIndex, pi *piSource, piVersion string) {
	text := d.Raw
	md := text
	if d.Status == DivRetired || d.Status == DivWithdrawn {
		md = d.Raw
	}
	d.NoteHTML = renderMarkdown(md, known)
	d.Scrutinized = strings.Contains(text, "SCRUTINIZED:approved")
	if m := removeRe.FindStringSubmatch(text); m != nil {
		d.RemoveWhen = strings.TrimSpace(whitespaces.ReplaceAllString(m[1], " "))
	}
	seenIssue := map[string]bool{}
	for _, m := range issueRe.FindAllStringSubmatch(d.Title+"\n"+text, -1) {
		u := "https://github.com/earendil-works/pi/issues/" + m[1]
		if !seenIssue[u] {
			seenIssue[u] = true
			d.PiIssues = append(d.PiIssues, Link{Label: "Pi #" + m[1], URL: u})
		}
	}
	for _, m := range urlIssueRe.FindAllStringSubmatch(text, -1) {
		u := m[0]
		if !seenIssue[u] {
			seenIssue[u] = true
			d.PiIssues = append(d.PiIssues, Link{Label: "Pi " + m[1] + " #" + m[2], URL: u})
		}
	}
	for _, e := range bugs.ByDivergence[d.ID] {
		d.BugIDs = appendUnique(d.BugIDs, e.ID)
		if e.PiIssue != "" && !seenIssue[e.PiIssue] {
			seenIssue[e.PiIssue] = true
			d.PiIssues = append(d.PiIssues, Link{Label: "Pi issue " + e.PiIssue[strings.LastIndex(e.PiIssue, "/")+1:], URL: e.PiIssue})
		}
	}
	// Tests that pin the record.
	seenTest := map[string]bool{}
	addTest := func(name string) {
		if seenTest[name] {
			return
		}
		seenTest[name] = true
		t := TestRef{Name: name}
		if sites := scan.Tests[name]; len(sites) > 0 {
			t.File, t.Line = sites[0].File, sites[0].Line
		}
		d.Tests = append(d.Tests, t)
	}
	for _, m := range testNameRe.FindAllStringSubmatch(text, -1) {
		addTest(m[1])
	}
	for _, m := range testBareRe.FindAllStringSubmatch(text, -1) {
		if len(scan.Tests[m[1]]) > 0 {
			addTest(m[1])
		}
	}
	seenScen := map[string]bool{}
	for _, m := range scenarioRe.FindAllStringSubmatch(text, -1) {
		id := strings.TrimSuffix(strings.TrimPrefix(m[1], "test/parity/scenarios/"), ".toml")
		if _, ok := scan.Scenarios[id]; ok && !seenScen[id] {
			seenScen[id] = true
			d.Scenarios = append(d.Scenarios, id)
		}
	}
	sort.Strings(d.Scenarios)
	d.CallSites = scan.Markers[d.ID]
	// Snippets: Pi citations on the left, PiG marker sites on the right.
	if d.Status != DivAdditive {
		for _, c := range findCites(text) {
			if len(d.PiSnips) >= 4 {
				break
			}
			d.PiSnips = append(d.PiSnips, piSnippet(pi, c, piVersion))
		}
	}
	sites := d.CallSites
	prod := make([]Site, 0, len(sites))
	for _, s := range sites {
		if !strings.HasSuffix(s.File, "_test.go") {
			prod = append(prod, s)
		}
	}
	if len(prod) == 0 {
		prod = sites
	}
	for _, s := range prod {
		if len(d.GoSnips) >= 4 {
			break
		}
		d.GoSnips = append(d.GoSnips, goSnippet(syms, s))
	}
	d.Class, d.ClassWhy = classify(d, bugs)
}

// classify assigns the divergence class and states the basis.
func classify(d *Divergence, bugs *BugLedger) (string, string) {
	if d.Status == DivAdditive {
		return ClassAdditive, "recorded in docs/additive-features.md: Pi has no counterpart"
	}
	for _, e := range bugs.ByDivergence[d.ID] {
		if e.Class == "ahead-of-pi" {
			return ClassPiBug, "bug ledger entry " + e.ID + " (fixed ahead of Pi)"
		}
	}
	if len(d.PiIssues) > 0 {
		return ClassPiBug, "the record cites a Pi issue"
	}
	switch {
	case perfWords.MatchString(d.Title):
		return ClassPerf, "title names performance, caching or a bound"
	case platWords.MatchString(d.Title):
		return ClassPlatform, "title names a platform or terminal environment"
	}
	return ClassOther, "a deliberate difference from Pi"
}

// goSnippet renders the declaration around a marker line, or a window around it when the declaration is long.
func goSnippet(syms *symIndex, s Site) Snippet {
	sn := Snippet{Path: s.File, Lang: languageFor(s.File)}
	f := syms.file(s.File)
	if !f.exists {
		sn.Unavailable = "file not found in this tree"
		return sn
	}
	start, end := s.Line-2, s.Line+12
	if sp, ok := syms.enclosing(s.File, s.Line); ok && sp.End-sp.Start+1 <= 26 {
		start, end = sp.Start, sp.End
	}
	if start < 1 {
		start = 1
	}
	text, a, b, cut := syms.text(s.File, start, end, 26)
	sn.Start, sn.End, sn.Truncated = a, b, cut
	sn.HTML = highlight(sn.Lang, text)
	return sn
}

// piSnippet renders the cited range of the pinned Pi tree.
func piSnippet(pi *piSource, c Cite, version string) Snippet {
	sn := Snippet{Cite: fmt.Sprintf("%s:%d-%d", c.File, c.Start, c.End), Lang: languageFor(c.File)}
	if !pi.available() {
		sn.Path = c.File
		sn.Start, sn.End = c.Start, c.End
		sn.Unavailable = "the Pi " + version + " source mirror is not present (run make upstream-mirror)"
		return sn
	}
	rel, ok := pi.resolve(c.File, c.End)
	if !ok {
		sn.Path = c.File
		sn.Start, sn.End = c.Start, c.End
		sn.Unavailable = "file not found in Pi " + version
		return sn
	}
	sn.Path = rel
	lines := pi.file(rel)
	start, end := c.Start, c.End
	if start > len(lines) {
		sn.Start, sn.End = start, end
		sn.Unavailable = "line outside the file at Pi " + version
		return sn
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end-start+1 > 26 {
		end = start + 25
		sn.Truncated = true
	}
	sn.Start, sn.End = start, end
	sn.HTML = highlight(sn.Lang, dedent(lines[start-1:end]))
	return sn
}
