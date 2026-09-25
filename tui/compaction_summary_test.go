package tui

import (
	"strings"
	"testing"
)

func TestCompactionSummaryCollapsed(t *testing.T) {
	c := NewCompactionSummaryComponent("## Goal\nDo things.", 87432)
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

func TestCompactionSummaryExpanded(t *testing.T) {
	c := NewCompactionSummaryComponent("## Goal\nDo things.", 87432)
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
