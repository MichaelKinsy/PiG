package codingagent

import (
	"strings"
	"testing"
)

// TestExtensionEditorLateExternalCompletionTouchesNothingButTheRepaint covers
// handleOpenExternalEditor's continuation (extension-editor.ts:128-140): the
// child's result arrives after the dialog closed (the key press is
// fire-and-forget). A late edit or failure report changes neither the editor
// text nor the status row, yet the forced repaint still runs.
func TestExtensionEditorLateExternalCompletionTouchesNothingButTheRepaint(t *testing.T) {
	restoreTUIKeybindings(t)
	c := newExtensionEditorProbe(t, "Edit", "kept", map[string][]string{"app.editor.external": {"ctrl+x"}}, nil)
	var apply func(string)
	var report func(string)
	c.SetExternalEditor(func(_, _ string, a func(string), r func(string)) { apply, report = a, r })
	c.HandleInput("\x18")
	c.HandleInput("\r") // submit while the editor child is still running
	if !c.done {
		t.Fatal("the dialog did not close")
	}
	before := c.ui.forced
	textBefore := c.editor.Text() // the submit cleared the editor; a late edit must not refill it
	apply("late edit")
	report("late failure")
	if got := c.editor.Text(); got != textBefore {
		t.Errorf("a late edit changed the editor text from %q to %q", textBefore, got)
	}
	if got := strings.Join(c.Render(80), "\n"); strings.Contains(got, "late failure") {
		t.Errorf("a late failure reached the status row:\n%s", got)
	}
	if c.ui.forced != before+2 {
		t.Errorf("forced repaints after two late completions = %d, want %d", c.ui.forced-before, 2)
	}
}
