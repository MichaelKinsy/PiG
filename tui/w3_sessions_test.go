package tui

import (
	"slices"
	"strings"
	"testing"
)

// Pi 0.87.1 tree-selector.ts:181-195, 243-286, 742-743: stable active-first DFS and markers only on the root-to-leaf path.
func TestTreeActiveBranchOrderAndMarkers(t *testing.T) {
	old := &fakeNode{id: "old", label: "old", kids: []TreeNode{&fakeNode{id: "old-leaf", label: "old leaf"}}}
	active := &fakeNode{id: "active", label: "active", kids: []TreeNode{&fakeNode{id: "active-leaf", label: "active leaf"}}}
	root := &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "common", label: "common", kids: []TreeNode{old, active}}}}
	ts := NewTreeSelect("", root)
	ts.SetInitialCursor("active-leaf", "old")
	var ids []string
	for _, row := range ts.rows {
		ids = append(ids, row.id)
	}
	if want := []string{"common", "active", "active-leaf", "old", "old-leaf"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
	if ts.rows[ts.cursor].id != "old" {
		t.Error("initial selection must not change the active path")
	}
	lines := strings.Join(markdownPlainLines(ts.Render(100)), "\n")
	for _, want := range []string{"• common", "• active", "• active leaf"} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q: %s", want, lines)
		}
	}
	if strings.Contains(lines, "• old") {
		t.Errorf("inactive path marked: %s", lines)
	}
	ts.HandleInput("old leaf")
	lines = strings.Join(markdownPlainLines(ts.Render(100)), "\n")
	if !strings.Contains(lines, "› old leaf\n") {
		t.Errorf("search must recompute flat structure, without inactive marker: %s", lines)
	}
}

// Pi tree-selector.ts:1047-1107: digits are printable search input, not filter shortcuts.
func TestTreeDigitsAreSearchText(t *testing.T) {
	ts := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "match", label: "12345"}}})
	for _, key := range []string{"1", "2", "3", "4", "5"} {
		ts.HandleInput(key)
	}
	if ts.searchQuery != "12345" || ts.filterMode != "default" || len(ts.rows) != 1 {
		t.Fatalf("query=%q filter=%q rows=%d", ts.searchQuery, ts.filterMode, len(ts.rows))
	}
}

// Pi tui/components/markdown.ts:606 caps a thematic break at 80 content columns.
func TestMarkdownRuleWidthCap(t *testing.T) {
	for _, width := range []int{1, 40, 79, 80, 81, 120} {
		lines := markdownPlainLines(NewMarkdown("---").Render(width))
		if len(lines) != 1 || lines[0] != strings.Repeat("─", min(width, 80)) {
			t.Errorf("width %d: %q", width, lines)
		}
	}
}

func BenchmarkTreeActiveBranch(b *testing.B) {
	root := &fakeNode{id: "root"}
	for i := range 1000 {
		root.kids = append(root.kids, &fakeNode{id: strings.Repeat("x", i+1), label: "entry"})
	}
	leaf := root.kids[len(root.kids)-1].NodeID()
	b.ReportAllocs()
	for b.Loop() {
		ts := NewTreeSelect("", root)
		ts.SetInitialCursor(leaf, "")
		ts.Render(120)
	}
}
