package tui

import (
	"strings"
	"testing"
)

// editorContentRows strips the leading blank + top + bottom dashed
// borders from an Editor.Render output so the existing assertions
// still target the content rows. (2026-04-30) introduced
// the borders + leading blank to match upstream pi-tui's editor.ts.
func editorContentRows(rows []string) []string {
	if len(rows) >= 3 {
		return rows[2 : len(rows)-1]
	}
	return rows
}

// TestEditorWrapsLongLine reproduces the "text walks off the page while
// typing" bug: the editor used to truncate logical lines to `width`
// instead of wrapping them, so anything past the right edge vanished
// until submit. Now Render() must split a long logical line into
// multiple wrapped rows, all within the requested width.
func TestEditorWrapsLongLine(t *testing.T) {
	e := NewEditor()
	long := strings.Repeat("x", 250)
	e.SetText(long)
	out := editorContentRows(e.Render(80))
	if len(out) < 4 {
		t.Fatalf("250 chars at width 80 should wrap to 4+ rows, got %d", len(out))
	}
	for i, row := range out {
		plain := stripANSI(row)
		if len(plain) > 80 {
			t.Errorf("row %d exceeds width 80: len=%d", i, len(plain))
		}
	}
	// Cursor sits at end of input → must appear on the LAST wrapped row.
	last := out[len(out)-1]
	if !strings.Contains(last, "\033[7m") {
		t.Errorf("cursor should land on last wrapped row, got: %q", last)
	}
}

func TestEditorCursorOnFirstWrappedRow(t *testing.T) {
	e := NewEditor()
	long := strings.Repeat("x", 200)
	e.SetText(long)
	// Move cursor to position 5 (in the first wrapped chunk).
	e.cursor = [2]int{0, 5}
	out := editorContentRows(e.Render(80))
	if len(out) < 2 {
		t.Fatalf("expected wrapped rows, got %d", len(out))
	}
	if !strings.Contains(out[0], "\033[7m") {
		t.Errorf("cursor should be on first wrapped row, got rows: %v", out)
	}
	if strings.Contains(out[1], "\033[7m") {
		t.Errorf("cursor should NOT be on second wrapped row, got: %q", out[1])
	}
}

func TestEditorEmptyHasCursor(t *testing.T) {
	e := NewEditor()
	out := e.Render(40)
	content := editorContentRows(out)
	if len(content) != 1 {
		t.Fatalf("empty editor should render exactly 1 content row, got %d (full output incl. borders: %d): %v", len(content), len(out), out)
	}
	if !strings.Contains(content[0], "\033[7m") {
		t.Errorf("cursor missing on empty editor: %q", content[0])
	}
	// leading blank + top + bottom dashed dividers wrap the content.
	if strings.TrimSpace(out[0]) != "" {
		t.Errorf("expected leading blank above top border, got: %q", out[0])
	}
	if !strings.Contains(out[1], "\u2500") {
		t.Errorf("expected dashed top border on row 1, got: %q", out[1])
	}
	if !strings.Contains(out[len(out)-1], "\u2500") {
		t.Errorf("expected dashed bottom border, got: %q", out[len(out)-1])
	}
}

