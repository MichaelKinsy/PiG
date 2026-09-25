package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestEditor_PasteMarker_LargeLines verifies that pasting >10 lines via
// the bracketed-paste protocol replaces the content with a compact
// "[paste #N +K lines]" marker, while GetExpandedText still returns the
// full content. Mirrors upstream editor.ts::handlePaste + getExpandedText
// (editor.ts:1084 + 929).
func TestEditor_PasteMarker_LargeLines(t *testing.T) {
	ed := NewEditor()
	var b strings.Builder
	for i := range 15 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	pasted := b.String()
	ed.HandleInput("\x1b[200~" + pasted + "\x1b[201~")

	got := ed.Text()
	if !strings.Contains(got, "[paste #1 +16 lines]") {
		t.Fatalf("expected compact marker, got %q", got)
	}
	if strings.Contains(got, "line 14") {
		t.Fatalf("buffer should not contain raw paste content, got %q", got)
	}
	expanded := ed.GetExpandedText()
	if !strings.Contains(expanded, "line 0\nline 1\n") || !strings.Contains(expanded, "line 14") {
		t.Fatalf("GetExpandedText should expand marker, got %q", expanded)
	}
	if strings.Contains(expanded, "[paste #1") {
		t.Fatalf("GetExpandedText should not contain markers, got %q", expanded)
	}
}

// TestEditor_PasteMarker_LargeChars verifies the char-count branch
// (>1000 chars, ≤10 lines) emits "[paste #N M chars]".
func TestEditor_PasteMarker_LargeChars(t *testing.T) {
	ed := NewEditor()
	pasted := strings.Repeat("x", 1500)
	ed.HandleInput("\x1b[200~" + pasted + "\x1b[201~")

	got := ed.Text()
	if !strings.Contains(got, fmt.Sprintf("[paste #1 %d chars]", 1500)) {
		t.Fatalf("expected char-count marker, got %q", got)
	}
	expanded := ed.GetExpandedText()
	if expanded != pasted {
		t.Fatalf("expanded mismatch: got %d chars, want %d", len(expanded), len(pasted))
	}
}

func TestEditor_PasteMarker_UsesUTF16Length(t *testing.T) {
	t.Run("CJK code points stay inline below threshold", func(t *testing.T) {
		ed := NewEditor()
		pasted := strings.Repeat("字", 400)
		ed.HandleInput("\x1b[200~" + pasted + "\x1b[201~")
		if got := ed.Text(); got != pasted {
			t.Fatalf("CJK paste = %q, want inline source text", got)
		}
	})

	t.Run("astral code points count as two units", func(t *testing.T) {
		ed := NewEditor()
		pasted := strings.Repeat("😀", 501)
		ed.HandleInput("\x1b[200~" + pasted + "\x1b[201~")
		if got := ed.Text(); got != "[paste #1 1002 chars]" {
			t.Fatalf("astral paste marker = %q, want UTF-16 length", got)
		}
		if got := ed.GetExpandedText(); got != pasted {
			t.Fatalf("expanded astral paste changed content")
		}
	})
}

func TestEditor_PastePathAfterWordAddsSpace(t *testing.T) {
	ed := NewEditor()
	ed.HandleInput("foo")
	ed.HandleInput("\x1b[200~/tmp/example\x1b[201~")
	if got := ed.Text(); got != "foo /tmp/example" {
		t.Fatalf("path paste = %q, want space after preceding word", got)
	}

	ed = NewEditor()
	ed.HandleInput("(")
	ed.HandleInput("\x1b[200~./example\x1b[201~")
	if got := ed.Text(); got != "(./example" {
		t.Fatalf("path paste after punctuation = %q, want no inserted space", got)
	}
}

// TestEditor_PasteMarker_SmallPasteInlined verifies pastes ≤10 lines
// and ≤1000 chars are inserted as-is, not replaced with a marker.
func TestEditor_PasteMarker_SmallPasteInlined(t *testing.T) {
	ed := NewEditor()
	ed.HandleInput("\x1b[200~hello\nworld\x1b[201~")
	if ed.Text() != "hello\nworld" {
		t.Fatalf("expected small paste inlined, got %q", ed.Text())
	}
	if ed.GetExpandedText() != "hello\nworld" {
		t.Fatalf("GetExpandedText mismatch: %q", ed.GetExpandedText())
	}
}

// TestEditor_PasteMarker_ClearedOnEditorClear verifies that Clear()
// resets the paste store so subsequent text doesn't accidentally expand
// stale markers.
func TestEditor_PasteMarker_ClearedOnEditorClear(t *testing.T) {
	ed := NewEditor()
	ed.HandleInput("\x1b[200~" + strings.Repeat("a\n", 15) + "\x1b[201~")
	if !strings.Contains(ed.Text(), "[paste #1") {
		t.Fatalf("setup failed, expected marker, got %q", ed.Text())
	}
	ed.Clear()
	// Manually type the same marker text; with Clear()'d state it
	// should NOT expand.
	ed.HandleInput("[paste #1 +15 lines]")
	if ed.GetExpandedText() != "[paste #1 +15 lines]" {
		t.Fatalf("post-clear marker text should be literal, got %q", ed.GetExpandedText())
	}
}

