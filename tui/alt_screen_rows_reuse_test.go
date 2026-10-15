package tui

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// referenceRows is the frame post-processing before the rows were reused: ApplyLineResets over every row, then a cut of
// each row wider than width (Pi tui.ts applyLineResets; the tui-alt-screen.ts width clamp).
func referenceRows(rows []string, width int) []string {
	out := widthx.ApplyLineResets(rows)
	for i, line := range out {
		if !widthx.IsImageLine(line) && widthx.VisibleWidth(line) > width {
			out[i] = widthx.SliceByColumn(line, 0, width, true)
		}
	}
	return out
}

func rowCorpus() []string {
	return []string{
		"", "plain", "a\tb", "\x1b[31mred\x1b[0m", "\x1b]8;;http://x\x07link\x1b]8;;\x07",
		"\xff\xfebad utf8", "\u0e33 thai", "\u0eb3 lao", "wide 日本語日本語日本語日本語日本語日本語",
		"\x1b[32mred\x1b[0m", "plane", "plain" + "\t", "\x1b_Gf=100;AAAA\x1b\\", "\x1b_Gf=100;AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\x1b\\", "\x1b]1337;File=inline=1:AAAA\x07", "0123456789012345678901234567890123456789",
		"\x1b[1mbold " + "x" + "\x1b[0m tab\there", "emoji 👩‍👩‍👧‍👦👩‍👩‍👧‍👦👩‍👩‍👧‍👦",
	}
}

// TestResetAndClampRowsEqualsFreshProcessing proves a frame that reuses the last frame's rows is the frame that
// processes every row: random row sequences (unchanged, replaced, grown and shrunk frames, width switches) match the
// reference at every step.
func TestResetAndClampRowsEqualsFreshProcessing(t *testing.T) {
	corpus := rowCorpus()
	rng := rand.New(rand.NewSource(7))
	var prevRows, prevScreen []string
	prevWidth := 0
	for step := range 4000 {
		width := []int{10, 20, 40}[rng.Intn(3)]
		if rng.Intn(4) != 0 && prevWidth != 0 {
			width = prevWidth
		}
		n := rng.Intn(9)
		rows := make([]string, n)
		for i := range rows {
			if i < len(prevRows) && rng.Intn(3) != 0 {
				rows[i] = prevRows[i]
			} else {
				rows[i] = corpus[rng.Intn(len(corpus))]
			}
		}
		got := resetAndClampRows(rows, prevRows, prevScreen, prevWidth == width, width)
		want := referenceRows(slices.Clone(rows), width)
		if !slices.Equal(got, want) {
			t.Fatalf("step %d width %d (previous width %d): rows %q\n got %q\nwant %q", step, width, prevWidth, rows, got, want)
		}
		prevRows, prevScreen, prevWidth = rows, got, width
	}
}

// TestResetAndClampRowsReusesUnchangedRows: a frame of unchanged rows allocates only the result slice.
func TestResetAndClampRowsReusesUnchangedRows(t *testing.T) {
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = "\x1b[32mrow " + string(rune('a'+i%26)) + "\x1b[0m some styled text that is long enough"
	}
	first := resetAndClampRows(rows, nil, nil, false, 80)
	allocs := testing.AllocsPerRun(50, func() {
		resetAndClampRows(rows, rows, first, true, 80)
	})
	if allocs > 1 {
		t.Fatalf("an unchanged frame allocates %v times, want 1 (the result slice)", allocs)
	}
}
