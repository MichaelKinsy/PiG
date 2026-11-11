package widthx

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// resolveCursorFreeLineByParts is the two steps ResolveCursorFreeLine fuses: resolve the line, then cut it at the first CursorMarker and drop every later one.
func resolveCursorFreeLineByParts(line string, showHardwareCursor bool) (string, int) {
	before, after, _ := strings.Cut(ResolveFakeCursorLine(line, showHardwareCursor), CursorMarker)
	return before + strings.ReplaceAll(after, CursorMarker, ""), VisibleWidth(before)
}

// The fused pass gives the line and the column the resolve-then-strip steps give, on lines built from marker fragments, partial markers and the replacements themselves.
func TestResolveCursorFreeLineMatchesResolveThenStrip(t *testing.T) {
	pieces := []string{FakeCursorStart, FakeCursorEnd, CursorMarker, CursorMarker + FakeCursorStart, fakeCursorMarkerPrefix, "\x1b", "\x1b_pi", "c\x07", "fc\x07", "/fc\x07",
		"\x1b[7m", "\x1b[27m", "a", "日", "\x07", "\x1b[31m"}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 50000 {
		var b strings.Builder
		for range rng.IntN(9) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		line := b.String()
		for _, show := range []bool{false, true} {
			wantLine, wantCol := resolveCursorFreeLineByParts(line, show)
			if gotLine, gotCol := ResolveCursorFreeLine(line, show); gotLine != wantLine || gotCol != wantCol {
				t.Fatalf("ResolveCursorFreeLine(%q, %v) = %q, %d; want %q, %d", line, show, gotLine, gotCol, wantLine, wantCol)
			}
		}
	}
}

// A focused editor row, a fake cursor at the end of a line, costs one allocation to resolve and strip.
func TestResolveCursorFreeLineAllocatesOneString(t *testing.T) {
	line := "> abc" + strings.Repeat("x", 60) + CursorMarker + RenderFakeCursor(" ")
	want := "> abc" + strings.Repeat("x", 60) + "\x1b[7m \x1b[27m"
	if got, col := ResolveCursorFreeLine(line, false); got != want || col != 65 {
		t.Fatalf("ResolveCursorFreeLine = %q, %d; want %q, 65", got, col, want)
	}
	if allocs := testing.AllocsPerRun(100, func() { ResolveCursorFreeLine(line, false) }); allocs != 1 {
		t.Fatalf("resolving and stripping a row allocates %v times, want 1", allocs)
	}
}
