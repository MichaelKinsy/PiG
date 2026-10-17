package codingagent

import (
	"slices"
	"testing"
)

// The selector keeps each session's prepared search text between keystrokes. Every Pi search case, filtered through one
// long-lived cache in turn, twice, and with the same paths standing for sessions of other cases, gives what a filter that
// builds the search text afresh gives.
func TestCachedSessionSearchEqualsAFreshSearch(t *testing.T) {
	var cache sessionSearches
	queries := []string{"", "a", "ab", "5opus", "opus5", `"node cve"`, `re:lo.in`, `re:[`, "login", "lgin", `"two  words"`}
	for _, tc := range sessionSearchCases(t) {
		for _, query := range append([]string{tc.Query, tc.Query}, queries...) {
			for _, mode := range []sessionSortMode{sessionSortRelevance, sessionSortRecent} {
				got := cache.filterAndSort(tc.Sessions, query, mode)
				want := filterAndSortSessions(tc.Sessions, query, mode)
				if !slices.EqualFunc(got, want, func(a, b SessionInfo) bool { return a.Path == b.Path }) {
					t.Fatalf("case %q, query %q: cached %v, fresh %v", tc.Name, query, sessionPaths(got), sessionPaths(want))
				}
			}
		}
	}
}

// A session renamed or given new messages under the same path is searched by its new text.
func TestCachedSessionSearchReadsAChangedSession(t *testing.T) {
	var cache sessionSearches
	session := SessionInfo{Path: "/s.jsonl", ID: "s", Name: "alpha", CWD: "/w", AllMessagesText: "first words"}
	for _, change := range []func(*SessionInfo){
		func(s *SessionInfo) { s.Name = "zebra" },
		func(s *SessionInfo) { s.AllMessagesText = "zebra words" },
		func(s *SessionInfo) { s.CWD = "/zebra" },
		func(s *SessionInfo) { s.ID = "zebra" },
	} {
		before := session
		if got := cache.filterAndSort([]SessionInfo{before}, "zebra", sessionSortRelevance); len(got) != 0 {
			t.Fatalf("%+v matched zebra before the change", before)
		}
		after := before
		change(&after)
		if got := cache.filterAndSort([]SessionInfo{after}, "zebra", sessionSortRelevance); len(got) != 1 {
			t.Fatalf("%+v did not match zebra after the change", after)
		}
	}
}

func sessionPaths(sessions []SessionInfo) []string {
	paths := make([]string, len(sessions))
	for i, s := range sessions {
		paths[i] = s.Path
	}
	return paths
}
