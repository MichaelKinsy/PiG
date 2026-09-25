package widthx

import "testing"

// TestExtractAnsi_AllSupportedSequences mirrors upstream's extractAnsiCode.
// Each case is a one-line slice from upstream's test suite + manual examples.
func TestExtractAnsi_AllSupportedSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		at   int
		want int
	}{
		{"empty", "", 0, 0},
		{"non-escape", "hello", 0, 0},
		{"CSI SGR reset", "\x1b[0m", 0, 4},
		{"CSI SGR bold red", "\x1b[1;31m", 0, 7},
		{"CSI cursor abs", "\x1b[5G", 0, 4},
		{"CSI clear line", "\x1b[K", 0, 3},
		{"CSI cursor home", "\x1b[H", 0, 3},
		{"CSI clear screen", "\x1b[2J", 0, 4},
		{"CSI cursor up (NOT stripped)", "\x1b[3A", 0, 0},
		{"OSC 8 hyperlink open", "\x1b]8;;https://e.com\x07", 0, 19},
		{"OSC 8 hyperlink close", "\x1b]8;;\x07", 0, 6},
		{"OSC with ST terminator", "\x1b]0;title\x1b\\", 0, 11},
		{"APC cursor marker", "\x1b_pi:c\x07", 0, 7},
		{"APC with ST terminator", "\x1b_abc\x1b\\", 0, 7},
		{"escape mid-string", "x\x1b[31my", 1, 5},
		{"unterminated CSI", "\x1b[", 0, 0},
		{"unterminated OSC", "\x1b]abc", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractAnsi(tc.in, tc.at); got != tc.want {
				t.Errorf("ExtractAnsi(%q, %d) = %d, want %d", tc.in, tc.at, got, tc.want)
			}
		})
	}
}

func TestStripAnsi_RoundTrip(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"hello", "hello"},
		{"\x1b[1;31mhello\x1b[0m", "hello"},
		{"\x1b]8;;https://e.com\x07link\x1b]8;;\x07", "link"},
		{"\x1b_pi:c\x07prompt", "prompt"},
		{"line\nwith\nnewlines", "line\nwith\nnewlines"},
		{"\x1b[31mred\x1b[0m \x1b[32mgreen\x1b[0m", "red green"},
	}
	for _, tc := range cases {
		if got := StripAnsi(tc.in); got != tc.want {
			t.Errorf("StripAnsi(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVisibleWidth_ParityCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"tab expands to 3 spaces", "a\tb", 5},
		{"two tabs", "\t\t", 6},
		{"ansi styled", "\x1b[1;31mhello\x1b[0m", 5},
		{"osc hyperlink", "\x1b]8;;https://e.com\x07link\x1b]8;;\x07", 4},
		{"cursor marker invisible", "a\x1b_pi:c\x07b", 2},
		{"wide CJK", "你好", 4},
		{"mixed ascii + cjk", "a你b好", 6},
		{"emoji", "👋", 2},
		{"styled wide char", "\x1b[31m世\x1b[0m", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisibleWidth(tc.in); got != tc.want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestVisibleWidth_DriftFromOldStripANSI documents the bugs in the legacy
// tui/tui.go::stripANSI that this package fixes. When τ.2 lands and
// the legacy function is removed, these regression cases prove the new
// implementation closes the drift.
func TestVisibleWidth_DriftFromOldStripANSI(t *testing.T) {
	// Old stripANSI handled only m/K/H/J finals: width for OSC and APC
	// sequences was reported wrong.
	osc := "\x1b]8;;https://example.com\x07X\x1b]8;;\x07"
	if got := VisibleWidth(osc); got != 1 {
		t.Errorf("OSC-wrapped 'X' width = %d, want 1 (old code reported wrong)", got)
	}
	apc := "\x1b_pi:c\x07Y"
	if got := VisibleWidth(apc); got != 1 {
		t.Errorf("APC-prefixed 'Y' width = %d, want 1 (old code reported wrong)", got)
	}
}

func TestNormalizeTerminalOutput(t *testing.T) {
	if got := NormalizeTerminalOutput("hello"); got != "hello" {
		t.Fatalf("NormalizeTerminalOutput ASCII = %q, want %q", got, "hello")
	}
	if got := NormalizeTerminalOutput("\u0e33"); got != "\u0e4d\u0e32" {
		t.Fatalf("NormalizeTerminalOutput Thai AM = %q, want %q", got, "\u0e4d\u0e32")
	}
	if got := NormalizeTerminalOutput("\u0eb3"); got != "\u0ecd\u0eb2" {
		t.Fatalf("NormalizeTerminalOutput Lao AM = %q, want %q", got, "\u0ecd\u0eb2")
	}
	// Visible tabs expand to 3 spaces (matching VisibleWidth) so terminal tab
	// stops cannot wrap a logical line.
	if got := NormalizeTerminalOutput("a\tb"); got != "a   b" {
		t.Fatalf("NormalizeTerminalOutput tab = %q, want %q", got, "a   b")
	}
	// Tabs inside an ANSI escape sequence are skipped by the expander, and
	// styled text keeps its SGR codes intact around an expanded tab.
	if got := NormalizeTerminalOutput("\x1b[31m\tx\x1b[0m"); got != "\x1b[31m   x\x1b[0m" {
		t.Fatalf("NormalizeTerminalOutput styled tab = %q, want %q", got, "\x1b[31m   x\x1b[0m")
	}
}

func TestVisibleWidth_ThaiAM(t *testing.T) {
	if got := VisibleWidth("กำ"); got != 2 {
		t.Fatalf("VisibleWidth(กำ) = %d, want 2", got)
	}
	if got := VisibleWidth("ກຳ"); got != 2 {
		t.Fatalf("VisibleWidth(ກຳ) = %d, want 2", got)
	}
	if got := VisibleWidth(NormalizeTerminalOutput("กำ")); got != 2 {
		t.Fatalf("VisibleWidth(normalized กำ) = %d, want 2", got)
	}
}

// Microbenchmark to make sure the strip+measure hot path is allocation-light.
func BenchmarkVisibleWidth_Styled(b *testing.B) {
	s := "\x1b[1;31mhello\x1b[0m \x1b[32mworld\x1b[0m"
	b.ReportAllocs()
	for range b.N {
		_ = VisibleWidth(s)
	}
}

func BenchmarkVisibleWidth_Plain(b *testing.B) {
	s := "hello world"
	b.ReportAllocs()
	for range b.N {
		_ = VisibleWidth(s)
	}
}

// Upstream extractAnsiCode ends a CSI only at m|G|K|H|J, so a lone DEC
// private-mode sequence is not an escape and its bytes after ESC are visible.
// PiG must measure exactly as Pi does (D47 removed); see pi_width_diff_test.go.
func TestExtractAnsi_PrivateModeMatchesUpstream(t *testing.T) {
	if got := ExtractAnsi("\x1b[?2026h", 0); got != 0 {
		t.Fatalf("ExtractAnsi(sync start) = %d, want 0 (upstream)", got)
	}
	if got := VisibleWidth("\x1b[?2026h"); got != 7 {
		t.Fatalf("VisibleWidth(sync start) = %d, want 7 (upstream)", got)
	}
	if got := VisibleWidth("\x1b[?25lhi\x1b[0m"); got != 0 {
		t.Fatalf("VisibleWidth = %d, want 0 (upstream consumes through m)", got)
	}
}
