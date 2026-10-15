package codingagent

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's filterAndSortSessions (modes/interactive/components/session-selector-search.js) runs against the vendored Pi module over a session corpus and a query corpus: plain and quoted tokens, fuzzy matches, unbalanced quotes, JavaScript whitespace, `re:` patterns with JavaScript-only syntax (lookahead, backreferences, \d, \w, flags), invalid patterns, and both sort modes. The ordered result paths must agree. The i flag's non-ASCII case canonicalization (U+0130 does not match "i" in JavaScript, where regexp2 alone would fold it) is translated by translateJSRegExp and is in the corpus.
func TestFilterAndSortSessionsMatchesPiOracle(t *testing.T) {
	type session struct {
		Path, ID, CWD, Name, Text string
		Modified                  int64
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	sessions := []session{
		{"/s/a", "id-aaa", "/home/u/app", "Fix login bug", "please fix the login bug in auth.go", base + 5000},
		{"/s/b", "id-bbb", "/home/u/web", "", "refactor the node cve scanner\nwith tabs\tand  spaces", base + 9000},
		{"/s/c", "id-ccc", "/home/u/日本語", "日本語のメモ", "日本語 text with 😀 emoji and ÅNGSTRÖM", base + 1000},
		{"/s/d", "id-ddd", "/tmp/x", "Release 1.2.3", "abcabc 123 123 foo_bar FOO-BAR", base + 3000},
		{"/s/e", "id-eee", "/tmp/y", "İstanbul", "dotted capital I and ß sharp s", base + 7000},
		{"/s/f", "id-fff", "/tmp/z", "", "", base + 2000},
		{"/s/g", "id-ggg", "/home/u/app", "node cve", "node\u00a0cve with nbsp and\u2003em space", base + 4000},
		{"/s/h", "id-hhh", "/home/u/app", "a+b", "a+b (c) [d] {e} ^f$ g|h i.j k\\l \"q\"", base + 6000},
		{"/s/i", "id-iii", "/home/u/q", "say \"hi\"", "he said \"login bug\" twice\u0085and\ufeffleft", base + 8000},
	}
	queries := []string{
		"", " ", "login", "LOGIN BUG", "fix bug", "\"node cve\"", "\"node  cve\"", "node \"cve scanner\"", "\"unclosed", "fix \"unclosed bug", "\"\"", "\" \"", "foo bar", "fb", "xyz",
		"日本語", "😀", "ångström", "istanbul", "İSTANBUL", "ß", "abc", "id-b", "/tmp", "a+b", "(c)", "[d]", "^f$", "\u00a0", "node\u00a0cve", "node\u2003cve",
		"re:login", "re:LOGIN\\s+BUG", "re:(", "re:", "re:  ", "re:[", "re:*", "re:foo(?=_bar)", "re:(?<!x)foo", "re:(abc)\\1", "re:(?<w>abc)\\k<w>", "re:\\d{3}", "re:\\bfoo\\b", "re:^id-", "re:cve$", "re:日本", "re:\\w+語", "re:.\\u3000?語", "re:\\p{L}+", "re:[\\u3040-\\u30ff]+", "re:(?i)login", "re:a\\+b", "re:a+b", "re:\\/tmp", "re:{", "re:}", "re:a{2}", "re:x{,3}", "re:\\cJ", "re:\\x41", "re:[^\\s]+bug", "re:\\u{1F600}", "re:\\1(a)", "RE:login", "re:(?:abc)", "re:[(?]", "re:\\\\p", "re:\\P{L}", "re:(?=a)b|foo", "re:(?<n>x)?foo", "re:i̇stanbul", "re:ß", "re:SS",
		// Fuzzy against phrase tokens, and the unbalanced-quote fallback, whose tokens keep the quote and split on JavaScript whitespace only (U+0085 is not \s, U+FEFF is).
		`lgn "bug"`, `lgn"bug"`, `a "`, `e "`, `h "`, `i "`, `s "`, `l "`, `"fix bug"`, `"login bug"`, `lgn "`, `bug "login`, `say "hi`, `"he said`, "said\u0085and \"", "twice\ufeffand \"", "and\ufeffleft \"", `said "hi" lgn`,
	}
	// Queries assembled from the tokens the search parser and the regular-expression translation branch on: the re: prefix, quotes, JavaScript whitespace, groups, classes, quantifiers, lookarounds, backreferences, escapes, flags-like text, Unicode and words of the corpus.
	rng := rand.New(rand.NewSource(131))
	pieces := []string{"re:", "re:", "re:", "RE:", " re:", "\"", "\"", " ", " ", "\u00a0", "\u2003", "\t", "login", "bug", "node", "cve", "foo", "bar", "lgn", "fxbg", "nde", "fix", "the", "please", "auth", "id-", "ccc", "/home", "app", "_", "-", "abc", "123", "日本語", "😀", "ß", "İ", "é", "(", ")", "[", "]", "{", "}", "{2}", "{1,3}", "|", "*", "+", "?", "*?", "+?", ".", "^", "$", "\\d", "\\w", "\\s", "\\S", "\\b", "\\B", "\\1", "\\k<w>", "(?<w>", "(?:", "(?=", "(?!", "(?<=", "(?<!", "(?i)", "\\p{L}", "\\u3000", "\\x41", "\\/", "\\", "[^", "a-z", "\\u{1F600}", "/", "/i", "/gu", "\\n", "$1", "\\cJ", "\\0"}
	for range 3000 {
		var b strings.Builder
		for range 1 + rng.Intn(7) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		queries = append(queries, b.String())
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
	for i, query := range queries {
		for k, mode := range []sessionSortMode{sessionSortRecent, sessionSortRelevance} {
			var got []string
			for _, s := range filterAndSortSessions(infos, query, mode) {
				got = append(got, s.Path)
			}
			if got == nil {
				got = []string{}
			}
			if !slices.Equal(got, want[i][k]) {
				t.Errorf("query %q mode %v:\n Go %q\n Pi %q", query, mode, got, want[i][k])
			}
		}
	}
}