func TestEditorMultiLine(t *testing.T) {
	e := NewEditor()
	e.SetText("first line\nsecond line is much longer than width should be")
	out := editorContentRows(e.Render(20))
	// Row 0: "first line": fits in 20.
	// Rows 1+: "second line is much longer...": wraps.
	if len(out) < 4 {
		t.Fatalf("expected first-line + wrapped second-line rows, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "first line") {
		t.Errorf("first row should be unwrapped: %q", out[0])
	}
	for i, row := range out {
		plain := stripANSI(row)
		if len(plain) > 20 {
			t.Errorf("row %d exceeds width 20: len=%d %q", i, len(plain), plain)
		}
	}
}

func TestChunkByWidth(t *testing.T) {
	chunks, starts := chunkByWidth("abcdefghij", 4)
	wantChunks := []string{"abcd", "efgh", "ij"}
	wantStarts := []int{0, 4, 8}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for i := range chunks {
		if chunks[i] != wantChunks[i] {
			t.Errorf("chunk[%d]: %q want %q", i, chunks[i], wantChunks[i])
		}
		if starts[i] != wantStarts[i] {
			t.Errorf("start[%d]: %d want %d", i, starts[i], wantStarts[i])
		}
	}
}

func TestChunkByWidthEmpty(t *testing.T) {
	chunks, starts := chunkByWidth("", 5)
	if len(chunks) != 1 || chunks[0] != "" || len(starts) != 1 || starts[0] != 0 {
		t.Errorf("empty input should yield one empty chunk at 0, got chunks=%v starts=%v", chunks, starts)
	}
}

// ─── word-jump and word-delete ────────────────────────────

func TestEditor_WordBoundaryHelpers(t *testing.T) {
	cases := []struct {
		name string
		line string
		col  int
		// expected results
		prevStart int
		nextEnd   int
	}{
		{"start", "hello world", 0, 0, 5},
		{"middle-of-word", "hello world", 3, 0, 5},
		{"end-of-word", "hello world", 5, 0, 11},
		{"on-space", "hello world", 6, 0, 11},
		{"end-of-line", "hello world", 11, 6, 11},
		{"with-punct", "foo, bar.", 9, 8, 9},
		{"underscore-stays-in-word", "foo_bar", 7, 0, 7},
		{"empty", "", 0, 0, 0},
		{"only-spaces-from-end", "   word", 7, 3, 7},
		// D27: pig classifies per grapheme, so a CJK run with no internal
		// whitespace/ASCII-punctuation is one word (12 bytes = 4 chars x 3).
		// Upstream's Intl.Segmenter would split 学生/です; this case locks the
		// documented divergence so a future uax29 port updates it deliberately.
		{"cjk-run-is-one-word-backward", "学生です", 12, 0, 12},
		{"cjk-ascii-punct-boundary", "学生.です", 13, 7, 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prevWordStart(tc.line, tc.col); got != tc.prevStart {
				t.Errorf("prevWordStart(%q, %d) = %d, want %d", tc.line, tc.col, got, tc.prevStart)
			}
			if got := nextWordEnd(tc.line, tc.col); got != tc.nextEnd {
				t.Errorf("nextWordEnd(%q, %d) = %d, want %d", tc.line, tc.col, got, tc.nextEnd)
			}
		})
	}
}

func TestEditor_AltBackspaceDeletesWord(t *testing.T) {
	// The reported live-use bug: typing "hello world" then pressing
	// Option+Delete (alt+backspace, byte sequence \x1b\x7f) should
	// delete "world" leaving "hello ".
	e := NewEditor()
	e.HandleInput("hello world")
	e.HandleInput("\x1b\x7f") // alt+backspace
	if got := e.Text(); got != "hello " {
		t.Errorf("after alt+backspace got=%q want=%q", got, "hello ")
	}
}

func TestEditor_CtrlWDeletesWord(t *testing.T) {
	e := NewEditor()
	e.HandleInput("foo bar baz")
	e.HandleInput("\x17") // ctrl+w
	if got := e.Text(); got != "foo bar " {
		t.Errorf("after ctrl+w got=%q want=%q", got, "foo bar ")
	}
}

func TestEditor_AltDDeletesNextWord(t *testing.T) {
	e := NewEditor()
	e.HandleInput("alpha beta gamma")
	// Move cursor to start.
	e.HandleInput("\x01")  // ctrl+a
	e.HandleInput("\x1bd") // alt+d
	if got := e.Text(); got != " beta gamma" {
		t.Errorf("after alt+d got=%q want=%q", got, " beta gamma")
	}
}

func TestEditor_AltLeftJumpsToWordStart(t *testing.T) {
	e := NewEditor()
	e.HandleInput("one two three")
	// Cursor at end (13). alt+left → start of "three" (col 8).
	e.HandleInput("\x1b[1;3D")
	if e.cursor[1] != 8 {
		t.Errorf("after alt+left cursor=%d want=8 (text=%q)", e.cursor[1], e.Text())
	}
}

func TestEditor_AltRightJumpsToWordEnd(t *testing.T) {
	e := NewEditor()
	e.HandleInput("one two three")
	e.HandleInput("\x01")      // ctrl+a → col 0
	e.HandleInput("\x1b[1;3C") // alt+right → end of "one" (col 3)
	if e.cursor[1] != 3 {
		t.Errorf("after alt+right cursor=%d want=3", e.cursor[1])
	}
}

func TestEditor_EmacsAltBJumpsBackward(t *testing.T) {
	// Emacs-style \x1bb (alt+b) is the readline alias for alt+left.
	e := NewEditor()
	e.HandleInput("foo bar")
	e.HandleInput("\x1bb")
	if e.cursor[1] != 4 {
		t.Errorf("after alt+b cursor=%d want=4", e.cursor[1])
	}
}

