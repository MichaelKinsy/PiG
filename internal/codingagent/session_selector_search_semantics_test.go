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

// Expected scores are direct matchSession results from the published Pi 0.87.1 package. Fuzzy scoring uses raw text, phrases normalize both sides, and regex/phrase positions use UTF-16 units.
func TestSessionSearchScores(t *testing.T) {
	for _, tc := range []struct {
		query, text string
		matches     bool
		score       float64
	}{
		{"ab", "a\n\n b", true, -13.200000000000001},
		{`"ab"`, "😀 ab", true, 0.30000000000000004},
		{"re:ab", "😀 ab", true, 0.5},
		{"\"i\u0307\"", "İ", true, 0},
		{`ab "c d"`, "ab c\n d", true, -14.2},
		{"ab", "not matching", false, 0},
	} {
		t.Run(tc.query+tc.text, func(t *testing.T) {
			matches, score := matchSession(SessionInfo{AllMessagesText: tc.text}, parseSearchQuery(tc.query))
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
