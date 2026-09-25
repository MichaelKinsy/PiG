package widthx

import "testing"

func TestExtractAnsiCode(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		pos     int
		want    string
		wantLen int
	}{
		{"csi sgr", "\x1b[31mX", 0, "\x1b[31m", 5},
		{"csi cursor col G", "\x1b[5GX", 0, "\x1b[5G", 4},
		{"osc hyperlink bel", "\x1b]8;;http://x\x07Y", 0, "\x1b]8;;http://x\x07", 14},
		{"osc st terminator", "\x1b]8;;u\x1b\\Y", 0, "\x1b]8;;u\x1b\\", 8},
		{"apc bel", "\x1b_Gi=1;AA\x07Z", 0, "\x1b_Gi=1;AA\x07", 10},
		{"not an escape", "abc", 0, "", 0},
		{"pos past end", "\x1b", 0, "", 0},
		// Upstream does NOT terminate CSI on h/l; it scans to end and returns none.
		{"csi h not consumed (upstream fidelity)", "\x1b[?25hX", 0, "", 0},
		{"csi l not consumed (upstream fidelity)", "\x1b[?25lX", 0, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, n := ExtractAnsiCode(tt.s, tt.pos)
			if code != tt.want || n != tt.wantLen {
				t.Errorf("ExtractAnsiCode(%q,%d) = (%q,%d), want (%q,%d)", tt.s, tt.pos, code, n, tt.want, tt.wantLen)
			}
		})
	}
}

func TestGraphemeCellRange(t *testing.T) {
	tests := []struct {
		name               string
		line               string
		column             int
		wantStart, wantEnd int
		wantOK             bool
	}{
		{"ascii col 0", "abc", 0, 0, 1, true},
		{"ascii col 2", "abc", 2, 2, 3, true},
		{"past end", "abc", 3, 0, 0, false},
		// ANSI escapes contribute no width; the letter after them is at column 0.
		{"leading sgr then char", "\x1b[31mA", 0, 0, 1, true},
		// A wide (CJK) grapheme occupies two cells; both columns map to [0,2).
		{"wide char left cell", "世", 0, 0, 2, true},
		{"wide char right cell", "世", 1, 0, 2, true},
		{"col after wide char", "世x", 2, 2, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := GraphemeCellRange(tt.line, tt.column)
			if ok != tt.wantOK || (ok && (start != tt.wantStart || end != tt.wantEnd)) {
				t.Errorf("GraphemeCellRange(%q,%d) = (%d,%d,%v), want (%d,%d,%v)",
					tt.line, tt.column, start, end, ok, tt.wantStart, tt.wantEnd, tt.wantOK)
			}
		})
	}
}

// TestVisibleWidthUnchangedAfterRefactor guards that extracting
// normalizeGraphemeWidth preserved VisibleWidth's behavior across the cases the
// per-grapheme logic covers (ASCII, CJK, ANSI, Thai AM).
func TestVisibleWidthUnchangedAfterRefactor(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"\x1b[31mabc\x1b[0m", 3},
		{"世界", 4},
		{"a世b", 4},
		{"", 0},
		{"\u0e01\u0e33", 2}, // Thai consonant + AM vowel
	}
	for _, c := range cases {
		if got := VisibleWidth(c.s); got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestStripTerminalSequences(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[1mBold\x1b[0m", "Bold"},
		{"a\x1b]8;;http://x\x07link\x1b]8;;\x07b", "alinkb"},
		// D47: a private-mode set/reset with no later CSI terminator is left
		// intact (ExtractAnsiCode has no h/l terminator, unlike StripAnsi).
		{"X\x1b[?2026l", "X\x1b[?2026l"},
	}
	for _, tt := range tests {
		if got := StripTerminalSequences(tt.in); got != tt.want {
			t.Errorf("StripTerminalSequences(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetOsc8LinkAtColumn(t *testing.T) {
	line := "\x1b]8;;https://x\x07AB\x1b]8;;\x07"
	if url, ok := GetOsc8LinkAtColumn(line, 0); !ok || url != "https://x" {
		t.Errorf("col 0 = (%q,%v), want (https://x,true)", url, ok)
	}
	if url, ok := GetOsc8LinkAtColumn(line, 1); !ok || url != "https://x" {
		t.Errorf("col 1 = (%q,%v), want (https://x,true)", url, ok)
	}
	// Past the linked text (link closed with empty URL) -> no active link.
	if url, ok := GetOsc8LinkAtColumn(line, 5); ok {
		t.Errorf("col 5 = (%q,%v), want no link", url, ok)
	}
	// A line with no link at all.
	if _, ok := GetOsc8LinkAtColumn("plain text", 2); ok {
		t.Error("plain text should have no link")
	}
}