func TestEditor_AltBackspaceAtStartJoinsPreviousLine(t *testing.T) {
	// Upstream editor.ts merges with the previous line when alt+backspace is
	// pressed at column 0.
	e := NewEditor()
	e.SetText("hello\nworld")
	e.cursor = [2]int{1, 0}
	e.HandleInput("\x1b\x7f")
	if got := e.Text(); got != "helloworld" {
		t.Errorf("alt+backspace at col 0 should join previous line; got=%q", got)
	}
	if e.cursor != [2]int{0, 5} {
		t.Errorf("cursor = %v, want [0 5]", e.cursor)
	}
}

func TestEditor_DeleteWordPushesToKillRing(t *testing.T) {
	// Word-delete operations push the deleted text onto the kill
	// ring so Ctrl+Y can yank it back.
	e := NewEditor()
	e.HandleInput("hello world")
	e.HandleInput("\x1b\x7f") // alt+backspace deletes "world"
	e.HandleInput("\x19")     // ctrl+y yanks
	if got := e.Text(); got != "hello world" {
		t.Errorf("yank after word-delete should restore text; got=%q", got)
	}
}

// TestEditorCursorAtTerminalWidthUsesReservedCursorColumn locks Pi's editor
// layout rule: without horizontal padding, one terminal column is reserved for
// the cursor. Text that is exactly terminal-width therefore uses two logical
// rows instead of relying on terminal soft-wrap state.
func TestEditorCursorAtTerminalWidthUsesReservedCursorColumn(t *testing.T) {
	const width = 10
	e := NewEditor()
	e.SetText(strings.Repeat("a", width))
	if e.cursor[1] != width {
		t.Fatalf("expected cursor at col %d, got %d", width, e.cursor[1])
	}

	contentRows := editorContentRows(e.Render(width))
	if len(contentRows) != 2 {
		t.Fatalf("content rows = %d, want 2: %q", len(contentRows), contentRows)
	}
	if got := lineDisplayWidth(contentRows[0]); got != width-1 {
		t.Fatalf("first row width = %d, want %d", got, width-1)
	}
	if strings.Contains(contentRows[0], "\033[7m") {
		t.Fatalf("first row contains cursor: %q", contentRows[0])
	}
	if !strings.Contains(contentRows[1], "\033[7m") {
		t.Fatalf("second row does not contain cursor: %q", contentRows[1])
	}
	for i, row := range contentRows {
		if got := lineDisplayWidth(row); got > width {
			t.Fatalf("row %d width = %d, exceeds %d: %q", i, got, width, row)
		}
	}
}

// TestEditorCursorPastFullWidthDoesNotDuplicate drives the exact character
// sequence that triggered the duplication bug: type width chars, then one
// more (which causes a wrap). Verify the second render produces exactly
// 2 content rows (not 3) and the first row has no cursor marker.
func TestEditorCursorPastFullWidthDoesNotDuplicate(t *testing.T) {
	const width = 10
	e := NewEditor()
	e.SetText(strings.Repeat("a", width+1)) // one char past full-width → wrap

	lines := e.Render(width)

	// Count content rows (non-empty, non-border).
	var contentRows []string
	for _, l := range lines {
		stripped := stripANSI(l)
		if stripped == "" || strings.Contains(l, "\u2500") {
			continue
		}
		contentRows = append(contentRows, l)
	}
	if len(contentRows) != 2 {
		t.Fatalf("expected 2 content rows after wrap, got %d: %v", len(contentRows), contentRows)
	}
	// First row must NOT have cursor (cursor is on the second chunk).
	if strings.Contains(contentRows[0], "\033[7m") {
		t.Errorf("first row should not have cursor after wrap, got: %q", contentRows[0])
	}
	// Second row must have cursor.
	if !strings.Contains(contentRows[1], "\033[7m") {
		t.Errorf("second row should have cursor after wrap, got: %q", contentRows[1])
	}
}

// ─── Input history () ────────────────────────────────────────────────

func TestEditor_AddToHistory_BasicAndDedupe(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("hello")
	e.AddToHistory("world")
	e.AddToHistory("world") // duplicate: should be skipped
	if len(e.inputHistory) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(e.inputHistory))
	}
	if e.inputHistory[0] != "world" {
		t.Errorf("newest first: want %q got %q", "world", e.inputHistory[0])
	}
	if e.inputHistory[1] != "hello" {
		t.Errorf("older: want %q got %q", "hello", e.inputHistory[1])
	}
}

