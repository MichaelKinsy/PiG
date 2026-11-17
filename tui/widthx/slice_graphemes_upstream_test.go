package widthx

import "testing"

// Ports packages/tui/test/slice-graphemes.test.ts of Pi b7bb7ca905b8 (#9549: slicing splits standalone characters without
// Intl.Segmenter; clusters must stay intact). Same inputs and expected results as upstream.

func TestSliceWithWidthSplitsBoxDrawingArrowsAndCJKByCell(t *testing.T) {
	line := "│ a → 中文 ✓ │"
	for _, c := range []struct {
		start, length int
		text          string
		width         int
	}{
		{0, 6, "│ a → ", 6},
		{6, 4, "中文", 4},
		{7, 3, "文", 2},
	} {
		if got := SliceWithWidth(line, c.start, c.length, true); got != (SliceResult{Text: c.text, Width: c.width}) {
			t.Errorf("SliceWithWidth(%q, %d, %d, true) = %+v, want {%q %d}", line, c.start, c.length, got, c.text, c.width)
		}
	}
	if start, end, ok := GraphemeCellRange(line, 7); !ok || start != 6 || end != 8 {
		t.Errorf("GraphemeCellRange(%q, 7) = %d, %d, %v, want 6, 8, true", line, start, end, ok)
	}
}

func TestSliceWithWidthKeepsClustersThatJoinStandaloneCharacters(t *testing.T) {
	// A ZWJ joins the heart to the preceding emoji; the combining accent joins its base letter.
	family := "x👨\u200d❤y"
	if got := SliceWithWidth(family, 1, 2, true).Text; got != "👨\u200d❤" {
		t.Errorf("SliceWithWidth(family, 1, 2, true).Text = %q, want %q", got, "👨\u200d❤")
	}
	accented := "→e\u0301→"
	if got := SliceWithWidth(accented, 1, 1, true); got != (SliceResult{Text: "e\u0301", Width: 1}) {
		t.Errorf("SliceWithWidth(accented, 1, 1, true) = %+v, want {e\\u0301 1}", got)
	}
	if start, end, ok := GraphemeCellRange(accented, 2); !ok || start != 2 || end != 3 {
		t.Errorf("GraphemeCellRange(accented, 2) = %d, %d, %v, want 2, 3, true", start, end, ok)
	}
}
