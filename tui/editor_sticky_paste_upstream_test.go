package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestUpstreamEditorStickyColumn(t *testing.T) {
	// upstream: packages/tui/test/editor.test.ts:3302
	t.Run("preserves target column when moving up through a shorter line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("2222222222x222\n\n1111111111_111111111111")
		assertEditorUnitCursor(t, e, 2, 23)
		e.HandleInput("\x01")
		for range 10 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 2, 10)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 1, 0)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 10)
	})

	// upstream: packages/tui/test/editor.test.ts:3325
	t.Run("preserves target column when moving down through a shorter line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1111111111_111\n\n2222222222x222222222222")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 10 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 0)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 10)
	})

	// upstream: packages/tui/test/editor.test.ts:3346
	t.Run("resets sticky column on horizontal movement (left arrow)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 2, 5)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 5)
		e.HandleInput("\x1b[D")
		assertEditorUnitCursor(t, e, 0, 4)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 4)
	})

	// upstream: packages/tui/test/editor.test.ts:3371
	t.Run("resets sticky column on horizontal movement (right arrow)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 5)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 5)
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 2, 6)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 6)
	})

	// upstream: packages/tui/test/editor.test.ts:3398
	t.Run("resets sticky column on typing", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 8)
		e.HandleInput("X")
		assertEditorUnitCursor(t, e, 0, 9)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 9)
	})

	// upstream: packages/tui/test/editor.test.ts:3422
	t.Run("resets sticky column on backspace", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 8)
		e.HandleInput("\x7f")
		assertEditorUnitCursor(t, e, 0, 7)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 7)
	})

	// upstream: packages/tui/test/editor.test.ts:3446
	t.Run("resets sticky column on Ctrl+A (move to line start)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 1, 0)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 0)
	})

	// upstream: packages/tui/test/editor.test.ts:3467
	t.Run("resets sticky column on Ctrl+E (move to line end)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("12345\n\n1234567890")
		e.HandleInput("\x01")
		for range 3 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 3)
		e.HandleInput("\x05")
		assertEditorUnitCursor(t, e, 0, 5)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 5)
	})

	// upstream: packages/tui/test/editor.test.ts:3491
	t.Run("resets sticky column on word movement (Ctrl+Left)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world\n\nhello world")
		assertEditorUnitCursor(t, e, 2, 11)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 11)
		e.HandleInput("\x1b[1;5D")
		assertEditorUnitCursor(t, e, 0, 6)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 6)
	})

	// upstream: packages/tui/test/editor.test.ts:3514
	t.Run("resets sticky column on word movement (Ctrl+Right)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world\n\nhello world")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 0)
		e.HandleInput("\x1b[1;5C")
		assertEditorUnitCursor(t, e, 2, 5)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 5)
	})

	// upstream: packages/tui/test/editor.test.ts:3540
	t.Run("resets sticky column on undo", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 8)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 8)
		e.HandleInput("X")
		assertEditorText(t, e, "1234567890\n\n12345678X90")
		assertEditorUnitCursor(t, e, 2, 9)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 9)
		e.HandleInput(kittyUndo)
		assertEditorText(t, e, "1234567890\n\n1234567890")
		assertEditorUnitCursor(t, e, 2, 8)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 8)
	})

	// upstream: packages/tui/test/editor.test.ts:3578
	t.Run("handles multiple consecutive up/down movements", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\nab\ncd\nef\n1234567890")
		e.HandleInput("\x01")
		for range 7 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 4, 7)
		for range 4 {
			e.HandleInput("\x1b[A")
		}
		assertEditorUnitCursor(t, e, 0, 7)
		for range 4 {
			e.HandleInput("\x1b[B")
		}
		assertEditorUnitCursor(t, e, 4, 7)
	})

	// upstream: packages/tui/test/editor.test.ts:3603
	t.Run("moves correctly through wrapped visual lines without getting stuck", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("short\n123456789012345678901234567890")
		e.Render(15)
		assertEditorUnitCursor(t, e, 1, 30)
		for _, wantLine := range []int{1, 1, 0} {
			e.HandleInput("\x1b[A")
			if got := e.GetCursor().Line; got != wantLine {
				t.Fatalf("cursor line = %d, want %d", got, wantLine)
			}
		}
	})

	// upstream: packages/tui/test/editor.test.ts:3627
	t.Run("handles setText resetting sticky column", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890\n\n1234567890")
		e.HandleInput("\x01")
		for range 8 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.SetText("abcdefghij\n\nabcdefghij")
		assertEditorUnitCursor(t, e, 2, 10)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 10)
	})

	// upstream: packages/tui/test/editor.test.ts:3647
	t.Run("sets preferredVisualCol when pressing right at end of prompt (last line)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("111111111x1111111111\n\n333333333_")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x05")
		assertEditorUnitCursor(t, e, 0, 20)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 10)
		e.HandleInput("\x1b[C")
		assertEditorUnitCursor(t, e, 2, 10)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 10)
	})

	// upstream: packages/tui/test/editor.test.ts:3676
	t.Run("handles editor resizes when preferredVisualCol is on the same line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("12345678901234567890\n\n12345678901234567890")
		e.HandleInput("\x01")
		for range 15 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 15)
		e.Render(12)
		e.HandleInput("\x1b[B")
		e.HandleInput("\x1b[B")
		if got := e.GetCursor().Col; got != 4 {
			t.Fatalf("cursor column = %d, want 4", got)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:3701
	t.Run("handles editor resizes when preferredVisualCol is on a different line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("short\n12345678901234567890")
		e.HandleInput("\x01")
		for range 15 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 1, 15)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 5)
		e.Render(10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 8)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 5)
		e.Render(80)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 15)
	})

	// upstream: packages/tui/test/editor.test.ts:3739
	t.Run("rewrapped lines: target fits current visual column", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("abcdefghijklmnopqr\n123456789012345678")
		// Inline upstream positionCursor(editor, 0, 18), including all 20 Up inputs.
		for range 20 {
			e.HandleInput("\x1b[A")
		}
		e.HandleInput("\x01")
		for range 18 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 18)
		e.Render(10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 8)
		e.Render(80)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 8)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 8)
	})

	// upstream: packages/tui/test/editor.test.ts:3765
	t.Run("rewrapped lines: target shorter than current visual column", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("abcdefghijklmnopqr\n123456789012345678\nab")
		// Inline upstream positionCursor(editor, 0, 18), including all 20 Up inputs.
		for range 20 {
			e.HandleInput("\x1b[A")
		}
		e.HandleInput("\x01")
		for range 18 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 18)
		e.Render(10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 8)
		e.Render(80)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 2)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 1, 8)
	})
}