func TestEditor_AddToHistory_SkipsEmpty(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("")
	e.AddToHistory("   ")
	if len(e.inputHistory) != 0 {
		t.Errorf("expected 0 entries for whitespace-only inputs, got %d", len(e.inputHistory))
	}
}

func TestEditor_HistoryNavUp_LoadsEntry(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second") // most recent

	// Up once from empty editor → loads "second"
	e.HandleInput("\033[A")
	if e.Text() != "second" {
		t.Errorf("after first Up: want %q got %q", "second", e.Text())
	}
	if e.inputHistIdx != 0 {
		t.Errorf("histIdx want 0 got %d", e.inputHistIdx)
	}

	// Up again → loads "first"
	e.HandleInput("\033[A")
	if e.Text() != "first" {
		t.Errorf("after second Up: want %q got %q", "first", e.Text())
	}
}

func TestEditor_HistoryNavDown_Restores(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")

	e.HandleInput("\033[A") // → second
	e.HandleInput("\033[A") // → first
	e.HandleInput("\033[B") // → second
	if e.Text() != "second" {
		t.Errorf("after Down: want %q got %q", "second", e.Text())
	}
	// Down again from most-recent → back to live (empty)
	e.HandleInput("\033[B")
	if e.Text() != "" {
		t.Errorf("after Down past newest: want empty got %q", e.Text())
	}
	if e.inputHistIdx != -1 {
		t.Errorf("histIdx should be -1 (live) got %d", e.inputHistIdx)
	}
}

func TestEditor_HistoryNav_ExitsOnTyping(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("previous")
	e.HandleInput("\033[A")
	if e.Text() != "previous" {
		t.Fatalf("precondition failed: %q", e.Text())
	}
	e.Clear() // simulates submit-clear
	// After Clear, histIdx should reset
	if e.inputHistIdx != -1 {
		t.Errorf("histIdx after Clear: want -1 got %d", e.inputHistIdx)
	}
}

// ─── Kill-ring / yank editor integration tests ───────────────────────────────

func TestEditor_ConsecutiveCtrlK_Accumulates(t *testing.T) {
	// Two consecutive Ctrl+K calls on a multiline editor should produce one
	// accumulated kill-ring entry containing both lines joined by "\n".
	e := NewEditor()
	e.HandleInput("foo")
	e.HandleInput("\n") // newline (standalone editor parity: Enter submits, Shift+Enter/newline inserts)
	e.HandleInput("bar")
	// Move cursor to start of first line.
	e.HandleInput("\033[A") // up
	e.cursor[1] = 0
	// First Ctrl+K kills "foo" from the first line.
	e.HandleInput("\x0b")
	// Second Ctrl+K at col 0 end-of-line kills the newline (joins lines).
	e.HandleInput("\x0b")
	got := e.killRing.Peek()
	if got != "foo\n" {
		t.Errorf("consecutive Ctrl+K: want %q got %q", "foo\n", got)
	}
	if e.killRing.Len() != 1 {
		t.Errorf("should be 1 ring entry, got %d", e.killRing.Len())
	}
}

func TestEditor_CtrlU_KillsToLineStart(t *testing.T) {
	e := NewEditor()
	e.HandleInput("hello world")
	// Move cursor to middle (after "hello ").
	e.cursor[1] = 6
	e.HandleInput("\x15") // Ctrl+U
	if got := e.Text(); got != "world" {
		t.Errorf("after Ctrl+U: want %q got %q", "world", got)
	}
	if got := e.killRing.Peek(); got != "hello " {
		t.Errorf("kill-ring after Ctrl+U: want %q got %q", "hello ", got)
	}
	if e.cursor[1] != 0 {
		t.Errorf("cursor should be at col 0, got %d", e.cursor[1])
	}
}

func TestEditor_YankPop_Cycles(t *testing.T) {
	// Push two non-consecutive kills, yank, then Alt+Y should cycle.
	e := NewEditor()
	// First kill.
	e.HandleInput("abc")
	e.cursor[1] = 0
	e.HandleInput("\x0b") // Ctrl+K kills "abc"; lastAction="kill"
	// Type something to reset lastAction.
	e.HandleInput("x")    // resets lastAction
	e.HandleInput("\x7f") // backspace to remove the x
	e.HandleInput("\x0b") // Ctrl+K on empty line end; kills "\n" if multiline or noop
	// Insert new content and second kill.
	e.SetText("def")
	e.cursor[1] = 3
	e.HandleInput("\x15") // Ctrl+U kills "def"
	// Now ring has at least 2 entries. Yank the most recent.
	e.HandleInput("\x19") // Ctrl+Y yanks "def"
	if e.lastAction != "yank" {
		t.Fatalf("lastAction after Ctrl+Y: want yank, got %q", e.lastAction)
	}
	firstYank := e.Text()
	// Alt+Y should cycle to previous entry.
	e.HandleInput("\x1by")
	secondYank := e.Text()
	if firstYank == secondYank {
		t.Errorf("Alt+Y should change yanked text; both are %q", firstYank)
	}
}

