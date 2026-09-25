package tui

import (
	"strings"
	"testing"
)

func TestRenderDiff_BasicColoring(t *testing.T) {
	diff := "+  1 added line\n-  2 removed line\n   3 context line"
	out := RenderDiff(diff)
	if !strings.Contains(out, "added line") {
		t.Error("should contain added line")
	}
	if !strings.Contains(out, "removed line") {
		t.Error("should contain removed line")
	}
	if !strings.Contains(out, "context line") {
		t.Error("should contain context line")
	}
}

func TestRenderDiff_IntraLineHighlighting(t *testing.T) {
	// A single removed + added pair triggers intra-line word diff.
	diff := "-  1 hello world\n+  1 hello universe"
	out := RenderDiff(diff)
	// The changed word should be wrapped in inverse.
	inverse := "\x1b[7m"
	if !strings.Contains(out, inverse) {
		t.Error("should contain inverse escape for intra-line diff")
	}
}

func TestRenderDiff_MultiLineNoIntra(t *testing.T) {
	// Multiple removed/added lines don't get intra-line diff.
	diff := "-  1 line a\n-  2 line b\n+  1 line c\n+  2 line d"
	out := RenderDiff(diff)
	inverse := "\x1b[7m"
	if strings.Contains(out, inverse) {
		t.Error("should NOT contain inverse for multi-line diff")
	}
}

func TestSplitWords(t *testing.T) {
	got := splitWords("hello   world foo")
	if len(got) != 5 {
		t.Errorf("len = %d, want 5, got %v", len(got), got)
	}
}

func TestDiffWords(t *testing.T) {
	old := splitWords("hello world")
	new := splitWords("hello universe")
	chunks := diffWords(old, new)
	// Should have: equal("hello"), equal(" "), remove("world"), add("universe")
	hasRemove := false
	hasAdd := false
	for _, c := range chunks {
		if c.op == diffRemove {
			hasRemove = true
		}
		if c.op == diffAdd {
			hasAdd = true
		}
	}
	if !hasRemove || !hasAdd {
		t.Errorf("expected remove and add ops, got %v", chunks)
	}
}