func TestUpstreamEditorPasteMarkerAtomicBehavior(t *testing.T) {
	// upstream: packages/tui/test/editor.test.ts:3921
	t.Run("undo restores marker after backspace deletion", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.HandleInput("A")
		upstreamPasteWithMarker(e)
		e.HandleInput("B")
		textBefore := e.Text()
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x7f")
		assertEditorText(t, e, "AB")
		e.HandleInput(kittyUndo)
		assertEditorText(t, e, textBefore)
	})

	// upstream: packages/tui/test/editor.test.ts:3943
	t.Run("undo after paste marker deletion restores the paste registry", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		lines := make([]string, 12)
		for i := range lines {
			lines[i] = fmt.Sprintf("alpha%d", i)
		}
		paste := strings.Join(lines, "\n")
		e.HandleInput("\x1b[200~" + paste + "\x1b[201~")
		e.HandleInput("\x7f")
		e.HandleInput(kittyUndo)
		e.HandleInput("\r")
		if submitted != paste {
			t.Fatalf("submitted = %q, want %q", submitted, paste)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:3958
	t.Run("undo after deleting the first of two paste markers restores both registry entries", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		linesA, linesB := make([]string, 12), make([]string, 12)
		for i := range linesA {
			linesA[i] = fmt.Sprintf("alpha%d", i)
			linesB[i] = fmt.Sprintf("beta%d", i)
		}
		pasteA, pasteB := strings.Join(linesA, "\n"), strings.Join(linesB, "\n")
		e.HandleInput("\x1b[200~" + pasteA + "\x1b[201~")
		e.HandleInput("\x1b[200~" + pasteB + "\x1b[201~")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x7f")
		e.HandleInput(kittyUndo)
		e.HandleInput("\r")
		if want := pasteA + pasteB; submitted != want {
			t.Fatalf("submitted = %q, want %q", submitted, want)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:3977
	t.Run("renumbers the paste registry in ascending id order when markers are out of order in text", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		linesA, linesB, linesC := make([]string, 12), make([]string, 12), make([]string, 12)
		for i := range linesA {
			linesA[i] = fmt.Sprintf("alpha%d", i)
			linesB[i] = fmt.Sprintf("beta%d", i)
			linesC[i] = fmt.Sprintf("gamma%d", i)
		}
		pasteA := strings.Join(linesA, "\n")
		pasteB := strings.Join(linesB, "\n")
		pasteC := strings.Join(linesC, "\n")
		e.HandleInput("\x1b[200~" + pasteA + "\x1b[201~")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[200~" + pasteB + "\x1b[201~")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[200~" + pasteC + "\x1b[201~")
		e.HandleInput("\x05")
		e.HandleInput("\x7f")
		e.HandleInput("\r")
		if want := pasteC + pasteB; submitted != want {
			t.Fatalf("submitted = %q, want %q", submitted, want)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:3998
	t.Run("undo after setText restores paste markers and registry", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		lines := make([]string, 12)
		for i := range lines {
			lines[i] = fmt.Sprintf("alpha%d", i)
		}
		paste := strings.Join(lines, "\n")
		e.HandleInput("\x1b[200~" + paste + "\x1b[201~")
		e.SetText("replacement")
		e.HandleInput(kittyUndo)
		e.HandleInput("\r")
		if submitted != paste {
			t.Fatalf("submitted = %q, want %q", submitted, paste)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:4057
	t.Run("does not crash when paste marker is wider than terminal width", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		bigContent := strings.TrimSuffix(strings.Repeat("line\n", 47), "\n")
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		marker := upstreamMarker(t, e.Text())
		if got := widthx.VisibleWidth(marker); got <= 8 {
			t.Fatalf("marker width = %d, want > 8: %q", got, marker)
		}
		for _, line := range e.Render(8) {
			if got := widthx.VisibleWidth(line); got > 8 {
				t.Fatalf("line exceeds width 8: visible=%d text=%q", got, line)
			}
		}
	})

	// upstream: packages/tui/test/editor.test.ts:4080
	t.Run("does not crash when text + paste marker exceeds terminal width with cursor on marker", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		for range 35 {
			e.HandleInput("b")
		}
		bigContent := strings.TrimSuffix(strings.Repeat("line\n", 27), "\n")
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for range 4 {
			e.HandleInput("b")
		}
		for range 5 {
			e.HandleInput("\x1b[D")
		}
		const renderWidth = 54
		for _, line := range e.Render(renderWidth) {
			if got := widthx.VisibleWidth(line); got > renderWidth {
				t.Fatalf("line exceeds width %d: visible=%d text=%q", renderWidth, got, line)
			}
		}
	})

	// upstream: packages/tui/test/editor.test.ts:4115
	t.Run("wordWrapLine re-checks overflow after backtracking to wrap opportunity", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.HandleInput(" ")
		for range 35 {
			e.HandleInput("b")
		}
		bigContent := strings.TrimSuffix(strings.Repeat("line\n", 27), "\n")
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for range 4 {
			e.HandleInput("b")
		}
		const renderWidth = 54
		for _, line := range e.Render(renderWidth) {
			if got := widthx.VisibleWidth(line); got > renderWidth {
				t.Fatalf("line exceeds width %d: visible=%d text=%q", renderWidth, got, line)
			}
		}
	})

	// upstream: packages/tui/test/editor.test.ts:4144
	t.Run("expands large pasted content literally in getExpandedText", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		pastedText := strings.Join([]string{
			"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8", "line 9", "line 10",
			"tokens $1 $2 $& $$ $` $' end",
		}, "\n")
		e.HandleInput("\x1b[200~" + pastedText + "\x1b[201~")
		upstreamMarker(t, e.Text())
		if got := e.GetExpandedText(); got != pastedText {
			t.Fatalf("expanded text = %q, want %q", got, pastedText)
		}
	})

	// upstream: packages/tui/test/editor.test.ts:4166
	t.Run("snaps to the paste marker start when navigating down into it", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("12345678901234567890\n\nhello ")
		bigContent := strings.Repeat("x", 2000)
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		e.Render(80)
		if text := e.Text(); !regexp.MustCompile(`\[paste #\d+ \d+ chars\]`).MatchString(text) {
			t.Fatalf("missing character-count paste marker in %q", text)
		}
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 10 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 0)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 6)
	})

	// upstream: packages/tui/test/editor.test.ts:4201
	t.Run("preserves sticky column when navigating through paste marker line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		for _, ch := range "1234567890123456" {
			e.HandleInput(string(ch))
		}
		e.HandleInput("\n")
		e.HandleInput("\n")
		e.HandleInput("\x1b[200~" + strings.Repeat("x", 2000) + "\x1b[201~")
		e.HandleInput("\n")
		e.HandleInput("\n")
		for _, ch := range "abcdefghijklmnop" {
			e.HandleInput(string(ch))
		}
		e.Render(30)
		for range 4 {
			e.HandleInput("\x1b[A")
		}
		e.HandleInput("\x01")
		for range 10 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 10)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 0)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 2, 0)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 3, 0)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 4, 10)
	})

	// upstream: packages/tui/test/editor.test.ts:4243
	t.Run("does not get stuck moving down from a multi-visual-line paste marker", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		for _, ch := range "abcdefgh" {
			e.HandleInput(string(ch))
		}
		bigContent := strings.TrimSuffix(strings.Repeat("line\n", 100), "\n")
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for _, ch := range "ijklmnopqr" {
			e.HandleInput(string(ch))
		}
		e.HandleInput("\n")
		for _, ch := range "123456789012345678" {
			e.HandleInput(string(ch))
		}
		e.Render(20)
		markerLen := len(upstreamMarker(t, e.Text())) // ASCII marker length is also its UTF-16 length.
		if markerLen <= 20 {
			t.Fatalf("marker length = %d, want > 20", markerLen)
		}
		const markerStart = 8
		markerEnd := markerStart + markerLen
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 6)
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 0, markerStart)
		// Preferred col 6 lands beyond the marker tail on the next visual line.
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 0, markerEnd)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, markerStart)
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 6)
	})

	// upstream: packages/tui/test/editor.test.ts:4304
	t.Run("skips marker continuation VLs when preferred col falls in marker tail", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		for _, ch := range "abcdefgh" {
			e.HandleInput(string(ch))
		}
		bigContent := strings.TrimSuffix(strings.Repeat("line\n", 100), "\n")
		e.HandleInput("\x1b[200~" + bigContent + "\x1b[201~")
		for _, ch := range "ijklmnopqr" {
			e.HandleInput(string(ch))
		}
		e.HandleInput("\n")
		for _, ch := range "123456789012345678" {
			e.HandleInput(string(ch))
		}
		e.Render(20)
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		for range 3 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 3)
		e.HandleInput("\x1b[B")
		if got := e.GetCursor().Col; got != 8 {
			t.Fatalf("cursor column = %d, want 8", got)
		}
		// Preferred col 3 falls inside the marker tail, so skip that continuation visual line.
		e.HandleInput("\x1b[B")
		assertEditorUnitCursor(t, e, 1, 3)
		e.HandleInput("\x1b[A")
		if got := e.GetCursor().Col; got != 8 {
			t.Fatalf("cursor column = %d, want 8", got)
		}
		e.HandleInput("\x1b[A")
		assertEditorUnitCursor(t, e, 0, 3)
	})

	// upstream: packages/tui/test/editor.test.ts:4345
	t.Run("submits large pasted content literally", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		pastedText := strings.Join([]string{
			"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8", "line 9", "line 10",
			"tokens $1 $2 $& $$ $` $' end",
		}, "\n")
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		e.HandleInput("\x1b[200~" + pastedText + "\x1b[201~")
		e.HandleInput("\r")
		if submitted != pastedText {
			t.Fatalf("submitted = %q, want %q", submitted, pastedText)
		}
	})
}
