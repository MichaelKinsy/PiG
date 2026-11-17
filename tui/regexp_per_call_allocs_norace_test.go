// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package tui

import (
	"regexp"
	"testing"
)

// A query that comes back is not compiled again: the search allocates what scanning with an expression compiled beforehand allocates, and nothing
// for the expression (BenchmarkFindSearchCorpusMatches: 15 allocations of a search that finds nothing in 200 rows, of which the compile was most).
func TestFindSearchCorpusMatchesCompilesAQueryOnce(t *testing.T) {
	corpus := buildSearchCorpus([]string{"alpha beta gamma", "delta"})
	for _, query := range []string{"zzz", "a.c*[", "Beta"} {
		ready := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
		own := testing.AllocsPerRun(100, func() { _ = ready.FindAllStringIndex(corpus.text, -1) })
		_ = findSearchCorpusMatches(corpus, query)
		got := testing.AllocsPerRun(100, func() { _ = findSearchCorpusMatches(corpus, query) })
		// A match adds the segments it maps to; none of the queries above matches more than once, so the bound is the scan plus the one match.
		if got > own+3 {
			t.Errorf("query %q: the search allocated %v times, a scan with the compiled expression %v", query, got, own)
		}
	}
}

// The paste marker of an id is compiled once.
func TestPasteMarkerRegexpIsCompiledOncePerID(t *testing.T) {
	first := pasteMarkerRegexp(7)
	if allocs := testing.AllocsPerRun(100, func() { _ = pasteMarkerRegexp(7) }); allocs != 0 {
		t.Errorf("pasteMarkerRegexp allocated %v times for an id it has seen, want 0", allocs)
	}
	if pasteMarkerRegexp(7) != first {
		t.Error("pasteMarkerRegexp returned another expression for the same id")
	}
	for text, want := range map[string]bool{"[paste #7]": true, "x [paste #7 +3 lines] y": true, "[paste #7 12 chars]": true, "[paste #70]": false, "[paste #8]": false, "[paste #7 +x lines]": false} {
		if got := first.MatchString(text); got != want {
			t.Errorf("marker 7 matches %q = %v, want %v", text, got, want)
		}
	}
}
