package widthx

import "testing"

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
