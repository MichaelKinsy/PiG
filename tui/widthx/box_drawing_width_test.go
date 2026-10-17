package widthx

import (
	"strings"
	"testing"
)

// TestBoxDrawingStaysOnTheFastPath: a table row or frame border, drawn with Box Drawing characters and ASCII, is measured in one pass without segmentation (Pi's visibleWidth gives each box character one cell; pi_width_diff_test.go holds every one to the oracle), while a box character that something non-ASCII follows takes the exact path.
func TestBoxDrawingStaysOnTheFastPath(t *testing.T) {
	row := "│ name       │ value      │ \x1b[1mbold\x1b[22m │\n" + "├" + strings.Repeat("─", 12) + "┼" + strings.Repeat("─", 12) + "┤"
	if w, ok := asciiVisibleWidth(row); !ok || w != VisibleWidth(row) {
		t.Fatalf("asciiVisibleWidth(table row) = %d, %v", w, ok)
	}
	for _, c := range []struct {
		text string
		want int
		ok   bool
	}{
		{"─", 1, true},
		{"a─b", 3, true},
		{"┌──┐", 4, true},
		{"─\t", 4, true},
		{"─\u0301", 0, false},
		{"─\ufe0f", 0, false},
		{"─\u200d", 0, false},
		{"─中", 0, false},
		{"\u0301─", 0, false},
		{"\u2580", 0, false},
		{"\xe2\x94", 0, false},
		{"\xe2\x94a", 0, false},
	} {
		w, ok := asciiVisibleWidth(c.text)
		if ok != c.ok || ok && w != c.want {
			t.Errorf("asciiVisibleWidth(%+q) = %d, %v; want %d, %v", c.text, w, ok, c.want, c.ok)
		}
	}
	if n := testing.AllocsPerRun(100, func() { VisibleWidth(row) }); n != 0 {
		t.Errorf("VisibleWidth of a table row allocates %v times", n)
	}
}
