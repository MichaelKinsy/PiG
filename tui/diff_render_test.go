package tui

// pi: packages/coding-agent/src/modes/interactive/components/diff.ts

import (
	"slices"
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

// Moved from internal/codingagent when that package's duplicate diff renderer was removed.
func TestParseDiffLine(t *testing.T) {
	tests := []struct {
		line    string
		prefix  string
		lineNum string
		content string
		ok      bool
	}{
		{"+  1 added line", "+", "  1", "added line", true},
		{"-  1 removed line", "-", "  1", "removed line", true},
		{"   2 context line", " ", "  2", "context line", true},
		{"      ...", " ", "    ", "...", true},
		{"", "", "", "", false},
		{"x invalid", "", "", "", false},
		// JavaScript `.` stops at LS, PS and CR, and `\s` matches the Unicode spaces (Node 24 probe of the upstream regex).
		{"+1 a\u2028b", "", "", "", false},
		{"-1 a\u2029b", "", "", "", false},
		{"+1 a\rb", "", "", "", false},
		{"+1 a\u0085b", "+", "1", "a\u0085b", true},
		{"\u00a0 1 ctx", "\u00a0", " 1", "ctx", true},
		{"+\u00a01\u00a0x", "+", "\u00a01", "x", true},
		{"+1\u3000x", "+", "1", "x", true},
		{"\ufeff1 x", "\ufeff", "1", "x", true},
		{"\v1 x", "\v", "1", "x", true},
	}
	for _, tc := range tests {
		p := parseDiffLine(tc.line)
		if (p != nil) != tc.ok {
			t.Errorf("parseDiffLine(%q): ok=%v, want %v", tc.line, p != nil, tc.ok)
			continue
		}
		if p == nil {
			continue
		}
		if p.prefix != tc.prefix || p.lineNum != tc.lineNum || p.content != tc.content {
			t.Errorf("parseDiffLine(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.line, p.prefix, p.lineNum, p.content, tc.prefix, tc.lineNum, tc.content)
		}
	}
}

// Upstream renderDiff draws a row parseDiffLine rejects verbatim in toolDiffContext, so a removed row holding U+2028 is a context row and does not pair with the added row that follows (components/diff.ts:88-92).
func TestRenderDiffDrawsALineSeparatorRowAsContext(t *testing.T) {
	th := ActiveTheme()
	got := strings.Split(RenderDiff("-1 a\u2028b\n+1 c"), "\n")
	want := []string{th.Fg("toolDiffContext", "-1 a\u2028b"), th.Fg("toolDiffAdded", "+1 c")}
	if !slices.Equal(got, want) {
		t.Errorf("RenderDiff = %q, want %q", got, want)
	}
}

// diff.ts:68-79: renderDiff(diffText, _options) ignores the options; a file path changes nothing.
func TestRenderDiffIgnoresItsOptions(t *testing.T) {
	diff := "+1 added\n-2 removed\n 3 context"
	if got, want := RenderDiff(diff, RenderDiffOptions{FilePath: "a.go"}), RenderDiff(diff); got != want || got == "" {
		t.Fatalf("options changed the output:\n%q\n%q", got, want)
	}
}
