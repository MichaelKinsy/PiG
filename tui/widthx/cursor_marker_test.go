package widthx

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestExtractCursorPosition_NoMarker(t *testing.T) {
	lines := []string{"hello", "world"}
	if _, ok := ExtractCursorPosition(lines, 5); ok {
		t.Error("expected no marker found")
	}
	// lines unchanged
	if lines[0] != "hello" || lines[1] != "world" {
		t.Errorf("lines mutated: %v", lines)
	}
}

func TestExtractCursorPosition_PlainAscii(t *testing.T) {
	lines := []string{"hello", "wor" + CursorMarker + "ld"}
	pos, ok := ExtractCursorPosition(lines, 5)
	if !ok {
		t.Fatal("expected marker found")
	}
	if pos.Row != 1 || pos.Col != 3 {
		t.Errorf("pos = %+v, want {Row:1, Col:3}", pos)
	}
	if lines[1] != "world" {
		t.Errorf("line[1] = %q, want %q", lines[1], "world")
	}
}

func TestExtractCursorPosition_AfterAnsi(t *testing.T) {
	lines := []string{"\x1b[31mhello\x1b[0m" + CursorMarker + " world"}
	pos, ok := ExtractCursorPosition(lines, 5)
	if !ok {
		t.Fatal("expected marker found")
	}
	if pos.Col != 5 {
		t.Errorf("col = %d, want 5 (ANSI doesn't count)", pos.Col)
	}
	// Marker stripped, ANSI preserved
	if lines[0] != "\x1b[31mhello\x1b[0m world" {
		t.Errorf("line = %q, want ANSI preserved + marker stripped", lines[0])
	}
}

func TestExtractCursorPosition_AfterWideChar(t *testing.T) {
	lines := []string{"a你b" + CursorMarker}
	pos, ok := ExtractCursorPosition(lines, 5)
	if !ok {
		t.Fatal("expected marker found")
	}
	// a=1col, 你=2col, b=1col → 4
	if pos.Col != 4 {
		t.Errorf("col = %d, want 4", pos.Col)
	}
}

func TestExtractCursorPosition_ScansBottomOnly(t *testing.T) {
	// Marker in row 0; viewport is only rows 3-5. Should not find it.
	lines := []string{
		"row0" + CursorMarker,
		"row1", "row2", "row3", "row4", "row5",
	}
	if _, ok := ExtractCursorPosition(lines, 3); ok {
		t.Error("marker in row 0 should be outside viewport (top 3 lines)")
	}
}

func TestExtractCursorPosition_PrefersBottomMost(t *testing.T) {
	// Two markers; the bottom one wins.
	lines := []string{
		"top" + CursorMarker,
		"bot" + CursorMarker + "tom",
	}
	pos, ok := ExtractCursorPosition(lines, 2)
	if !ok {
		t.Fatal("expected marker found")
	}
	if pos.Row != 1 {
		t.Errorf("row = %d, want 1 (bottom-most)", pos.Row)
	}
	// Only the bottom marker is stripped; the top remains.
	if lines[0] != "top"+CursorMarker {
		t.Errorf("top marker should remain, got %q", lines[0])
	}
	if lines[1] != "bottom" {
		t.Errorf("bottom marker should be stripped, got %q", lines[1])
	}
}

func TestExtractCursorPosition_MarkerIsZeroWidthInVisibleCalc(t *testing.T) {
	// Sanity: VisibleWidth ignores the marker.
	if VisibleWidth("x"+CursorMarker+"y") != 2 {
		t.Errorf("CursorMarker should be zero-width")
	}
}

// A resolved line is one string: both marker kinds are replaced in one pass (tui.ts resolveFakeCursors replaces
// FAKE_CURSOR_START, then FAKE_CURSOR_END; the two never overlap, so one pass gives the same line).
func TestResolveFakeCursorLineAllocatesOneString(t *testing.T) {
	line := "> abc" + CursorMarker + RenderFakeCursor(" ") + strings.Repeat("x", 120) + RenderFakeCursor("y")
	want := "> abc" + CursorMarker + "\x1b[7m \x1b[27m" + strings.Repeat("x", 120) + "\x1b[7my\x1b[27m"
	if got := ResolveFakeCursorLine(line, false); got != want {
		t.Fatalf("ResolveFakeCursorLine = %q, want %q", got, want)
	}
	if allocs := testing.AllocsPerRun(100, func() { ResolveFakeCursorLine(line, false) }); allocs != 1 {
		t.Fatalf("resolving a line allocates %v times, want 1", allocs)
	}
}

// The one-pass replacement matches tui.ts's two replaceAll calls on lines built from marker fragments, partial markers and
// the replacements themselves.
func TestReplaceFakeCursorMarkersMatchesTwoReplaceAll(t *testing.T) {
	pieces := []string{FakeCursorStart, FakeCursorEnd, CursorMarker, fakeCursorMarkerPrefix, "\x1b", "\x1b_pi", "fc\x07", "/fc\x07",
		"\x1b[7m", "\x1b[27m", "a", "日", "\x07"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		var b strings.Builder
		for range rng.IntN(8) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		line := b.String()
		want := strings.ReplaceAll(strings.ReplaceAll(line, FakeCursorStart, "\x1b[7m"), FakeCursorEnd, "\x1b[27m")
		if got := replaceFakeCursorMarkers(line); got != want {
			t.Fatalf("replaceFakeCursorMarkers(%q) = %q, want %q", line, got, want)
		}
	}
}

// A frame the caller built is resolved in place: only the rows that hold a marker get a new string.
func TestResolveFakeCursorsInPlace(t *testing.T) {
	frame := []string{"a", "> " + CursorMarker + RenderFakeCursor("b") + "c", "plain"}
	ResolveFakeCursorsInPlace(frame, false)
	if want := []string{"a", "> " + CursorMarker + "\x1b[7mb\x1b[27mc", "plain"}; !slices.Equal(frame, want) {
		t.Fatalf("frame = %q, want %q", frame, want)
	}
	frame[1] = "> " + CursorMarker + RenderFakeCursor("b") + "c"
	ResolveFakeCursorsInPlace(frame, true)
	if frame[1] != "> "+CursorMarker+"bc" {
		t.Fatalf("with the hardware cursor the focused fake cursor is dropped: %q", frame[1])
	}
	marked := "> " + RenderFakeCursor("b") + "c"
	if allocs := testing.AllocsPerRun(100, func() { frame[1] = marked; ResolveFakeCursorsInPlace(frame, false) }); allocs != 1 {
		t.Fatalf("resolving a frame with one marked row allocates %v times, want 1", allocs)
	}
}
