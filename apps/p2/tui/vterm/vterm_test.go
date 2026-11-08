package vterm

import (
	"slices"
	"strings"
	"sync"
	"testing"

	pitui "github.com/MichaelKinsy/PiG/tui"
)

func TestWriteAndViewport(t *testing.T) {
	term := New(10, 3)
	term.Write("hello\r\nworld")
	if got := term.Viewport(); !slices.Equal(got, []string{"hello", "world", ""}) {
		t.Fatalf("viewport = %q", got)
	}
	if x, y := term.Cursor(); x != 5 || y != 1 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
	term.MoveBy(-1)
	term.MoveBy(1)
	term.MoveBy(0)
	if _, y := term.Cursor(); y != 1 {
		t.Fatalf("row after moves = %d", y)
	}
	term.ClearScreen()
	if got := strings.Join(term.Viewport(), ""); got != "" {
		t.Fatalf("not cleared: %q", got)
	}
}

func TestScrollBufferKeepsScrolledRows(t *testing.T) {
	term := New(10, 2)
	term.Write("a\r\nb\r\nc\r\nd")
	if got := term.ScrollBuffer(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("buffer = %q", got)
	}
	term.Reset()
	if got := term.ScrollBuffer(); strings.Join(got, "") != "" {
		t.Fatalf("not reset: %q", got)
	}
}

func TestStartInputResizeAndModes(t *testing.T) {
	term := New(10, 3)
	var inputs []string
	resized := 0
	if err := term.Start(func(s string) { inputs = append(inputs, s) }, func() { resized++ }); err != nil {
		t.Fatal(err)
	}
	if !term.BracketedPaste() {
		t.Fatal("bracketed paste off after Start")
	}
	term.SendInput("x")
	term.Resize(20, 4)
	if term.Columns() != 20 || term.Rows() != 4 || resized != 1 || !slices.Equal(inputs, []string{"x"}) {
		t.Fatalf("cols=%d rows=%d resized=%d inputs=%q", term.Columns(), term.Rows(), resized, inputs)
	}
	term.HideCursor()
	if term.CursorVisible() {
		t.Fatal("cursor visible")
	}
	term.ShowCursor()
	term.SetTitle("t")
	if term.Title() != "t" || !term.KittyProtocolActive() {
		t.Fatal("title/kitty")
	}
	term.Stop()
	term.SendInput("ignored")
	if len(inputs) != 1 || term.BracketedPaste() {
		t.Fatal("Stop did not detach")
	}
}

func TestRendersATUI(t *testing.T) {
	term := New(30, 6)
	screen := pitui.NewTuiAltScreen(term, nil, t.TempDir())
	defer screen.Stop()
	screen.AddChild(pitui.NewPaddedText("hello from the tui", 0, 0, nil))
	screen.Start()
	screen.RepaintAll()
	if got := strings.Join(term.Viewport(), "\n"); !strings.Contains(got, "hello from the tui") {
		t.Fatalf("viewport:\n%s", got)
	}
}

// The renderer writes from its own goroutine while tests read: run with -race.
func TestConcurrentWriteAndRead(t *testing.T) {
	term := New(20, 4)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			term.Write("line\r\n")
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = term.Viewport()
			_ = term.ScrollBuffer()
		}
	}()
	wg.Wait()
}
