package pixel

import (
	"image/color"
	"regexp"
	"strings"
	"testing"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func cells(line string) int {
	n := 0
	for _, r := range sgr.ReplaceAllString(line, "") {
		n += CellWidth(r)
	}
	return n
}

func TestEncodePairsRowsIntoExactWidthHalfBlocks(t *testing.T) {
	var c Canvas
	c.Resize(5, 3)
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	c.Rect(0, 0, 5, 1, red)
	c.Rect(0, 1, 5, 2, blue)
	e := Encoder{TrueColor: true}
	lines := e.Encode(&c, nil)
	if len(lines) != 2 {
		t.Fatalf("3 pixel rows encode to %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		if cells(line) != 5 || !strings.HasSuffix(line, reset) {
			t.Fatalf("line %d = %q", i, line)
		}
	}
	if want := "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀▀▀▀▀" + reset; lines[0] != want {
		t.Fatalf("line 0 = %q, want %q (colors emitted once per run)", lines[0], want)
	}
}

func TestEncodeFallsBackTo256Colors(t *testing.T) {
	var c Canvas
	c.Resize(2, 2)
	c.Rect(0, 0, 2, 2, color.RGBA{0x48, 0xA3, 0x81, 0xFF})
	lines := (&Encoder{}).Encode(&c, nil)
	if strings.Contains(lines[0], "38;2;") || !strings.Contains(lines[0], "\x1b[38;5;") {
		t.Fatalf("256-color line = %q", lines[0])
	}
}

func TestEncodeReusesUnchangedLinesWithoutAllocating(t *testing.T) {
	var c Canvas
	c.Resize(80, 40)
	c.Rect(0, 0, 80, 40, color.RGBA{10, 20, 30, 255})
	e := Encoder{TrueColor: true}
	dst := make([]string, 0, 20)
	dst = e.Encode(&c, dst[:0])
	if allocs := testing.AllocsPerRun(20, func() { dst = e.Encode(&c, dst[:0]) }); allocs != 0 {
		t.Fatalf("re-encoding an unchanged canvas allocates %.1f times", allocs)
	}
	c.Set(3, 7, color.RGBA{200, 0, 0, 255})
	if allocs := testing.AllocsPerRun(1, func() {
		c.Set(3, 7, color.RGBA{uint8(len(dst)), 1, 0, 255})
		dst = e.Encode(&c, dst[:0])
	}); allocs > 1 {
		t.Fatalf("one changed line allocates %.1f times, want at most 1", allocs)
	}
}

func TestFitLinePadsTruncatesAndKeepsStyles(t *testing.T) {
	if got := FitLine("\x1b[1mab\x1b[0m", 4); got != "\x1b[1mab\x1b[0m"+reset+"  " {
		t.Fatalf("pad = %q", got)
	}
	if got := FitLine("abc💥d", 4); cells(got) != 4 || strings.Contains(got, "💥") || !strings.HasPrefix(got, "abc") {
		t.Fatalf("a wide rune that does not fit must be dropped: %q", got)
	}
	if got := FitLine("abcdef", 3); cells(got) != 3 {
		t.Fatalf("truncate = %q", got)
	}
}

func TestDrawTextUsesThePixelFont(t *testing.T) {
	var c Canvas
	c.Resize(TextWidth("HI"), GlyphHeight)
	ink := color.RGBA{255, 255, 255, 255}
	c.DrawText(0, 0, "hi", ink, color.RGBA{})
	if c.At(0, 0) != ink || c.At(1, 0) == ink || c.At(4, 0) != ink || c.At(5, 4) != ink {
		t.Fatal("lowercase text did not draw the uppercase H and I glyphs")
	}
}

func TestRGBTo256MatchesTheXtermPalette(t *testing.T) {
	for _, tc := range []struct {
		c    color.RGBA
		want uint8
	}{
		{color.RGBA{0, 0, 0, 255}, 16},
		{color.RGBA{255, 255, 255, 255}, 231},
		{color.RGBA{128, 128, 128, 255}, 244},
		{color.RGBA{255, 0, 0, 255}, 196},
	} {
		if got := RGBTo256(tc.c); got != tc.want {
			t.Fatalf("RGBTo256(%v) = %d, want %d", tc.c, got, tc.want)
		}
	}
}

// SupportsTrueColor reads the environment on every call, matches COLORTERM
// without regard to case, treats any non-empty WT_SESSION as support, and
// allocates nothing (Windows os.Getenv allocates; this is the per-frame read).
func TestSupportsTrueColorReadsTheEnvironmentPerCall(t *testing.T) {
	cases := []struct {
		colorTerm, session string
		want               bool
	}{
		{"", "", false},
		{"truecolor", "", true},
		{"TrueColor", "", true},
		{"24BIT", "", true},
		{"24bİt", "", true}, // U+0130 lowercases to "i", as strings.ToLower does
		{"truecolor ", "", false},
		{"true", "", false},
		{"256color", "", false},
		{"truecolortruecolortruecolor", "", false},
		{"", "abc", true},
		{"", "a-very-long-windows-terminal-session-identifier-0123456789", true},
		{"256color", "abc", true},
	}
	for _, tc := range cases {
		t.Setenv("COLORTERM", tc.colorTerm)
		t.Setenv("WT_SESSION", tc.session)
		if got := SupportsTrueColor(); got != tc.want {
			t.Errorf("COLORTERM=%q WT_SESSION=%q: SupportsTrueColor() = %v, want %v", tc.colorTerm, tc.session, got, tc.want)
		}
	}
	t.Setenv("COLORTERM", "TRUECOLOR")
	t.Setenv("WT_SESSION", "")
	if allocs := testing.AllocsPerRun(50, func() { _ = SupportsTrueColor() }); allocs != 0 {
		t.Fatalf("SupportsTrueColor allocates %.1f times, want 0", allocs)
	}
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "abc")
	if allocs := testing.AllocsPerRun(50, func() { _ = SupportsTrueColor() }); allocs != 0 {
		t.Fatalf("SupportsTrueColor allocates %.1f times with WT_SESSION set, want 0", allocs)
	}
}
