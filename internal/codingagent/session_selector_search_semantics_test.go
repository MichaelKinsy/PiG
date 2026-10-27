package codingagent

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestSessionSearchQueryTokens(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  []searchToken
	}{
		{" \ufeff ", nil},
		{"foo/bar", []searchToken{{kind: "fuzzy", value: "foo/bar"}}},
		{"foo\r\v\f\u00a0\ufeffbar", []searchToken{{kind: "fuzzy", value: "foo"}, {kind: "fuzzy", value: "bar"}}},
		{"foo\u0085bar", []searchToken{{kind: "fuzzy", value: "foo\u0085bar"}}},
		{`foo"bar baz"qux`, []searchToken{{kind: "fuzzy", value: "foo"}, {kind: "phrase", value: "bar baz"}, {kind: "fuzzy", value: "qux"}}},
		{`foo "bar baz`, []searchToken{{kind: "fuzzy", value: "foo"}, {kind: "fuzzy", value: `"bar`}, {kind: "fuzzy", value: "baz"}}},
		{`"" "   "`, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got := parseSearchQuery(tc.query)
			if got.mode != "tokens" || !slices.Equal(got.tokens, tc.want) {
				t.Fatalf("parse = %+v, want tokens %v", got, tc.want)
			}
		})
	}
	if got := parseSearchQuery("re:\ufeff"); got.mode != "regex" || got.regex != nil || got.error != "Empty regex" {
		t.Fatalf("empty regex = %+v", got)
	}
}

// Expected scores are direct matchSession results from Pi 1.4.0+936e5f40 (#11220): every field is matched on its own, name text scores in
// tier 0, id, message and cwd text in tier 1 (a million per tier), and scattered name or cwd matches in tier 2; messages and ids never match
// as scattered characters.
func TestSessionSearchScores(t *testing.T) {
	full := SessionInfo{ID: "019e9024-aaaa", Name: "Cache work", CWD: "/home/x/proj", AllMessagesText: "A discussion mentioning the Cache.\n  more"}
	for _, tc := range []struct {
		query   string
		session SessionInfo
		matches bool
		score   float64
	}{
		{"ab", SessionInfo{AllMessagesText: "a\n\n b"}, false, 0},
		{`"ab"`, SessionInfo{AllMessagesText: "😀 ab"}, true, 1000000},
		{"re:ab", SessionInfo{AllMessagesText: "😀 ab"}, true, 1000000},
		{"\"i\u0307\"", SessionInfo{AllMessagesText: "İ"}, true, 1000000},
		{`ab "c d"`, SessionInfo{AllMessagesText: "ab c\n d"}, true, 2000000},
		{"ab", SessionInfo{AllMessagesText: "not matching"}, false, 0},
		{"cache", full, true, -84},
		{"ache", full, true, -29},
		{`"cache work"`, full, true, -390.5},
		{`"mentioning  the"`, full, true, 1000000},
		{"re:Cache", full, true, 0},
		{"re:zzz", full, false, 0},
		{"proj", full, true, 1000000},
		{"9024", full, true, 1000000},
		{"chwk", full, true, 1999988.8},
		{"cw", full, true, 1999985.6},
		{"x proj", full, true, 2000000},
		{"fuzzy cache", full, false, 0},
	} {
		t.Run(tc.query+tc.session.AllMessagesText, func(t *testing.T) {
			matches, score := matchSession(tc.session, parseSearchQuery(tc.query))
			if matches != tc.matches || score != tc.score {
				t.Fatalf("match = %v, %v, want %v, %v", matches, score, tc.matches, tc.score)
			}
		})
	}
}

func TestSessionSearchStableTiesAndInputOwnership(t *testing.T) {
	// Equal modified milliseconds preserve input order even when Go timestamps differ below JavaScript Date precision.
	sessions := make([]SessionInfo, 40)
	for i := range sessions {
		sessions[i] = SessionInfo{ID: "same", Path: string(rune('a' + i)), Modified: time.Unix(1, int64(i)), AllMessagesText: "text"}
	}
	before := slices.Clone(sessions)
	for _, mode := range []sessionSortMode{sessionSortThreaded, sessionSortRecent, sessionSortRelevance} {
		got := filterAndSortSessions(sessions, "text", mode)
		if !reflect.DeepEqual(got, before) || !reflect.DeepEqual(sessions, before) {
			t.Fatalf("%s changed tied order or input", mode)
		}
	}
}
