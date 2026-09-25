package widthx

import "testing"

// Terminals allocate two cells for an emoji presentation sequence and for a
// regional-indicator flag pair. Upstream agrees: packages/tui/src/utils.ts
// returns 2 for anything matching \p{RGI_Emoji} (line 190) and has an explicit
// regional-indicator branch (line 204).
//
// go-runewidth measures code points rather than grapheme clusters, so it
// reports 1 for both: the base character is neutral-width and VS16 is
// zero-width. Pig took that fast path for every string without Thai/Lao AM
// vowels, so any row containing a warning or info emoji was measured one cell
// narrower than the terminal drew it.
//
// The consequence is a rendering desync rather than a cosmetic off-by-one. A
// row believed to fit but physically wider wraps to a second screen row, every
// position below it shifts, and the differential renderer's per-row erase
// clears only the first physical row. In a long session tree a single ⚠️ froze
// the rows above the selection through every scroll, surviving repaints because
// their buffer content never changed.
func TestVariationSelectorAndFlagWidthsMatchTerminalCells(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    string
		want int
	}{
		{"warning sign with VS16", "\u26A0\uFE0F", 2},
		{"information source with VS16", "\u2139\uFE0F", 2},
		{"heart with VS16", "\u2764\uFE0F", 2},
		{"bare warning sign keeps text presentation", "\u26A0", 1},
		{"regional indicator flag pair", "\U0001F1FA\U0001F1F8", 2},
		{"already-wide emoji is unchanged", "\u2705", 2},
		{"plain emoji is unchanged", "\U0001F680", 2},

		// The row that exposed the bug: a tree label mixing a correctly measured
		// wide emoji with an undercounted VS16 one.
		{"tree row fragment", "1. \u2705 fires 2. \u26A0\uFE0F null-byte", 27},

		// Regressions guarded by the untouched fast paths.
		{"ascii", "hello", 5},
		{"cjk", "\u4E2D\u6587", 4},
		{"combining acute is one cell", "e\u0301", 1},
		{"empty", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisibleWidth(tc.s); got != tc.want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", tc.s, got, tc.want)
			}
		})
	}
}

// The Thai/Lao AM path shares the grapheme walk that the emoji fix now reaches,
// so it is pinned here against a regression in the shared trigger.
func TestThaiLaoAMStillUsesGraphemeWidth(t *testing.T) {
	if got := VisibleWidth("\u0e33"); got < 1 {
		t.Errorf("VisibleWidth(Thai SARA AM) = %d, want at least 1", got)
	}
}
