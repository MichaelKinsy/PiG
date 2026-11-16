// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkFindSearchCorpusMatches is the search of a transcript (200 rows) for a query the user has typed: the work findSearchCorpusMatches does for each
// query the incremental search settles on and for each one-shot search.
func BenchmarkFindSearchCorpusMatches(b *testing.B) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("\x1b[2mrow %d\x1b[0m the quick brown fox jumps over the lazy dog Token token TOKEN %d", i, i)
	}
	corpus := buildSearchCorpus(lines)
	for _, query := range []string{"token", "quick brown", "zzz"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = findSearchCorpusMatches(corpus, query)
			}
		})
	}
}

// BenchmarkExpandPasteMarkers is the expansion of the compact markers of a submitted message that holds three pastes.
func BenchmarkExpandPasteMarkers(b *testing.B) {
	e := NewEditor()
	e.pastes = map[int]string{1: strings.Repeat("line one\n", 20), 2: strings.Repeat("two ", 400), 3: "three"}
	text := "before [paste #1 +20 lines] middle [paste #2 1600 chars] and [paste #3] after"
	b.ReportAllocs()
	for b.Loop() {
		_ = e.expandPasteMarkers(text)
	}
}