func TestEditor_CtrlSlash_Undo(t *testing.T) {
	e := NewEditor()
	e.HandleInput("hello")
	e.HandleInput(hostUndoInput()) // the platform undo key; Ctrl+/ sends ctrl+-'s 0x1f
	// After undo, text should be shorter or empty.
	if e.Text() == "hello" {
		t.Errorf("Ctrl+/ undo: text unchanged, still %q", e.Text())
	}
}

// ─── Bracketed Paste Tests ───────────────────────────────────────────────────

func TestEditor_BracketedPaste_SingleChunk(t *testing.T) {
	e := NewEditor()
	// Simulate a bracketed paste: start + content + end
	e.HandleInput("\x1b[200~hello world\x1b[201~")
	if got := e.Text(); got != "hello world" {
		t.Errorf("after paste, text = %q, want %q", got, "hello world")
	}
}

func TestEditor_BracketedPaste_MultiLine(t *testing.T) {
	e := NewEditor()
	e.HandleInput("\x1b[200~line1\nline2\nline3\x1b[201~")
	if got := e.Text(); got != "line1\nline2\nline3" {
		t.Errorf("after multi-line paste, text = %q, want %q", got, "line1\nline2\nline3")
	}
}

func TestEditor_BracketedPaste_SplitAcrossInputs(t *testing.T) {
	e := NewEditor()
	// Paste arrives in multiple HandleInput calls (common in practice).
	e.HandleInput("\x1b[200~first part")
	e.HandleInput(" second part\x1b[201~")
	if got := e.Text(); got != "first part second part" {
		t.Errorf("split paste, text = %q, want %q", got, "first part second part")
	}
}

// Upstream searches the accumulated paste buffer, so a terminal read boundary
// inside the end marker still closes paste mode and dispatches following input.
func TestEditor_BracketedPaste_EndMarkerSplitAcrossInputs(t *testing.T) {
	e := NewEditor()
	e.HandleInput("\x1b[200~hello\x1b[20")
	e.HandleInput("1~")
	e.HandleInput("x")
	if got := e.Text(); got != "hellox" {
		t.Fatalf("split end marker paste = %q, want %q", got, "hellox")
	}
}

