package termsim

import (
	"strings"
	"testing"
)

func TestGridBasicWrite(t *testing.T) {
	g := New(3, 10)
	g.WriteString("hello")
	if got := g.String(); !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected hello, got %q", got)
	}
	row, col := g.Cursor()
	if row != 0 || col != 5 {
		t.Errorf("cursor = (%d,%d), want (0,5)", row, col)
	}
}

func TestGridLineFeedAndScroll(t *testing.T) {
	g := New(2, 5)
	g.WriteString("aaa\r\nbbb\r\nccc")
	got := g.String()
	if got != "bbb  \nccc  " && got != "bbb\nccc" {
		t.Errorf("scroll content = %q, want bbb / ccc", got)
	}
	if sb := g.Scrollback(); len(sb) != 1 || strings.TrimSpace(sb[0]) != "aaa" {
		t.Errorf("scrollback = %v, want [aaa]", sb)
	}
}

func TestGridCursorPositioning(t *testing.T) {
	g := New(5, 10)
	g.WriteString("\x1b[3;5H*")
	if r, c := g.Cursor(); r != 2 || c != 5 {
		t.Errorf("cursor after CUP and putRune = (%d,%d), want (2,5)", r, c)
	}
	if got := g.CellAt(2, 4).Rune; got != '*' {
		t.Errorf("cell at (2,4) = %q, want *", got)
	}
}

func TestGridEraseDisplay2(t *testing.T) {
	g := New(3, 5)
	g.WriteString("aaaaa\nbbbbb\nccccc")
	g.WriteString("\x1b[2J\x1b[H")
	if got := g.String(); strings.TrimSpace(got) != "" {
		t.Errorf("after 2J, content = %q, want empty", got)
	}
}

func TestGridSGR(t *testing.T) {
	g := New(1, 10)
	g.WriteString("\x1b[1;31mX\x1b[0mY")
	x := g.CellAt(0, 0)
	if !x.Style.Bold || x.Style.FG.Mode != Color16 || x.Style.FG.N != 1 {
		t.Errorf("X style = %+v, want bold+red", x.Style)
	}
	y := g.CellAt(0, 1)
	if y.Style.Bold || y.Style.FG.Mode != ColorDefault {
		t.Errorf("Y style = %+v, want default", y.Style)
	}
}

func TestGridSyncOutputDepth(t *testing.T) {
	g := New(1, 5)
	g.WriteString("\x1b[?2026hXYZ\x1b[?2026l")
	if g.SyncDepth() != 0 {
		t.Errorf("sync depth after balanced enter/exit = %d, want 0", g.SyncDepth())
	}
	if got := strings.TrimSpace(g.String()); got != "XYZ" {
		t.Errorf("content inside sync block = %q, want XYZ", got)
	}
}

func TestGridWideChar(t *testing.T) {
	g := New(1, 6)
	g.WriteString("a世b")
	if got := g.String(); got != "a世b" {
		t.Errorf("wide char render = %q, want %q", got, "a世b")
	}
	// 世 occupies cols 1+2, so cursor should be at col 4
	if _, c := g.Cursor(); c != 4 {
		t.Errorf("cursor col after wide char = %d, want 4", c)
	}
	if !g.CellAt(0, 2).Continuation {
		t.Errorf("col 2 should be continuation cell")
	}
}

func TestGridOSC8Hyperlink(t *testing.T) {
	g := New(1, 20)
	g.WriteString("\x1b]8;;https://example.com\x07link\x1b]8;;\x07tail")
	if got := g.CellAt(0, 0).Link; got != "https://example.com" {
		t.Errorf("link cell = %q, want https://example.com", got)
	}
	if got := g.CellAt(0, 4).Link; got != "" {
		t.Errorf("tail cell link = %q, want empty", got)
	}
}

