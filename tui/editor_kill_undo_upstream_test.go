package tui

import "testing"

func TestUpstreamEditorKillRing(t *testing.T) {
	// upstream: packages/tui/test/editor.test.ts:1201
	t.Run("Ctrl+W saves deleted text to kill ring and Ctrl+Y yanks it", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("foo bar baz")
		e.HandleInput("\x17")
		assertEditorText(t, e, "foo bar ")
		e.HandleInput("\x01")
		e.HandleInput("\x19")
		assertEditorText(t, e, "bazfoo bar ")
	})

	// upstream: packages/tui/test/editor.test.ts:1214
	t.Run("Ctrl+U saves deleted text to kill ring", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x15")
		assertEditorText(t, e, "world")
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1234
	t.Run("Ctrl+K saves deleted text to kill ring", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		e.HandleInput("\x0b")
		assertEditorText(t, e, "")
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1247
	t.Run("Ctrl+Y does nothing when kill ring is empty", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("test")
		e.HandleInput("\x19")
		assertEditorText(t, e, "test")
	})

	// upstream: packages/tui/test/editor.test.ts:1255
	t.Run("Alt+Y cycles through kill ring after Ctrl+Y", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("first")
		e.HandleInput("\x17")
		e.SetText("second")
		e.HandleInput("\x17")
		e.SetText("third")
		e.HandleInput("\x17")
		assertEditorText(t, e, "")
		e.HandleInput("\x19")
		assertEditorText(t, e, "third")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "second")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "first")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "third")
	})

	// upstream: packages/tui/test/editor.test.ts:1282
	t.Run("Alt+Y does nothing if not preceded by yank", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("test")
		e.HandleInput("\x17")
		e.SetText("other")
		e.HandleInput("x")
		assertEditorText(t, e, "otherx")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "otherx")
	})

	// upstream: packages/tui/test/editor.test.ts:1298
	t.Run("Alt+Y does nothing if kill ring has ≤1 entry", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("only")
		e.HandleInput("\x17")
		e.HandleInput("\x19")
		assertEditorText(t, e, "only")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "only")
	})

	// upstream: packages/tui/test/editor.test.ts:1311
	t.Run("consecutive Ctrl+W accumulates into one kill ring entry", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("one two three")
		e.HandleInput("\x17")
		e.HandleInput("\x17")
		e.HandleInput("\x17")
		assertEditorText(t, e, "")
		e.HandleInput("\x19")
		assertEditorText(t, e, "one two three")
	})

	// upstream: packages/tui/test/editor.test.ts:1326
	t.Run("Ctrl+U accumulates multiline deletes including newlines", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("line1\nline2\nline3")
		e.HandleInput("\x15")
		assertEditorText(t, e, "line1\nline2\n")
		e.HandleInput("\x15")
		assertEditorText(t, e, "line1\nline2")
		e.HandleInput("\x15")
		assertEditorText(t, e, "line1\n")
		e.HandleInput("\x15")
		assertEditorText(t, e, "line1")
		e.HandleInput("\x15")
		assertEditorText(t, e, "")
		e.HandleInput("\x19")
		assertEditorText(t, e, "line1\nline2\nline3")
	})

	// upstream: packages/tui/test/editor.test.ts:1358
	t.Run("backward deletions prepend, forward deletions append during accumulation", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("prefix|suffix")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		// The original sends Ctrl+K twice at column 6; retain the second no-op.
		e.HandleInput("\x0b")
		e.HandleInput("\x0b")
		assertEditorText(t, e, "prefix")
		e.HandleInput("\x19")
		assertEditorText(t, e, "prefix|suffix")
	})

	// upstream: packages/tui/test/editor.test.ts:1374
	t.Run("non-delete actions break kill accumulation", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("foo bar baz")
		e.HandleInput("\x17")
		assertEditorText(t, e, "foo bar ")
		e.HandleInput("x")
		assertEditorText(t, e, "foo bar x")
		e.HandleInput("\x17")
		assertEditorText(t, e, "foo bar ")
		e.HandleInput("\x19")
		assertEditorText(t, e, "foo bar x")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "foo bar baz")
	})

	// upstream: packages/tui/test/editor.test.ts:1397
	t.Run("non-yank actions break Alt+Y chain", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("first")
		e.HandleInput("\x17")
		e.SetText("second")
		e.HandleInput("\x17")
		e.SetText("")
		e.HandleInput("\x19")
		assertEditorText(t, e, "second")
		e.HandleInput("x")
		assertEditorText(t, e, "secondx")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "secondx")
	})

	// upstream: packages/tui/test/editor.test.ts:1416
	t.Run("kill ring rotation persists after cycling", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("first")
		e.HandleInput("\x17")
		e.SetText("second")
		e.HandleInput("\x17")
		e.SetText("third")
		e.HandleInput("\x17")
		e.SetText("")
		e.HandleInput("\x19")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "second")
		e.HandleInput("x")
		e.SetText("")
		e.HandleInput("\x19")
		assertEditorText(t, e, "second")
	})

	// upstream: packages/tui/test/editor.test.ts:1444
	t.Run("consecutive deletions across lines coalesce into one entry", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1\n2\n3")
		e.HandleInput("\x17")
		assertEditorText(t, e, "1\n2\n")
		e.HandleInput("\x17")
		assertEditorText(t, e, "1\n2")
		e.HandleInput("\x17")
		assertEditorText(t, e, "1\n")
		e.HandleInput("\x17")
		assertEditorText(t, e, "1")
		e.HandleInput("\x17")
		assertEditorText(t, e, "")
		e.HandleInput("\x19")
		assertEditorText(t, e, "1\n2\n3")
	})

	// upstream: packages/tui/test/editor.test.ts:1469
	t.Run("Ctrl+K at line end deletes newline and coalesces", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("")
		typeInto(e, "ab\ncd")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x05")
		e.HandleInput("\x0b")
		assertEditorText(t, e, "abcd")
		e.HandleInput("\x0b")
		assertEditorText(t, e, "ab")
		e.HandleInput("\x19")
		assertEditorText(t, e, "ab\ncd")
	})

	// upstream: packages/tui/test/editor.test.ts:1496
	t.Run("handles yank in middle of text", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("word")
		e.HandleInput("\x17")
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello wordworld")
	})

	// upstream: packages/tui/test/editor.test.ts:1511
	t.Run("handles yank-pop in middle of text", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("FIRST")
		e.HandleInput("\x17")
		e.SetText("SECOND")
		e.HandleInput("\x17")
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello SECONDworld")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "hello FIRSTworld")
	})

	// upstream: packages/tui/test/editor.test.ts:1536
	t.Run("multiline yank and yank-pop in middle of text", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("SINGLE")
		e.HandleInput("\x17")
		e.SetText("A\nB")
		e.HandleInput("\x15")
		e.HandleInput("\x15")
		e.HandleInput("\x15")
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello A\nBworld")
		e.HandleInput("\x1by")
		assertEditorText(t, e, "hello SINGLEworld")
	})

	// upstream: packages/tui/test/editor.test.ts:1564
	t.Run("Alt+D deletes word forward and saves to kill ring", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world test")
		e.HandleInput("\x01")
		e.HandleInput("\x1bd")
		assertEditorText(t, e, " world test")
		e.HandleInput("\x1bd")
		assertEditorText(t, e, " test")
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello world test")
	})

	// upstream: packages/tui/test/editor.test.ts:1581
	t.Run("Alt+D at end of line deletes newline", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("line1\nline2")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x05")
		e.HandleInput("\x1bd")
		assertEditorText(t, e, "line1line2")
		e.HandleInput("\x19")
		assertEditorText(t, e, "line1\nline2")
	})
}

