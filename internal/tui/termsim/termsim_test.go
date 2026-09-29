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
