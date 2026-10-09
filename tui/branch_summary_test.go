package tui

import (
	"strings"
	"testing"
)

func TestBranchSummaryCollapsed(t *testing.T) {
	comp := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "The user explored a sidebar about error handling."}, nil, 1)
	out := comp.Render(80)

	// Collapsed: must have top pad + label + spacer + body + bottom pad = 5 lines.
	if len(out) < 5 {
		t.Fatalf("collapsed: expected >= 5 rows, got %d", len(out))
	}

	// Row 0: top padding (blank bg-painted).
	if v := stripANSI(out[0]); strings.TrimSpace(v) != "" {
		t.Errorf("row 0: expected blank top padding, got: %q", v)
	}

	// Row 1: label "[branch]".
	if !strings.Contains(stripANSI(out[1]), "[branch]") {
		t.Errorf("row 1: expected [branch] label, got: %q", stripANSI(out[1]))
	}

	// Row 2: structural spacer: visible text is empty.
	if v := stripANSI(out[2]); strings.TrimSpace(v) != "" {
		t.Errorf("row 2: expected blank spacer, got: %q", v)
	}

	// Row 3: collapsed hint.
	body := stripANSI(out[3])
	if !strings.Contains(body, "Branch summary") {
		t.Errorf("row 3: expected 'Branch summary', got: %q", body)
	}
	if !strings.Contains(body, "ctrl+o") {
		t.Errorf("row 3: expected 'ctrl+o' hint, got: %q", body)
	}
	if !strings.Contains(body, "expand") {
		t.Errorf("row 3: expected 'expand', got: %q", body)
	}

	// Row 4: bottom padding (blank bg-painted).
	if v := stripANSI(out[4]); strings.TrimSpace(v) != "" {
		t.Errorf("row 4: expected blank bottom padding, got: %q", v)
	}

	// All rows have bg paint applied (non-empty ANSI prefix from paintBgWith).
	for i, row := range out {
		if !strings.Contains(row, "\x1b[") {
			t.Errorf("row %d: missing ANSI escape (bg paint not applied?): %q", i, row)
		}
	}

	// Summary text must NOT appear in collapsed state.
	all := stripANSI(strings.Join(out, "\n"))
	if strings.Contains(all, "sidebar about error handling") {
		t.Errorf("collapsed: summary text leaked into output")
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts:22 (BranchSummaryMessageComponent.setExpanded).
func TestBranchSummaryExpanded(t *testing.T) {
	summary := "The user explored a sidebar about error handling."
	comp := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: summary}, nil, 1)
	comp.SetExpanded(true)
	out := comp.Render(80)

	all := stripANSI(strings.Join(out, "\n"))

	// Label must appear.
	if !strings.Contains(all, "[branch]") {
		t.Errorf("expanded: missing [branch] label")
	}

	// Markdown header "Branch Summary" must appear.
	if !strings.Contains(all, "Branch Summary") {
		t.Errorf("expanded: missing 'Branch Summary' header, got:\n%s", all)
	}

	// Summary body must appear.
	if !strings.Contains(all, "error handling") {
		t.Errorf("expanded: summary body missing, got:\n%s", all)
	}

	// ctrl+o hint must NOT appear in expanded state.
	if strings.Contains(all, "ctrl+o") {
		t.Errorf("expanded: ctrl+o hint should not appear in expanded state")
	}
}

func TestBranchSummaryToggle(t *testing.T) {
	comp := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "test summary"}, nil, 1)

	// Default: collapsed.
	collapsed := comp.Render(80)
	if strings.Contains(stripANSI(strings.Join(collapsed, "\n")), "test summary") {
		t.Error("toggle: summary should not appear when collapsed")
	}

	// Expand.
	comp.SetExpanded(true)
	expanded := comp.Render(80)
	if !strings.Contains(stripANSI(strings.Join(expanded, "\n")), "test summary") {
		t.Error("toggle: summary should appear when expanded")
	}

	// Collapse again.
	comp.SetExpanded(false)
	recollapsed := comp.Render(80)
	if strings.Contains(stripANSI(strings.Join(recollapsed, "\n")), "test summary") {
		t.Error("toggle: summary leaked after re-collapse")
	}
}

// branch-summary-message.ts:15-21: constructor(message, markdownTheme = getMarkdownTheme(), outputPad = 1) builds `super(outputPad, 1, ...)` and the
// expanded body is `new Markdown(header + message.summary, 0, 0, this.markdownTheme, ...)`. The constructor's outputPad is the Box's horizontal padding
// and its markdownTheme is the theme the expanded summary renders with.
func TestBranchSummaryMessageComponentTakesItsPadAndMarkdownTheme(t *testing.T) {
	for _, pad := range []int{0, 1, 3} {
		c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "s"}, nil, pad)
		if got := leadingColumns(t, c, "[branch]"); got != pad {
			t.Errorf("outputPad %d: the label starts after %d blank columns", pad, got)
		}
	}
	theme := GetMarkdownTheme()
	theme.Bold = func(text string) string { return "<<" + text + ">>" }
	c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "a **loud** word"}, &theme, 1)
	c.SetExpanded(true)
	// The header and **loud** both render bold, so the custom Bold wraps two runs; the colour codes sit between the markers and the words.
	if out := strings.Join(c.Render(60), "\n"); strings.Count(out, "<<") != 2 {
		t.Fatalf("the expanded summary ignores the markdown theme given to the constructor:\n%s", out)
	}
}
