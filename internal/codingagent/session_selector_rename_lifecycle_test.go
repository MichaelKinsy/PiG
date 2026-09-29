package codingagent

import (
	"testing"
	"time"
)

// Pi session-selector.ts:726,885-932 retains the Input across both cancel and submit.
func TestSessionSelectorRenameRetainsInputAcrossVisits(t *testing.T) {
	for _, tc := range []struct {
		name, next, want string
	}{
		{"ASCII", "Old", "OXld"},
		{"surrogate half", "😀", "\xed\xa0\xbdX\xed\xb8\x80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := []SessionInfo{{Path: "/tmp/a.jsonl", ID: "a", Created: time.UnixMilli(0), Modified: time.UnixMilli(0)}}
			var calls [][2]string
			sel := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return nil, nil }, func(path, name string) error {
				calls = append(calls, [2]string{path, name})
				return nil
			}, nil, "", sessionSelectorInputBindings(t))
			sel.HandleInput("\x1b[114;5u")
			sel.HandleInput("a")
			sel.HandleInput("\x1b")
			if len(calls) != 0 || sel.renameMode {
				t.Fatalf("cancel committed rename or retained mode: %q", calls)
			}
			sessions[0].Name = tc.next
			sel.refreshCurrentScope()
			sel.HandleInput("\x1b[114;5u")
			sel.HandleInput("X")
			sel.HandleInput("\r")
			if len(calls) != 1 || calls[0] != ([2]string{sessions[0].Path, tc.want}) {
				t.Fatalf("rename calls=%q; want name bytes % x", calls, tc.want)
			}
		})
	}
}

// Pi session-selector.ts:916-918 leaves the rename panel open on whitespace.
func TestSessionSelectorRenameEmptySubmissionKeepsFocus(t *testing.T) {
	sessions := []SessionInfo{{Path: "/tmp/a.jsonl", ID: "a"}}
	calls := 0
	sel := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return nil, nil }, func(string, string) error { calls++; return nil }, nil, "", sessionSelectorInputBindings(t))
	sel.HandleInput("\x1b[114;5u")
	sel.HandleInput(" ")
	sel.HandleInput("\r")
	if !sel.renameMode || calls != 0 {
		t.Fatalf("empty submit mode=%v calls=%d", sel.renameMode, calls)
	}
	sel.HandleInput("X")
	sel.HandleInput("\r")
	sel.drainLoadUpdates() // Await the fixture's resolved post-rename refresh.
	if sel.renameMode || calls != 1 {
		t.Fatalf("corrected submit mode=%v calls=%d", sel.renameMode, calls)
	}
}
