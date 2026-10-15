//go:build !race

package tui

import (
	"slices"
	"strings"
	"testing"
)

// editorKeystrokeAllocCeiling is the allocations of one printable key, handled by the editor and rendered into a frame, plus the backspace that restores the text: 6 when measured (60 before keystrokes were parsed once per byte, jsstring.Slice and Splice took byte offsets, the last grapheme was found without a segment list and Render reused its unchanged rows, borders, wrap cache and visual line map; the key-matching part alone was 162 before key identifiers were parsed once). The 6 are the two undo snapshots Pi takes (the key after a backspace and the backspace each start an undo unit), the wrapped chunk of the edited line, the cursor row, its padded row and the frame's line list. It only goes down.
const editorKeystrokeAllocCeiling = 6

func TestEditorPrintableKeystrokeAllocationCeiling(t *testing.T) {
	editor := NewEditor()
	editor.SetText(strings.Repeat("a line of the draft with several words in it\n", 40))
	editor.Render(100)
	const letters = "abcdefghij"
	i := 0
	got := testing.AllocsPerRun(300, func() {
		editor.HandleInput(letters[i%len(letters) : i%len(letters)+1])
		editor.Render(100)
		editor.HandleInput("\x7f")
		i++
	})
	t.Logf("%.1f allocs per key", got)
	if got > editorKeystrokeAllocCeiling {
		t.Fatalf("a keystroke allocates %v times, ceiling %d", got, editorKeystrokeAllocCeiling)
	}
	if text := editor.Text(); !strings.HasSuffix(text, "in it\n") {
		t.Fatalf("the backspace did not restore the draft: %q", text[max(0, len(text)-20):])
	}
}

// A printable key typed inside a word at the end of a line allocates nothing in the editor: the key's parse is shared, the undo unit is already
// open and the line grows in place (the key-matching, Splice and line-copy sites measured 1-7 per key before).
func TestEditorTypingInAWordAllocatesNothing(t *testing.T) {
	editor := NewEditor()
	editor.SetText(strings.Repeat("a line of the draft with several words in it\n", 40) + "w")
	editor.Render(100)
	const letters = "abcdefghij"
	i := 0
	got := testing.AllocsPerRun(1000, func() {
		editor.HandleInput(letters[i%len(letters) : i%len(letters)+1])
		i++
	})
	t.Logf("%.2f allocs per key", got)
	if got != 0 {
		t.Fatalf("a key typed in a word allocates %v times, want 0", got)
	}
	lines := strings.Split(editor.Text(), "\n")
	if last := lines[len(lines)-1]; len(last) != 1+1001 || last[:4] != "wabc" {
		t.Fatalf("typed line = %q", last[:min(len(last), 20)])
	}
}

// Rendering an editor whose text and cursor did not change allocates only the frame's line list: rows, borders, wrapping and the visual line
// map are reused (32 before).
func TestEditorUnchangedRenderAllocatesOnlyTheFrame(t *testing.T) {
	editor := NewEditor()
	editor.SetText(strings.Repeat("a line of the draft with several words in it\n", 40))
	first := slices.Clone(editor.Render(100))
	got := testing.AllocsPerRun(300, func() { editor.Render(100) })
	if got > 1 {
		t.Fatalf("an unchanged render allocates %v times, want 1", got)
	}
	if again := editor.Render(100); !slices.Equal(again, first) {
		t.Fatalf("the reused frame differs:\n%q\n%q", again, first)
	}
}
