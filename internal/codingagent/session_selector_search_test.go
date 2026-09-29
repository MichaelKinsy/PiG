package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

type sessionSearchCase struct {
	Name       string
	Query      string
	SortMode   sessionSortMode
	NameFilter sessionNameFilter
	Sessions   []SessionInfo
	Want       []string
}

func sessionSearchCases(t testing.TB) []sessionSearchCase {
	t.Helper()
	data, err := os.ReadFile("../../test/parity/scenarios/session/testdata/search-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []sessionSearchCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for i := range cases {
		for j := range cases[i].Sessions {
			s := &cases[i].Sessions[j]
			if s.Path == "" {
				s.Path = "/tmp/" + s.ID + ".jsonl"
			}
			if s.Created.IsZero() {
				s.Created = time.UnixMilli(0)
			}
			if s.MessageCount == 0 {
				s.MessageCount = 1
			}
			if s.FirstMessage == "" {
				s.FirstMessage = "(no messages)"
			}
		}
	}
	return cases
}

// The first ten rows preserve every input and expectation in Pi 0.87.1 session-selector-search.test.ts (including the two assertions in its relevance case).
func TestUpstreamSessionSelectorSearch(t *testing.T) {
	for _, tc := range sessionSearchCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			base := tc.Sessions
			if tc.NameFilter == sessionNameNamed {
				base = slices.DeleteFunc(slices.Clone(base), func(s SessionInfo) bool { return jsTrim(s.Name) == "" })
			}
			got := filterAndSortSessions(base, tc.Query, tc.SortMode)
			ids := make([]string, 0, len(got))
			for _, s := range got {
				ids = append(ids, s.ID)
			}
			if !slices.Equal(ids, tc.Want) {
				t.Fatalf("ordered IDs = %q, want %q", ids, tc.Want)
			}
			// Drive the production picker as well: filtering must preserve/clamp the index, and Enter must use that row.
			loader := func() ([]SessionInfo, error) { return tc.Sessions, nil }
			sel := newLoadedSessionSelector(loader, loader, nil, nil, "", sessionSelectorInputBindings(t))
			sel.sortMode = tc.SortMode
			if tc.NameFilter != "" {
				sel.nameFilter = tc.NameFilter
			}
			sel.searchInput.SetText(tc.Query)
			sel.refilter()
			rows := make([]string, 0, len(sel.filtered))
			for _, node := range sel.filtered {
				rows = append(rows, node.Session.ID)
			}
			if !slices.Equal(rows, tc.Want) || sel.selected != 0 {
				t.Fatalf("picker IDs = %q, selected = %d, want %q at 0", rows, sel.selected, tc.Want)
			}
			sel.HandleInput("\r")
			if len(got) > 0 {
				if !sel.Done() || sel.SelectedPath() != got[0].Path {
					t.Fatalf("Enter: done=%v path=%q, want %q", sel.Done(), sel.SelectedPath(), got[0].Path)
				}
			} else if sel.Done() {
				t.Fatal("Enter on empty results completed picker")
			}
			record, err := json.Marshal([]any{tc.Name, ids})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("SESSION_SEARCH %s\n", record)
		})
	}
}

func TestSessionSearchRetainsAndClampsSelection(t *testing.T) {
	for _, tc := range sessionSearchCases(t) {
		if tc.Name != "DD1 five named sessions" && tc.Name != "fuzzy beats fixed cost" {
			continue
		}
		for _, initial := range []int{0, 1, len(tc.Sessions) - 1} {
			loader := func() ([]SessionInfo, error) { return tc.Sessions, nil }
			sel := newLoadedSessionSelector(loader, loader, nil, nil, "", sessionSelectorInputBindings(t))
			sel.selected = initial
			for _, key := range "delta" {
				sel.HandleInput(string(key))
			}
			wantIndex := min(initial, len(tc.Want)-1)
			if sel.selected != wantIndex || sel.filtered[sel.selected].Session.ID != tc.Want[wantIndex] {
				t.Fatalf("from %d: selected=%d rows=%v", initial, sel.selected, sel.filtered)
			}
			sel.HandleInput("\r")
			if sel.SelectedPath() != "/tmp/"+tc.Want[wantIndex]+".jsonl" {
				t.Fatalf("Enter selected %q", sel.SelectedPath())
			}
			record, err := json.Marshal([]any{tc.Name, initial, wantIndex, sel.SelectedPath()})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("SESSION_SELECTION %s\n", record)
		}
	}
}

func BenchmarkSessionSelectorSearch(b *testing.B) {
	for _, size := range []int{10, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			base := sessionSearchCases(b)[10].Sessions
			sessions := make([]SessionInfo, size)
			for i := range sessions {
				sessions[i] = base[i%len(base)]
			}
			loader := func() ([]SessionInfo, error) { return sessions, nil }
			sel := newLoadedSessionSelector(loader, loader, nil, nil, "", nil)
			sel.searchInput.SetText("delta")
			b.ReportAllocs()
			for b.Loop() {
				sel.refilter()
			}
		})
	}
}