func TestEditor_BracketedPaste_ContentBeforeAndAfter(t *testing.T) {
	e := NewEditor()
	e.HandleInput("before")
	e.HandleInput("\x1b[200~pasted\x1b[201~after")
	want := "beforepastedafter"
	if got := e.Text(); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestHandlePasteFlush_CSIuDecode(t *testing.T) {
	e := NewEditor()
	e.handlePasteFlush("hello\x1b[106;5uworld")
	if got := e.Text(); got != "hello\nworld" {
		t.Fatalf("handlePasteFlush CSI-u decode = %q, want %q", got, "hello\nworld")
	}
}

// ─── Forward Delete Tests ────────────────────────────────────────────────────

func TestEditor_ForwardDelete_Middle(t *testing.T) {
	e := NewEditor()
	e.SetText("hello")
	e.cursor = [2]int{0, 2}  // between 'e' and 'l'
	e.HandleInput("\x1b[3~") // Delete key
	if got := e.Text(); got != "helo" {
		t.Errorf("forward delete middle, text = %q, want %q", got, "helo")
	}
}

func TestEditor_ForwardDelete_JoinsLines(t *testing.T) {
	e := NewEditor()
	e.SetText("ab\ncd")
	e.cursor = [2]int{0, 2}  // end of first line
	e.HandleInput("\x1b[3~") // Delete key
	if got := e.Text(); got != "abcd" {
		t.Errorf("forward delete join, text = %q, want %q", got, "abcd")
	}
}

func TestEditor_CtrlD_ForwardDelete(t *testing.T) {
	e := NewEditor()
	e.SetText("xyz")
	e.cursor = [2]int{0, 0}
	e.HandleInput("\x04") // Ctrl+D
	if got := e.Text(); got != "yz" {
		t.Errorf("Ctrl+D, text = %q, want %q", got, "yz")
	}
}

// ─── Visual Line Movement Tests ──────────────────────────────────────────────

func TestEditor_VisualLineUp_WrappedLine(t *testing.T) {
	e := NewEditor()
	// Set a long line that will wrap at width=10.
	e.SetText("abcdefghij1234567890")
	e.renderWidth = 10
	e.cursor = [2]int{0, 12} // on visual line 2 (past first 10 chars)
	e.moveVisualLine(-1)     // should move to visual line 1
	// Cursor should be within the first chunk (byte 0-9).
	if e.cursor[1] >= 10 {
		t.Errorf("after visual-up, cursor[1] = %d, want < 10", e.cursor[1])
	}
}

func TestEditor_VisualLineDown_WrappedLine(t *testing.T) {
	e := NewEditor()
	e.SetText("abcdefghij1234567890")
	e.renderWidth = 10
	e.cursor = [2]int{0, 2} // on visual line 1, column 2
	e.moveVisualLine(1)     // should move to visual line 2
	if e.cursor[1] < 10 {
		t.Errorf("after visual-down, cursor[1] = %d, want >= 10", e.cursor[1])
	}
}

func TestEditor_VisualLineUp_AcrossLogicalLines(t *testing.T) {
	e := NewEditor()
	e.SetText("short\nanother")
	e.renderWidth = 80
	e.cursor = [2]int{1, 3} // on second line
	e.moveVisualLine(-1)    // should move to first line
	if e.cursor[0] != 0 {
		t.Errorf("after visual-up across lines, cursor[0] = %d, want 0", e.cursor[0])
	}
}

// ─── InsertTextAtCursor Tests ────────────────────────────────────────────────

func TestEditor_InsertTextAtCursor_MultiLine(t *testing.T) {
	e := NewEditor()
	e.SetText("before")
	e.cursor = [2]int{0, 6}
	e.InsertTextAtCursor("\nline2\nline3")
	want := "before\nline2\nline3"
	if got := e.Text(); got != want {
		t.Errorf("InsertTextAtCursor multi-line, text = %q, want %q", got, want)
	}
	if e.cursor[0] != 2 || e.cursor[1] != 5 {
		t.Errorf("cursor = %v, want [2, 5]", e.cursor)
	}
}

// ─── Keybinding registry tests ──────────────────────────────────────────────
// These verify that Editor.HandleInput now dispatches through the global
// TUI keybinding manager (mirrors upstream editor.ts handleInput chain).
// They are the regression marker for the registry-vs-hardcoded-bytes
// refactor: if a future worker reintroduces literal byte switches without
// going through GetTUIKeybindings(), one of these must break.

func TestEditor_KeybindingsResolvedViaRegistry(t *testing.T) {
	// Snapshot the global manager so the test doesn't leak overrides.
	prev := GetTUIKeybindings()
	defer SetTUIKeybindings(prev)

	// Reset to defaults.
	SetTUIKeybindings(NewTUIKeybindingsManager(nil))

	e := NewEditor()
	e.HandleInput("foo bar baz")
	e.HandleInput("\x1b\x7f") // alt+backspace = tui.editor.deleteWordBackward default
	if got := e.Text(); got != "foo bar " {
		t.Errorf("alt+backspace via registry: got=%q want=%q", got, "foo bar ")
	}
}

func TestEditor_KeybindingsUserOverrideRemapsAction(t *testing.T) {
	// User remaps deleteWordBackward off of alt+backspace and onto
	// ctrl+r. With the registry wired correctly, alt+backspace should
	// fall through to the default no-op insert (which drops the
	// non-printable bytes) and ctrl+r should now delete the word.
	prev := GetTUIKeybindings()
	defer SetTUIKeybindings(prev)

	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{
		"tui.editor.deleteWordBackward": {"ctrl+r"},
	}))

	e := NewEditor()
	e.HandleInput("alpha beta gamma")

	// alt+backspace should now NOT trigger word delete (since user
	// removed it from that action) and falls through to the default
	// branch which silently drops non-printable bytes.
	e.HandleInput("\x1b\x7f")
	if got := e.Text(); got != "alpha beta gamma" {
		t.Errorf("alt+backspace after rebind should be no-op; got=%q", got)
	}

	// ctrl+r should now delete the word.
	e.HandleInput("\x12") // ctrl+r
	if got := e.Text(); got != "alpha beta " {
		t.Errorf("ctrl+r after rebind should delete word; got=%q want=%q", got, "alpha beta ")
	}
}
