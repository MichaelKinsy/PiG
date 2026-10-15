package codingagent

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestSessionSearchTokensMatchPi: filterAndSortSessions (session-selector-search.ts) in both sort modes over random sessions (words, names, cwd paths with Unicode, no-name sessions, equal timestamps) and random token queries (fuzzy words that are subsequences, substrings, scrambles; quoted phrases; unbalanced quotes; mixed whitespace), against the pinned Pi: the order of every result.
func TestSessionSearchTokensMatchPi(t *testing.T) {
	type session struct {
		Path, ID, CWD, Name, Text string
		Modified                  int64
	}
	rng := rand.New(rand.NewSource(20261014))
	words := []string{"fix", "login", "bug", "node", "cve", "scanner", "refactor", "auth.go", "Release", "1.2.3", "foo_bar", "FOO-BAR", "日本語", "😀", "ÅNGSTRÖM", "ß", "İstanbul", "résumé", "naïve", "a+b", "(c)", "tab\there", "x", "ab", "abcabc", "123", "the", "and", "of", "in", "to", "Go", "TypeScript", "Ünïcödé"}
	spaces := []string{" ", " ", " ", "  ", "\t", "\n", "\u00a0", "\u2003", "\r\n"}
	phrase := func(n int) string {
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteString(spaces[rng.Intn(len(spaces))])
			}
			b.WriteString(words[rng.Intn(len(words))])
		}
		return b.String()
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	var sessions []session
	for i := range 40 {
		name := ""
		if rng.Intn(2) == 0 {
			name = phrase(1 + rng.Intn(3))
		}
		sessions = append(sessions, session{
			Path: "/s/" + string(rune('a'+i%26)) + string(rune('a'+i/26)), ID: "id-" + phrase(1)[:1] + string(rune('a'+i)), CWD: "/home/" + phrase(1), Name: name, Text: phrase(rng.Intn(25)),
			Modified: base + int64(rng.Intn(8))*1000,
		})
	}
	mutate := func(word string) string {
		runes := []rune(word)
		switch rng.Intn(6) {
		case 0:
			return strings.ToUpper(word)
		case 1:
			if len(runes) > 2 {
				lo := rng.Intn(len(runes) - 1)
				return string(runes[lo : lo+1+rng.Intn(len(runes)-lo-1)])
			}
		case 2:
			rng.Shuffle(len(runes), func(i, j int) { runes[i], runes[j] = runes[j], runes[i] })
			return string(runes)
		case 3:
			var out []rune
			for _, r := range runes {
				if rng.Intn(2) == 0 {
					out = append(out, r)
				}
			}
			return string(out)
		}
		return word
	}
	var queries []string
	for len(queries) < 1500 {
		var parts []string
		for range 1 + rng.Intn(3) {
			w := mutate(words[rng.Intn(len(words))])
			switch rng.Intn(5) {
			case 0:
				w = `"` + w + spaces[rng.Intn(len(spaces))] + mutate(words[rng.Intn(len(words))]) + `"`
			case 1:
				if rng.Intn(3) == 0 {
					w = `"` + w
				}
			}
			parts = append(parts, w)
		}
		queries = append(queries, strings.Join(parts, spaces[rng.Intn(len(spaces))]))
	}
	type input struct {
		Sessions []session `json:"sessions"`
		Queries  []string  `json:"queries"`
	}
	var want [][][]string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/modes/interactive/components/session-selector-search.js");
const sessions = input.sessions.map((s) => ({ path: s.Path, id: s.ID, cwd: s.CWD, name: s.Name || undefined, allMessagesText: s.Text, modified: new Date(s.Modified) }));
emit(input.queries.map((q) => ["recent", "relevance"].map((mode) => mod.filterAndSortSessions(sessions, q, mode, "all").map((s) => s.path))));`, input{sessions, queries}, &want)
	infos := make([]SessionInfo, len(sessions))
	for i, s := range sessions {
		infos[i] = SessionInfo{Path: s.Path, ID: s.ID, CWD: s.CWD, Name: s.Name, AllMessagesText: s.Text, Modified: time.UnixMilli(s.Modified)}
	}
	matched, failures := 0, 0
	for i, query := range queries {
		for k, mode := range []sessionSortMode{sessionSortRecent, sessionSortRelevance} {
			got := []string{}
			for _, s := range filterAndSortSessions(infos, query, mode) {
				got = append(got, s.Path)
			}
			if k == 1 && len(want[i][k]) > 0 && len(want[i][k]) < len(sessions) {
				matched++
			}
			if !slices.Equal(got, want[i][k]) {
				if failures++; failures <= 8 {
					t.Errorf("%q mode %d:\n Pig %q\n Pi  %q", query, k, got, want[i][k])
				}
			}
		}
	}
	if matched < len(queries)/5 {
		t.Fatalf("only %d of %d queries select a strict subset of the sessions", matched, len(queries))
	}
	if failures > 0 {
		t.Fatalf("%d result lists differ from Pi", failures)
	}
}
