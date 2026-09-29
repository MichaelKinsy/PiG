package tui

import (
	"testing"
	"testing/synctest"
)

// upstream: packages/tui/src/components/editor.ts:749-763; packages/tui/src/components/select-list.ts:148-154,244-259
func TestCompletionSelectionKeyboardWrapAndMouseClamp(t *testing.T) {
	previous := GetTUIKeybindings()
	SetTUIKeybindings(NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(NewSlashOnlyProvider([]SlashCommand{{Name: "alpha"}, {Name: "beta"}}))
		e.HandleInput("/")
		flush()
		for _, step := range []struct {
			key      string
			selected int
		}{{"\x1b[A", 1}, {"\x1b[B", 0}, {"\x10", 0}, {"\x0e", 0}, {"\x1b[B", 1}} {
			e.HandleInput(step.key)
			if e.autocompleteCursor != step.selected {
				t.Fatalf("key %q selected %d, want %d", step.key, e.autocompleteCursor, step.selected)
			}
		}
		e.Render(40)
		wheel := componentMouseEvent(MouseWheel, 1, e.renderedVisibleLineCount+2)
		wheel.WheelDelta = 1
		e.HandleMouse(wheel)
		if e.autocompleteCursor != 1 {
			t.Fatalf("wheel wrapped to %d, want last item 1", e.autocompleteCursor)
		}
	})
}
