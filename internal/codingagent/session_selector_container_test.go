package codingagent

import (
	"strings"
	"testing"
)

// session-selector.ts:694-754 and 885-908: SessionSelectorComponent extends Container; buildBaseLayout adds spacer, accent border,
// spacer, header, spacer, list, spacer, border, and rename mode swaps header and list for the rename panel.
func TestSessionSelectorComponentIsAContainerWhoseLayoutFollowsRenameMode(t *testing.T) {
	sessions := []SessionInfo{{Path: "/s/a.jsonl", ID: "a", Name: "alpha", FirstMessage: "hello"}}
	s := newLoadedSessionSelector(func() ([]SessionInfo, error) { return sessions, nil }, func() ([]SessionInfo, error) { return sessions, nil },
		func(string, string) error { return nil }, nil, "/s/a.jsonl", sessionSelectorInputBindings(t))
	if got := len(s.Children()); got != 8 {
		t.Fatalf("list-mode children = %d, want spacer, border, spacer, header, spacer, list, spacer, border", got)
	}
	s.enterRenameMode()
	if got := len(s.Children()); got != 6 {
		t.Fatalf("rename-mode children = %d, want spacer, border, spacer, panel, spacer, border", got)
	}
	if got := strings.Join(s.Render(60), "\n"); !strings.Contains(got, "Rename Session") {
		t.Fatalf("rename panel not rendered: %s", got)
	}
	s.exitRenameMode()
	if got := len(s.Children()); got != 8 {
		t.Fatalf("children after leaving rename mode = %d, want 8", got)
	}
}
