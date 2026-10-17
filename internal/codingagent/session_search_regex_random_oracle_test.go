package codingagent

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestSessionSearchRegexMatchesPi: the `re:` mode of parseSearchQuery and filterAndSortSessions (session-selector-search.ts, new RegExp(pattern, "i")) over random JavaScript patterns built from literals, classes, escapes, groups, lookaround, backreferences, quantifiers, anchors and deliberate syntax errors: whether the pattern compiles, the error message, and which sessions match, against the pinned Pi.
func TestSessionSearchRegexMatchesPi(t *testing.T) {
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
		{"/s/e", "id-eee", "/tmp/y", "İstanbul", "dotted capital I and ß sharp s ſ K (Kelvin)", base + 7000},
		{"/s/h", "id-hhh", "/home/u/app", "a+b", "a+b (c) [d] {e} ^f$ g|h i.j k\\l p{L} \u00a0 \u2028 \u0000 _ -", base + 6000},
		{"/s/i", "id-iii", "/x", "Case folds", "Ǆ ǅ ǆ µ μ Μ ǰ ΐ Å Å ﬀ ǈ ς σ Σ ϑ θ ẞ ß ı İ i I ſ s S K k", base + 8000},
		{"/s/j", "id-jjj", "/y", "", "line one\r\nline two\nend\n", base + 500},
		// No ASCII whitespace: only the edges of JavaScript's \s (U+FEFF, U+1680, U+202F, U+205F, U+3000 are in it; U+0085, U+180E, U+200B are not).
		{"/s/k", "id-kkk", "/k", "", "q\ufeffq\u1680q\u202fq\u205fq\u3000q", base + 400},
		{"/s/l", "id-lll", "/l", "", "q\u0085q\u180eq\u200bq", base + 300},
	}
	rng := rand.New(rand.NewSource(20261013))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var atom func(depth int) string
	var seq func(depth int) string
	atom = func(depth int) string {
		switch rng.Intn(18) {
		case 0, 1, 2:
			return pick("a", "b", "c", "o", "e", "x", "1", "2", " ", "-", "_", "i", "k", "s", "f", "u", "日", "😀", "é", "İ", "ß")
		case 3:
			return pick(".", "\\d", "\\D", "\\w", "\\W", "\\s", "\\S", "\\b", "\\B", "\\n", "\\t", "\\0", "\\.", "\\/", "\\-", "\\u00e9", "\\x41", "\\cJ", "\\u{1F600}", "\\p{L}", "\\P{L}", "\\k<n>", "\\q", "\\1", "\\2", "\\8", "\\", "\\u12", "\\x4", "\\ud83d", "\\u{41}")
		case 4:
			return "[" + pick("a-z", "^a-z", "abc", "^", "", "\\d\\s", "a-", "-a", "\\w-x", "z-a", "\\]", "^]", "]", "\\b", "é-ü", "😀", "\\u00e9", "\\p{L}", "[a]", "a-\\d", "\\d-z") + "]"
		case 5:
			return "(" + seq(depth+1) + ")"
		case 6:
			return "(?:" + seq(depth+1) + ")"
		case 7:
			return pick("(?=", "(?!", "(?<=", "(?<!") + seq(depth+1) + ")"
		case 8:
			return "(?<" + pick("n", "m", "n", "1a", "", "é") + ">" + seq(depth+1) + ")"
		case 9:
			return pick("^", "$", "\\b")
		case 10:
			return pick("(?i)", "(?", "(?P<n>a)", "(?#c)", "(?>a)", "(?s:a)", "(?<", ")", "(", "[", "]", "{", "}", "*", "+", "?", "{1}", "a{", "{,2}", "x{2,1}")
		case 11:
			return pick("\\u00", "\\x", "\\c", "\\k", "\\p", "\\P", "\\u{", "\\u{110000}", "\\8", "\\9")
		default:
			return pick("a", "b", "c", "o", "e", "n", "t", "r", " ", "i", "1")
		}
	}
	quant := func() string {
		switch rng.Intn(14) {
		case 0:
			return "*"
		case 1:
			return "+"
		case 2:
			return "?"
		case 3:
			return "{2}"
		case 4:
			return "{1,3}"
		case 5:
			return "{2,}"
		case 6:
			return "*?"
		case 7:
			return "+?"
		case 8:
			return "{1,2}?"
		case 9:
			return pick("{,3}", "{", "{1", "{1,", "{a}", "**", "+*", "{3,1}")
		default:
			return ""
		}
	}
	seq = func(depth int) string {
		var b strings.Builder
		n := 1 + rng.Intn(3)
		if depth > 2 {
			n = 1
		}
		for range n {
			b.WriteString(atom(depth))
			b.WriteString(quant())
		}
		if depth < 2 && rng.Intn(5) == 0 {
			b.WriteString("|" + seq(depth+1))
		}
		return b.String()
	}
	// \s and \S against the k and l sessions, whose only separators are the edges of JavaScript's WhiteSpace and LineTerminator sets.
	queries := []string{`re:q\sq`, `re:q\Sq`, `re:^q(?:\sq)+$`, `re:^q(?:\Sq)+$`, `re:\bq\b`}
	for len(queries) < 3000 {
		queries = append(queries, "re:"+seq(0))
	}
	type input struct {
		Sessions []session `json:"sessions"`
		Queries  []string  `json:"queries"`
	}
	type answer struct {
		Error string   `json:"error"`
		Paths []string `json:"paths"`
	}
	var want []answer
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/modes/interactive/components/session-selector-search.js");
const sessions = input.sessions.map((s) => ({ path: s.Path, id: s.ID, cwd: s.CWD, name: s.Name || undefined, allMessagesText: s.Text, modified: new Date(s.Modified) }));
emit(input.queries.map((q) => ({ error: mod.parseSearchQuery(q).error ?? "", paths: mod.filterAndSortSessions(sessions, q, "recent", "all").map((s) => s.path) })));`, input{sessions, queries}, &want)
	infos := make([]SessionInfo, len(sessions))
	for i, s := range sessions {
		infos[i] = SessionInfo{Path: s.Path, ID: s.ID, CWD: s.CWD, Name: s.Name, AllMessagesText: s.Text, Modified: time.UnixMilli(s.Modified)}
	}
	compiled, matched, failures := 0, 0, 0
	for i, query := range queries {
		parsed := parseSearchQuery(query)
		got := []string{}
		for _, s := range filterAndSortSessions(infos, query, sessionSortRecent) {
			got = append(got, s.Path)
		}
		if want[i].Error == "" {
			compiled++
			if len(want[i].Paths) > 0 {
				matched++
			}
		}
		if parsed.error != want[i].Error || !slices.Equal(got, want[i].Paths) {
			if failures++; failures <= 25 {
				t.Errorf("%q:\n Pig error %q paths %q\n Pi  error %q paths %q", query, parsed.error, got, want[i].Error, want[i].Paths)
			}
		}
	}
	if compiled < len(queries)/4 || matched < len(queries)/10 {
		t.Fatalf("only %d of %d patterns compile and %d match a session in Pi", compiled, len(queries), matched)
	}
	if failures > 0 {
		t.Fatalf("%d of %d patterns differ from Pi", failures, len(queries))
	}
}
