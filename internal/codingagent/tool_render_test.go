package codingagent

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSITest(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Collapsed read cards show only the call header (no content); Ctrl+O
// expands to the full file. Mirrors upstream read.ts:173 (#4916): collapsed
// formatReadResult returns "".
func TestRenderReadLinesCollapsedShowsNoContent(t *testing.T) {
	d := &tools.ReadDetails{Path: "main.go", StartLine: 1, TotalLines: 3}
	content := "line one\nline two\nline three"
	r := toolBodyRenderer("read", agent.AgentToolResult{Details: d, Content: content}, nil)
	if r == nil {
		t.Fatal("expected renderer for read with ReadDetails")
	}

	collapsed := r(80, false)
	if len(collapsed) != 0 {
		t.Fatalf("collapsed read must emit no body lines, got %d: %q", len(collapsed), collapsed)
	}

	expanded := r(80, true)
	plain := stripANSITest(strings.Join(expanded, "\n"))
	if plain != content {
		t.Errorf("expanded read = %q, want %q", plain, content)
	}
}

func TestEditDiffRenderer(t *testing.T) {
	d := &tools.EditToolDetails{
		Diff:             " 1 before line\n-2 REPLACE_ME\n+2 REPLACED\n 3 after line",
		Patch:            "--- main.go\n+++ main.go\n@@ -1,3 +1,3 @@\n before line\n-REPLACE_ME\n+REPLACED\n after line\n",
		FirstChangedLine: 2,
	}
	r := toolBodyRenderer("edit", agent.AgentToolResult{Details: d}, nil)
	if r == nil {
		t.Fatal("expected renderer for edit with EditToolDetails")
	}
	out := r(80, true)
	joined := strings.Join(out, "\n")
	// Strip ANSI for content checks since intra-line diff adds highlighting.
	plain := stripANSITest(joined)
	for _, want := range []string{"before line", "REPLACE_ME", "REPLACED", "after line"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	// Should have ANSI color escapes (theme-based).
	if !strings.Contains(joined, "\033[") {
		t.Errorf("should have ANSI styling: %s", joined)
	}
	// Should have intra-line highlighting (inverse for the 1:1 change).
	if !strings.Contains(joined, "\x1b[7m") {
		t.Errorf("should have intra-line inverse highlighting: %s", joined)
	}
}

func TestWriteRenderer(t *testing.T) {
	d := &tools.WriteDetails{Path: "hello.sh", Content: "#!/bin/bash\necho hi\n", Overwrote: false}
	r := toolBodyRenderer("write", agent.AgentToolResult{Details: d}, nil)
	if r == nil {
		t.Fatal("expected renderer for write")
	}
	joined := strings.Join(r(80, true), "\n")
	plain := stripANSITest(joined)
	if strings.Contains(plain, "created") || strings.Contains(plain, "@@") {
		t.Errorf("write body must not add a summary header: %s", joined)
	}
	for _, want := range []string{"#!/bin/bash", "echo hi"} {
		if !strings.Contains(plain, want) {
			t.Errorf("write content body missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "+ #!/bin/bash") {
		t.Errorf("write content should not be diff-prefixed: %s", joined)
	}
}

func TestRenderersWrapLongPlainContentWithoutDroppingText(t *testing.T) {
	const width = 20
	long := strings.Repeat("abcdefghij", 8)
	tests := []struct {
		name   string
		render func() []string
	}{
		{
			name: "read",
			render: func() []string {
				details := &tools.ReadDetails{Path: "notes.txt", StartLine: 1, TotalLines: 1}
				return renderReadLines(details, long, width, true)
			},
		},
		{
			name: "write",
			render: func() []string {
				details := &tools.WriteDetails{Path: "notes.txt", Content: long}
				return renderWriteContent(details, width, true)
			},
		},
		{
			name: "edit context",
			render: func() []string {
				return renderDiffString(" 1 "+long, width)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := tt.render()
			plain := strings.ReplaceAll(stripANSITest(strings.Join(lines, "")), "\n", "")
			if !strings.Contains(plain, long) {
				t.Fatalf("wrapped output dropped source text: %q", plain)
			}
			for _, line := range lines {
				for row := range strings.SplitSeq(line, "\n") {
					if got := len([]rune(stripANSITest(row))); got > width {
						t.Fatalf("wrapped row width = %d, want <= %d: %q", got, width, row)
					}
				}
			}
		})
	}
}

func TestWriteRendererTruncatesPreviewToTenLines(t *testing.T) {
	var content strings.Builder
	for i := range 25 {
		fmt.Fprintf(&content, "line %d\n", i+1)
	}
	d := &tools.WriteDetails{Path: "x.txt", Content: content.String()}
	r := toolBodyRenderer("write", agent.AgentToolResult{Details: d}, nil)
	// Collapsed view: should show the 10-line preview + "more lines" hint.
	out := r(80, false)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "15 more lines, 25 total") {
		t.Errorf("collapsed view should show 15 more lines hint: %s", joined)
	}
	// Expanded view: should show all lines.
	outExp := r(80, true)
	joinedExp := strings.Join(outExp, "\n")
	if strings.Contains(joinedExp, "more lines") {
		t.Errorf("expanded view should not show more-lines hint: %s", joinedExp)
	}
	if !strings.Contains(stripANSITest(joinedExp), "line 25") {
		t.Errorf("expanded view should include last line: %s", joinedExp)
	}
}

func TestWriteRendererDoesNotRenderResultMetadata(t *testing.T) {
	d := &tools.WriteDetails{Path: "x", Content: "y", Overwrote: true}
	r := toolBodyRenderer("write", agent.AgentToolResult{Details: d}, nil)
	out := stripANSITest(strings.Join(r(80, true), "\n"))
	if out != "y" {
		t.Errorf("write body = %q, want only file content", out)
	}
}

func TestReadRendererMatchesUpstreamBody(t *testing.T) {
	d := &tools.ReadDetails{Path: "a.go", StartLine: 1, TotalLines: 3}
	content := "alpha\nbeta\ngamma"
	r := toolBodyRenderer("read", agent.AgentToolResult{Details: d, Content: content}, nil)
	if r == nil {
		t.Fatal("expected renderer for read")
	}
	plain := stripANSITest(strings.Join(r(80, true), "\n"))
	if plain != content {
		t.Errorf("read body = %q, want source content without a summary header or line-number gutter", plain)
	}
}

func TestReadRendererTruncationWarnings(t *testing.T) {
	tests := []struct {
		name string
		tr   *tools.TruncationResult
		want string
	}{
		{
			name: "lines",
			tr: &tools.TruncationResult{
				Truncated: true, TruncatedBy: "lines", OutputLines: 2, TotalLines: 10, MaxLines: 2,
			},
			want: "[Truncated: showing 2 of 10 lines (2 line limit)]",
		},
		{
			name: "bytes",
			tr: &tools.TruncationResult{
				Truncated: true, TruncatedBy: "bytes", OutputLines: 3, MaxBytes: 50 * 1024,
			},
			want: "[Truncated: 3 lines shown (50.0KB limit)]",
		},
		{
			name: "first line",
			tr: &tools.TruncationResult{
				Truncated: true, FirstLineExceedsLimit: true, MaxBytes: 50 * 1024,
			},
			want: "[First line exceeds 50.0KB limit]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &tools.ReadDetails{Path: "big.txt", Truncated: true, Truncation: tt.tr}
			plain := stripANSITest(strings.Join(renderReadLines(d, "line\n", 80, true), "\n"))
			if !strings.Contains(plain, tt.want) {
				t.Errorf("truncation output missing %q: %s", tt.want, plain)
			}
		})
	}
}

func TestReadRendererUsesScopedResetsInsideBgPaint(t *testing.T) {
	d := &tools.ReadDetails{Path: "a.go", StartLine: 1, TotalLines: 4}
	content := "package main\n\nfunc main() {}\n"
	r := toolBodyRenderer("read", agent.AgentToolResult{Details: d, Content: content}, nil)
	out := strings.Join(r(80, true), "\n")
	if strings.Contains(out, "\x1b[0m") {
		t.Fatalf("read renderer should avoid full SGR resets inside bg-painted tool output; got %q", out)
	}
	if !strings.Contains(out, "\x1b[39m") {
		t.Fatalf("read renderer should use scoped foreground resets; got %q", out)
	}
}

func TestRendererNilForUnknownTool(t *testing.T) {
	// bash always returns a renderer now (truncate-from-top + Took footer).
	if r := toolBodyRenderer("bash", agent.AgentToolResult{}, nil); r == nil {
		t.Errorf("bash should always return a renderer")
	}
	if r := toolBodyRenderer("edit", agent.AgentToolResult{}, nil); r != nil {
		t.Errorf("edit with no details should fall back to nil renderer")
	}
}

// TestTruncToWidth_CJK: case 4: truncToWidth with CJK characters.
// "日本語テスト" = 6×2 = 12 cols. At width=5: keep 日本 (4 cols) + ellipsis "…" = 5 cols.
// Old rune-count path would keep 4 runes (8 cols) + "…" = 9 cols, overflowing the budget.
func TestTruncToWidth_CJK(t *testing.T) {
	got := truncToWidth("日本語テスト", 5)
	want := "日本\u2026"
	if got != want {
		t.Fatalf("truncToWidth(\"日本語テスト\", 5) = %q, want %q", got, want)
	}
}

func TestParseDiffLine(t *testing.T) {
	tests := []struct {
		line    string
		prefix  byte
		lineNum string
		content string
		ok      bool
	}{
		{"+  1 added line", '+', "  1", "added line", true},
		{"-  1 removed line", '-', "  1", "removed line", true},
		{"   2 context line", ' ', "  2", "context line", true},
		{"      ...", ' ', "    ", "...", true},
		{"", 0, "", "", false},
		{"x invalid", 0, "", "", false},
	}
	for _, tc := range tests {
		p, ln, c, ok := parseDiffLine(tc.line)
		if ok != tc.ok {
			t.Errorf("parseDiffLine(%q): ok=%v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if p != tc.prefix || ln != tc.lineNum || c != tc.content {
			t.Errorf("parseDiffLine(%q) = (%c, %q, %q), want (%c, %q, %q)",
				tc.line, p, ln, c, tc.prefix, tc.lineNum, tc.content)
		}
	}
}

func TestExtractDiffString(t *testing.T) {
	m := map[string]any{"diff": "-1 old\n+1 new", "firstChangedLine": float64(1)}
	got := extractDiffString(m)
	if got != "-1 old\n+1 new" {
		t.Fatalf("extractDiffString = %q, want %q", got, "-1 old\n+1 new")
	}
	if extractDiffString(nil) != "" {
		t.Fatal("expected empty for nil")
	}
	if extractDiffString("not a map") != "" {
		t.Fatal("expected empty for string")
	}
}

func TestRenderDiffString_IntraLineHighlight(t *testing.T) {
	diff := "-1 old line\n+1 new line"
	lines := renderDiffString(diff, 80)
	if len(lines) < 2 {
		t.Fatalf("expected >=2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "-1 ") {
		t.Errorf("line 0 missing removed marker: %q", lines[0])
	}
	if !strings.Contains(lines[1], "+1 ") {
		t.Errorf("line 1 missing added marker: %q", lines[1])
	}
}

func TestRenderDiffString_ContextLines(t *testing.T) {
	diff := "  1 context before\n- 2 removed\n+ 2 added\n  3 context after"
	lines := renderDiffString(diff, 80)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
}

// TestToolBodyRendererWithPreview verifies that when an AgentToolResult
// carries a Preview field, toolBodyRenderer returns a non-nil renderer
// that shows the preview when collapsed and full content when expanded.
func TestToolBodyRendererWithPreview(t *testing.T) {
	result := agent.AgentToolResult{
		Content: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\n",
		Preview: "# Session TOC\n| turn | time | user |\n|---:|---|---|\n| 1 | 00:01 | hello |",
	}
	renderer := toolBodyRenderer("read_session", result, nil)
	if renderer == nil {
		t.Fatal("expected non-nil renderer for tool with Preview")
	}

	// Collapsed: should show preview, not full content.
	collapsed := renderer(80, false)
	joined := strings.Join(collapsed, "\n")
	if !strings.Contains(joined, "Session TOC") {
		t.Errorf("collapsed should contain preview text, got:\n%s", joined)
	}
	if strings.Contains(joined, "line 10") {
		t.Errorf("collapsed should not contain full content")
	}
	if !strings.Contains(joined, "ctrl+o to expand") {
		t.Errorf("collapsed should have expand hint")
	}

	// Expanded: should show full content.
	expanded := renderer(80, true)
	joined = strings.Join(expanded, "\n")
	if !strings.Contains(joined, "line 10") {
		t.Errorf("expanded should contain full content, got:\n%s", joined)
	}
	if strings.Contains(joined, "ctrl+o") {
		t.Errorf("expanded should not have expand hint")
	}
}

// TestToolBodyRendererWithoutPreviewReturnsNil verifies the default
// fallback: unknown tools without Preview get nil (TUI handles them
// with the generic collapsed preview in ToolExecutionComponent).
func TestToolBodyRendererWithoutPreviewReturnsNil(t *testing.T) {
	result := agent.AgentToolResult{
		Content: "some output\n",
	}
	renderer := toolBodyRenderer("custom_tool", result, nil)
	if renderer != nil {
		t.Errorf("expected nil renderer for unknown tool without Preview")
	}
}