func TestUpstreamEditorUndo(t *testing.T) {
	// upstream: packages/tui/src/keybindings.ts:142
	// The original TUI tests use Ctrl+- on every platform, independently of coding-agent defaults.
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{KBEditorUndo: {"ctrl+-"}}))

	// upstream: packages/tui/test/editor.test.ts:1598
	t.Run("does nothing when undo stack is empty", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1605
	t.Run("coalesces consecutive word characters into one undo unit", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		assertEditorText(t, e, "hello world")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1630
	t.Run("undoes spaces one at a time", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello  ")
		assertEditorText(t, e, "hello  ")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello ")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1652
	t.Run("undoes newlines and signals next word to capture state", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello\nworld")
		assertEditorText(t, e, "hello\nworld")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello\n")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1678
	t.Run("undoes backspace", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello")
		e.HandleInput("\x7f")
		assertEditorText(t, e, "hell")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
	})

	// upstream: packages/tui/test/editor.test.ts:1693
	t.Run("undoes forward delete", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello")
		e.HandleInput("\x01")
		e.HandleInput("\x1b[C")
		e.HandleInput("\x1b[3~")
		assertEditorText(t, e, "hllo")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
	})

	// upstream: packages/tui/test/editor.test.ts:1710
	t.Run("undoes Ctrl+W (delete word backward)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		assertEditorText(t, e, "hello world")
		e.HandleInput("\x17")
		assertEditorText(t, e, "hello ")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1733
	t.Run("undoes Ctrl+K (delete to line end)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x0b")
		assertEditorText(t, e, "hello ")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
		e.HandleInput("|")
		assertEditorText(t, e, "hello |world")
	})

	// upstream: packages/tui/test/editor.test.ts:1760
	t.Run("undoes Ctrl+U (delete to line start)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		e.HandleInput("\x01")
		for range 6 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x15")
		assertEditorText(t, e, "world")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1784
	t.Run("undoes yank", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello ")
		e.HandleInput("\x17")
		e.HandleInput("\x19")
		assertEditorText(t, e, "hello ")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1801
	t.Run("undoes single-line paste atomically", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[200~beep boop\x1b[201~")
		assertEditorText(t, e, "hellobeep boop world")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
		e.HandleInput("|")
		assertEditorText(t, e, "hello| world")
	})

	// upstream: packages/tui/test/editor.test.ts:1820
	t.Run("does not trigger autocomplete during single-line paste", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		t.Cleanup(e.AutocompleteCancel)
		provider := &editorRequestCounter{}
		e.SetAutocomplete(provider)
		e.HandleInput("\x1b[200~look at @node_modules/react/index.js please\x1b[201~")
		assertEditorText(t, e, "look at @node_modules/react/index.js please")
		if provider.requests != 0 {
			t.Fatalf("suggestionCalls = %d, want 0", provider.requests)
		}
		if e.AutocompleteOpen() {
			t.Fatal("isShowingAutocomplete = true, want false")
		}
	})

	// upstream: packages/tui/test/editor.test.ts:1840
	t.Run("decodes CSI-u Ctrl+letter sequences inside bracketed paste (tmux popup)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.HandleInput("\x1b[200~line1\x1b[106;5uline2\x1b[106;5uline3\x1b[201~")
		assertEditorText(t, e, "line1\nline2\nline3")
	})

	// upstream: packages/tui/test/editor.test.ts:1850
	t.Run("undoes multi-line paste atomically", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		e.HandleInput("\x1b[200~line1\nline2\nline3\x1b[201~")
		assertEditorText(t, e, "helloline1\nline2\nline3 world")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
		e.HandleInput("|")
		assertEditorText(t, e, "hello| world")
	})

	// upstream: packages/tui/test/editor.test.ts:1869
	t.Run("undoes insertTextAtCursor atomically", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		e.InsertTextAtCursor("/tmp/image.png")
		assertEditorText(t, e, "hello/tmp/image.png world")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
		e.HandleInput("|")
		assertEditorText(t, e, "hello| world")
	})

	// upstream: packages/tui/test/editor.test.ts:1888
	t.Run("insertTextAtCursor handles multiline text", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 5 {
			e.HandleInput("\x1b[C")
		}
		e.InsertTextAtCursor("line1\nline2\nline3")
		assertEditorText(t, e, "helloline1\nline2\nline3 world")
		if cursor := e.GetCursor(); cursor != (EditorCursor{Line: 2, Col: 5}) {
			t.Fatalf("cursor = %+v, want {Line:2 Col:5}", cursor)
		}
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1909
	t.Run("insertTextAtCursor normalizes CRLF and CR line endings", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("")
		e.InsertTextAtCursor("a\r\nb\r\nc")
		assertEditorText(t, e, "a\nb\nc")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
		e.InsertTextAtCursor("x\ry\rz")
		assertEditorText(t, e, "x\ny\nz")
	})

	// upstream: packages/tui/test/editor.test.ts:1926
	t.Run("undoes setText to empty string", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		assertEditorText(t, e, "hello world")
		e.SetText("")
		assertEditorText(t, e, "")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
	})

	// upstream: packages/tui/test/editor.test.ts:1949
	t.Run("clears undo stack on submit", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		submitted := ""
		e.OnSubmit = func(text string) { submitted = text }
		typeInto(e, "hello")
		e.HandleInput("\r")
		if submitted != "hello" {
			t.Fatalf("submitted = %q, want %q", submitted, "hello")
		}
		assertEditorText(t, e, "")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "")
	})

	// upstream: packages/tui/test/editor.test.ts:1971
	// "exits history browsing mode on undo" is retained in TestEditorUndoExitsHistoryBrowsing.
	// upstream: packages/tui/test/editor.test.ts:2003
	// "undo restores to pre-history state even after multiple history navigations" is retained in TestEditorUndoRestoresPreHistoryStateAfterSeveralNavigations.

	// upstream: packages/tui/test/editor.test.ts:2042
	t.Run("cursor movement starts new undo unit", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello world")
		assertEditorText(t, e, "hello world")
		for range 5 {
			e.HandleInput("\x1b[D")
		}
		typeInto(e, "lol")
		assertEditorText(t, e, "hello lolworld")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello world")
		e.HandleInput("|")
		assertEditorText(t, e, "hello |world")
	})

	// upstream: packages/tui/test/editor.test.ts:2075
	t.Run("no-op delete operations do not push undo snapshots", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		typeInto(e, "hello")
		assertEditorText(t, e, "hello")
		e.HandleInput("\x17")
		assertEditorText(t, e, "")
		e.HandleInput("\x17")
		e.HandleInput("\x17")
		e.HandleInput("\x1b[45;5u")
		assertEditorText(t, e, "hello")
	})
}