// TestEditor_PasteMarker_MultiplePastesDistinctIDs verifies each large
// paste increments pasteCounter so markers remain unique.
func TestEditor_PasteMarker_MultiplePastesDistinctIDs(t *testing.T) {
	ed := NewEditor()
	ed.HandleInput("\x1b[200~" + strings.Repeat("a\n", 15) + "\x1b[201~")
	ed.HandleInput(" ")
	ed.HandleInput("\x1b[200~" + strings.Repeat("b\n", 20) + "\x1b[201~")
	got := ed.Text()
	if !strings.Contains(got, "[paste #1") || !strings.Contains(got, "[paste #2") {
		t.Fatalf("expected both markers, got %q", got)
	}
	expanded := ed.GetExpandedText()
	if strings.Contains(expanded, "[paste #") {
		t.Fatalf("expanded should drop all markers, got %q", expanded)
	}
	if !strings.Contains(expanded, "a\na\n") || !strings.Contains(expanded, "b\nb\n") {
		t.Fatalf("expanded should contain both paste bodies, got %q", expanded)
	}
}

// TestEditor_PasteMarker_BackspaceDeletesWholeMarker verifies that
// backspacing with the cursor immediately after a paste marker deletes the
// whole marker atomically (not just the trailing "]") and drops its registry
// entry, and that undo restores both the marker text and the registry.
// Mirrors upstream editor.ts backspace + EditorSnapshot undo.
func TestEditor_PasteMarker_BackspaceDeletesWholeMarker(t *testing.T) {
	ed := NewEditor()
	var b1, b2 strings.Builder
	for i := range 15 {
		fmt.Fprintf(&b1, "a %d\n", i)
	}
	for i := range 20 {
		fmt.Fprintf(&b2, "b %d\n", i)
	}
	ed.HandleInput("\x1b[200~" + b1.String() + "\x1b[201~")
	ed.HandleInput("\x1b[200~" + b2.String() + "\x1b[201~")
	if len(ed.pastes) != 2 || ed.pasteCounter != 2 {
		t.Fatalf("setup: pastes=%d counter=%d, want 2/2", len(ed.pastes), ed.pasteCounter)
	}

	ed.HandleInput("\x7f") // backspace, cursor is after the second marker
	got := ed.Text()
	if strings.Contains(got, "[paste #2") {
		t.Fatalf("second marker should be fully removed, got %q", got)
	}
	if !strings.Contains(got, "[paste #1 +16 lines]") {
		t.Fatalf("first marker should stay intact, got %q", got)
	}
	if len(ed.pastes) != 1 || ed.pasteCounter != 1 {
		t.Fatalf("after backspace: pastes=%d counter=%d, want 1/1", len(ed.pastes), ed.pasteCounter)
	}

	ed.undo()
	if len(ed.pastes) != 2 || ed.pasteCounter != 2 {
		t.Fatalf("after undo: pastes=%d counter=%d, want 2/2", len(ed.pastes), ed.pasteCounter)
	}
	if got := ed.Text(); !strings.Contains(got, "[paste #2 +21 lines]") {
		t.Fatalf("undo should restore the second marker, got %q", got)
	}
}

// TestEditor_PasteMarker_BackspaceRenumbersRegistry verifies that deleting a
// non-final marker renumbers the surviving markers and registry ids so they
// stay contiguous ([paste #2]/[paste #3] become [paste #1]/[paste #2] when
// [paste #1] is removed). Mirrors upstream editor.ts registry compaction.
func TestEditor_PasteMarker_BackspaceRenumbersRegistry(t *testing.T) {
	ed := NewEditor()
	ed.pastes = map[int]string{1: "aaa", 2: "bbb", 3: "ccc"}
	ed.pasteCounter = 3
	m1, m2, m3 := "[paste #1 3 chars]", "[paste #2 3 chars]", "[paste #3 3 chars]"
	ed.lines = []string{m1 + m2 + m3}
	ed.cursor = [2]int{0, len(m1)} // cursor right after the first marker

	ed.backspace()

	if got, want := ed.Text(), "[paste #1 3 chars][paste #2 3 chars]"; got != want {
		t.Fatalf("renumbered text: got %q want %q", got, want)
	}
	if ed.pasteCounter != 2 {
		t.Fatalf("pasteCounter = %d, want 2", ed.pasteCounter)
	}
	if len(ed.pastes) != 2 || ed.pastes[1] != "bbb" || ed.pastes[2] != "ccc" {
		t.Fatalf("registry = %v, want {1:bbb 2:ccc}", ed.pastes)
	}
}
