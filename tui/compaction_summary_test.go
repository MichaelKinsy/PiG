package tui

// pi: packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts

import (
	"strings"
	"testing"
)

func TestCompactionSummaryCollapsed(t *testing.T) {
	c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "## Goal\nDo things.", TokensBefore: 87432}, nil, 1)
	lines := c.Render(60)

	// Must have top pad + label + spacer + body + bottom pad = 5 rows.
	if len(lines) < 5 {
		t.Fatalf("collapsed: expected >= 5 rows, got %d", len(lines))
	}

	// Top and bottom padding rows should be blank bg-painted.
	if v := stripANSI(lines[0]); strings.TrimSpace(v) != "" {
		t.Errorf("row 0: expected blank top padding, got: %q", v)
	}
	if v := stripANSI(lines[len(lines)-1]); strings.TrimSpace(v) != "" {
		t.Errorf("last row: expected blank bottom padding, got: %q", v)
	}

	joined := strings.Join(lines, "\n")
	plain := stripANSI(joined)

	if !strings.Contains(plain, "[compaction]") {
		t.Error("collapsed form must contain [compaction] label")
	}
	if !strings.Contains(plain, "87,432") {
		t.Error("collapsed form must contain formatted token count")
	}
	if !strings.Contains(plain, "ctrl+o") {
		t.Error("collapsed form must contain ctrl+o expand hint")
	}
	if strings.Contains(plain, "Do things") {
		t.Error("collapsed form must not contain summary text")
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts:22 (CompactionSummaryMessageComponent.setExpanded).
func TestCompactionSummaryExpanded(t *testing.T) {
	c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "## Goal\nDo things.", TokensBefore: 87432}, nil, 1)
	c.SetExpanded(true)
	lines := c.Render(60)

	joined := strings.Join(lines, "\n")
	plain := stripANSI(joined)

	if !strings.Contains(plain, "[compaction]") {
		t.Error("expanded form must contain [compaction] label")
	}
	if !strings.Contains(plain, "Compacted from 87,432 tokens") {
		t.Error("expanded form must contain token header")
	}
	if !strings.Contains(plain, "Do things") {
		t.Error("expanded form must contain summary text")
	}
	// ctrl+o hint is only shown in the collapsed body line.
	if strings.Contains(plain, "ctrl+o") {
		t.Error("expanded form must not contain ctrl+o expand hint")
	}
}

func TestCompactionSummaryTokenFormat(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{87432, "87,432"},
		{1000000, "1,000,000"},
		{999, "999"},
		{1000, "1,000"},
		{0, "0"},
		{42, "42"},
		{1234567890, "1,234,567,890"},
	}
	for _, tc := range cases {
		got := formatThousands(tc.n)
		if got != tc.want {
			t.Errorf("formatThousands(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestCompactionSummaryMessageComponentUsesTheInjectedMarkdownTheme: Pi's constructor takes the markdown theme the expanded
// summary renders with (compaction-summary-message.ts:15,47); nil is the active theme's.
func TestCompactionSummaryMessageComponentUsesTheInjectedMarkdownTheme(t *testing.T) {
	theme := GetMarkdownTheme()
	theme.Heading = func(text string) string { return "<h>" + text + "</h>" }
	message := CompactionSummaryMessage{Summary: "## Goal\nDo things.", TokensBefore: 87432}
	injected := NewCompactionSummaryMessageComponent(message, &theme, 1)
	injected.SetExpanded(true)
	if plain := stripANSI(strings.Join(injected.Render(60), "\n")); !strings.Contains(plain, "<h>Goal</h>") {
		t.Fatalf("the expanded summary must render with the injected markdown theme:\n%s", plain)
	}
	active := NewCompactionSummaryMessageComponent(message, nil, 1)
	active.SetExpanded(true)
	if plain := stripANSI(strings.Join(active.Render(60), "\n")); strings.Contains(plain, "<h>") || !strings.Contains(plain, "Goal") {
		t.Fatalf("a nil markdown theme is the active theme's:\n%s", plain)
	}
}

// compaction-summary-message.ts:15: the third constructor argument outputPad (default 1) is the Box's horizontal padding.
func TestCompactionSummaryOutputPadIsTheHorizontalPadding(t *testing.T) {
	message := CompactionSummaryMessage{Summary: "s", TokensBefore: 1}
	indent := func(c *CompactionSummaryMessageComponent) int {
		for _, line := range c.Render(60) {
			if plain := stripANSI(line); strings.Contains(plain, "[compaction]") {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		t.Fatal("no [compaction] line")
		return 0
	}
	if got := indent(NewCompactionSummaryMessageComponent(message, nil)); got != 1 {
		t.Errorf("default outputPad indents the label by %d columns, want 1", got)
	}
	for _, pad := range []int{0, 3} {
		if got := indent(NewCompactionSummaryMessageComponent(message, nil, pad)); got != pad {
			t.Errorf("outputPad %d indents the label by %d columns", pad, got)
		}
	}
}
