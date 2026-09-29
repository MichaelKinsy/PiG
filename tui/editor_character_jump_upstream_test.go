package tui

import "testing"

func TestUpstreamEditorCharacterJump(t *testing.T) {
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	// upstream: packages/tui/src/keybindings.ts:142. The standalone TUI uses Ctrl+- for undo, without coding-agent platform overrides.
	SetTUIKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(KeybindingPlatformLinux), nil))

	// upstream: packages/tui/test/editor.test.ts:3070
	t.Run("jumps forward to first occurrence of character on same line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("o")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 4)
	})

	// upstream: packages/tui/test/editor.test.ts:3083
	t.Run("jumps forward to next occurrence after cursor", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		for range 4 {
			e.HandleInput("\x1b[C")
		}
		assertEditorUnitCursor(t, e, 0, 4)

		e.HandleInput("\x1d")
		e.HandleInput("o")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 7)
	})

	// upstream: packages/tui/test/editor.test.ts:3098
	t.Run("jumps forward across multiple lines", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("abc\ndef\nghi")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x1b[A")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("g")
		e.Render(80)
		assertEditorUnitCursor(t, e, 2, 0)
	})

	// upstream: packages/tui/test/editor.test.ts:3114
	t.Run("jumps backward to first occurrence before cursor on same line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		assertEditorUnitCursor(t, e, 0, 11)

		e.HandleInput("\x1b\x1d")
		e.HandleInput("o")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 7)
	})

	// upstream: packages/tui/test/editor.test.ts:3127
	t.Run("jumps backward across multiple lines", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("abc\ndef\nghi")
		assertEditorUnitCursor(t, e, 2, 3)

		e.HandleInput("\x1b\x1d")
		e.HandleInput("a")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 0)
	})

	// upstream: packages/tui/test/editor.test.ts:3140
	t.Run("does nothing when character is not found (forward)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("z")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 0)
	})

	// upstream: packages/tui/test/editor.test.ts:3153
	t.Run("does nothing when character is not found (backward)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		assertEditorUnitCursor(t, e, 0, 11)

		e.HandleInput("\x1b\x1d")
		e.HandleInput("z")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 11)
	})

	// upstream: packages/tui/test/editor.test.ts:3166
	t.Run("is case-sensitive", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Hello World")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("h")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("W")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 6)
	})

	// upstream: packages/tui/test/editor.test.ts:3186
	t.Run("cancels jump mode when Ctrl+] is pressed again", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("\x1d")
		e.HandleInput("o")
		e.Render(80)
		assertEditorText(t, e, "ohello world")
	})

	// upstream: packages/tui/test/editor.test.ts:3201
	t.Run("cancels jump mode on Escape and processes the Escape", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("\x1b")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("o")
		e.Render(80)
		assertEditorText(t, e, "ohello world")
	})

	// upstream: packages/tui/test/editor.test.ts:3219
	t.Run("cancels backward jump mode when Ctrl+Alt+] is pressed again", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		assertEditorUnitCursor(t, e, 0, 11)

		e.HandleInput("\x1b\x1d")
		e.HandleInput("\x1b\x1d")
		e.HandleInput("o")
		e.Render(80)
		assertEditorText(t, e, "hello worldo")
	})

	// upstream: packages/tui/test/editor.test.ts:3234
	t.Run("searches for special characters", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("foo(bar) = baz;")
		e.HandleInput("\x01")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("(")
		assertEditorUnitCursor(t, e, 0, 3)

		e.HandleInput("\x1d")
		e.HandleInput("=")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 9)
	})

	// upstream: packages/tui/test/editor.test.ts:3254
	t.Run("handles empty text gracefully", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("")
		assertEditorUnitCursor(t, e, 0, 0)

		e.HandleInput("\x1d")
		e.HandleInput("x")
		e.Render(80)
		assertEditorUnitCursor(t, e, 0, 0)
	})

	// upstream: packages/tui/test/editor.test.ts:3266
	t.Run("resets lastAction when jumping", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("hello world")
		e.HandleInput("\x01")

		e.HandleInput("x")
		assertEditorText(t, e, "xhello world")

		e.HandleInput("\x1d")
		e.HandleInput("o")
		e.HandleInput("Y")
		assertEditorText(t, e, "xhellYo world")

		e.HandleInput("\x1b[45;5u")
		e.Render(80)
		assertEditorText(t, e, "xhello world")
	})
}
