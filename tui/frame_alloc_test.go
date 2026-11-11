package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The cursor-column escapes are the bytes positionHardwareCursor always wrote, for the columns it keeps and the ones past them.
func TestCursorColumnSequenceIsTheColumnEscape(t *testing.T) {
	screen := &TuiMainScreen{}
	for _, col := range []int{0, 1, 9, 10, 99, 100, 511, 512, 513, 5000, 1 << 20} {
		if got, want := screen.cursorColumnSequence(col), fmt.Sprintf("\x1b[%dG", col+1); got != want {
			t.Errorf("cursorColumnSequence(%d) = %q, want %q", col, got, want)
		}
	}
	// Asking for a column again, in any order, gives the same escape.
	for _, col := range []int{300, 5, 300, 0, 511} {
		if got, want := screen.cursorColumnSequence(col), fmt.Sprintf("\x1b[%dG", col+1); got != want {
			t.Errorf("second cursorColumnSequence(%d) = %q, want %q", col, got, want)
		}
	}
}

// The differential frame's writer drops what an abandoned frame buffered, and sizing it for a frame changes none of the bytes it writes.
func TestBoundedTerminalWriterDiscardAndGrow(t *testing.T) {
	var writes []string
	w := boundedTerminalWriter{write: func(s string) { writes = append(writes, s) }}
	w.grow(64)
	w.WriteString("abandoned frame")
	w.discard()
	w.WriteString("next ")
	w.WriteString("frame")
	w.flush()
	if want := []string{"next frame"}; !slices.Equal(writes, want) {
		t.Fatalf("writes = %q, want %q", writes, want)
	}
	// A frame that outgrows its estimate is still written whole, within the bound of one write.
	writes = nil
	w.grow(1)
	w.WriteString(strings.Repeat("x", 100))
	w.flush()
	if len(writes) != 1 || writes[0] != strings.Repeat("x", 100) {
		t.Fatalf("writes = %q, want one write of 100 x", writes)
	}
}

// A line that fits is wrapped into the chunk slice the cache held for the line, which yields the chunks wordWrapLine does, for lines of every shape.
func TestEditorWrapLineReusesTheChunkSliceAndMatchesWordWrapLine(t *testing.T) {
	editor := NewEditor()
	rng := rand.New(rand.NewPCG(5, 6))
	pieces := []string{"a", "bc", " ", "  ", "日本", "e\u0301", "\U0001F600", "word", "x"}
	var reuse []textChunk
	for range 3000 {
		var b strings.Builder
		for range rng.IntN(40) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		line, width := b.String(), rng.IntN(30)-2
		want := wordWrapLine(line, width, nil)
		if line != "" && width > 0 && widthx.VisibleWidth(line) > width {
			want = wordWrapLine(line, width, editor.segmentLine(line))
		}
		got := editor.wrapLine(reuse, line, width)
		if !slices.Equal(got, want) {
			t.Fatalf("wrapLine(%q, %d) = %v, want %v", line, width, got, want)
		}
		reuse = got
	}
	short := []textChunk{{text: "old", endIndex: 3}}
	if got := editor.wrapLine(short, "new", 10); &got[0] != &short[0] || got[0].text != "new" {
		t.Fatalf("a short line was not stored in the slice it replaces: %v", got)
	}
}
