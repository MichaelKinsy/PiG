package tui

import (
	"strings"
	"testing"
)

func TestTreeSelect_EmptyStateMatchesUpstreamTextShape(t *testing.T) {
	ts := NewTreeSelect("", &fakeNode{id: "root"})
	lines := ts.Render(100)
	joined := strings.Join(lines, "\n")

	for _, want := range []string{
		"Session Tree",
		"Type to search:",
		"No entries found",
		"(0/0)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("empty tree render missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "(empty tree)") {
		t.Fatalf("legacy empty-tree marker leaked:\n%s", joined)
	}
}
