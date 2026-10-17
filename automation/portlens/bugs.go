// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BugCommit is a fix commit of a ledger entry.
type BugCommit struct {
	Hash string `json:"hash"`
	Date string `json:"date"`
	PR   string `json:"pr"`
}

// BugEntry is one entry of docs/bug-ledger.json.
type BugEntry struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Symptom     string      `json:"symptom"`
	RootCause   string      `json:"rootCause"`
	CauseFrom   string      `json:"rootCauseSource"`
	Impact      string      `json:"userImpact"`
	Platform    string      `json:"platform"`
	Class       string      `json:"class"`
	Divergence  string      `json:"divergence"`
	PiIssue     string      `json:"piIssue"`
	RemoveWhen  string      `json:"removeWhen"`
	FixedIn     string      `json:"fixedIn"`
	ReleaseNote string      `json:"releaseNote"`
	Origin      string      `json:"origin"`
	Commits     []BugCommit `json:"commits"`
	CommitsFrom string      `json:"commitsFrom"`
	Tests       []string    `json:"tests"`
}

// BugDivergence accounts for one divergence record added since the baseline release.
type BugDivergence struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Entry string `json:"entry"`
}

// BugLedger is docs/bug-ledger.json with an index by divergence number.
type BugLedger struct {
	Since        string          `json:"since"`
	Entries      []BugEntry      `json:"entries"`
	Divergences  []BugDivergence `json:"divergences"`
	Exempt       map[string]int  `json:"exemptCommits"`
	ByDivergence map[string][]*BugEntry
}

func loadBugs(root string) (*BugLedger, error) {
	var l BugLedger
	if err := readJSON(filepath.Join(root, "docs", "bug-ledger.json"), &l); err != nil {
		return nil, err
	}
	l.ByDivergence = map[string][]*BugEntry{}
	for i := range l.Entries {
		e := &l.Entries[i]
		for _, f := range []*string{&e.Title, &e.Symptom, &e.RootCause, &e.ReleaseNote, &e.RemoveWhen} {
			*f = scrub(*f)
		}
		if e.Divergence != "" {
			for _, d := range dRef.FindAllString(e.Divergence, -1) {
				l.ByDivergence[d] = append(l.ByDivergence[d], e)
			}
		}
	}
	byID := map[string]*BugEntry{}
	for i := range l.Entries {
		byID[l.Entries[i].ID] = &l.Entries[i]
	}
	for _, d := range l.Divergences {
		e := byID[d.Entry]
		if e == nil {
			continue
		}
		have := false
		for _, x := range l.ByDivergence[d.ID] {
			have = have || x == e
		}
		if !have {
			l.ByDivergence[d.ID] = append(l.ByDivergence[d.ID], e)
		}
	}
	for _, v := range l.ByDivergence {
		sort.Slice(v, func(i, j int) bool { return v[i].ID < v[j].ID })
	}
	return &l, nil
}

// bugClassLabel is the reader-facing name of a source class.
func bugClassLabel(c string) string {
	switch c {
	case "own":
		return "PiG's own defect"
	case "ahead-of-pi":
		return "Fixed ahead of Pi"
	case "pi-port":
		return "Pi fix ported"
	}
	return c
}

// testParts splits "file::TestName" of a ledger test entry.
func testParts(s string) (file, name string) {
	f, n, ok := strings.Cut(s, "::")
	if !ok {
		return "", s
	}
	return f, n
}

var (
	trailerRe = regexp.MustCompile(`\s*(?:Signed-off-by|Co-authored-by):[^\n]*`)
	emailRe   = regexp.MustCompile(`<?[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+>?`)
)

// scrub removes commit trailers and e-mail addresses from text that was copied from commit messages, so a report built from it can be published.
func scrub(s string) string {
	return strings.TrimSpace(emailRe.ReplaceAllString(trailerRe.ReplaceAllString(s, ""), ""))
}
