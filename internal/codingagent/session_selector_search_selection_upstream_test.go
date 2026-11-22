package codingagent

// Ports the cases Pi 37b74c546 (#12246) and 5bcf370a5 (#12334) add to test/session-selector-selection.test.ts, both fixes for
// issue #12245: a non-empty query selects the top row after the cursor moved, and a key that leaves the query unchanged keeps
// the selection.

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func searchSelectionSession(id, text string, modified int64) SessionInfo {
	return SessionInfo{Path: "/tmp/" + id + ".jsonl", ID: id, Created: time.UnixMilli(0), Modified: time.UnixMilli(modified), MessageCount: 1, FirstMessage: text, AllMessagesText: text}
}

func searchSelectionSelector(t *testing.T, sessions []SessionInfo, selected *[]string) *SessionSelectorComponent {
	t.Helper()
	s := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return []SessionInfo{}, nil }, nil, nil, "", sessionSelectorInputBindings(t))
	s.GetSessionList().OnSelect = func(path string) { *selected = append(*selected, path) }
	return s
}

func typeSessionQuery(s *SessionSelectorComponent, query string) {
	for _, r := range query {
		s.HandleInput(string(r))
	}
}

// "selects the best match after typing a query, even after scrolling".
func TestSessionSelectorSelectsTheBestMatchAfterTypingAQueryEvenAfterScrolling(t *testing.T) {
	var selected []string
	s := searchSelectionSelector(t, []SessionInfo{
		searchSelectionSession("login", "fix login", 6000),
		searchSelectionSession("logout", "fix logout", 5000),
		searchSelectionSession("refactor", "refactor", 4000),
		searchSelectionSession("docs", "docs", 3000),
		searchSelectionSession("tests", "tests", 2000),
	}, &selected)
	for range 4 {
		s.HandleInput("\x1b[B")
	}
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/tests.jsonl" {
		t.Fatalf("four Down keys selected %q, want /tmp/tests.jsonl", got)
	}
	typeSessionQuery(s, "fix")
	s.HandleInput("\r")
	if !slices.Equal(selected, []string{"/tmp/login.jsonl"}) {
		t.Fatalf("Enter selected %v, want [/tmp/login.jsonl]", selected)
	}
}

// "keeps the selection when a cursor key leaves the query unchanged".
func TestSessionSelectorKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	var selected []string
	s := searchSelectionSelector(t, []SessionInfo{searchSelectionSession("login", "fix login", 6000), searchSelectionSession("logout", "fix logout", 5000)}, &selected)
	typeSessionQuery(s, "fix")
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[D")
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/logout.jsonl" {
		t.Fatalf("selection = %q after Left, want /tmp/logout.jsonl", got)
	}
}

// "keeps the top row selected when sessions arrive after typing a query": a query clears selectionTouched, so a list that
// grows keeps the top row instead of the session that was selected before.
func TestSessionSelectorKeepsTheTopRowSelectedWhenSessionsArriveAfterTypingAQuery(t *testing.T) {
	var selected []string
	sessions := []SessionInfo{searchSelectionSession("login", "fix login", 6000), searchSelectionSession("docs", "docs", 3000)}
	s := searchSelectionSelector(t, sessions, &selected)
	s.HandleInput("\x1b[B")
	typeSessionQuery(s, "fix")
	if s.selectionTouched || s.pendingSelectedPath != "" {
		t.Fatalf("a query left selectionTouched=%v pendingSelectedPath=%q", s.selectionTouched, s.pendingSelectedPath)
	}
	s.setScopeSessions(sessionScopeCurrent, append([]SessionInfo{searchSelectionSession("newer", "fix newer", 7000)}, sessions...))
	s.refilterLoadedSessions(false)
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/newer.jsonl" {
		t.Fatalf("selection = %q after the list grew, want /tmp/newer.jsonl", got)
	}
}

// Boundary transitions for session selector search: cursor keys at start/end of input, typing after a cursor move, and no-match query.
func TestSessionSelectorSearchBoundaryTransitions(t *testing.T) {
	var selected []string
	s := searchSelectionSelector(t, []SessionInfo{
		searchSelectionSession("login", "fix login", 6000),
		searchSelectionSession("logout", "fix logout", 5000),
	}, &selected)
	typeSessionQuery(s, "fi")
	s.HandleInput("\x1b[B")
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/logout.jsonl" {
		t.Fatalf("Down selected %q, want /tmp/logout.jsonl", got)
	}
	// Cursor keys at end and start keep selection.
	for _, key := range []string{"\x1b[C", "\x1b[F", "\x1b[H", "\x1b[D"} {
		s.HandleInput(key)
		if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/logout.jsonl" {
			t.Fatalf("key %q moved selection to %q, want /tmp/logout.jsonl", key, got)
		}
	}
	// Typing after a cursor move changes the query and resets selection to row 0.
	s.HandleInput("\x1b[F")
	s.HandleInput("x")
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "/tmp/login.jsonl" {
		t.Fatalf("typing after cursor move selected %q, want /tmp/login.jsonl", got)
	}
	// Query matching nothing + cursor key leaves selection empty.
	typeSessionQuery(s, "zzzz")
	s.HandleInput("\x1b[D")
	if got := s.GetSessionList().GetSelectedSessionPath(); got != "" {
		t.Fatalf("no-match selection = %q, want empty", got)
	}
}

// thinking-selector.ts (5bcf370a5): a key that leaves the query unchanged does not rebuild the list.
func TestThinkingSelectorKeepsItsListWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	useKeybindings(t, nil)
	selector := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high"}, func(ai.ThinkingLevel) {}, func() {}, nil, "")
	selector.HandleInput("m")
	list := selector.GetSelectList()
	selector.HandleInput("\x1b[D")
	if selector.GetSelectList() != list {
		t.Fatal("a cursor key rebuilt the select list")
	}
	if item, ok := list.SelectedItem(); !ok || item.Value != "medium" {
		t.Fatalf("selection = %#v, want medium", item)
	}
}