func TestGridAPCDroppedSilently(t *testing.T) {
	g := New(1, 10)
	g.WriteString("a\x1b_pi:c\x07b")
	if got := strings.TrimSpace(g.String()); got != "ab" {
		t.Errorf("after APC, visible = %q, want %q", got, "ab")
	}
}

func TestGridReset(t *testing.T) {
	g := New(2, 5)
	g.WriteString("\x1b[31m\x1b]8;;http://x\x1b\\aaa\r\nbbb\r\nccc\x1b[?2026h")
	g.Reset()
	if got := strings.TrimSpace(g.String()); got != "" {
		t.Errorf("screen after Reset = %q", got)
	}
	if sb := g.Scrollback(); len(sb) != 0 {
		t.Errorf("scrollback after Reset = %v", sb)
	}
	if r, c := g.Cursor(); r != 0 || c != 0 {
		t.Errorf("cursor after Reset = (%d,%d)", r, c)
	}
	g.WriteString("x")
	if cell := g.CellAt(0, 0); cell.Rune != 'x' || cell.Style != (Style{}) || cell.Link != "" {
		t.Errorf("pen state survived Reset: %+v", cell)
	}
}

func TestGridResizeClampsToMinimum(t *testing.T) {
	g := New(3, 4)
	g.WriteString("abc\r\ndef\r\nghi")
	g.Resize(0, 0)
	if g.Rows != 1 || g.Cols != 2 {
		t.Fatalf("size after Resize(0,0) = %dx%d, want 1x2", g.Rows, g.Cols)
	}
	if r, c := g.Cursor(); r != 0 || c != 1 {
		t.Errorf("cursor = (%d,%d), want (0,1)", r, c)
	}
	g.WriteString("zz\r\nyy") // must not panic
	g.Resize(-5, -5)
	g.WriteString("q")
}

func TestGridResizeShrinkKeepsCursorRow(t *testing.T) {
	g := New(4, 5)
	g.WriteString("aaa\r\nbbb\r\nccc\r\nddd")
	g.Resize(2, 5)
	if got := strings.Fields(g.String()); len(got) != 2 || got[0] != "ccc" || got[1] != "ddd" {
		t.Errorf("rows after shrink = %q, want ccc ddd", got)
	}
	sb := g.Scrollback()
	if len(sb) != 2 || strings.TrimSpace(sb[0]) != "aaa" || strings.TrimSpace(sb[1]) != "bbb" {
		t.Errorf("scrollback = %q, want [aaa bbb]", sb)
	}
	if r, _ := g.Cursor(); r != 1 {
		t.Errorf("cursor row = %d, want 1", r)
	}
}

func TestGridResizeShrinkDropsBlankRowsBelowCursor(t *testing.T) {
	g := New(4, 5)
	g.WriteString("aaa\r\nbbb")
	g.Resize(2, 5)
	if got := strings.Fields(g.String()); len(got) != 2 || got[0] != "aaa" || got[1] != "bbb" {
		t.Errorf("rows = %q, want aaa bbb", got)
	}
	if sb := g.Scrollback(); len(sb) != 0 {
		t.Errorf("scrollback = %q, want none", sb)
	}
}

func TestGridResizeGrowAndNarrow(t *testing.T) {
	g := New(2, 4)
	g.WriteString("abcd\r\nefgh")
	g.Resize(3, 6)
	if g.Rows != 3 || g.Cols != 6 || len(g.cells) != 3 || len(g.cells[2]) != 6 {
		t.Fatalf("size %dx%d", g.Rows, g.Cols)
	}
	if got := strings.Fields(g.String()); len(got) != 2 || got[0] != "abcd" || got[1] != "efgh" {
		t.Errorf("content after grow = %q", got)
	}
	g.Resize(3, 3)
	if got := strings.Fields(g.String()); got[0] != "abc" || got[1] != "efg" {
		t.Errorf("content after narrowing = %q", got)
	}
	if _, c := g.Cursor(); c != 2 {
		t.Errorf("cursor col = %d, want 2", c)
	}
}
